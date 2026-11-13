package cli

// switch, rollback and secrets: the commands that change the machine. The
// argv to nix / sudo / darwin-rebuild / home-manager / sops / yq, the exit
// codes and the messages are pinned by cli/e2e/testdata/switch_rollback_secrets.txtar.
// The age key is read and generated with filippo.io/age, so `secrets` needs
// no age-keygen on PATH.

import (
	"bufio"
	"bytes"
	"charm.land/huh/v2"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/flake"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/plugins"
	"github.com/elocke/piste/cli/internal/release"
	"github.com/elocke/piste/cli/internal/ui"
)

func init() {
	register("switch", newSwitch)
	register("rollback", newRollback)
	register("secrets", newSecrets)
}

// --- prompts --------------------------------------------------------------------

// errAborted: the user stopped a prompt ("Aborted!" on stderr, exit 1).
var errAborted = errors.New("aborted")

func (a *App) readLine(in *bufio.Reader, prompt string) (string, error) {
	fmt.Fprint(a.Stdout, prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", errAborted
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// confirm asks a yes/no question, no by default; at a terminal, a yes/no
// form (Ctrl+C aborts).
func (a *App) confirm(prompt string) (bool, error) {
	if a.useForms() {
		ok, err := a.askYesNo(prompt, "Yes", "No", false)
		if errors.Is(err, errStopped) {
			return false, errAborted
		}
		return ok, err
	}
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := a.readLine(in, prompt+" [y/N]: ")
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return false, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(a.Stdout, "Error: invalid input")
	}
}

// promptIndex asks for a number from 1 to n until it gets one.
func (a *App) promptIndex(prompt string, n int) (int, error) {
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := a.readLine(in, prompt+": ")
		if err != nil {
			return 0, err
		}
		v := strings.TrimSpace(line)
		if v == "" {
			continue
		}
		if i, err := strconv.Atoi(v); err == nil && i >= 1 && i <= n {
			return i, nil
		}
		fmt.Fprintf(a.Stdout, "Error: enter a number from 1 to %d.\n", n)
	}
}

// aborted maps errAborted to the "Aborted!" exit.
func (a *App) aborted(err error) error {
	if errors.Is(err, errAborted) {
		fmt.Fprintln(a.Stderr, "Aborted!")
		return ExitError{1}
	}
	return err
}

// interactive: stdin and w are terminals. A var so tests can stand one in.
var interactive = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(f.Fd())
}

// --- switch ------------------------------------------------------------------

// pisteCmd is how to run piste in a hint: "piste" once it is on PATH, before
// that (a first run through `nix run`, piste setup) through the flake of the
// checkout or overlay the hint is read in. flakesOff: this Nix has flakes off
// (a fresh NixOS's), so the nix run form turns them on as setup did for
// itself: in NIX_CONFIG, which reaches the nix calls piste makes too (a
// --extra-experimental-features flag reaches nix run's own only).
func pisteCmd(onPath, flakesOff bool) string {
	switch {
	case onPath:
		return "piste"
	case flakesOff:
		return "NIX_CONFIG='" + flakesConfig + "' nix run .#piste --"
	}
	return "nix run .#piste --"
}

// flakesConfig turns nix-command and flakes on, as a line of NIX_CONFIG.
const flakesConfig = "extra-experimental-features = nix-command flakes"

// nixConfigFlakes: this run has the flakes through NIX_CONFIG, its Nix
// having them off: setup put them there (flakesOff), or the person ran a
// line a run like it printed, which starts with them. The lines this run
// prints pass them on.
func (a *App) nixConfigFlakes() bool {
	if a.flakesOff {
		return true
	}
	if a.Env.Getenv == nil {
		return false
	}
	for _, l := range strings.Split(a.Env.Getenv("NIX_CONFIG"), "\n") {
		if strings.TrimSpace(l) == flakesConfig {
			return true
		}
	}
	return false
}

// pisteHere is pisteCmd for this run, in a line that already has the cd into
// the checkout.
func (a *App) pisteHere() string {
	return pisteCmd(a.pisteOnPath(), a.nixConfigFlakes())
}

// gitHere is how a line printed for this machine runs git: plain git, or
// through nix shell where the person's shell may have none. On a fresh
// NixOS (the flakes in NIX_CONFIG), the git setup ran with was its own nix
// shell's; nix's flag turns the flakes on for that one nix shell, as git
// makes no nix calls.
func (a *App) gitHere() string {
	if a.nixConfigFlakes() {
		return "nix --extra-experimental-features 'nix-command flakes' shell nixpkgs#git --command git"
	}
	if _, err := exec.LookPath("git"); err != nil {
		return "nix shell nixpkgs#git --command git"
	}
	return "git"
}

