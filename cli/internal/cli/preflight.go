package cli

// The switch preflight. Before a NixOS switch activates, piste evaluates the
// new system (preflight.nix) and refuses one that would lock the person out:
// an SSH session left with no SSH server, no key or (as root) root's login
// off, sudo taken away, no boot loader, or another machine's root filesystem. preflight decides from what the eval
// and the running machine say; runPreflight gathers both. --force skips it.

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/platform"
)

// preflightNix is the expression the eval applies to the new system's config.
//
//go:embed preflight.nix
var preflightNix string

// facts is what preflight.nix reads from the new system config, as JSON.
// KbdInteractive is read but isn't password login: NixOS's sshd PAM takes a
// password only with PasswordAuthentication on.
type facts struct {
	SSHEnabled                                                           bool // services.openssh.enable
	PasswordAuth, KbdInteractive, KeysInHomedir                          bool
	Keys, KeyFiles                                                       int
	PermitRootLogin                                                      string // services.openssh.settings.PermitRootLogin, "" when null (sshd's default)
	Wheel, SudoEnabled, WheelNeedsPassword, NopasswdForUser, HasPassword bool
	BootLoader, IsContainer                                              bool
	RootDevice                                                           string // fileSystems."/".device, "" when undefined
}

// machine is what piste reads from the running machine. User is the
// invoking user, whom the rules protect; OverlayUser, the overlay's user
// (its flake.nix's, "" when unknown), whom root is told to switch as; Piste,
// how to run piste here, for the --force advice.
type machine struct {
	User, OverlayUser, Piste                            string
	SSHSession, HomeKeys, SudoPasswordless, InContainer bool
	RunningRoot                                         string // findmnt -no SOURCE /, as findmnt names it
}

// refusal is one rule's refusal. Rule: "ssh", "sudo", "boot", "machine".
type refusal struct{ Rule, Message string }

// preflight is the rules, in that order: each refuses only when what it
// protects is at stake here. resolve resolves a device path's links.
func preflight(f facts, m machine, resolve func(string) string) []refusal {
	var r []refusal
	u := m.User
	// With the server off, keys don't matter: its line alone. Nor do they
	// for root with root's login off (the base's "no"): an image reached
	// as root, switched as root, would let no one in as root again.
	switch {
	case m.SSHSession && !f.SSHEnabled:
		r = append(r, refusal{"ssh", "you're connected over SSH and the new config turns the SSH server off: keep services.openssh.enable = true"})
	case m.SSHSession && u == "root" && (f.PermitRootLogin == "no" || f.PermitRootLogin == "forced-commands-only"):
		as := m.OverlayUser
		if as == "" || as == "root" {
			as = "your overlay's user"
		}
		r = append(r, refusal{"ssh", "you're connected over SSH as root and the new config turns root's SSH login off: " +
			`set services.openssh.settings.PermitRootLogin = "prohibit-password"; or switch as ` + as})
	case m.SSHSession && !f.PasswordAuth && f.Keys+f.KeyFiles == 0 && !(f.KeysInHomedir && m.HomeKeys):
		r = append(r, refusal{"ssh", fmt.Sprintf("you're connected over SSH and the new config gives %s no SSH keys: "+
			"add users.users.%s.openssh.authorizedKeys.keys to your host", u, u)})
	}
	// Run as root, the switch needs no sudo: root's own sudo isn't at
	// stake.
	switch {
	case u == "root":
	case !f.Wheel || !f.SudoEnabled:
		r = append(r, refusal{"sudo", fmt.Sprintf(`the new config takes sudo away from %s: keep "wheel" in users.users.%s.extraGroups`, u, u)})
	// The image-built VM whose user never had a password: after the switch,
	// sudo, rollback and the undo would wait for one that doesn't exist.
	case m.SudoPasswordless && f.WheelNeedsPassword && !f.NopasswdForUser && !f.HasPassword:
		r = append(r, refusal{"sudo", fmt.Sprintf("sudo needs no password today, and the new config would ask for one %s doesn't have: "+
			"set security.sudo.wheelNeedsPassword = false or give the user a password (if you do have one, run %s switch --force)", u, m.Piste)})
	}
	if !f.BootLoader && !f.IsContainer {
		r = append(r, refusal{"boot", "the new config enables no boot loader: carry over your machine's boot.loader settings (piste host add does)"})
	}
	// A container's root is its host's; a config with no fileSystems."/"
	// gets one from a virtualisation module; an unknown running root can't
	// be compared.
	if f.RootDevice != "" && !m.InContainer && m.RunningRoot != "" && resolve(f.RootDevice) != resolve(m.RunningRoot) {
		r = append(r, refusal{"machine", fmt.Sprintf("the new config's root filesystem is %s, this machine's is %s: "+
			"this host's config looks like another machine's", f.RootDevice, m.RunningRoot)})
	}
	return r
}

