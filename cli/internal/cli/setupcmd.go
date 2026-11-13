package cli

import (
	"bytes"
	"charm.land/huh/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/layer"
	"github.com/elocke/piste/cli/internal/onboard"
	"github.com/elocke/piste/cli/internal/overlay"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/release"
	"github.com/elocke/piste/cli/internal/workspace"
)

func init() { register("setup", newSetup) }

type setupOpts struct {
	from, dir        string
	github, noGithub bool
}

// newSetup is `piste setup [<overlay or team layer url>]` (guided
// onboarding, with a team layer's starter): prerequisites, the checkout, the
// overlay, the first switch, then the agents and MCP sync.
func newSetup(a *App) *cobra.Command {
	var o setupOpts
	c := &cobra.Command{
		Use:   "setup [<overlay or team layer url>]",
		Short: "Set this machine up: prerequisites, the checkout, an overlay, the first switch",
		Long: "Guided onboarding. Checks what piste needs (and says how to fix what's\n" +
			"missing), clones the piste checkout at its newest release (unless run in one),\n" +
			"then the overlay: <url> is your overlay to add, or a team layer to start your\n" +
			"own overlay on (piste init --layer); with no url, the overlay that has this\n" +
			"machine, or a new one (piste init). It builds this machine and offers the\n" +
			"first switch; after it, piste agents sync --user and piste mcp sync bring the\n" +
			"plugins, hooks and MCP servers to Claude Code and Codex. When gh is logged in,\n" +
			"it first offers a GitHub SSH key for that account (see piste github).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runSetup(cmd.Flags().Changed, args, o)
		},
	}
	c.Flags().StringVar(&o.from, "from", onboard.DefaultSource, "the git URL to clone piste from")
	c.Flags().StringVar(&o.dir, "dir", "", "where to clone piste (default ~/piste)")
	c.Flags().BoolVar(&o.github, "github", false, "set up a GitHub SSH key for gh's account without asking (see piste github)")
	c.Flags().BoolVar(&o.noGithub, "no-github", false, "don't offer a GitHub SSH key")
	return c
}

// runOut is a command's stdout; a failure carries its first fatal:/error:
// line (else its last), not just the exit status.
func runOut(argv ...string) (string, error) {
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(strings.TrimSpace(string(ee.Stderr))) > 0 {
		err = errors.New(gitFailure(string(ee.Stderr)))
	}
	return string(out), err
}

// gitFailure is what a failed git (or other command) said: its first
// fatal:/error: line, after the line before it when that is the cause (ssh's
// "Permission denied (publickey)." before git's "Could not read from remote
// repository."); without one, its last line.
func gitFailure(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "fatal:") || strings.HasPrefix(l, "error:") {
			if i > 0 && strings.TrimSpace(lines[i-1]) != "" {
				return strings.TrimSpace(lines[i-1]) + " / " + strings.TrimSpace(l)
			}
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(lines[len(lines)-1])
}

