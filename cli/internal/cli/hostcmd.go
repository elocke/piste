package cli

// piste host add: the machine piste runs on becomes a host of an
// overlay made elsewhere. Its host file and hosts entry come from the
// checkout's template (what's the person's on every machine is in the
// overlay's home.nix); a NixOS machine's host also carries its /etc/nixos
// and SSH keys in, and its name becomes the hostname. With sops it gets its
// own age key, which goes into .sops.yaml, and a machine that can already
// decrypt re-encrypts for it (piste secrets updatekeys). piste stages; it
// never commits or pushes.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/execx"
	carry "github.com/elocke/piste/cli/internal/machine"
	"github.com/elocke/piste/cli/internal/overlay"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/workspace"
)

func init() { register("host", newHost) }

func newHost(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "host",
		Short: "Add this machine to an overlay",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	c.AddCommand(newHostAdd(a))
	return c
}

func newHostAdd(a *App) *cobra.Command {
	var roles []string
	var extras bool
	var name string
	c := &cobra.Command{
		Use:   "add",
		Short: "Make this machine a host of an overlay, with its own age key if the overlay keeps secrets",
		Long: "Make the machine you're on a host of an overlay: the one you're in, the one\n" +
			"--overlay names, or the only one. Its host file (hosts/<hostname>.nix, from the\n" +
			"engine's template) and its entry in flake.nix are staged, and it is built. What's\n" +
			"yours on every machine comes from the overlay's home.nix.\n\n" +
			"On NixOS the host is the whole system, hosts/<name>/: its machine/ is a copy of\n" +
			"this machine's /etc/nixos, rewritten to build from the flake (what can't be is\n" +
			"marked \"# piste:\" and listed), and the keys in ~/.ssh/authorized_keys may log\n" +
			"in. Its name is --name, else the hostname; a fresh machine's (nixos) is asked for\n" +
			"at a terminal. So is a name the overlay has a NixOS host of already, unless that\n" +
			"host's root filesystem is this machine's, by uuid: another machine's is refused,\n" +
			"even with --name, and one piste can't tell (a root by label or device name,\n" +
			"which every machine made from one image shares) needs --name. A name that isn't\n" +
			"the hostname becomes it now (sudo hostname), and for good at the switch, but an\n" +
			"image module that forces its own name (Proxmox images, Proxmox LXCs) wins; host\n" +
			"add warns when it does, with the setting that keeps the name.\n\n" +
			"Its roles are the ones the overlay's other hosts of its kind (Mac, NixOS or\n" +
			"Linux) have, extras included (the ones they all have, if they differ); with no\n" +
			"other host of its kind, init's defaults. --role gives it others.\n\n" +
			"If the overlay keeps secrets (.sops.yaml), this machine gets an age key, made if\n" +
			"missing, and its public key goes into .sops.yaml. This machine can't read the\n" +
			"secrets until a machine that can runs piste secrets updatekeys; the steps are\n" +
			"printed. piste stages; it never commits or pushes. Run again, it skips each\n" +
			"step already done.\n\n" +
			"To remove a machine, by hand: delete its entry in flake.nix and its host file\n" +
			"(a NixOS host's directory), remove its key from .sops.yaml (keys: and every age\n" +
			"list), run piste secrets updatekeys (it also makes a new data key), and change\n" +
			"the secrets' values: that machine has read them. If it had a GitHub key (piste\n" +
			"github), revoke it: gh ssh-key list, then gh ssh-key delete <id>.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var r []string
			if cmd.Flags().Changed("role") {
				// --role none: no roles, not the other hosts' or the default.
				r = append([]string{}, splitRoles(strings.Join(roles, ","))...)
			}
			// --extras, init's spelling: the roles (given, else the ones host
			// add would take) with extras, or without it for --extras=false.
			var ex *bool
			if cmd.Flags().Changed("extras") {
				ex = &extras
			}
			_, _, err := a.hostAdd(r, ex, name)
			return err
		},
	}
	c.Flags().StringSliceVar(&roles, "role", nil, "a role: base, dev, desktop or extras (repeatable; default: the roles of the overlay's other hosts of this kind, else dev on Linux, none on macOS)")
	c.Flags().BoolVar(&extras, "extras", false, "add the extras role to the roles, as piste init --extras does (--extras=false leaves it out)")
	c.Flags().StringVar(&name, "name", "", "on NixOS, the host's name, which becomes this machine's hostname (default: its hostname)")
	return c
}

// defaultRoles are host add's roles when --role isn't given: dev on Linux,
// none on a Mac (it has desktop already).
func defaultRoles(system string) []string {
	if overlay.Darwin(system) {
		return nil
	}
	return []string{"dev"}
}