// piste is pisteCmd for this run. After piste setup moved into the checkout
// from elsewhere, the person's shell is still where they were, so the nix run
// form starts with the cd there. A line that already has that cd uses
// pisteHere.
func (a *App) piste() string {
	if a.movedTo != "" && !a.pisteOnPath() {
		return "cd " + shellPath(a.home(), a.movedTo) + " && " + a.pisteHere()
	}
	return a.pisteHere()
}

// home is $HOME, else the user's home directory.
func (a *App) home() string {
	if h := a.Env.Getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// pisteOnPath: this machine has piste installed (not a first run through
// nix run).
func (a *App) pisteOnPath() bool {
	_, err := exec.LookPath("piste")
	return err == nil
}

// darwinConfigNames is _darwin_config_names, read against the engine builds
// use (engine override o): nil when unavailable.
func (a *App) darwinConfigNames(p platform.Platform, o platform.EngineOverride) ([]string, error) {
	if err := a.checkNix(); err != nil {
		return nil, err
	}
	names, err := flake.ConfigurationNames(context.Background(), a.runner(), p.FlakeDir, "darwinConfigurations", o.Args())
	if err != nil { // includes a non-zero exit
		return nil, nil
	}
	return names, nil
}

func newSwitch(a *App) *cobra.Command {
	var host string
	var noUndo, force bool
	c := &cobra.Command{
		Use:   "switch",
		Short: "Build and apply configuration",
		Long: `Build the configuration and activate it.

When the switch changes the active generation, piste then checks it: a new
login shell (the user database's shell, in a clean environment, with no
terminal, as a new terminal or ssh login starts it) must run
"piste --version" within 30 seconds. If it doesn't, piste puts back the
generation that was active before, with nix-darwin's, NixOS's or
home-manager's own tools, and exits 1; the new generation stays in the
store. It checks and undoes only when the generation before passed the same
check, run just before the switch, so a first switch isn't checked.

On NixOS the switch is sudo nixos-rebuild switch, which needs a terminal for
sudo's password. Before it, piste evaluates the new system and refuses
(exit 1) one that would lock you out: an SSH session it leaves with no SSH
server or no key for you (or, switched as root, root's login turned off),
sudo taken away (no "wheel", or a password asked that you don't have), no
boot loader, or another machine's root filesystem.
--force switches without these lock-out checks.

On Linux, inside a systemd user service (an agent's runner, which the switch
may restart), the switch runs as its own unit, piste-switch-<time>.service:
its output is followed here and kept in
${XDG_STATE_HOME:-~/.local/state}/piste/switch.log. Ctrl-C leaves it running
(systemctl --user stop <unit> stops it). This needs systemd 250 or newer, for
systemd-run to pass your environment on by name; with an older one, run
piste switch outside the service: from a terminal or ssh, or under
systemd-run --user --scope.

--no-undo skips the checks and the undo. To undo a switch that passed:
piste rollback.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.runSwitch(host, noUndo, force); err != nil {
				return err
			}
			// After the switch, not in it: setup's and adopt's switches say
			// nothing.
			if l := release.Check(lastSwitched.Checkout, lastSwitched.FlakeDir); l != "" {
				a.info(l)
			}
			return nil
		},
	}
	hostFlags(c, &host)
	c.Flags().BoolVar(&noUndo, "no-undo", false, "don't check that a new login shell runs piste, and don't undo the switch")
	c.Flags().BoolVar(&force, "force", false, "skip the NixOS lock-out checks")
	return c
}

// lastSwitched is the machine runSwitch last switched: its checkout and
// overlay, for newSwitch's release notice. (A package variable, not an App
// field: App is root.go's, which this change leaves alone.)
var lastSwitched platform.Platform

// lastSwitchedKind is the kind of configuration runSwitch last switched, for
// setup's zsh hint (a home's only); a home when the switch ran as its own
// unit (switchdetach.go), whose piste resolved it.
var lastSwitchedKind Kind

// refuseHostname is the hostname guard's refusal: hostname is none of
// declared, the names of the overlay's attr (darwinConfigurations,
// nixosConfigurations); extra lines follow the advice. Exit 1.
func (a *App) refuseHostname(hostname, attr string, declared []string, extra ...string) error {
	for _, l := range append([]string{
		fmt.Sprintf("Hostname '%s' doesn't match any %s.", hostname, attr),
		"  Declared: " + strings.Join(declared, ", "),
		"",
		"  To build another host's config without activating it:",
		"      piste build --host <name>",
		"  To force activation on this hostname (dangerous — only if you",
		"  really mean to activate a different host's config here):",
		"      piste switch --host <name>",
	}, extra...) {
		a.errorf(l)
	}
	return ExitError{1}
}

// runSwitch is `piste switch` (also offered by `piste setup`): host is the
// hostname to switch to, "" for this machine's.
// Unless noUndo, a switch that changes the active generation is checked and
// undone when a new login shell can't run piste (switchcheck.go). Unless
// force, a NixOS switch that would lock the person out is refused before
// any sudo (preflight.go).
func (a *App) runSwitch(host string, noUndo, force bool) error {
	lastSwitchedKind = KindHome
	// Inside a user service the activation may restart (Linux): the whole
	// switch runs as its own unit (switchdetach.go).
	if caller := switchCaller(); caller != "" {
		// The outer piste prints the release notice (the inner, in its
		// piste-switch-* unit, doesn't): machine(), not plat(), so plat()'s
		// warnings don't print twice.
		if m, err := a.machine(); err == nil {
			lastSwitched = m
		}
		return a.detachSwitch(caller, host, noUndo, force)
	}
	p, err := a.plat()
	if err != nil {
		return err
	}
	if err := a.refusePlaintextSecrets(p.FlakeDir, a.piste()+" switch"); err != nil {
		return err
	}
	ref := p.FlakeDir
	lastSwitched = p
	t, err := a.targetFor(p, host)
	if err != nil {
		return err
	}
	lastSwitchedKind = t.Kind
	// A local piste checkout overrides the input (cross-repo
	// development; on darwin it also spares root's ssh). The guard's names
	// are read against it too, as the build reads the overlay.
	o, err := a.engineOverride(p)
	if err != nil {
		return err
	}

	// Hostname guard: never activate another host's config here.
	// Skipped for an explicit --host; on a Mac, for a flake with no
	// darwinConfigurations (the shared library); on NixOS, for one with no
	// nixosConfigurations or with a home for this machine (t.Unmatched).
	if t.Kind == KindDarwin && host == "" {
		names, err := a.darwinConfigNames(p, o)
		if err != nil {
			return err
		}
		if len(names) > 0 && !slices.Contains(names, p.Hostname) {
			return a.refuseHostname(p.Hostname, "darwinConfigurations", names)
		}
	}
	if len(t.Unmatched) > 0 {
		return a.refuseHostname(p.Hostname, "nixosConfigurations", t.Unmatched, "  Add this machine: piste host add")
	}
	// --host can name a NixOS host from any Linux (build does): switching
	// to one would replace a system that isn't NixOS. Before any fetch,
	// sudo or check.
	if t.Kind == KindNixOS && !p.IsNixOS {
		a.errorf(t.Name + " is a NixOS host and this machine doesn't run NixOS: switching to it here would replace this system. Build it instead: piste build --host " + t.Name)
		return ExitError{1}
	}

	override := o.Args()
	a.announceFlake(p, &o)
	a.info("Building and switching...")
	// home-manager's -b, also the undo's HOME_MANAGER_BACKUP_EXT.
	backup := fmt.Sprintf("backup-%d", time.Now().Unix())
	var argv []string
	switch t.Kind {
	case KindDarwin:
		rebuild, err := a.darwinRebuild(p)
		if err != nil {
			return err
		}
		// Always the host by name: darwin-rebuild's own pick
		// (LocalHostName) is the name piste checked above, but a name
		// passed can't drift from it.
		argv = a.buildArgs(append(append([]string{"sudo"}, rebuild...), "switch", "--flake", ref+"#"+t.Name))
	case KindNixOS:
		argv = a.buildArgs([]string{"sudo", "nixos-rebuild", "switch", "--flake", ref + "#" + t.Name})
	default:
		argv = a.buildArgs([]string{"home-manager", "switch",
			"-b", backup,
			"--flake", ref + "#" + t.Name})
	}
	if t.Kind != KindHome {
		// Root runs the switch, without your git credentials: fetch the
		// flake's inputs as you first, so a private one (a team layer) is in
		// the store. Quick once they are; if it fails, root tries anyway.
		arch := append([]string{"nix", "flake", "archive", "--json", ref}, override...)
		a.info("Fetching the flake's inputs as you...")
		if res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: arch}); err != nil {
			why := stderrTail(res.Stderr, 1)
			if why == "" {
				why = err.Error()
			}
			a.warn(fmt.Sprintf("Couldn't fetch the flake's inputs as you (%s): the switch fetches them as root", why))
		}
	}
	// After the fetch, which leaves the inputs in the store for its eval;
	// before the baseline and any sudo, so a refusal changes nothing.
	if t.Kind == KindNixOS && !force {
		a.info("Checking that the new system won't lock you out...")
		refusals, err := a.runPreflight(p, t, o)
		if err != nil {
			a.errorf("piste switch: can't evaluate the new system to check it: " + err.Error())
			a.errorf("To switch without the check: " + a.piste() + " switch --force")
			a.fixHint(host)
			return ExitError{1}
		}
		if len(refusals) > 0 {
			a.errorf("piste switch: refusing, the new configuration would lock you out:")
			for _, r := range refusals {
				a.errorf("  " + r.Message)
			}
			a.errorf("Fix it, or run " + a.piste() + " switch --force to switch anyway.")
			return ExitError{1}
		}
	}

	// The generation to go back to, and whether going back is safe: it
	// passes the check now.
	prof := a.switchProfile(p, t.Kind)
	before, haveBefore := activeGeneration(prof)
	undoable := false
	if runsBaseline(noUndo, haveBefore) {
		c := a.checkLogin(p)
		undoable = c.ok()
		switch {
		case undoable:
		case pisteNotInstalled(c, a.pisteOnPath()):
			a.info("piste isn't installed in the current generation yet, so this switch isn't checked.")
		default:
			a.warn(fmt.Sprintf("This switch can't undo itself: a new login shell can't run piste now (%s).", c.why()))
			for _, l := range c.tail(checkTailLines) {
				a.warn("  " + l)
			}
		}
	}

	rc, err := a.pass(append(argv, override...), "")
	if err != nil {
		return err
	}

	// Both tools point the profile at the new generation before activating
	// it, so a failed activation that moved it is checked too.
	after, haveAfter := activeGeneration(prof)
	var check shellCheck
	checked := runsCheck(undoable, before, after, haveAfter)
	if checked {
		// The shell again: the switch may have changed it.
		sh := a.loginShell(p)
		a.info(fmt.Sprintf("Checking that a new login shell (%s) starts and runs piste...", sh))
		check = checkShell(sh, checkEnv(p.HomeDir, p.User, sh))
	}

	// The endings. Every exit-1 one returns through the line below.
	failed, hint := true, true
	switch {
	case checked && !check.ok():
		a.errorf(fmt.Sprintf("The new generation fails the check: %s.", check.why()))
		for _, l := range check.tail(checkTailLines) {
			a.errorf("  " + l)
		}
		// An undo that failed ends with the steps that put the old
		// generation back: nothing goes after them.
		hint = a.undoSwitch(prof, before, after, undoSteps(t.Kind, prof, before, backup))
	case rc != 0:
		a.errorf(fmt.Sprintf("Switch failed (exit code %d)", rc))
		a.warn("To rollback: " + a.piste() + " rollback")
		a.warn("To debug:    " + a.piste() + " --verbose switch")
	default:
		failed = false
		a.success("Switch complete!")
	}
	if failed {
		// After the undo, so the machine is on a working generation while
		// piste fix looks.
		if hint {
			a.fixHint(host)
		}
		return ExitError{1}
	}
	return nil
}

// --- rollback ------------------------------------------------------------------

type hmGeneration struct {
	date, path string
	id         int
}

var hmGenLine = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2})\s*:\s*id\s+(\d+)\s*->\s*(\S+)`)

// hmGenerations is _parse_hm_generations: newest first.
func (a *App) hmGenerations() []hmGeneration {
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"home-manager", "generations"}})
	if err != nil { // includes a non-zero exit
		return nil
	}
	var gens []hmGeneration
	for _, l := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
		if m := hmGenLine.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			id, _ := strconv.Atoi(m[2])
			gens = append(gens, hmGeneration{date: m[1], id: id, path: m[3]})
		}
	}
	return gens
}