func (a *App) runSetup(changed func(string) bool, args []string, o setupOpts) error {
	// The real CPU, not platform.Arch (the test binaries pin that to amd64).
	if l := onboard.Unsupported(runtime.GOOS, runtime.GOARCH); l != "" {
		a.errorf(l)
		return ExitError{1}
	}
	nixos := runtime.GOOS == "linux" && platform.Host().NixOS
	cs := onboard.Prereqs(onboard.Probe{
		LookPath: exec.LookPath, Run: runOut, Exists: fileExists, Darwin: runtime.GOOS == "darwin", NixOS: nixos,
	})
	// Marked, not only coloured: NO_COLOR and a pipe show them too.
	for _, c := range cs {
		switch {
		case c.OK && c.Info:
			a.info("· " + c.Name + ": " + c.Fix) // passes, with a note
		case c.OK:
			a.success("✓ " + c.Name)
		case c.Fatal:
			a.errorf("✗ " + c.Name + ": " + c.Fix)
		case c.Info:
			a.info("· " + c.Name + ": " + c.Fix)
		default:
			a.warn("! " + c.Name + ": " + c.Fix)
		}
	}
	if onboard.Blocked(cs) {
		a.errorf("Fix the above, then run piste setup again.")
		return ExitError{1}
	}
	if onboard.FlakesOff(cs) {
		// A fresh NixOS has flakes off, and every nix call from here on
		// (the builds, the evals, nix flake archive) needs them. The
		// checklist above saw the Nix as it is: with them on already, the
		// person's NIX_CONFIG stays as it is. Not the switch's sudo
		// (env_reset drops this): nixos-rebuild turns them on for itself,
		// and the switch makes them permanent. The person's shell still has
		// them off: the nix run lines printed from here on turn them on too.
		os.Setenv("NIX_CONFIG", withFlakes(os.Getenv("NIX_CONFIG")))
		a.flakesOff = true
	}
	root, err := a.setupCheckout(o.from, o.dir, changed("dir") || changed("from"))
	if err != nil {
		return err
	}
	// A local overlay path is the user's, from where they are: before the
	// chdir into the checkout.
	if len(args) == 1 {
		if fi, err := os.Stat(args[0]); err == nil && fi.IsDir() && !strings.Contains(args[0], "://") {
			if abs, err := filepath.Abs(args[0]); err == nil {
				args = []string{abs}
			}
		}
	}
	// From here on the hints are read in a shell that may be elsewhere.
	if cwd, err := os.Getwd(); err != nil || physical(cwd) != physical(root) {
		a.movedTo = root
	}
	if err := os.Chdir(root); err != nil {
		return err
	}
	name, created, ok, err := a.setupOverlay(args, root)
	if err != nil {
		return err
	}
	// The overlay had this machine before this run (not made or added to
	// for it just now).
	existing := ok && !created
	if !ok && name != "" && !created {
		// An overlay without this machine (made on another one): add it
		// (piste host add).
		yes, err := a.offerHostAdd()
		if err != nil {
			return err
		}
		if !yes {
			home, _ := os.UserHomeDir()
			a.warn(fmt.Sprintf("Setup stopped. To add this machine when you want it: cd %s && %s host add --overlay %s", tilde(home, root), a.pisteHere(), name))
			return ExitError{1}
		}
		a.Overlay = name
		_, ready, err := a.hostAdd(nil, nil, "")
		if err != nil {
			return err
		}
		if !ready {
			return nil // host add said what's left
		}
		ok = true
	}
	if !ok {
		a.warn("Setup stopped before the switch: see above.")
		return ExitError{1}
	}
	// Never a switch to a generation whose secrets this machine can't read.
	if !a.setupSecretsReady(filepath.Join(root, "overlays", name)) {
		a.info(fmt.Sprintf("This machine can't read overlays/%s's secrets yet: piste host add says what's left", name))
		return nil
	}
	ghLater := a.setupGithub(name, o.github, o.noGithub)
	a.previewSwitch(name)
	if nixos && existing {
		a.warnClone(filepath.Join(root, "overlays", name))
	}
	switched := false
	sw, err := a.offerSwitch()
	if err != nil {
		return err
	}
	if sw {
		q, _, err := a.thisMachine()
		if err != nil {
			return err
		}
		a.Overlay = name
		if err := a.runSwitch(q.Hostname, false, false); err != nil {
			return err
		}
		switched = true
		a.syncAfterSwitch()
	}
	if switched && runtime.GOOS != "darwin" && lastSwitchedKind != KindNixOS {
		// A home's switch leaves the login shell alone (chsh needs the
		// password, and /etc/shells root). A NixOS host's sets it.
		if p, err := a.machine(); err == nil {
			for _, l := range zshHint(a.loginShell(p), a.zshPath(p.HomeDir)) {
				a.info(l)
			}
		}
	}
	lines := []string{"  cd " + root}
	if switched {
		// Ghostty came with the switch; Terminal.app's font shows the
		// prompt's icons as boxes.
		open := "Open a new shell"
		if runtime.GOOS == "darwin" {
			open = "Open Ghostty"
		}
		a.info("Switched. " + open + " (piste is installed now), then:")
		lines = append(lines, "  piste switch    # after changing overlays/"+name)
	} else {
		a.info("Next:")
		lines = append(lines, fmt.Sprintf("  %s switch    # the first switch, when you're ready", a.pisteHere()),
			"  piste agents sync --user    # then, in a new shell: plugins and MCP servers for Claude Code and Codex")
	}
	if ghLater {
		lines = append(lines, "  "+onboard.LoginFix+" && piste github    # a GitHub SSH key, once gh is here")
	}
	if created {
		// Private: an overlay holds your machines' names, your packages and
		// your encrypted secrets.
		lines = append(lines, fmt.Sprintf("  git -C overlays/%s commit -m 'My overlay' && gh repo create %s --private --source overlays/%s --push    # keep it, in a private repo", name, name, name))
	}
	for _, l := range lines {
		fmt.Fprintln(a.Stdout, l)
	}
	return nil
}