// inheritRoles: the roles a new host takes when --role isn't given, those of
// the overlay's other hosts of its kind (Mac, NixOS or a Linux home), what
// they all have; with none of its kind, or their roles unreadable (said),
// defaultRoles.
func (a *App) inheritRoles(name, dir, user, system string, nixos bool, hosts workspace.Hosts) []string {
	darwin := overlay.Darwin(system)
	others, kind, hostKind := hosts.Home, "Linux", KindHome
	switch {
	case darwin:
		others, kind, hostKind = hosts.Darwin, "Mac", KindDarwin
	case nixos:
		others, kind, hostKind = hosts.NixOS, "NixOS", KindNixOS
	}
	if len(others) == 0 {
		return defaultRoles(system)
	}
	what := fmt.Sprintf("overlays/%s's other %s host", name, kind)
	if len(others) > 1 {
		what += "s"
	}
	shown := func(rs []string) string {
		if len(rs) == 0 {
			return "none"
		}
		return strings.Join(rs, ", ")
	}
	got, err := a.hostRoles(dir, user, hostKind)
	if err == nil {
		if rs, ok := overlay.SharedRoles(darwin, got); ok {
			names := slices.Sorted(maps.Keys(got))
			share := ""
			if len(names) > 1 {
				share = ", the ones they share"
			}
			a.info(fmt.Sprintf("Roles from %s (%s)%s: %s; --role sets others", what, strings.Join(names, ", "), share, shown(rs)))
			return rs
		}
		err = errors.New("nix eval listed none")
	}
	def := defaultRoles(system)
	a.warn(fmt.Sprintf("Couldn't read the roles of %s (%v): this machine takes the default ones (%s); --role sets others", what, err, shown(def)))
	return def
}