func newRollback(a *App) *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:   "rollback",
		Short: "Revert to previous generation",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.machine()
			if err != nil {
				return err
			}
			kind, err := a.machineKind(p)
			if err != nil {
				// The overlay isn't needed to go back: nixos-rebuild does it
				// from the system profile.
				if p.IsNixOS {
					a.errorf("To roll back the system by hand: sudo nixos-rebuild switch --rollback")
				}
				return err
			}
			a.info("Rolling back...")
			switch kind {
			case KindDarwin:
				if err := a.checkDarwinRebuild(); err != nil {
					return err
				}
				// Activation needs root on current nix-darwin (as switch does).
				if err := a.runChecked([]string{"sudo", "darwin-rebuild", "switch", "--rollback"}, "", nil); err != nil {
					return err
				}
				a.success("Rollback complete!")
				return nil
			case KindNixOS:
				if err := a.runChecked([]string{"sudo", "nixos-rebuild", "switch", "--rollback"}, "", nil); err != nil {
					return err
				}
				a.success("Rollback complete!")
				return nil
			}

			gens := a.hmGenerations()
			if len(gens) < 2 {
				a.errorf("Cannot rollback: fewer than 2 generations found.")
				return ExitError{1}
			}
			cur, prev := gens[0], gens[1]
			s := ui.For(a.Stdout)
			fmt.Fprintf(a.Stdout, "  Current:  %s\n", s.Dim.Render(fmt.Sprintf("id %d (%s)", cur.id, cur.date)))
			fmt.Fprintf(a.Stdout, "  Rollback: %s\n", s.OK.Render(fmt.Sprintf("id %d (%s)", prev.id, prev.date)))
			fmt.Fprintf(a.Stdout, "  Path:     %s\n", s.Dim.Render(prev.path))
			if !yes {
				ok, err := a.confirm("Activate previous generation?")
				if err != nil {
					return a.aborted(err)
				}
				if !ok {
					a.warn("Rollback cancelled.")
					return nil
				}
			}

			script := prev.path + "/activate"
			c := exec.Command(script)
			c.Stdin, c.Stdout, c.Stderr = os.Stdin, a.Stdout, a.Stderr
			err = c.Run()
			var ee *exec.ExitError
			switch {
			case err == nil:
				a.success(fmt.Sprintf("Rolled back to generation %d!", prev.id))
				return nil
			case errors.As(err, &ee):
				a.errorf(fmt.Sprintf("Rollback failed: %q exited with status %d.", script, ee.ExitCode()))
			case errors.Is(err, fs.ErrNotExist):
				a.errorf(fmt.Sprintf("Rollback failed: %q: no such file.", script))
			default:
				a.errorf(fmt.Sprintf("Rollback failed: %v", err))
			}
			return ExitError{1}
		},
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompt")
	return c
}