// runPreflight evaluates t's new system with engine override o, reads this
// machine, and returns the rules' refusals. An error is an eval that failed
// (its stderr's tail) or gave no facts.
func (a *App) runPreflight(p platform.Platform, t target, o platform.EngineOverride) ([]refusal, error) {
	ctx := context.Background()
	f, err := evalFacts(ctx, a.runner(), preflightArgv(p.FlakeDir, t.Name, p.User, o.Args()))
	if err != nil {
		return nil, err
	}
	m := machine{
		User:        p.User,
		OverlayUser: flakeUser(p.FlakeDir),
		Piste:       a.piste(),
		SSHSession:  sshSession(a.Env.Getenv),
		HomeKeys:    homeKeys(p.HomeDir),
		InContainer: fileExists(platform.ContainerMarker),
	}
	// Probed only when a rule reads them: sudo, only when the new config
	// would ask a password the user has none for (each sudo run is a line
	// in the auth log), and never for root, whom the sudo rule leaves out;
	// the root, only when there is one to compare. The root is kept as
	// findmnt names it (/dev/mapper/root, not the /dev/dm-0 behind it), for
	// the message: the rule compares the two resolved.
	if f.WheelNeedsPassword && !f.NopasswdForUser && !f.HasPassword && p.User != "root" {
		// -k: a password typed a minute ago (sudo's cached credentials)
		// isn't sudo without one.
		_, err := a.runner().Run(ctx, execx.Cmd{Argv: []string{"sudo", "-n", "-k", "true"}, Timeout: 10 * time.Second})
		m.SudoPasswordless = err == nil
	}
	if f.RootDevice != "" && !m.InContainer {
		res, err := a.runner().Run(ctx, execx.Cmd{Argv: []string{"findmnt", "-no", "SOURCE", "/"}, Timeout: 10 * time.Second})
		if err == nil {
			m.RunningRoot = rootSource(string(res.Stdout))
		}
	}
	return preflight(f, m, resolveDevice), nil
}

// preflightArgv evaluates preflight.nix for user against flakeDir's NixOS
// host name, with the switch's override args: only the options it reads,
// never the system's build, and no lock file written.
func preflightArgv(flakeDir, name, user string, override []string) []string {
	argv := append([]string{"nix", "eval", "--json", flakeDir + `#nixosConfigurations."` + name + `".config`}, override...)
	return append(argv, "--apply", "("+nixOneLine(preflightNix)+") "+nixString(user), "--no-write-lock-file")
}

// evalFacts runs the eval argv and reads its facts.
func evalFacts(ctx context.Context, r execx.Runner, argv []string) (facts, error) {
	res, err := r.Run(ctx, execx.Cmd{Argv: argv})
	if err != nil {
		why := stderrTail(res.Stderr, 3)
		if why == "" {
			why = err.Error()
		}
		return facts{}, errors.New(why)
	}
	var f facts
	if err := json.Unmarshal(res.Stdout, &f); err != nil {
		return facts{}, fmt.Errorf("nix eval's output: %w", err)
	}
	return f, nil
}

// nixOneLine is a Nix expression without its full-line comments, on one line
// (an argv entry the calls log and an error show whole).
func nixOneLine(nix string) string {
	var keep []string
	for _, l := range strings.Split(nix, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, " ")
}

// nixString is s as a Nix string literal: nothing in it interpolates.
func nixString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(s) + `"`
}

// sshSession: this session is over SSH, by any variable sshd sets for it:
// SSH_CONNECTION, else SSH_CLIENT (an environment that kept only that one)
// or SSH_TTY (a login with a terminal).
func sshSession(getenv func(string) string) bool {
	for _, v := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if getenv(v) != "" {
			return true
		}
	}
	return false
}

// homeKeys: ~/.ssh/authorized_keys is a regular file (a link into the store
// fails sshd's StrictModes) with a key line. A line with options before its
// key isn't counted: the check errs toward refusing.
func homeKeys(home string) bool {
	if home == "" {
		return false
	}
	path := filepath.Join(home, ".ssh", "authorized_keys")
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "ssh-") || strings.HasPrefix(l, "ecdsa-") || strings.HasPrefix(l, "sk-") {
			return true
		}
	}
	return false
}

// rootSource is the device in findmnt -no SOURCE /'s output: the last
// (visible) mount's, without a btrfs subvolume's [/path].
func rootSource(out string) string {
	srcs := strings.Fields(out)
	if len(srcs) == 0 {
		return ""
	}
	src := srcs[len(srcs)-1]
	if i := strings.IndexByte(src, '['); i > 0 && strings.HasSuffix(src, "]") {
		src = src[:i]
	}
	return src
}

// resolveDevice is an absolute device path with its links resolved
// (/dev/disk/by-uuid/…, by-label, /dev/mapper/…), else dev as it is: a ZFS
// dataset, "none", a path that isn't on this machine.
func resolveDevice(dev string) string {
	if !filepath.IsAbs(dev) {
		return dev
	}
	if r, err := filepath.EvalSymlinks(dev); err == nil {
		return r
	}
	return dev
}