// hostAdd is `piste host add` (also run by `piste setup`): roles nil is
// the roles of the overlay's other hosts of this kind, else init's default;
// extras, when set, adds the extras role to them or takes it out. named is
// --name, a NixOS host's name ("": this machine's hostname). built: the
// build ran and passed; ready: this machine can read the overlay's secrets
// (or it has none). A refusal writes nothing.
func (a *App) hostAdd(roles []string, extras *bool, named string) (built, ready bool, err error) {
	refuse := func(msg string) (bool, bool, error) {
		a.errorf(msg)
		return false, false, ExitError{1}
	}

	// 1. The overlay.
	p, err := a.plat()
	if err != nil {
		return false, false, err
	}
	if p.Checkout == "" || p.Overlay == "" {
		return refuse("piste host add: run it in a piste checkout, in an overlay or with --overlay <name>")
	}
	root, name, dir := p.Checkout, p.Overlay, p.FlakeDir
	q, key, err := a.thisMachine()
	if err != nil {
		return false, false, err
	}

	// 2. Every check, before any write.
	flakePath := filepath.Join(dir, "flake.nix")
	flake, err := os.ReadFile(flakePath)
	if err != nil {
		return false, false, err
	}
	owner, ok := overlay.FlakeUser(flake)
	if !ok {
		return refuse(fmt.Sprintf("overlays/%s/flake.nix has no user = \"…\"; line: piste host add doesn't know its shape", name))
	}
	if owner != q.User {
		return refuse(fmt.Sprintf("overlays/%s is %s's (its flake.nix's user); you are %s, and an overlay has one user", name, owner, q.User))
	}
	// On NixOS the host is the whole system, named by the hostname it will
	// have: --name, else this one's.
	nixos, running := q.NixOS, q.Hostname
	chosen := false // the person gave the name (--name, or at a terminal)
	switch {
	case nixos:
		host, picked, err := a.nixosHostName(q.Hostname, named, p.System)
		if err != nil {
			return false, false, err
		}
		q.Hostname, key, chosen = host, host, picked
	case named != "":
		return refuse("--name is for a NixOS machine: a Mac or a home-manager home is named by this machine's hostname")
	default:
		if err := overlay.CheckHostname(q.Hostname, p.System, false); err != nil {
			return refuse("this machine's " + err.Error())
		}
	}
	given := roles != nil
	if given {
		if extras != nil {
			roles = overlay.WithExtras(roles, *extras)
		}
		if err := overlay.CheckRoles(roles); err != nil {
			return refuse(fmt.Sprintf("%v (--role)", err))
		}
	}
	hosts, err := a.overlayHosts(dir)
	if err != nil {
		return refuse(fmt.Sprintf("overlays/%s: %v", name, err))
	}
	// A name the overlay has a NixOS host of already is this machine's only
	// when that host's root filesystem says so: another machine's (a clone,
	// a name typed by mistake) is no default, and is refused; one piste
	// can't tell is this machine's when the person names it.
	for nixos && slices.Contains(hosts.NixOS, q.Hostname) {
		h := a.whoseHost(dir, q.Hostname, q.User, p.HomeDir)
		if h.verdict == rootSame || h.verdict == rootUnknown && chosen {
			break
		}
		if chosen || !a.terminal() {
			a.errorf(h.refusal[0])
			for _, l := range h.refusal[1:] {
				fmt.Fprintln(a.Stderr, l)
			}
			return false, false, ExitError{1}
		}
		host, err := a.askNixOSName(h.ask, a.nixosNameCheck(p.System), h.refusal...)
		if err != nil {
			return false, false, err
		}
		q.Hostname, key, chosen = host, host, true
	}
	have := workspace.Matches(hosts, q)
	if have && nixos && !slices.Contains(hosts.NixOS, q.Hostname) {
		key = q.User + "@" + q.Hostname // its home-manager home, as before NixOS hosts
	}
	// What host add writes for the host (hostPath: one file, or a NixOS
	// host's directory) and the host's own file in it, which imports home.
	hostPath := filepath.Join("hosts", q.Hostname+".nix")
	hostRel, homeRel, what := hostPath, "../home.nix", "the file"
	if nixos {
		hostPath = filepath.Join("hosts", q.Hostname)
		hostRel, homeRel, what = filepath.Join(hostPath, "default.nix"), "../../home.nix", "the directory"
	}
	var hostFiles map[string][]byte
	var entry string
	var newFlake []byte
	var carried carriedMachine
	if !have {
		if nixos {
			if carried, err = carryMachine(p.HomeDir); err != nil {
				return refuse(fmt.Sprintf("piste host add can't carry this machine's %s into overlays/%s: %v", platform.MachineEtc, name, err))
			}
		}
		// The cheap refusals come first: the roles other hosts take cost an
		// eval (and a line of output) that an error would waste. What they
		// print for adding by hand has the roles known without it.
		render := func(rs []string) error {
			ans := overlay.Answers{
				Name: name, User: q.User, Hostname: q.Hostname, System: p.System, Roles: rs,
				Home:     linuxHome(q.User, q.User, p.HomeDir, p.System),
				Checkout: overlay.CheckoutExpr(root, p.HomeDir),
				NixOS:    nixos, AuthorizedKeys: carried.keys,
			}
			files, e, rerr := overlay.RenderHost(filepath.Join(root, "templates", "default"), ans)
			if rerr != nil {
				return rerr
			}
			carryInto(files, hostPath, carried.files)
			hostFiles, entry = files, e
			return nil
		}
		first := roles
		if !given {
			first = defaultRoles(p.System)
			if extras != nil {
				first = overlay.WithExtras(first, *extras)
			}
		}
		if err := render(first); err != nil {
			return refuse(err.Error())
		}
		if !fileExists(filepath.Join(dir, "home.nix")) {
			a.errorf(fmt.Sprintf("overlays/%s has no home.nix (a team layer's starter, or an overlay made by hand): add %s by hand.", name, key))
			fmt.Fprintln(a.Stdout, "Its entry, in flake.nix's hosts:")
			a.block(entry)
			fmt.Fprintf(a.Stdout, "and %s, which imports %s (what's yours on every machine, from your other host's file):\n", hostRel, homeRel)
			a.block(string(hostFiles[hostRel]))
			for _, rel := range slices.Sorted(maps.Keys(hostFiles)) {
				if rel != hostRel {
					fmt.Fprintf(a.Stdout, "and %s:\n", rel)
					a.block(string(hostFiles[rel]))
				}
			}
			fmt.Fprintln(a.Stdout, "Then run piste host add again: it does the rest.")
			return false, false, ExitError{1}
		}
		if fileExists(filepath.Join(dir, hostPath)) {
			a.errorf(fmt.Sprintf("overlays/%s/%s exists, but the overlay's hosts don't have %s: add its entry to flake.nix by hand, or move %s away. The entry:", name, hostPath, key, what))
			a.block(entry)
			return false, false, ExitError{1}
		}
		if _, err := overlay.AddHostEntry(flake, entry); err != nil {
			a.errorf(fmt.Sprintf("overlays/%s/flake.nix has no hosts line to add %s beside: add its entry to the hosts by hand:", name, key))
			a.block(entry)
			return false, false, ExitError{1}
		}
		if !given {
			roles = a.inheritRoles(name, dir, owner, p.System, nixos, hosts)
			if extras != nil {
				roles = overlay.WithExtras(roles, *extras)
			}
			if err := render(roles); err != nil {
				return refuse(err.Error())
			}
		}
		if newFlake, err = overlay.AddHostEntry(flake, entry); err != nil {
			return refuse(err.Error())
		}
	}
	sopsYAML := filepath.Join(dir, ".sops.yaml")
	withSops := fileExists(sopsYAML)
	if withSops {
		keyPath := ageKeyPath(p.HomeDir)
		if fileExists(keyPath) && agePubkey(keyPath) == "" {
			return refuse(fmt.Sprintf("can't read the age key at %s: piste host add needs its public key for overlays/%s/.sops.yaml", keyPath, name))
		}
		src, err := os.ReadFile(sopsYAML)
		if err != nil {
			return false, false, err
		}
		if err := checkRecipientShape(src); err != nil {
			// The key is needed for the lines to add; it is this machine's
			// and lives outside the overlay.
			pub, kerr := a.ensureAgeKey(p.HomeDir)
			if kerr != nil {
				return false, false, kerr
			}
			a.errorf(fmt.Sprintf("overlays/%s/.sops.yaml: %v, so piste can't add this machine's key to it. Add these lines by hand, then run piste host add again:", name, err))
			fmt.Fprint(a.Stdout, recipientLines(q.Hostname, pub))
			return false, false, ExitError{1}
		}
	}

	// 3. The host.
	if have {
		a.info(fmt.Sprintf("overlays/%s already has %s", name, key))
	} else {
		if err := a.addHostFiles(dir, hostFiles, flakePath, flake, newFlake); err != nil {
			return false, false, err
		}
		after, err := a.overlayHosts(dir)
		if err != nil || !workspace.Matches(after, q) {
			why := "its hosts aren't a set piste can add to"
			if err != nil {
				why = err.Error()
			}
			a.undoHostFiles(dir, hostFiles, flakePath, flake)
			a.errorf(fmt.Sprintf("nix eval of overlays/%s doesn't list %s after adding its entry (%s): flake.nix is back as it was and %s removed. Add the entry to its hosts by hand:", name, key, why, hostPath))
			a.block(entry)
			return false, false, ExitError{1}
		}
		hosts = after
		if err := a.gitIn(dir, "add", "--", "flake.nix"); err != nil {
			return false, false, err
		}
		shown, wrote := "none", hostPath
		if len(roles) > 0 {
			shown = strings.Join(roles, ", ")
		}
		if nixos {
			wrote += string(filepath.Separator)
		}
		a.success(fmt.Sprintf("Added %s (roles: %s) to overlays/%s: %s and its entry in flake.nix, staged", key, shown, name, wrote))
		if nixos {
			a.sayCarried(carried, hostPath, q.User)
		}
		// A first machine that started with everything: this one has the
		// default roles, so say so rather than leave it thinner unsaid.
		if overlay.FlakeHasExtras(flake) && !overlay.HasExtras(roles) {
			a.info(fmt.Sprintf(`The overlay's other hosts take the extras role; %s doesn't: add "extras" to its roles in overlays/%s/flake.nix (piste host add --extras takes it when a machine is added).`, key, name))
		}
	}

	// 4. The key.
	ready = true
	if withSops {
		pub, err := a.ensureAgeKey(p.HomeDir)
		if err != nil {
			return false, false, err
		}
		anchor, changed, err := addRecipient(sopsYAML, q.Hostname, pub)
		if err != nil {
			return false, false, err
		}
		if changed {
			if err := a.gitIn(dir, "add", "--", ".sops.yaml"); err != nil {
				return false, false, err
			}
			a.success(fmt.Sprintf("This machine's key is in overlays/%s/.sops.yaml (&%s), staged", name, anchor))
		} else {
			a.info(fmt.Sprintf("This machine's key is already in overlays/%s/.sops.yaml", name))
		}
		ready = secretsReady(dir, pub)
	} else {
		// Secrets encrypted without a .sops.yaml (sops --age by hand, or
		// the file deleted): nothing to add this machine's key to, and no
		// key made.
		ready = secretsReady(dir, agePubkey(ageKeyPath(p.HomeDir)))
	}

	// 5. The build: no secrets needed (sops-nix decrypts at activation).
	// Then the hostname: a NixOS host is found by it (the switch's guard,
	// nixos-rebuild), so it is set now, for the next build and the first
	// switch; and mkFlake sets it to the host's name for good, unless an
	// image's module takes it back (checkHostName).
	a.Overlay = name
	renamed := nixos && q.Hostname != running
	if err := a.runBuild(q.Hostname); err != nil {
		if renamed {
			a.setHostname(q.Hostname, false)
		}
		a.warn(fmt.Sprintf("overlays/%s: this machine was added; fix the build and run: %s build --overlay %s", name, a.piste(), name))
		return false, ready, err
	}
	var warning []string
	kept := false
	if nixos && slices.Contains(hosts.NixOS, q.Hostname) {
		warning, kept = a.checkHostName(dir, p.HomeDir, q.Hostname, hostRel)
	}
	if renamed {
		a.setHostname(q.Hostname, kept)
	}
	a.warnLines(warning)

	// 6. What's next.
	home := p.HomeDir
	shown := tilde(home, dir)
	staged := exec.Command("git", "-C", dir, "diff", "--cached", "--quiet").Run() != nil
	// Only host add's own files: anything else the person has staged in the
	// overlay stays out of this commit.
	mine := []string{"flake.nix"}
	if fileExists(filepath.Join(dir, hostPath)) {
		mine = append(mine, hostPath)
	}
	if withSops {
		mine = append(mine, ".sops.yaml")
	}
	// Each git through gitHere: on a fresh NixOS, git was setup's own.
	git := a.gitHere()
	keep := fmt.Sprintf("%s -C %s commit -m 'Add %s' -- %s && %s -C %s push", git, shown, key, strings.Join(mine, " "), git, shown)
	if !ready && !withSops {
		a.warn(fmt.Sprintf("This machine can't read overlays/%s's secrets, and there's no .sops.yaml to add its key to: on a machine that can read them, piste secrets init in overlays/%s makes one; push it, then pull here and run piste host add again.", name, name))
		if staged {
			fmt.Fprintln(a.Stdout, "  To keep it: "+keep)
		}
		return true, false, nil
	}
	if !ready {
		var others []string
		for _, h := range slices.Concat(hosts.Darwin, hosts.Home, hosts.NixOS) {
			if h != key && !slices.Contains(others, h) {
				others = append(others, h)
			}
		}
		where := "a machine that can read them"
		if len(others) > 0 {
			where = strings.Join(others, " or ")
		}
		first := keep
		if !staged {
			first = fmt.Sprintf("%s -C %s push    # if you haven't", git, shown)
		}
		a.warn(fmt.Sprintf("This machine can't read overlays/%s's secrets yet. To finish:", name))
		for _, l := range []string{
			"  1. here:   " + first,
			fmt.Sprintf("  2. on %s, in its overlays/%s:", where, name),
			fmt.Sprintf("             git pull && piste secrets updatekeys && git commit -m 'Secrets for %s' -- '*.sops.yaml' && git push", key),
			fmt.Sprintf("  3. here:   cd %s && %s -C overlays/%s pull && %s setup", tilde(home, root), git, name, a.pisteHere()),
		} {
			fmt.Fprintln(a.Stdout, l)
		}
		return true, false, nil
	}
	if withSops {
		a.success("This machine can read the secrets.")
	}
	if staged {
		fmt.Fprintln(a.Stdout, "  To keep it: "+keep)
	}
	return true, true, nil
}