// --- secrets -----------------------------------------------------------------

func newSecrets(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "secrets",
		Short: "Manage sops-encrypted secrets",
		// An unknown subcommand runs the piste-secrets-<sub> plugin
		// (an overlay may ship some), with its flags untouched.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || strings.HasPrefix(args[0], "-") {
				return cmd.Help()
			}
			path := plugins.Find([]string{"secrets", args[0]}, a.Env.Getenv("PATH"))
			if path == "" {
				return fmt.Errorf("unknown command %q for \"piste secrets\"", args[0])
			}
			if err := execFn(path, append([]string{filepath.Base(path)}, args[1:]...), os.Environ()); err != nil {
				fmt.Fprintf(a.Stderr, "piste: exec %s: %v\n", path, err)
				return ExitError{127}
			}
			return nil
		},
	}
	c.AddCommand(newSecretsInit(a), newSecretsEdit(a), newSecretsStatus(a), newSecretsUpdatekeys(a))
	return c
}

func ageKeyPath(home string) string {
	return filepath.Join(home, ".config", "sops", "age", "keys.txt")
}

// agePubkey is `age-keygen -y`: the recipient of the key file's first X25519
// identity, "" when it can't be read.
func agePubkey(keyPath string) string {
	f, err := os.Open(keyPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	ids, err := age.ParseIdentities(f)
	if err != nil {
		return ""
	}
	for _, id := range ids {
		if x, ok := id.(*age.X25519Identity); ok {
			return x.Recipient().String()
		}
	}
	return ""
}

// ageKeygen is `age-keygen -o keyPath`: a new X25519 identity in
// age-keygen's file format, mode 0600. It returns the public key.
func ageKeygen(keyPath string) (string, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", err
	}
	pub := id.Recipient().String()
	body := fmt.Sprintf("# created: %s\n# public key: %s\n%s\n", time.Now().Format(time.RFC3339), pub, id)
	f, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		return "", err
	}
	return pub, f.Close()
}