// warnClone says, before setup offers the first switch on a NixOS machine,
// when this hostname is already one of the overlay at dir's NixOS hosts and
// piste can't tell this machine is it, or can tell it isn't (whoseHost:
// anything but a match by uuid). A clone of that machine has its hostname,
// and its root by label or device name: the switch would make it that
// machine's twin. The offer still follows: it may well be that machine.
func (a *App) warnClone(dir string) {
	q, _, err := a.thisMachine()
	if err != nil || !q.NixOS {
		return
	}
	h, err := a.overlayHosts(dir)
	if err != nil || !slices.Contains(h.NixOS, q.Hostname) {
		return
	}
	newMachine := fmt.Sprintf("If this is a new machine, stop and run %s host add --name <new name>.", a.piste())
	switch o := a.whoseHost(dir, q.Hostname, q.User, a.home()); o.verdict {
	case rootOther:
		a.warn(fmt.Sprintf("%s is another machine's host in this overlay (%s). %s", q.Hostname, o.why, newMachine))
	case rootUnknown:
		a.warn(fmt.Sprintf("%s is already a host in this overlay, and piste can't tell this machine is it (%s). %s", q.Hostname, o.why, newMachine))
	}
}

// withFlakes is a NIX_CONFIG (nixConfig) with nix-command and flakes on
// after what it held: extra- adds to experimental-features, wherever it is
// set.
func withFlakes(nixConfig string) string {
	if strings.TrimSpace(nixConfig) == "" {
		return flakesConfig
	}
	return nixConfig + "\n" + flakesConfig
}

// zshHint is what setup says after a Linux switch when the login shell
// (shell) isn't zsh, with the commands that make zsh (at zsh) it; nothing
// when it is.
func zshHint(shell, zsh string) []string {
	if filepath.Base(shell) == "zsh" {
		return nil
	}
	return []string{
		fmt.Sprintf("Your login shell is still %s, and piste sets up zsh. To make zsh your login shell:", filepath.Base(shell)),
		fmt.Sprintf("  echo %s | sudo tee -a /etc/shells && chsh -s %s", zsh, zsh),
	}
}

// zshPath is the zsh the switch put in the home-manager profile, written
// with ~: ~/.nix-profile's, or the XDG profile's (use-xdg-base-directories);
// ~/.nix-profile/bin/zsh when neither is there.
func (a *App) zshPath(home string) string {
	state := a.Env.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	for _, p := range []string{filepath.Join(home, ".nix-profile", "bin", "zsh"), filepath.Join(state, "nix", "profile", "bin", "zsh")} {
		if fileExists(p) {
			return tilde(home, p)
		}
	}
	return "~/.nix-profile/bin/zsh"
}