// nixosHostName is the name of this NixOS machine's host: named (--name)
// when given, else its hostname. A hostname that can't be one
// (overlay.CheckNixOSName: a fresh machine's nixos, one with dots) is asked
// for at a terminal, and refused elsewhere. chosen: the person gave the
// name (--name, or an answer), rather than it being the hostname.
func (a *App) nixosHostName(hostname, named, system string) (name string, chosen bool, err error) {
	check := a.nixosNameCheck(system)
	if named != "" {
		err := check(named)
		if overlay.GenericNixOSName(named) {
			err = overlay.CheckHostname(named, system, true) // said without "with --name"
		}
		if err != nil {
			a.errorf(fmt.Sprintf("%v (--name)", err))
			return "", false, ExitError{1}
		}
		return named, true, nil
	}
	why := check(hostname)
	if why == nil {
		return hostname, false, nil
	}
	if !overlay.GenericNixOSName(hostname) {
		why = fmt.Errorf("this machine's hostname %q: %v: give this machine its own name with --name", hostname, why)
	}
	if !a.terminal() {
		a.errorf(why.Error())
		return "", false, ExitError{1}
	}
	v, err := a.askNixOSName(fmt.Sprintf("Its hostname, %s, can't name a NixOS host.", hostname), check, why.Error())
	return v, true, err
}