// sopsEnv is _sops_env: point SOPS_AGE_KEY_FILE at the default key when it
// is unset or empty (sops chokes on an empty value).
func sopsEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SOPS_AGE_KEY_FILE=") {
			env = append(env, kv)
		}
	}
	if v := os.Getenv("SOPS_AGE_KEY_FILE"); v != "" {
		return append(env, "SOPS_AGE_KEY_FILE="+v)
	}
	if k := ageKeyPath(home); fileExists(k) {
		env = append(env, "SOPS_AGE_KEY_FILE="+k)
	}
	return env
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func newSecretsInit(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Bootstrap age key and .sops.yaml for this machine",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			if err := a.refusePlaintextSecrets(p.FlakeDir, a.piste()+" secrets init"); err != nil {
				return err
			}
			return a.ensureSops(p.HomeDir, p.FlakeDir, p.Hostname)
		},
	}
}

// ensureAgeKey is this machine's age key at home: made if missing, never
// replaced. It returns the public key, "" when an existing key can't be read
// (the caller refuses or carries on).
func (a *App) ensureAgeKey(home string) (string, error) {
	keyPath := ageKeyPath(home)
	if fileExists(keyPath) {
		a.info("Age key already exists: " + keyPath)
		return agePubkey(keyPath), nil
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o777); err != nil {
		return "", err
	}
	pub, err := ageKeygen(keyPath)
	if err != nil {
		a.errorf("age-keygen failed: " + err.Error())
		return "", ExitError{1}
	}
	a.success("Created age key: " + keyPath)
	a.info("  Keep a copy of it in a password manager: a reinstalled machine needs it to read your secrets.")
	return pub, nil
}

