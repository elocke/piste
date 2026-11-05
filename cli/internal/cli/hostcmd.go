package cli

// piste host add: the machine piste runs on becomes a host of an
// overlay made elsewhere. Its host file and hosts entry come from the
// checkout's template (what's the person's on every machine is in the
// overlay's home.nix); with sops it gets its own age key, which goes into
// .sops.yaml, and a machine that can already decrypt re-encrypts for it
// (piste secrets updatekeys). piste stages; it never commits or pushes.

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/overlay"
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
	c := &cobra.Command{
		Use:   "add",
		Short: "Make this machine a host of an overlay, with its own age key if the overlay keeps secrets",
		Long: "Make the machine you're on a host of an overlay: the one you're in, the one\n" +
			"--overlay names, or the only one. Its host file (hosts/<hostname>.nix, from the\n" +
			"engine's template) and its entry in flake.nix are staged, and it is built. What's\n" +
			"yours on every machine comes from the overlay's home.nix.\n\n" +
			"Its roles are the ones the overlay's other hosts of its kind (Mac or Linux) have,\n" +
			"extras included (the ones they all have, if they differ); with no other host of\n" +
			"its kind, init's defaults. --role gives it others.\n\n" +
			"If the overlay keeps secrets (.sops.yaml), this machine gets an age key, made if\n" +
			"missing, and its public key goes into .sops.yaml. This machine can't read the\n" +
			"secrets until a machine that can runs piste secrets updatekeys; the steps are\n" +
			"printed. piste stages; it never commits or pushes. Run again, it skips each\n" +
			"step already done.\n\n" +
			"To remove a machine, by hand: delete its entry in flake.nix and its host file,\n" +
			"remove its key from .sops.yaml (keys: and every age list), run piste secrets\n" +
			"updatekeys (it also makes a new data key), and change the secrets' values:\n" +
			"that machine has read them. If it had a GitHub key (piste github), revoke it:\n" +
			"gh ssh-key list, then gh ssh-key delete <id>.",
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
			_, _, err := a.hostAdd(r, ex)
			return err
		},
	}
	c.Flags().StringSliceVar(&roles, "role", nil, "a role: base, dev, desktop or extras (repeatable; default: the roles of the overlay's other hosts of this kind, else dev on Linux, none on macOS)")
	c.Flags().BoolVar(&extras, "extras", false, "add the extras role to the roles, as piste init --extras does (--extras=false leaves it out)")
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
// the overlay's other hosts of its kind (Mac or Linux), what they all have;
// with none of its kind, or their roles unreadable (said), defaultRoles.
func (a *App) inheritRoles(name, dir, user, system string, hosts workspace.Hosts) []string {
	darwin := overlay.Darwin(system)
	others, kind := hosts.Home, "Linux"
	if darwin {
		others, kind = hosts.Darwin, "Mac"
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
	got, err := a.hostRoles(dir, user, darwin)
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
// extras, when set, adds the extras role to them or takes it out. built: the
// build ran and passed; ready: this machine can read the overlay's secrets
// (or it has none). A refusal writes nothing.
func (a *App) hostAdd(roles []string, extras *bool) (built, ready bool, err error) {
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
	if err := overlay.CheckHostname(q.Hostname, p.System); err != nil {
		return refuse("this machine's " + err.Error())
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
	have := workspace.Matches(hosts, q)
	rel := filepath.Join("hosts", q.Hostname+".nix")
	var hostFile []byte
	var entry string
	var newFlake []byte
	if !have {
		// The cheap refusals come first: the roles other hosts take cost an
		// eval (and a line of output) that an error would waste. What they
		// print for adding by hand has the roles known without it.
		render := func(rs []string) error {
			ans := overlay.Answers{
				Name: name, User: q.User, Hostname: q.Hostname, System: p.System, Roles: rs,
				Home:     linuxHome(q.User, q.User, p.HomeDir, p.System),
				Checkout: overlay.CheckoutExpr(root, p.HomeDir),
			}
			var rerr error
			hostFile, entry, rerr = overlay.RenderHost(filepath.Join(root, "templates", "default"), ans)
			return rerr
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
			fmt.Fprintf(a.Stdout, "and %s, which imports ../home.nix (what's yours on every machine, from your other host's file):\n", rel)
			a.block(string(hostFile))
			fmt.Fprintln(a.Stdout, "Then run piste host add again: it does the rest.")
			return false, false, ExitError{1}
		}
		if fileExists(filepath.Join(dir, rel)) {
			a.errorf(fmt.Sprintf("overlays/%s/%s exists, but the overlay's hosts don't have %s: add its entry to flake.nix by hand, or move the file away. The entry:", name, rel, key))
			a.block(entry)
			return false, false, ExitError{1}
		}
		if _, err := overlay.AddHostEntry(flake, entry); err != nil {
			a.errorf(fmt.Sprintf("overlays/%s/flake.nix has no hosts line to add %s beside: add its entry to the hosts by hand:", name, key))
			a.block(entry)
			return false, false, ExitError{1}
		}
		if !given {
			roles = a.inheritRoles(name, dir, owner, p.System, hosts)
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
		if err := a.addHostFiles(dir, rel, hostFile, flakePath, flake, newFlake); err != nil {
			return false, false, err
		}
		after, err := a.overlayHosts(dir)
		if err != nil || !workspace.Matches(after, q) {
			why := "its hosts aren't a set piste can add to"
			if err != nil {
				why = err.Error()
			}
			a.undoHostFiles(dir, rel, flakePath, flake)
			a.errorf(fmt.Sprintf("nix eval of overlays/%s doesn't list %s after adding its entry (%s): flake.nix is back as it was and %s removed. Add the entry to its hosts by hand:", name, key, why, rel))
			a.block(entry)
			return false, false, ExitError{1}
		}
		hosts = after
		if err := a.gitIn(dir, "add", "--", "flake.nix"); err != nil {
			return false, false, err
		}
		shown := "none"
		if len(roles) > 0 {
			shown = strings.Join(roles, ", ")
		}
		a.success(fmt.Sprintf("Added %s (roles: %s) to overlays/%s: %s and its entry in flake.nix, staged", key, shown, name, rel))
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
	a.Overlay = name
	if err := a.runBuild(q.Hostname); err != nil {
		a.warn(fmt.Sprintf("overlays/%s: this machine was added; fix the build and run: %s build --overlay %s", name, a.piste(), name))
		return false, ready, err
	}

	// 6. What's next.
	home := p.HomeDir
	shown := tilde(home, dir)
	staged := exec.Command("git", "-C", dir, "diff", "--cached", "--quiet").Run() != nil
	// Only host add's own files: anything else the person has staged in the
	// overlay stays out of this commit.
	mine := []string{"flake.nix"}
	if fileExists(filepath.Join(dir, rel)) {
		mine = append(mine, rel)
	}
	if withSops {
		mine = append(mine, ".sops.yaml")
	}
	keep := fmt.Sprintf("git -C %s commit -m 'Add %s' -- %s && git -C %s push", shown, key, strings.Join(mine, " "), shown)
	if !ready && !withSops {
		a.warn(fmt.Sprintf("This machine can't read overlays/%s's secrets, and there's no .sops.yaml to add its key to: on a machine that can read them, piste secrets init in overlays/%s makes one; push it, then pull here and run piste host add again.", name, name))
		if staged {
			fmt.Fprintln(a.Stdout, "  To keep it: "+keep)
		}
		return true, false, nil
	}
	if !ready {
		var others []string
		for _, h := range append(append([]string{}, hosts.Darwin...), hosts.Home...) {
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
			first = fmt.Sprintf("git -C %s push    # if you haven't", shown)
		}
		a.warn(fmt.Sprintf("This machine can't read overlays/%s's secrets yet. To finish:", name))
		for _, l := range []string{
			"  1. here:   " + first,
			fmt.Sprintf("  2. on %s, in its overlays/%s:", where, name),
			fmt.Sprintf("             git pull && piste secrets updatekeys && git commit -m 'Secrets for %s' -- '*.sops.yaml' && git push", key),
			fmt.Sprintf("  3. here:   cd %s && git -C overlays/%s pull && %s setup", tilde(home, root), name, pisteCmd(a.pisteOnPath())),
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

// addHostFiles writes the new host's file (staged: Nix sees tracked files
// only) and the edited flake.nix; a failure takes them back.
func (a *App) addHostFiles(dir, rel string, hostFile []byte, flakePath string, flake, newFlake []byte) error {
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, hostFile, 0o644); err != nil {
		return err
	}
	if err := a.gitIn(dir, "add", "--", rel); err != nil {
		os.Remove(path)
		return err
	}
	if err := os.WriteFile(flakePath, newFlake, 0o644); err != nil {
		a.undoHostFiles(dir, rel, flakePath, flake)
		return err
	}
	return nil
}

// undoHostFiles puts flake.nix back and unstages and removes the host file.
func (a *App) undoHostFiles(dir, rel, flakePath string, flake []byte) {
	if err := os.WriteFile(flakePath, flake, 0o644); err != nil {
		a.errorf(fmt.Sprintf("couldn't put flake.nix back: %v", err))
	}
	rm := exec.Command("git", "-C", dir, "rm", "-q", "--cached", "--", rel)
	if out, err := rm.CombinedOutput(); err != nil {
		a.errorf(fmt.Sprintf("git rm --cached %s: %v: %s", rel, err, strings.TrimSpace(string(out))))
	}
	if err := os.Remove(filepath.Join(dir, rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
		a.errorf(err.Error())
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