// nixosNameCheck checks a NixOS host's name for a host of system.
func (a *App) nixosNameCheck(system string) func(string) error {
	return func(n string) error {
		if err := overlay.CheckNixOSName(n); err != nil {
			return err
		}
		return overlay.CheckHostname(n, system, false)
	}
}

// askNixOSName asks the person at a terminal for this machine's NixOS host
// name, checked by check: in a form, under desc; else as a prompt after
// why, a warning (its first line; the rest under it).
func (a *App) askNixOSName(desc string, check func(string) error, why ...string) (string, error) {
	var v string
	if a.useForms() {
		err := a.form(huh.NewForm(huh.NewGroup(huh.NewInput().Title("A name for this machine").
			Description(desc + " The name you give is its host's in the overlay, and its hostname from now on.").
			Value(&v).Validate(check))))
		if errors.Is(err, errStopped) {
			a.errorf("Stopped: nothing written.")
			return "", ExitError{1}
		}
		return v, err
	}
	a.warnLines(why)
	v, err := a.prompter().askCheck("A name for this machine (the NixOS host's, and its hostname from now on)", "", check)
	if err != nil {
		a.errorf(err.Error())
		return "", ExitError{1}
	}
	return v, nil
}

// rootVerdict is whether this machine is an overlay's NixOS host, by root
// filesystem (judgeRoot).
type rootVerdict int

const (
	rootSame    rootVerdict = iota // this machine's
	rootOther                      // another machine's
	rootUnknown                    // piste can't tell
)

// judgeRoot compares a NixOS host's root device (fileSystems."/".device,
// "" when it declares none) with this machine's running root (findmnt's
// source, "" when unknown), both resolved by resolve, as the switch
// preflight's machine rule does. Resolving elsewhere is another machine's.
// Only a match by uuid or partition uuid (/dev/disk/by-uuid/…,
// /dev/disk/by-partuuid/…) is this machine's: by anything else, a label,
// a device name (/dev/vda1, by-id, by-path, /dev/mapper/…) or a ZFS
// dataset, every machine made from one image (a clone) has the same. A
// host with no root, a container (whose root is its host's to give) and an
// unknown running root can't be compared. why says why piste can't tell.
func judgeRoot(hostRoot, running string, inContainer bool, resolve func(string) string) (v rootVerdict, why string) {
	switch {
	case hostRoot == "":
		return rootUnknown, `its configuration sets no fileSystems."/"`
	case inContainer:
		return rootUnknown, "this machine is a container"
	case running == "":
		return rootUnknown, "this machine's root filesystem is unknown"
	case resolve(hostRoot) != resolve(running):
		return rootOther, ""
	case strings.Contains(hostRoot, "/disk/by-uuid/") || strings.Contains(hostRoot, "/disk/by-partuuid/"):
		return rootSame, ""
	case strings.Contains(hostRoot, "/disk/by-label/") || strings.Contains(hostRoot, "/disk/by-partlabel/"):
		return rootUnknown, fmt.Sprintf("its root, %s, is a label every machine made from one image has", hostRoot)
	}
	return rootUnknown, fmt.Sprintf("its root, %s, is a name every machine made from one image has", hostRoot)
}