// secretsReady: pub can read every secrets file in dir, by its sops metadata
// (a `recipient: <pub>` line, in one key group or several); nothing is
// decrypted. A plaintext file has nothing to read, so only encrypted ones
// count, and no files is ready.
func secretsReady(dir, pub string) bool {
	recipient := regexp.MustCompile(`(?m)recipient:\s*` + regexp.QuoteMeta(pub) + `\s*$`)
	for _, f := range sopsFiles(dir) {
		if !isEncrypted(f) {
			continue
		}
		b, err := os.ReadFile(f)
		if pub == "" || err != nil || !recipient.Match(b) {
			return false
		}
	}
	return true
}

// ensureSops is `piste secrets init` (also run by `piste init --sops`): the
// age key at home (made if missing, never replaced), a .sops.yaml in dir for
// its public key, anchored by host (this machine's name, as piste host add
// anchors the next machine's, so a key to remove is found by its machine),
// and dir/secrets/env.
func (a *App) ensureSops(home, dir, host string) error {
	s := ui.For(a.Stdout)
	pub, err := a.ensureAgeKey(home)
	if err != nil {
		return err
	}
	if pub != "" {
		fmt.Fprintf(a.Stdout, "  Public key: %s\n", s.OK.Render(pub))
	}

	sopsYAML := filepath.Join(dir, ".sops.yaml")
	if fileExists(sopsYAML) {
		a.info(".sops.yaml already exists: " + sopsYAML)
	} else {
		if pub == "" {
			a.errorf("Could not read age public key — cannot generate .sops.yaml")
			return ExitError{1}
		}
		body := initSopsYAML(sopsAnchor(host), pub)
		if err := os.WriteFile(sopsYAML, []byte(body), 0o644); err != nil {
			return err
		}
		a.success("Created " + sopsYAML)
	}

	if err := os.MkdirAll(filepath.Join(dir, "secrets", "env"), 0o777); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout)
	a.success("Secrets infrastructure ready.")
	fmt.Fprintf(a.Stdout, "  Next: add secrets with %s\n", s.OK.Render("piste secrets edit"))
	return nil
}

// initSopsYAML is the .sops.yaml ensureSops writes: one recipient, pub,
// anchored &anchor, for every secrets file.
func initSopsYAML(anchor, pub string) string {
	return "keys:\n" +
		"  - &" + anchor + " " + pub + "\n" +
		"creation_rules:\n" +
		"  - path_regex: secrets/.*\\.sops\\.yaml$\n" +
		"    key_groups:\n" +
		"      - age:\n" +
		"          - *" + anchor + "\n"
}

// sopsAnchor is a YAML anchor for a machine's key: its hostname, with
// anything an anchor can't hold (space, a flow indicator) as "-"; "machine"
// for none.
func sopsAnchor(host string) string {
	a := strings.Map(func(r rune) rune {
		if r <= ' ' || strings.ContainsRune(",[]{}*&!|>'\"%@`#", r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(host))
	if strings.Trim(a, "-") == "" {
		return "machine"
	}
	return a
}

func newSecretsUpdatekeys(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "updatekeys",
		Short: "Rekey every secrets file for .sops.yaml's recipients (after piste host add)",
		Long: "Re-encrypt every *.sops.yaml file of the overlay for the recipients its\n" +
			".sops.yaml now lists (sops updatekeys -y, one file at a time), then under a\n" +
			"new data key (sops rotate -i), so a machine whose key was removed can't read\n" +
			"what comes next with the data key it had. Run it on a machine that can read\n" +
			"the secrets, after another machine's key was added (piste host add) or\n" +
			"removed; then commit and push. No secret value is written out in plaintext.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			return a.updatekeys(p.FlakeDir, p.HomeDir)
		},
	}
}