// setupOverlay is setup's step 3: add <url>; else the overlay that already
// has this machine; else ask for a URL to add, Enter for init. ok is false
// when it stopped short of an overlay built for this machine (add or init
// said why).
func (a *App) setupOverlay(args []string, root string) (name string, created, ok bool, err error) {
	names, err := workspace.Overlays(root)
	if err != nil {
		return "", false, false, err
	}
	q, key, err := a.thisMachine()
	if err != nil {
		return "", false, false, err
	}
	if len(args) == 1 {
		// Run again with the same URL: the overlay already cloned from it, or
		// already on that team layer.
		ref, _ := layer.Ref(args[0])
		for _, n := range names {
			dir := filepath.Join(root, "overlays", n)
			if origin := gitOut(dir, "remote", "get-url", "origin"); origin != "" && origin == args[0] {
				a.info(fmt.Sprintf("overlays/%s is %s: using it", n, origin))
				ok, err := a.hasThisMachine(root, n, q, key)
				return n, false, ok, err
			}
			if b, err := os.ReadFile(filepath.Join(dir, "flake.nix")); err == nil && ref != "" && bytes.Contains(b, []byte(`url = "`+overlay.NixString(ref)+`"`)) {
				a.info(fmt.Sprintf("overlays/%s is on the team layer %s: using it", n, ref))
				ok, err := a.hasThisMachine(root, n, q, key)
				return n, false, ok, err
			}
		}
		return a.addOrLayer(args[0])
	}
	if a.Overlay != "" {
		if !slices.Contains(names, a.Overlay) {
			a.errorf(fmt.Sprintf("overlays/%s: no such overlay in %s (--overlay)", a.Overlay, root))
			return "", false, false, ExitError{1}
		}
		ok, err := a.hasThisMachine(root, a.Overlay, q, key)
		if ok {
			a.info(fmt.Sprintf("overlays/%s has this machine", a.Overlay))
		}
		return a.Overlay, false, ok, err
	}
	var mine []string
	for _, n := range names {
		h, err := a.overlayHosts(filepath.Join(root, "overlays", n))
		if err != nil {
			a.warn(fmt.Sprintf("overlays/%s: %v", n, err))
			continue
		}
		if workspace.Matches(h, q) {
			mine = append(mine, n)
		}
	}
	switch len(mine) {
	case 1:
		a.info(fmt.Sprintf("overlays/%s has this machine", mine[0]))
		return mine[0], false, true, nil
	case 0:
	default:
		a.errorf(fmt.Sprintf("overlays %s all have this machine: run piste setup --overlay <name>", strings.Join(mine, ", ")))
		return "", false, false, ExitError{1}
	}
	// No answer (Ctrl-D, a closed pipe, Ctrl+C) is not a choice of init
	// with every default; the checkout may be new, so nothing claims it
	// wasn't written.
	stopped := func() (string, bool, bool, error) {
		a.errorf("No answer: stopped. Run piste setup again.")
		return "", false, false, ExitError{1}
	}
	url, err := a.setupChoice()
	if errors.Is(err, errStopped) {
		return stopped()
	}
	if err != nil {
		return "", false, false, err
	}
	if url != "" {
		return a.addOrLayer(url)
	}
	name, ok, err = a.initOverlay(func(string) bool { return false }, nil, initOpts{})
	if errors.Is(err, errStopped) {
		return stopped()
	}
	return name, name != "", ok, err
}

// setupChoice: the git URL of an overlay to add, or "" for a new one;
// errStopped when there's no answer.
func (a *App) setupChoice() (string, error) {
	if !a.useForms() {
		p := a.prompter()
		url := p.ask("Git URL of an overlay to add (Enter: create a new one)", "")
		if p.eof {
			return "", errStopped
		}
		return url, nil
	}
	// No URL for "Add one I already have": the choice again (a check on
	// the URL would keep Shift+Tab from going back to it).
	kind, url := "create", ""
	for {
		err := a.form(huh.NewForm(
			huh.NewGroup(huh.NewSelect[string]().Title("Your overlay").Options(
				huh.NewOption("Create a new overlay", "create"),
				huh.NewOption("Add one I already have", "add"),
			).Value(&kind)),
			huh.NewGroup(huh.NewInput().Title("Its git URL").Value(&url).
				Description("Empty, or Shift+Tab: back to the choice.")).
				WithHideFunc(func() bool { return kind != "add" }),
		))
		if err != nil || kind != "add" {
			return "", err
		}
		if url = strings.TrimSpace(url); url != "" {
			return url, nil
		}
	}
}