// uuidLink is dev's link in dir (/dev/disk/by-uuid): the one that resolves
// to the same device, the name a host's fileSystems."/" must give for
// judgeRoot to know it as this machine's; dev as it is when none does (a
// ZFS dataset, a filesystem without a uuid, no such directory).
func uuidLink(dir, dev string) string {
	want := resolveDevice(dev)
	es, err := os.ReadDir(dir)
	if err != nil || !filepath.IsAbs(want) {
		return dev
	}
	for _, e := range es {
		if l := filepath.Join(dir, e.Name()); resolveDevice(l) == want {
			return l
		}
	}
	return dev
}

// hostOwner is whose an overlay's NixOS host is, by whoseHost: the verdict,
// and, unless it is this machine's, why piste can't say it is (for
// another machine's, the two roots), the refusal (its first line, then any
// under it: for another machine's, what to change if it is this one
// reinstalled) and a form's description for asking another name.
type hostOwner struct {
	verdict rootVerdict
	why     string
	refusal []string
	ask     string
}

// whoseHost says whether the overlay at dir's NixOS host name is this
// machine: its root filesystem, read by the preflight's facts eval (as
// builds evaluate it, the engine override) for user, against this
// machine's (findmnt), by judgeRoot. An eval that fails can't tell.
func (a *App) whoseHost(dir, name, user, home string) hostOwner {
	unknown := func(why string) hostOwner {
		return hostOwner{verdict: rootUnknown, why: why,
			refusal: []string{fmt.Sprintf("%s is already a host in this overlay, and piste can't tell whether it is this machine (%s): if it is, run %s host add --name %s; else give this machine its own name with --name", name, why, a.piste(), name)},
			ask:     fmt.Sprintf("%s is already a host in this overlay, and piste can't tell whether it is this machine (%s): give %s again if it is.", name, why, name)}
	}
	o, err := platform.ResolveEngineOverride(dir, a.Env.Getenv, home)
	if err != nil {
		return unknown(err.Error())
	}
	ctx := context.Background()
	f, err := evalFacts(ctx, a.runner(), preflightArgv(dir, name, user, o.Args()))
	if err != nil {
		return unknown("can't evaluate its root filesystem: " + err.Error())
	}
	inContainer := fileExists(platform.ContainerMarker)
	running := ""
	if f.RootDevice != "" && !inContainer {
		res, err := a.runner().Run(ctx, execx.Cmd{Argv: []string{"findmnt", "-no", "SOURCE", "/"}, Timeout: 10 * time.Second})
		if err == nil {
			running = rootSource(string(res.Stdout))
		}
	}
	switch v, why := judgeRoot(f.RootDevice, running, inContainer, resolveDevice); v {
	case rootOther:
		roots := fmt.Sprintf("its root is %s, this machine's is %s", f.RootDevice, running)
		other := fmt.Sprintf("%s is already another machine's host in this overlay (%s)", name, roots)
		// By its uuid link: by findmnt's name (/dev/vda1) the next host add
		// still couldn't tell it is this machine.
		reinstalled := fmt.Sprintf("If this is %s reinstalled, update fileSystems in hosts/%s/ to this machine's root (%s) instead.",
			name, name, uuidLink(platform.DiskByUUID, running))
		return hostOwner{verdict: rootOther, why: roots,
			refusal: []string{other + ": give this machine its own name with --name", "  " + reinstalled},
			ask:     other + ". " + reinstalled}
	case rootUnknown:
		return unknown(why)
	}
	return hostOwner{verdict: rootSame}
}

// terminal: a person is at a terminal to answer a question, as a form or a
// plain prompt (NO_COLOR, a screen reader).
func (a *App) terminal() bool {
	return a.useForms() || a.Stdin == io.Reader(os.Stdin) && interactive(a.Stdout)
}

// carriedMachine is what a NixOS host made on this machine takes from it:
// its /etc/nixos, carried (files by path relative to it; nil when it has no
// configuration.nix, and the template's machine file stays), what couldn't
// be carried whole (notes), and the SSH keys that may log in as its user.
type carriedMachine struct {
	files map[string][]byte
	notes []string
	keys  []string
}

// carryMachine reads this machine's /etc/nixos (platform.MachineEtc) and
// the keys in home's ~/.ssh/authorized_keys.
func carryMachine(home string) (carriedMachine, error) {
	c := carriedMachine{keys: authorizedKeys(home)}
	files, notes, err := carry.Carry(platform.MachineEtc)
	if errors.Is(err, carry.ErrNoConfiguration) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	c.files, c.notes = files, notes
	return c, nil
}

// authorizedKeys are the key lines of home's ~/.ssh/authorized_keys, as
// they are (options and comment kept), no comments or blank lines; none
// when it can't be read.
func authorizedKeys(home string) []string {
	b, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		return nil
	}
	var keys []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			keys = append(keys, l)
		}
	}
	return keys
}