// updatekeys is `piste secrets updatekeys`: sops updatekeys -y, then sops
// rotate -i, for each encrypted secrets file in dir, run in dir (sops finds
// .sops.yaml from its working directory and matches path_regex against the
// path relative to it), with home's age key. updatekeys only re-wraps the
// data key, which a removed machine had; rotate makes a new one, every time
// (harmless when a machine was added). Both work in memory: no plaintext on
// disk. A plaintext file (a team layer's skeleton before piste secrets edit)
// has no recipients and sops fails on it: skipped, as secretsReady skips it.
// A file sops fails on is named; the rest still run.
func (a *App) updatekeys(dir, home string) error {
	if _, err := exec.LookPath("sops"); err != nil {
		a.errorf("sops not found on PATH.")
		return ExitError{1}
	}
	if !fileExists(filepath.Join(dir, ".sops.yaml")) {
		a.errorf(fmt.Sprintf("no .sops.yaml in %s, so no recipients to re-encrypt for (piste secrets init makes one)", dir))
		return ExitError{1}
	}
	var files []string
	for _, f := range sopsFiles(dir) {
		if isEncrypted(f) {
			files = append(files, f)
			continue
		}
		a.info(fmt.Sprintf("Skipped %s: not encrypted yet (piste secrets edit encrypts it)", rel(dir, f)))
	}
	if len(files) == 0 {
		a.info("No encrypted secrets files: nothing to re-encrypt.")
		return nil
	}
	env := sopsEnv(home)
	recipients, done, failed := 0, 0, 0
	for _, f := range files {
		before, _ := os.ReadFile(f)
		ok := true
		for _, argv := range [][]string{{"updatekeys", "-y"}, {"rotate", "-i"}} {
			c := exec.Command("sops", append(argv, rel(dir, f))...)
			c.Dir, c.Env = dir, env
			c.Stdout, c.Stderr = a.Stdout, a.Stderr
			if err := c.Run(); err != nil {
				a.errorf(fmt.Sprintf("sops %s failed on %s (%v)", argv[0], rel(dir, f), err))
				ok = false
				break
			}
			// updatekeys writes the file only when its recipients change;
			// rotate always does (a new data key).
			if argv[0] == "updatekeys" {
				if mid, _ := os.ReadFile(f); !bytes.Equal(mid, before) {
					recipients++
				}
			}
		}
		if !ok {
			failed++
			continue
		}
		done++
	}
	switch {
	case recipients > 0:
		a.success(fmt.Sprintf("✓ Re-encrypted %s for .sops.yaml's recipients, every file under a new data key. Commit and push the changes.", plural(recipients, "file")))
	case done > 0:
		a.info("Recipients unchanged: every file already had .sops.yaml's. Each has a new data key now: commit and push the changes.")
	}
	if failed > 0 {
		return ExitError{1}
	}
	return nil
}

// isEncrypted is _is_encrypted: the file has a top-level sops: block.
func isEncrypted(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "sops:") {
			return true
		}
	}
	return false
}

// sopsFiles is _find_sops_files: every *.sops.yaml under dir except the
// .sops.yaml config, in pathlib order (by path component).
func sopsFiles(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() && strings.HasSuffix(e.Name(), ".sops.yaml") && e.Name() != ".sops.yaml" {
			out = append(out, path)
		}
		return nil
	})
	slices.SortFunc(out, func(x, y string) int {
		return slices.Compare(strings.Split(x, "/"), strings.Split(y, "/"))
	})
	return out
}

func rel(dir, path string) string {
	r, err := filepath.Rel(dir, path)
	if err != nil {
		return path
	}
	return r
}

// sopsEdit is _sops_edit: encrypt a plaintext file first, then open it.
func (a *App) sopsEdit(home, target string) error {
	env := sopsEnv(home)
	if !isEncrypted(target) {
		a.info("File is not yet encrypted — encrypting first: " + filepath.Base(target))
		c := exec.Command("sops", "-e", "-i", target)
		c.Env = env
		var stderr strings.Builder
		c.Stderr = &stderr
		if err := c.Run(); err != nil {
			a.errorf("Failed to encrypt: " + strings.TrimSpace(stderr.String()))
			return ExitError{1}
		}
		a.success("Encrypted successfully.")
	}
	c := exec.Command("sops", target)
	c.Env = env
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, a.Stdout, a.Stderr
	err := c.Run()
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		a.errorf(fmt.Sprintf("sops exited with code %d", ee.ExitCode()))
		return ExitError{1}
	case err != nil:
		fmt.Fprintf(a.Stderr, "piste: sops: %v\n", err)
		return ExitError{1}
	}
	return nil
}

func newSecretsEdit(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "edit [file]",
		Short: "Open a sops-encrypted file for editing",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if _, err := exec.LookPath("sops"); err != nil {
				a.errorf("sops not found on PATH.")
				fmt.Fprintln(a.Stdout, "Run piste via: nix run .#piste")
				return ExitError{1}
			}
			p, err := a.plat()
			if err != nil {
				return err
			}
			s := ui.For(a.Stdout)
			if len(args) == 1 {
				target := args[0]
				if !filepath.IsAbs(target) {
					target = filepath.Join(p.FlakeDir, target)
				}
				if !fileExists(target) {
					a.errorf("File not found: " + target)
					return ExitError{1}
				}
				return a.sopsEdit(p.HomeDir, target)
			}

			files := sopsFiles(p.FlakeDir)
			switch len(files) {
			case 0:
				a.errorf("No *.sops.yaml files found.")
				fmt.Fprintf(a.Stdout, "  Run %s to set up secrets infrastructure.\n", s.OK.Render("piste secrets init"))
				return ExitError{1}
			case 1:
				a.info("Opening " + rel(p.FlakeDir, files[0]))
				return a.sopsEdit(p.HomeDir, files[0])
			}
			f, err := a.chooseFile(p.FlakeDir, files)
			if errors.Is(err, errAborted) {
				return nil // stopping the pick is not an error
			}
			if err != nil {
				return err
			}
			return a.sopsEdit(p.HomeDir, f)
		},
	}
}