// offerSwitch offers the first switch: Later unless taken (Ctrl+C too:
// the overlay is built, nothing is lost).
func (a *App) offerSwitch() (bool, error) {
	if !a.useForms() {
		return strings.HasPrefix(strings.ToLower(a.prompter().ask("Switch this machine to it now? y/n", "n")), "y"), nil
	}
	ok, err := a.askYesNo("Switch this machine to it now?", "Switch now", "Later", false)
	if errors.Is(err, errStopped) {
		return false, nil
	}
	return ok, err
}

// offerHostAdd asks whether to add this machine to the overlay setup found
// without it: yes by default; no answer (the end of the input, Ctrl+C) is a
// stop, not a yes.
func (a *App) offerHostAdd() (bool, error) {
	if !a.useForms() {
		p := a.prompter()
		v := p.ask("Add this machine to it? y/n", "y")
		if p.eof {
			return false, nil
		}
		return strings.HasPrefix(strings.ToLower(v), "y"), nil
	}
	ok, err := a.askYesNo("Add this machine to it?", "Add it", "Not now", true)
	if errors.Is(err, errStopped) {
		return false, nil
	}
	return ok, err
}

// setupSecretsReady: this machine's age key can read every secrets file of
// the overlay at dir (no key and a secrets file is not ready).
func (a *App) setupSecretsReady(dir string) bool {
	home, _ := os.UserHomeDir()
	return secretsReady(dir, agePubkey(ageKeyPath(home)))
}

// hasThisMachine: overlays/<n>'s hosts include this machine (else it says
// so).
func (a *App) hasThisMachine(root, n string, q workspace.Query, key string) (bool, error) {
	h, err := a.overlayHosts(filepath.Join(root, "overlays", n))
	if err != nil {
		return false, err
	}
	if !workspace.Matches(h, q) {
		a.noHost(n, key, false)
		return false, nil
	}
	return true, nil
}

// setupCheckout is the piste checkout setup works in: the one around the
// cwd, else --dir (~/piste) if it is one (through a symlink too), else a
// fresh clone there, or into it when it is an empty directory, at the
// newest release. Anything else at --dir is refused, untouched. explicit:
// --dir or --from was given.
func (a *App) setupCheckout(from, dir string, explicit bool) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if root, ok := workspace.FindCheckout(cwd); ok {
		if explicit {
			a.warn("--dir/--from: not used, setup runs in the checkout at " + root)
		}
		a.info("Using the piste checkout at " + root)
		return root, nil
	}
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, "piste")
	}
	if dir, err = filepath.Abs(dir); err != nil {
		return "", err
	}
	existed := false
	if _, err := os.Lstat(dir); err == nil {
		fi, err := os.Stat(dir)
		switch {
		case err == nil && fi.IsDir() && fileExists(filepath.Join(dir, workspace.Marker)):
			a.info("Using the piste checkout at " + dir)
			return dir, nil
		case err == nil && fi.IsDir() && emptyDir(dir):
			existed = true
		default:
			a.errorf(fmt.Sprintf("%s exists and isn't a piste checkout: pass --dir <where to clone piste>", dir))
			return "", ExitError{1}
		}
	}
	var tags []string
	out, err := runOut("git", "ls-remote", "--tags", "--refs", from)
	if err != nil {
		a.errorf(fmt.Sprintf("can't list %s's releases: %v", from, err))
		return "", ExitError{1}
	}
	for _, l := range strings.Split(out, "\n") {
		if i := strings.Index(l, "refs/tags/"); i >= 0 {
			tags = append(tags, strings.TrimSpace(l[i+len("refs/tags/"):]))
		}
	}
	tag := release.Latest(tags)
	// A release is a tag: no detached-HEAD lecture on the way.
	argv := []string{"-c", "advice.detachedHead=false", "clone", "-q"}
	if tag != "" {
		argv = append(argv, "--branch", tag)
	} else {
		a.warn("no release yet: on the default branch")
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	if err := a.gitIn(filepath.Dir(dir), append(argv, from, dir)...); err != nil {
		return "", err
	}
	if !fileExists(filepath.Join(dir, workspace.Marker)) {
		// Our fresh clone: away with it, so a re-run with the right --from
		// can clone there.
		unclone(dir, existed)
		a.errorf(fmt.Sprintf("%s is not a piste engine (no %s at its top): pass --from <the piste repo>", from, workspace.Marker))
		return "", ExitError{1}
	}
	at := tag
	if at == "" {
		at = "(default branch)"
	}
	a.success(fmt.Sprintf("Cloned piste %s into %s", at, dir))
	return dir, nil
}