// carryInto puts a NixOS host's carried /etc/nixos (files by path relative
// to it) in the host's files, in place of the template's machine/ under
// hostDir; with none carried, the template's stays.
func carryInto(files map[string][]byte, hostDir string, carried map[string][]byte) {
	if carried == nil {
		return
	}
	mdir := filepath.Join(hostDir, "machine")
	for rel := range files {
		if strings.HasPrefix(rel, mdir+string(filepath.Separator)) {
			delete(files, rel)
		}
	}
	for rel, b := range carried {
		files[filepath.Join(mdir, filepath.FromSlash(rel))] = b
	}
}

// sayCarried says what a NixOS host at hostDir took from this machine: its
// /etc/nixos (or that it had none: the template's stand-in is no real
// machine's), what of it needs the person (the "# piste:" markers), that
// /etc/nixos stays and isn't what piste builds, and the SSH keys for user.
func (a *App) sayCarried(c carriedMachine, hostDir, user string) {
	mdir := filepath.Join(hostDir, "machine")
	if c.files == nil {
		a.warn(templateMachine(hostDir))
	} else {
		a.info(fmt.Sprintf("This machine's /etc/nixos is in %s%c", mdir, filepath.Separator))
	}
	if len(c.notes) > 0 {
		a.warn(`Needs you (marked "# piste:" in the files):`)
		for _, n := range c.notes {
			fmt.Fprintln(a.Stdout, "  "+n)
		}
	}
	if c.files != nil {
		// A plain nixos-rebuild switch never builds the overlay: before the
		// first switch it builds /etc/nixos (the system the overlay
		// replaces), after it finds no /etc/nixos either (a flake-built
		// system's NIX_PATH has no nixos-config).
		fmt.Fprintln(a.Stdout, "/etc/nixos stays on this machine; piste switch builds this overlay, and a plain nixos-rebuild switch doesn't.")
	}
	host := filepath.Join(hostDir, "default.nix")
	if len(c.keys) == 0 {
		a.warn(fmt.Sprintf("No keys in ~/.ssh/authorized_keys: add the ones you log in with to users.users.%s.openssh.authorizedKeys.keys in %s before you switch (the switch turns SSH password logins off)", user, host))
		return
	}
	a.info(fmt.Sprintf("%s from ~/.ssh/authorized_keys may log in as %s (%s)", plural(len(c.keys), "SSH key"), user, host))
}

// templateMachine says the NixOS host at hostDir has the template's stand-in
// machine (no /etc/nixos/configuration.nix to carry): no real machine's.
func templateMachine(hostDir string) string {
	return fmt.Sprintf("No /etc/nixos/configuration.nix here: %s is the template's VM; check its boot loader and root filesystem before switching.",
		filepath.Join(hostDir, "machine", "configuration.nix"))
}

// setHostname sets this machine's running hostname to a NixOS host's name
// (sudo hostname), so the first switch finds its host by it. kept: the
// host's networking.hostName is name (checkHostName passed), so the switch
// makes it permanent, and says so; else only "for now", and the warning
// that follows says why. A failure is said, not fatal: the host is added.
func (a *App) setHostname(name string, kept bool) {
	if kept {
		a.info(fmt.Sprintf("Setting this machine's hostname to %s for now; the switch makes it permanent.", name))
	} else {
		a.info(fmt.Sprintf("Setting this machine's hostname to %s for now.", name))
	}
	if rc, err := a.pass([]string{"sudo", "hostname", name}, ""); err != nil || rc != 0 {
		a.warn(fmt.Sprintf("sudo hostname %s didn't: run it before piste switch, which looks for this machine's host by its hostname", name))
	}
}

// hostNameFacts is what checkHostName reads of a NixOS host's config: its
// networking.hostName (nil when the eval's output has none), whether a
// Proxmox container's module forces it to "" (proxmoxLXC on, managing
// neither the network nor the hostname), and whether the Proxmox VM
// image's does (proxmox.cloudInit.enable, on by default where the image's
// module is imported: cloud-init names the machine).
type hostNameFacts struct {
	HostName         *string `json:"hostName"`
	ProxmoxLXC       bool    `json:"proxmoxLXC"`
	ProxmoxCloudInit bool    `json:"proxmoxCloudInit"`
}

// hostNameNix reads hostNameFacts from a NixOS configuration's config.
const hostNameNix = `c: { hostName = c.networking.hostName; proxmoxLXC = (c.proxmoxLXC.enable or false) && !(c.proxmoxLXC.manageNetwork or false) && !(c.proxmoxLXC.manageHostName or false); proxmoxCloudInit = c.proxmox.cloudInit.enable or false; }`

// checkHostName evaluates the networking.hostName of the overlay at dir's
// NixOS host name, as builds evaluate it (the engine override): the
// warning (warnLines) when the switch wouldn't name this machine name
// (hostNameWarning: an image's module can leave it to the platform, and
// piste finds the host by the hostname; hostFile is where the fix goes),
// and kept, the switch would. An eval that fails, or says no hostName, is
// left to the build: no warning, and not kept.
func (a *App) checkHostName(dir, home, name, hostFile string) (warning []string, kept bool) {
	o, err := platform.ResolveEngineOverride(dir, a.Env.Getenv, home)
	if err != nil {
		return nil, false
	}
	argv := append([]string{"nix", "eval", "--json", dir + `#nixosConfigurations."` + name + `".config`}, o.Args()...)
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: append(argv, "--apply", hostNameNix, "--no-write-lock-file")})
	if err != nil {
		return nil, false
	}
	var f hostNameFacts
	if json.Unmarshal(res.Stdout, &f) != nil || f.HostName == nil {
		return nil, false
	}
	warning = hostNameWarning(name, hostFile, f)
	return warning, warning == nil
}