// chooseFile is secrets edit's pick of one of several files: a numbered
// list, or at a terminal a picker; errAborted when none was picked.
func (a *App) chooseFile(root string, files []string) (string, error) {
	if a.useForms() {
		opts := make([]huh.Option[string], len(files))
		for i, f := range files {
			opts[i] = huh.NewOption(rel(root, f), f)
		}
		f, err := a.pickOne("Secrets file", opts, files[0])
		if errors.Is(err, errStopped) {
			return "", errAborted
		}
		return f, err
	}
	s := ui.For(a.Stdout)
	fmt.Fprintf(a.Stdout, "\n  Secrets files in %s:\n\n", s.Bold.Render(root))
	for i, f := range files {
		fmt.Fprintf(a.Stdout, "  %d. %s\n", i+1, rel(root, f))
	}
	fmt.Fprintln(a.Stdout)
	i, err := a.promptIndex("  Select file", len(files))
	if err != nil {
		return "", err
	}
	return files[i-1], nil
}

// yqLines runs yq and returns its non-empty output lines; ok=false on failure.
func (a *App) yqLines(expr, path string) ([]string, bool) {
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"yq", expr, path}})
	if err != nil { // includes a non-zero exit
		return nil, false
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, true
}

func newSecretsStatus(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show secrets infrastructure health",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			s := ui.For(a.Stdout)
			keyPath := ageKeyPath(p.HomeDir)
			haveKey := fileExists(keyPath)
			var rows [][2]string
			if haveKey {
				rows = append(rows, [2]string{"Age key", s.OK.Render(keyPath)})
				if pub := agePubkey(keyPath); pub != "" {
					rows = append(rows, [2]string{"", s.OK.Render(pub)})
				}
			} else {
				rows = append(rows, [2]string{"Age key", s.Err.Render("not found")},
					[2]string{"", s.Dim.Render("Expected: " + keyPath)})
			}
			sopsYAML := filepath.Join(p.FlakeDir, ".sops.yaml")
			haveYAML := fileExists(sopsYAML)
			if haveYAML {
				rows = append(rows, [2]string{".sops.yaml", s.OK.Render("found")})
			} else {
				rows = append(rows, [2]string{".sops.yaml", s.Err.Render("missing")})
			}
			rows = append(rows, [2]string{"", ""})

			files := sopsFiles(p.FlakeDir)
			plaintext := false
			for i, f := range files {
				enc := s.OK.Render("encrypted")
				if !isEncrypted(f) {
					enc = s.Err.Render("PLAINTEXT")
					plaintext = true
				}
				keys := s.Dim.Render("unreadable")
				if ks, ok := a.yqLines(`keys | .[] | select(. != "sops")`, f); ok {
					keys = fmt.Sprintf("%d keys", len(ks))
				}
				label := ""
				if i == 0 {
					label = "Secrets files"
				}
				rows = append(rows, [2]string{label, fmt.Sprintf("%s  %s  %s", rel(p.FlakeDir, f), enc, keys)})
			}
			if len(files) == 0 {
				rows = append(rows, [2]string{"Secrets files", s.Dim.Render("none found")})
			}

			// the secret field names: leaf keys of every file, first seen
			var leaves []string
			for _, f := range files {
				ks, _ := a.yqLines(`.. | select(tag == "!!str") | path | .[-1]`, f)
				var seen []string // deduplicated per file, as _collect_leaf_keys
				for _, k := range ks {
					if k != "sops" && !slices.Contains(seen, k) {
						seen = append(seen, k)
					}
				}
				leaves = append(leaves, seen...)
			}
			if len(leaves) > 0 {
				rows = append(rows, [2]string{"", ""}, [2]string{"Secret keys", strings.Join(leaves, ", ")})
			}
			a.kvPanel("piste secrets", rows)

			switch {
			case !haveKey || !haveYAML:
				fmt.Fprintln(a.Stdout)
				fmt.Fprintf(a.Stdout, "  Run %s to bootstrap.\n", s.OK.Render("piste secrets init"))
			case plaintext:
				fmt.Fprintln(a.Stdout)
				a.warn("Plaintext secrets detected! Encrypt with: sops -e -i <file>")
			}
			return nil
		},
	}
}