// emptyDir: a directory with nothing in it.
func emptyDir(dir string) bool {
	es, err := os.ReadDir(dir)
	return err == nil && len(es) == 0
}

// unclone removes a clone setup just made: what is in dir when it was an
// empty directory before, else dir.
func unclone(dir string, existed bool) {
	if !existed {
		os.RemoveAll(dir)
		return
	}
	es, _ := os.ReadDir(dir)
	for _, e := range es {
		os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

// addOrLayer is setup's add <url>: an overlay is added; a team layer's URL
// starts this person's overlay on it instead (init --layer, created).
func (a *App) addOrLayer(url string) (name string, created, ok bool, err error) {
	name, ok, err = a.addOverlay(url, "", true)
	var el errLayer
	if !errors.As(err, &el) {
		return name, false, ok, err
	}
	// init removes the clone; this covers an init that stops before it.
	defer os.RemoveAll(el.dir)
	a.info(url + " is a team layer: starting your own overlay on it")
	name, ok, err = a.initOverlay(func(string) bool { return false }, nil, initOpts{layer: url, layerDir: el.dir})
	if errors.Is(err, errStopped) {
		a.errorf("No answer: stopped. Run piste setup again.")
		return "", false, false, ExitError{1}
	}
	return name, name != "", ok, err
}

// syncAfterSwitch brings the new generation's plugins, hooks and MCP servers
// to Claude Code and Codex: piste agents sync --user, then piste mcp sync,
// each a run of this piste. The new profile's bin directories go first on
// PATH, since claude and codex came with the switch. A failing sync warns;
// the switch stands.
func (a *App) syncAfterSwitch() {
	home, _ := os.UserHomeDir()
	path := strings.Join([]string{
		filepath.Join(home, ".nix-profile", "bin"),
		"/etc/profiles/per-user/" + a.Env.Getenv("USER") + "/bin",
		"/run/current-system/sw/bin",
		os.Getenv("PATH"),
	}, string(os.PathListSeparator))
	for _, args := range [][]string{{"agents", "sync", "--user", "--no-mcp"}, {"mcp", "sync"}} {
		shown := strings.TrimSuffix(strings.Join(args, " "), " --no-mcp")
		fmt.Fprintf(a.Stdout, "\n$ piste %s\n", shown)
		c := exec.Command(selfExe(), args...)
		c.Env = append(os.Environ(), "PATH="+path)
		c.Stdout, c.Stderr = a.Stdout, a.Stderr
		if err := c.Run(); err != nil {
			a.warn(fmt.Sprintf("piste %s didn't finish (%v): run it again in a new shell", shown, err))
		}
	}
}

// selfExe is the running piste, to run its own subcommands: os.Args[0] when
// it is a path (the Nix wrapper's), else found on PATH.
func selfExe() string {
	if strings.ContainsRune(os.Args[0], filepath.Separator) {
		return os.Args[0]
	}
	if p, err := exec.LookPath(os.Args[0]); err == nil {
		return p
	}
	return os.Args[0]
}