// warnLines prints a warning of several lines: the first as a warning, the
// rest under it.
func (a *App) warnLines(lines []string) {
	for i, l := range lines {
		if i == 0 {
			a.warn(l)
		} else {
			fmt.Fprintln(a.Stdout, l)
		}
	}
}

// hostNameWarning is checkHostName's warning for the host name whose config
// sets networking.hostName as f says (none when it is name): what the name
// is instead, then the setting in hostFile that keeps name. Never a
// lib.mkForce: the Proxmox image's own is mkForce "", and two clash (the
// build fails); mkOverride 49 is stronger than either.
func hostNameWarning(name, hostFile string, f hostNameFacts) []string {
	got := ""
	if f.HostName != nil {
		got = *f.HostName
	}
	if got == name {
		return nil
	}
	override := fmt.Sprintf(`  To keep %s: set networking.hostName = lib.mkOverride 49 "%s"; in %s (lib among its arguments).`, name, name, hostFile)
	if got != "" {
		return []string{
			fmt.Sprintf("The switch won't name this machine %s: its networking.hostName is %q, and piste finds this machine's host by its hostname.", name, got),
			override,
		}
	}
	first := fmt.Sprintf(`The switch won't name this machine %s: its networking.hostName is "" (a module the host imports leaves the name to Proxmox, cloud-init or DHCP), and piste finds this machine's host by its hostname.`, name)
	switch {
	case f.ProxmoxLXC:
		return []string{first, fmt.Sprintf("  To keep %s: set proxmoxLXC.manageHostName = true; in %s, or name the container %s in Proxmox.", name, hostFile, name)}
	case f.ProxmoxCloudInit:
		return []string{first, fmt.Sprintf("  To keep %s: name the VM %s in Proxmox (cloud-init sets the hostname from it), or set proxmox.cloudInit.enable = false; in %s "+
			"(cloud-init then configures no network: only for a VM that gets its address by DHCP).", name, name, hostFile)}
	}
	return []string{first, override}
}

// addHostFiles writes the new host's files (by path relative to dir) and
// stages them (Nix sees tracked files only), then the edited flake.nix; a
// failure takes them back.
func (a *App) addHostFiles(dir string, files map[string][]byte, flakePath string, flake, newFlake []byte) error {
	rels := slices.Sorted(maps.Keys(files))
	for i, rel := range rels {
		path := filepath.Join(dir, rel)
		err := os.MkdirAll(filepath.Dir(path), 0o755)
		if err == nil {
			err = os.WriteFile(path, files[rel], 0o644)
		}
		if err != nil {
			a.removeHostFiles(dir, rels[:i+1])
			return err
		}
	}
	if err := a.gitIn(dir, append([]string{"add", "--"}, rels...)...); err != nil {
		a.removeHostFiles(dir, rels)
		return err
	}
	if err := os.WriteFile(flakePath, newFlake, 0o644); err != nil {
		a.undoHostFiles(dir, files, flakePath, flake)
		return err
	}
	return nil
}

// undoHostFiles puts flake.nix back and unstages and removes the host's
// files.
func (a *App) undoHostFiles(dir string, files map[string][]byte, flakePath string, flake []byte) {
	if err := os.WriteFile(flakePath, flake, 0o644); err != nil {
		a.errorf(fmt.Sprintf("couldn't put flake.nix back: %v", err))
	}
	rels := slices.Sorted(maps.Keys(files))
	rm := exec.Command("git", append([]string{"-C", dir, "rm", "-q", "--cached", "--ignore-unmatch", "--"}, rels...)...)
	if out, err := rm.CombinedOutput(); err != nil {
		a.errorf(fmt.Sprintf("git rm --cached %s: %v: %s", strings.Join(rels, " "), err, strings.TrimSpace(string(out))))
	}
	a.removeHostFiles(dir, rels)
}

// removeHostFiles removes the host's files at rels (relative to dir), and
// the directories under hosts/ they leave empty (a NixOS host's).
func (a *App) removeHostFiles(dir string, rels []string) {
	for _, rel := range rels {
		if err := os.Remove(filepath.Join(dir, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
			a.errorf(err.Error())
		}
	}
	for _, rel := range rels {
		for d := filepath.Dir(rel); d != "." && d != "hosts"; d = filepath.Dir(d) {
			if os.Remove(filepath.Join(dir, d)) != nil {
				break
			}
		}
	}
}

// block prints text indented, for the person to copy.
func (a *App) block(text string) {
	for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if l == "" {
			fmt.Fprintln(a.Stdout)
			continue
		}
		fmt.Fprintln(a.Stdout, "    "+l)
	}
}

// tilde is path with home written as ~.
func tilde(home, path string) string {
	if home != "" {
		if r, err := filepath.Rel(home, path); err == nil && r != ".." && !strings.HasPrefix(r, "../") {
			if r == "." {
				return "~"
			}
			return "~/" + r
		}
	}
	return path
}
