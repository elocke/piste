package cli

// The mutating but reversible commands: sync, edit, clean, claude, update,
// update-overlays, add, pkg. Their external argv, exit codes, messages and
// file edits are pinned by cli/e2e/testdata/mutating.txtar.

import (
	"bufio"
	"bytes"
	"charm.land/huh/v2"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/packages"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/release"
	"github.com/elocke/piste/cli/internal/ui"
	"github.com/elocke/piste/cli/internal/workspace"
)

func init() {
	register("sync", newSync)
	register("edit", newEdit)
	register("clean", newClean)
	register("claude", newClaude)
	register("update", newUpdate)
	register("update-overlays", newUpdateOverlays)
	register("pkg", newPkg)
}

// passEnv is pass with extra environment.
func (a *App) passEnv(argv []string, dir string, env []string) (int, error) {
	if argv[0] == "nix" {
		if err := a.checkNix(); err != nil {
			return 1, err
		}
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, a.Stdout, a.Stderr
	err := c.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), nil
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "piste: %s: %v\n", argv[0], err)
		return 1, ExitError{1}
	}
	return 0, nil
}

// --- sync ----------------------------------------------------------------------

func newSync(a *App) *cobra.Command {
	var input string
	c := &cobra.Command{
		Use:    "sync",
		Hidden: true, // duplicates `piste update <input>`
		Short:  "Update flake inputs (like nix flake update)",
		Args:   cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			var argv []string
			if input != "" {
				a.info(fmt.Sprintf("Updating flake input '%s'...", input))
				argv = []string{"nix", "flake", "lock", "--update-input", input}
			} else {
				a.info("Updating all flake inputs...")
				argv = []string{"nix", "flake", "update"}
			}
			rc, err := a.pass(argv, p.FlakeDir)
			if err != nil {
				return err
			}
			if rc != 0 {
				a.errorf(fmt.Sprintf("Sync failed (exit code %d)", rc))
				return ExitError{1}
			}
			a.success("Flake inputs updated! Run 'piste switch' to apply.")
			return nil
		},
	}
	c.Flags().StringVarP(&input, "input", "i", "", "Update a single input (e.g. nixpkgs, piste)")
	return c
}

// --- edit / clean / claude -----------------------------------------------------------

func newEdit(a *App) *cobra.Command {
	return &cobra.Command{
		Use:    "edit [file]",
		Hidden: true, // maintainer tool
		Short:  "Open a config file in $EDITOR (default: flake.nix)",
		Args:   cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			file := "flake.nix"
			if len(args) == 1 {
				file = args[0]
			}
			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vim"
			}
			target := filepath.Join(p.FlakeDir, file)
			if _, err := os.Stat(target); err != nil {
				a.errorf("File not found: " + target)
				return ExitError{1}
			}
			rc, err := a.pass([]string{editor, target}, "")
			if err != nil {
				return err
			}
			if rc != 0 {
				return ExitError{1}
			}
			return nil
		},
	}
}

func newClean(a *App) *cobra.Command {
	var keepSince string
	var keep int
	var dry bool
	c := &cobra.Command{
		Use:   "clean",
		Short: "Clean up old generations (wraps nh clean: user on Linux, all on a Mac)",
		Long: "Delete old generations and garbage-collect the store, with nh clean. On Linux\n" +
			"it is nh clean user: only your own profiles, no sudo, and other users'\n" +
			"rollback points stay. On a Mac, nh clean all, as the system's generations are\n" +
			"nix-darwin's.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if _, err := exec.LookPath("nh"); err != nil {
				a.errorf("nh not found on PATH. Install via `piste switch` (nh is in devPkgs).")
				return ExitError{1}
			}
			scope := "user"
			if runtime.GOOS == "darwin" {
				scope = "all"
			}
			argv := []string{"nh", "clean", scope, "--keep-since", keepSince, "--keep", fmt.Sprint(keep)}
			if dry {
				argv = append(argv, "--dry")
			}
			a.info(fmt.Sprintf("Cleaning generations older than %s (keeping %d newest)...", keepSince, keep))
			rc, err := a.pass(argv, "")
			if err != nil {
				return err
			}
			if rc != 0 {
				a.errorf(fmt.Sprintf("Clean failed (exit code %d)", rc))
				if !a.Verbose {
					a.warn("Rerun with: piste --verbose clean")
				}
				return ExitError{1}
			}
			return nil
		},
	}
	c.Flags().StringVar(&keepSince, "keep-since", "30d", "Keep generations newer than this age (e.g. 7d, 30d, 2w). Default: 30d.")
	c.Flags().IntVar(&keep, "keep", 5, "Keep at least this many recent generations regardless of age. Default: 5.")
	c.Flags().BoolVar(&dry, "dry-run", false, "Preview what would be removed without deleting.")
	return c
}

// checkoutRoot is the piste checkout piste works in: the one the cwd is in (or,
// from a worktree of an overlay, the one around its main repo), else the
// one the agents manifest records, else ~/piste.
func checkoutRoot() (string, error) {
	cwd, _ := os.Getwd()
	if root, ok := workspace.FindCheckout(cwd); ok {
		return root, nil
	}
	for p := physical(cwd); ; p = filepath.Dir(p) {
		if main := platform.WorktreeMainCheckout(p); main != "" {
			if root, ok := workspace.FindCheckout(main); ok {
				return root, nil
			}
			break
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	home, _ := os.UserHomeDir()
	if root, ok := workspace.CheckoutFromManifest(filepath.Join(home, ".config", "piste-agents", "manifest.json")); ok {
		return root, nil
	}
	if _, err := os.Stat(filepath.Join(home, "piste", workspace.Marker)); err == nil {
		return physical(filepath.Join(home, "piste")), nil
	}
	return "", errors.New("no piste checkout: run piste inside one, or clone it to ~/piste")
}

func physical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func newClaude(a *App) *cobra.Command {
	return &cobra.Command{
		Use:                "claude [claude args...]",
		Short:              "Launch Claude Code at the piste checkout, with its overlays added",
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			// Sessions start at the checkout root, the only place every tool
			// sees both the engine's and the overlays' instructions.
			root, err := checkoutRoot()
			if err != nil {
				fmt.Fprintf(a.Stderr, "piste: claude: %v\n", err)
				return ExitError{1}
			}
			argv := []string{"claude"}
			overlays, _ := workspace.Overlays(root)
			dirs, dirsEnv := claudeAddDirs(root, overlays)
			argv = append(append(argv, dirs...), args...)
			bin, err := exec.LookPath("claude")
			if err != nil {
				fmt.Fprintf(a.Stderr, "piste: claude: %v\n", err)
				return ExitError{1}
			}
			if err := os.Chdir(root); err != nil {
				fmt.Fprintf(a.Stderr, "piste: claude: %v\n", err)
				return ExitError{1}
			}
			env := append(append(os.Environ(), "PWD="+root), dirsEnv...)
			return syscall.Exec(bin, argv, env)
		},
	}
}

// claudeAddDirs is how Claude Code started at a checkout root sees its
// overlays (piste claude, piste fix): an --add-dir for each, and the env
// that has it read their CLAUDE.md files too.
func claudeAddDirs(root string, overlays []string) (args, env []string) {
	for _, n := range overlays {
		args = append(args, "--add-dir", filepath.Join(root, "overlays", n))
	}
	return args, []string{"CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1"}
}

// --- update --------------------------------------------------------------------------------

func lockTimestamps(path string) map[string]int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]int64{}
	}
	var d struct {
		Nodes map[string]struct {
			Locked map[string]any `json:"locked"`
		} `json:"nodes"`
	}
	if json.Unmarshal(b, &d) != nil {
		return map[string]int64{}
	}
	out := map[string]int64{}
	for n, node := range d.Nodes {
		if n == "root" {
			continue
		}
		if ts, ok := node.Locked["lastModified"].(float64); ok && ts != 0 {
			out[n] = int64(ts)
		}
	}
	return out
}

func tsDate(ts int64) string { return time.Unix(ts, 0).UTC().Format("2006-01-02") }

func daysFloor(d time.Duration) int {
	h := d.Hours() / 24
	if h < 0 && h != float64(int(h)) {
		return int(h) - 1
	}
	return int(h)
}

// updateEngine is the update cascade's target: the piste checkout around the
// overlay at flakeDir (for a worktree of an overlay, around its main repo),
// when it has the engine's packages module.
func updateEngine(flakeDir string) string {
	here := physical(flakeDir)
	root := ""
	for _, d := range []string{here, platform.WorktreeMainCheckout(here)} {
		if d == "" {
			continue
		}
		if r, ok := workspace.FindCheckout(d); ok {
			if _, in := workspace.OverlayOf(r, d); in {
				root = r
				break
			}
		}
	}
	if root == "" {
		return ""
	}
	for _, f := range []string{"packages-module.nix", "packages.nix"} {
		if _, err := os.Stat(filepath.Join(root, "modules", f)); err == nil {
			return root
		}
	}
	return ""
}

// followsEngine: the overlay at flakeDir takes input from the engine
// ("X.follows = \"piste/X\"").
func followsEngine(flakeDir, input string) bool {
	b, err := os.ReadFile(filepath.Join(flakeDir, "flake.nix"))
	if err != nil {
		return false
	}
	re := regexp.MustCompile(regexp.QuoteMeta(input) + `\.follows\s*=\s*"` + release.Input + `/` + regexp.QuoteMeta(input) + `"`)
	return re.Match(b)
}

// engineOwned: input is the engine's (followed from it, or not the
// overlay's own), so it moves with piste releases, not on its own.
func engineOwned(flakeDir, input string) bool {
	return followsEngine(flakeDir, input) || !inputDeclared(flakeDir, input)
}

func inputDeclared(flakeDir, input string) bool {
	b, err := os.ReadFile(filepath.Join(flakeDir, "flake.nix"))
	if err != nil {
		return false
	}
	q := regexp.QuoteMeta(input)
	re := regexp.MustCompile(`\b` + q + `\.(url|follows)\s*=|\b` + q + `\s*=\s*\{`)
	return re.Match(b)
}

func (a *App) gitSSHEnv(dir string) []string {
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"git", "config", "--get", "core.sshCommand"}, Dir: dir})
	if err == nil {
		if c := strings.TrimSpace(string(res.Stdout)); c != "" {
			return []string{"GIT_SSH_COMMAND=" + c}
		}
	}
	return nil
}

func (a *App) updateTable(title string, before, after map[string]int64) int {
	s := ui.For(a.Stdout)
	fmt.Fprint(a.Stdout, s.Panel("", title))
	names := map[string]bool{}
	for k := range before {
		names[k] = true
	}
	for k := range after {
		names[k] = true
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var rows [][]string
	updated := 0
	for _, n := range keys {
		b, bok := before[n]
		af, aok := after[n]
		switch {
		case !bok && aok:
			rows = append(rows, []string{n, "new", tsDate(af), "", "added"})
			updated++
		case bok && !aok:
			rows = append(rows, []string{n, tsDate(b), "removed", "", "removed"})
			updated++
		case b == af:
			rows = append(rows, []string{n, tsDate(b), tsDate(af), "-", "unchanged"})
		default:
			j := daysFloor(time.Unix(af, 0).Sub(time.Unix(b, 0)))
			if j < 0 {
				j = -j
			}
			st := "updated"
			if j > 30 {
				st = fmt.Sprintf("large jump (%dd)", j)
			}
			rows = append(rows, []string{n, tsDate(b), tsDate(af), fmt.Sprintf("%dd", j), st})
			updated++
		}
	}
	fmt.Fprint(a.Stdout, s.Table([]string{"Input", "Before", "After", "Jump", "Status"}, rows))
	return updated
}

// runChecked runs argv; a failure ends the command with exit 1.
func (a *App) runChecked(argv []string, dir string, env []string) error {
	rc, err := a.passEnv(argv, dir, env)
	if err != nil {
		return err
	}
	if rc != 0 {
		a.errorf(fmt.Sprintf("%s exited with status %d.", execx.Quote(argv), rc))
		return ExitError{1}
	}
	return nil
}

func newUpdate(a *App) *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "update [input]",
		Short: "Move to the newest piste release and update the overlays' inputs",
		Long: "With no input: fetches piste's releases, prints the notes of each release between\n" +
			"yours and the newest (Upgrading notes set apart), moves the checkout to the newest\n" +
			"release (a checkout on a branch pulls it instead), moves every overlay's engine pin\n" +
			"to that release, and updates the overlays' other inputs. --dry-run prints the notes\n" +
			"and the moves and changes nothing. Going back: git -C <checkout> checkout <release>,\n" +
			"and git checkout flake.nix flake.lock in each overlay.\n\n" +
			"piste update <input> updates one input of this machine's overlay; an input the\n" +
			"engine owns (nixpkgs, home-manager when it follows the engine's) moves with piste\n" +
			"releases instead. piste update overlays is piste update-overlays.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return a.updateRelease(dryRun)
			}
			p, err := a.plat()
			if err != nil {
				return err
			}
			if args[0] == "overlays" {
				return a.updateOverlays(p, dryRun)
			}
			if dryRun {
				a.errorf("--dry-run takes no input: piste update --dry-run shows what piste update would do")
				return ExitError{1}
			}
			return a.updateInput(p, args[0])
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Print the notes and the moves without changing anything")
	return c
}

// updateInput is `piste update <input>`: one input of this machine's overlay.
func (a *App) updateInput(p platform.Platform, input string) error {
	if engineOwned(p.FlakeDir, input) {
		a.errorf(fmt.Sprintf("%s comes from the engine: it moves with piste releases (piste update)", input))
		return ExitError{1}
	}
	lock := filepath.Join(p.FlakeDir, "flake.lock")
	before := lockTimestamps(lock)
	a.info(fmt.Sprintf("Updating flake input: %s...", input))
	if err := a.runChecked([]string{"nix", "flake", "update", input, "--flake", p.FlakeDir}, p.FlakeDir, a.gitSSHEnv(p.FlakeDir)); err != nil {
		return err
	}
	n := a.updateTable("Flake input update summary", before, lockTimestamps(lock))
	fmt.Fprintln(a.Stdout)
	if n == 0 {
		a.info("All inputs already up to date.")
		return nil
	}
	a.success(fmt.Sprintf("Updated %d input(s). Run 'piste build' to test.", n))
	return nil
}

// gitCapture runs git in dir, its stdout and stderr captured.
func gitCapture(dir string, env []string, args ...string) (stdout, stderr string, err error) {
	var o, e bytes.Buffer
	c := exec.Command("git", args...)
	c.Dir, c.Stdout, c.Stderr = dir, &o, &e
	c.Env = append(os.Environ(), env...)
	err = c.Run()
	return o.String(), e.String(), err
}

// gitStop reports a git command that stopped piste update, with git's reason
// and the command that fixes it.
func (a *App) gitStop(root, what, branch, stderr string) error {
	why := gitFailure(stderr)
	if why == "" {
		why = "it failed"
	}
	a.errorf(fmt.Sprintf("piste update: %s in %s: %s", what, root, why))
	low := strings.ToLower(stderr)
	fix := "Fix that in " + root + ", then run piste update again."
	switch {
	case strings.Contains(low, "untracked working tree files"):
		// git stash leaves untracked files where they are.
		var named []string
		for _, l := range strings.Split(stderr, "\n") {
			if strings.HasPrefix(l, "\t") && strings.TrimSpace(l) != "" {
				named = append(named, strings.TrimSpace(l))
			}
		}
		files := "the files git names"
		if len(named) > 0 {
			files = strings.Join(named, ", ")
		}
		fix = "Move or remove " + files + " in " + root + " (or git -C " + root + " stash -u), then run piste update again."
	case strings.Contains(low, "would be overwritten") || strings.Contains(low, "local changes"):
		fix = "Commit or stash your changes (git -C " + root + " stash), then run piste update again."
	case strings.Contains(low, "no tracking information"):
		fix = "Give " + branch + " its upstream (git -C " + root + " branch --set-upstream-to=origin/" + branch + "), then run piste update again."
	case strings.Contains(low, "not possible to fast-forward") || strings.Contains(low, "diverg"):
		fix = branch + " has commits origin doesn't: rebase them (git -C " + root + " pull --rebase), then run piste update again."
	case strings.Contains(low, "repository") || strings.Contains(low, "could not resolve") || strings.Contains(low, "connect"):
		fix = "Check the checkout's origin (git -C " + root + " remote -v) and your network, then run piste update again."
	}
	a.errorf(fix)
	return ExitError{1}
}

// printNotes prints each release's notes, Upgrading set apart; true if any
// release has Upgrading notes.
func (a *App) printNotes(rs []release.Section) bool {
	if len(rs) == 0 {
		return false
	}
	s := ui.For(a.Stdout)
	upgrading := false
	line := func(l string) {
		if strings.TrimSpace(l) == "" {
			fmt.Fprintln(a.Stdout)
			return
		}
		fmt.Fprintln(a.Stdout, "  "+l)
	}
	fmt.Fprintln(a.Stdout)
	for _, r := range rs {
		head := r.Version
		if r.Date != "" {
			head += " (" + r.Date + ")"
		}
		fmt.Fprintln(a.Stdout, s.Bold.Render(head))
		for _, l := range r.Changes {
			line(l)
		}
		if len(r.Upgrading) > 0 {
			upgrading = true
			fmt.Fprintln(a.Stdout, "  "+s.Warn.Render("Upgrading:"))
			for _, l := range r.Upgrading {
				line(l)
			}
		}
	}
	fmt.Fprintln(a.Stdout)
	return upgrading
}

// updateRelease is `piste update`: the checkout to the newest release (or a
// branch's pull), every overlay's engine pin to that release, then each
// overlay's inputs.
func (a *App) updateRelease(dryRun bool) error {
	root, err := checkoutRoot()
	if err != nil {
		a.errorf(err.Error())
		return ExitError{1}
	}
	env := a.gitSSHEnv(root)
	a.info("Fetching piste's releases...")
	if _, e, err := gitCapture(root, env, "fetch", "-q", "--tags", "origin"); err != nil {
		return a.gitStop(root, "git fetch", "", e)
	}
	tags, _, _ := gitCapture(root, nil, "tag", "-l")
	newest := release.Latest(strings.Fields(tags))
	// A branch first: on main, a release commit carries its tag too, and
	// a checkout on main must pull, not detach onto the tag.
	branch, _, _ := gitCapture(root, nil, "symbolic-ref", "-q", "--short", "HEAD")
	branch = strings.TrimSpace(branch)
	yours := ""
	if branch == "" {
		yours = release.HeadRelease(root)
	}
	overlays, _ := workspace.Overlays(root)
	upgrading := false
	switch {
	case branch == "" && yours == "":
		to := newest
		if to == "" {
			to = "main"
		}
		a.errorf(fmt.Sprintf("piste update: %s is on neither a release nor a branch.", root))
		a.errorf(fmt.Sprintf("Check one out (git -C %s checkout %s), then run piste update again.", root, to))
		return ExitError{1}
	case branch != "" && newest == "":
		a.info(fmt.Sprintf("You're on %s. No release yet.", branch))
	case branch != "":
		a.info(fmt.Sprintf("You're on %s. Newest release: %s.", branch, newest))
	default:
		if release.Newer(newest, yours) {
			a.info(fmt.Sprintf("You're on %s. Newest: %s.", yours, newest))
		} else {
			a.info(fmt.Sprintf("You're on %s, the newest release.", yours))
		}
		// The notes start from the lowest of the checkout and the pins: run
		// again after an overlay failed, the checkout is already on the
		// newest, but the pins still move across those releases.
		from := yours
		if low, name := lowestPin(root, overlays); low != "" && release.Newer(from, low) {
			from = low
			a.info(fmt.Sprintf("overlays/%s's engine is on %s.", name, low))
		}
		if release.Newer(newest, from) {
			if text, _, err := gitCapture(root, nil, "show", newest+":CHANGELOG.md"); err == nil {
				upgrading = a.printNotes(release.Between(text, from, newest))
			}
		}
	}

	// The checkout first: if git refuses, no overlay is touched.
	switch {
	case dryRun && branch != "":
		a.info(fmt.Sprintf("Would pull %s (git pull --ff-only)", branch))
	case dryRun && release.Newer(newest, yours):
		a.info(fmt.Sprintf("Would move piste: %s → %s", yours, newest))
	case dryRun:
	case branch != "":
		before, _, _ := gitCapture(root, nil, "rev-parse", "--short", "HEAD")
		if _, e, err := gitCapture(root, env, "pull", "--ff-only", "-q"); err != nil {
			return a.gitStop(root, "git pull --ff-only", branch, e)
		}
		after, _, _ := gitCapture(root, nil, "rev-parse", "--short", "HEAD")
		if before, after = strings.TrimSpace(before), strings.TrimSpace(after); before == after {
			a.success(fmt.Sprintf("✓ piste: %s is up to date", branch))
		} else {
			a.success(fmt.Sprintf("✓ piste: %s %s → %s", branch, before, after))
		}
	case release.Newer(newest, yours):
		if _, e, err := gitCapture(root, nil, "-c", "advice.detachedHead=false", "checkout", "-q", newest); err != nil {
			return a.gitStop(root, "git checkout "+newest, "", e)
		}
		a.success(fmt.Sprintf("✓ piste: %s → %s", yours, newest))
	}

	failed := false
	var commit []string
	for _, name := range overlays {
		changed, ok := a.updateOverlayPin(root, name, newest, dryRun)
		failed = failed || !ok
		if changed {
			commit = append(commit, "overlays/"+name)
		}
	}
	if dryRun {
		a.info("Dry run: nothing changed.")
		return nil
	}
	switch {
	case failed:
		a.errorf("Fix the overlays above, then run piste update again.")
	case upgrading:
		a.info("Next: read the Upgrading notes above, then piste switch.")
	default:
		a.info("Next: piste switch.")
	}
	for _, o := range commit {
		a.info(fmt.Sprintf("Commit %s's flake.nix and flake.lock once it works.", o))
	}
	if failed {
		return ExitError{1}
	}
	return nil
}

// lowestPin is the lowest release the overlays' engine pins name and the
// first overlay pinned to it; "" when no pin names a release.
func lowestPin(root string, overlays []string) (tag, name string) {
	for _, o := range overlays {
		b, err := os.ReadFile(filepath.Join(root, "overlays", o, "flake.nix"))
		if err != nil {
			continue
		}
		url, _ := release.ReadPin(string(b))
		if t := release.PinTag(url); t != "" && (tag == "" || release.Newer(tag, t)) {
			tag, name = t, o
		}
	}
	return tag, name
}

// updateOverlayPin moves overlays/<name>'s engine pin to newest (a release
// pin only) and updates its inputs. changed: its flake.nix or flake.lock
// changed; !ok: it failed, and its flake.nix is as it was.
func (a *App) updateOverlayPin(root, name, newest string, dryRun bool) (changed, ok bool) {
	dir := filepath.Join(root, "overlays", name)
	label := "overlays/" + name
	nixFile, lockFile := filepath.Join(dir, "flake.nix"), filepath.Join(dir, "flake.lock")
	orig, err := os.ReadFile(nixFile)
	if err != nil {
		a.errorf(fmt.Sprintf("%s: %v", label, err))
		return false, false
	}
	origLock, _ := os.ReadFile(lockFile)
	url, pinned := release.ReadPin(string(orig))
	tag := release.PinTag(url)
	switch {
	case !pinned && release.Unreadable(string(orig)):
		// Declared, but not in a form piste update reads: it fails, rather
		// than leaving the pin on another release than the checkout's.
		if newest != "" {
			a.errorf(fmt.Sprintf("%s: couldn't read its engine pin: give its %s input a url = \"…\" (piste update moves that), or move it to %s by hand", label, release.Input, newest))
			return false, false
		}
	case !pinned:
		a.warn(fmt.Sprintf("%s: no %s input, so no engine pin to move", label, release.Input))
	case tag == "":
		a.warn(fmt.Sprintf("%s: engine pin %s isn't a release: left as it is", label, url))
	case !release.Newer(newest, tag):
		a.success(fmt.Sprintf("✓ %s: engine already %s", label, tag))
	case dryRun:
		a.info(fmt.Sprintf("Would move %s: engine %s → %s", label, tag, newest))
	default:
		moved, _ := release.MovePin(string(orig), newest)
		if err := os.WriteFile(nixFile, []byte(moved), 0o644); err != nil {
			a.errorf(fmt.Sprintf("%s: %v", label, err))
			return false, false
		}
		a.success(fmt.Sprintf("✓ %s: engine %s → %s", label, tag, newest))
	}
	if dryRun {
		a.info(fmt.Sprintf("Would update %s's inputs (nix flake update)", label))
		return false, true
	}
	before := lockTimestamps(lockFile)
	if err := a.runChecked([]string{"nix", "flake", "update", "--flake", dir}, dir, a.gitSSHEnv(dir)); err != nil {
		// Its pin doesn't get ahead of its lock.
		_ = os.WriteFile(nixFile, orig, 0o644)
		a.errorf(fmt.Sprintf("%s: nix flake update failed: its flake.nix is as it was", label))
		return false, false
	}
	if after := lockTimestamps(lockFile); len(before) > 0 || len(after) > 0 {
		a.updateTable(label+" inputs", before, after)
	}
	nowNix, _ := os.ReadFile(nixFile)
	nowLock, _ := os.ReadFile(lockFile)
	return !bytes.Equal(nowNix, orig) || !bytes.Equal(nowLock, origLock), true
}

// --- update-overlays --------------------------------------------------------------------------

var (
	overlayMeta = regexp.MustCompile(`#\s*@overlay-update\s+(github|herdr):(\S+)`)
	versionRe   = regexp.MustCompile(`version\s*=\s*"([^"]+)"`)
	assetRe     = regexp.MustCompile(`asset\s*=\s*"([^"]+)"`)
)

// overlayPkg: a pkgs/overlays file the updater keeps current. source is where
// releases are read: github (owner/repo, GitHub's latest release) or herdr
// (app|core, herdr's feed at releases.herdr.dev/<kind>/latest.json).
type overlayPkg struct{ file, source, repo, version string }

// herdrReleases is herdr's release feed: a version and, per target, the
// download and its sha256 (the hash fetchurl checks, so nothing is prefetched).
const herdrReleases = "https://releases.herdr.dev"

func (a *App) latestHerdr(kind string) (string, []map[string]string, bool) {
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"curl", "-fsSL", herdrReleases + "/" + kind + "/latest.json"}})
	if err != nil {
		return "", nil, false
	}
	var d struct {
		Version string `json:"version"`
		Targets map[string]struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"targets"`
	}
	if json.Unmarshal(res.Stdout, &d) != nil || d.Version == "" {
		return "", nil, false
	}
	var assets []map[string]string
	for _, t := range d.Targets {
		assets = append(assets, map[string]string{"name": path.Base(t.URL), "url": t.URL, "sha256": t.SHA256})
	}
	return d.Version, assets, true
}

func (a *App) latestRelease(repo string) (string, []map[string]string, bool) {
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"gh", "api", "repos/" + repo + "/releases/latest",
		"--jq", `{tag: .tag_name, assets: [.assets[] | {name, url: .browser_download_url}]}`}})
	if err != nil {
		return "", nil, false
	}
	var d struct {
		Tag    string              `json:"tag"`
		Assets []map[string]string `json:"assets"`
	}
	if json.Unmarshal(res.Stdout, &d) != nil || d.Tag == "" {
		return "", nil, false
	}
	return strings.TrimLeft(d.Tag, "v"), d.Assets, true
}

func (a *App) prefetch(url string) (string, bool) {
	// The file's own hash: every updated overlay downloads with fetchurl.
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"nix-prefetch-url", "--type", "sha256", url}})
	if err != nil {
		return "", false
	}
	l := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	return l[len(l)-1], true
}

func (a *App) updateOverlay(pkg overlayPkg, dryRun bool) [4]string {
	name := strings.TrimSuffix(filepath.Base(pkg.file), ".nix")
	latest, assets, ok := a.latestRelease(pkg.repo)
	if pkg.source == "herdr" {
		latest, assets, ok = a.latestHerdr(pkg.repo)
	}
	if !ok {
		return [4]string{name, pkg.version, "?", "error"}
	}
	if latest == pkg.version {
		return [4]string{name, pkg.version, latest, "up-to-date"}
	}
	if dryRun {
		return [4]string{name, pkg.version, latest, "update available"}
	}
	b, _ := os.ReadFile(pkg.file)
	content := string(b)
	hashes := map[string]string{}
	var order []string
	for _, m := range assetRe.FindAllStringSubmatch(content, -1) {
		an := m[1]
		na := an
		if strings.Contains(an, pkg.version) {
			na = strings.ReplaceAll(an, pkg.version, latest)
		}
		var asset map[string]string
		for _, as := range assets {
			if as["name"] == na {
				asset = as
				break
			}
		}
		if asset == nil {
			for _, as := range assets {
				if as["name"] == an {
					asset = as
					break
				}
			}
		}
		if asset == nil {
			return [4]string{name, pkg.version, latest, "missing asset: " + na}
		}
		h := asset["sha256"]
		if h == "" {
			a.info(fmt.Sprintf("  Prefetching %s...", na))
			var ok bool
			if h, ok = a.prefetch(asset["url"]); !ok {
				return [4]string{name, pkg.version, latest, "prefetch failed: " + na}
			}
		}
		if _, seen := hashes[an]; !seen {
			order = append(order, an)
		}
		hashes[an] = h
	}
	content = regexp.MustCompile(`(version\s*=\s*)"[^"]+"`).ReplaceAllString(content, `${1}"`+latest+`"`)
	for _, an := range order {
		re := regexp.MustCompile(`(asset\s*=\s*"` + regexp.QuoteMeta(an) + `";\s*\n\s*hash\s*=\s*)"[^"]+"`)
		content = re.ReplaceAllString(content, `${1}"`+hashes[an]+`"`)
	}
	os.WriteFile(pkg.file, []byte(content), 0o644)
	return [4]string{name, pkg.version, latest, "updated"}
}

func (a *App) updateOverlays(p platform.Platform, dryRun bool) error {
	dir := filepath.Join(p.FlakeDir, "pkgs", "overlays")
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		dir = filepath.Join(updateEngine(p.FlakeDir), "pkgs", "overlays")
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			a.errorf("No pkgs/overlays/ directory found")
			return nil
		}
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.nix"))
	sort.Strings(files)
	var pkgs []overlayPkg
	for _, f := range files {
		b, _ := os.ReadFile(f)
		m := overlayMeta.FindStringSubmatch(string(b))
		v := versionRe.FindStringSubmatch(string(b))
		if m != nil && v != nil {
			pkgs = append(pkgs, overlayPkg{f, m[1], m[2], v[1]})
		}
	}
	if len(pkgs) == 0 {
		a.info("No overlay packages with @overlay-update metadata found.")
		return nil
	}
	s := ui.For(a.Stdout)
	title := "Overlay package updates"
	if dryRun {
		title += " (dry run)"
	}
	fmt.Fprint(a.Stdout, s.Panel("", title))
	var rows [][]string
	updated := 0
	for _, pk := range pkgs {
		a.info(fmt.Sprintf("Checking %s...", pk.repo))
		r := a.updateOverlay(pk, dryRun)
		if r[3] == "updated" {
			updated++
		}
		rows = append(rows, r[:])
	}
	fmt.Fprint(a.Stdout, s.Table([]string{"Package", "Current", "Latest", "Status"}, rows))
	if updated > 0 {
		a.success(fmt.Sprintf("Updated %d package(s). Run 'piste build' to test.", updated))
	}
	return nil
}

func newUpdateOverlays(a *App) *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:    "update-overlays",
		Hidden: true, // maintainer tool: edits the engine from a checkout
		Short:  "Check and update overlay packages from GitHub releases",
		Args:   cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			return a.updateOverlays(p, dryRun)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Check for updates without applying them")
	return c
}

// --- add --------------------------------------------------------------------------------------

// addScopes: the flakes `piste pkg add` can target in the checkout: each
// overlay by name. (Never the engine: a release checkout isn't edited.)
func addScopes(root string) []string {
	if root == "" {
		return nil
	}
	names, _ := workspace.Overlays(root)
	return names
}

func scopeDir(root, scope string) string {
	return filepath.Join(root, "overlays", scope)
}

// resolveScope: --scope, else the overlay the cwd is in, else a pick among
// the checkout's scopes (default: this machine's overlay), else the flake.
func (a *App) resolveScope(p platform.Platform, scope string) (string, error) {
	root := p.Checkout
	avail := addScopes(root)
	if scope != "" {
		for _, n := range avail {
			if n == scope {
				return scopeDir(root, n), nil
			}
		}
		if len(avail) == 0 {
			fmt.Fprintf(a.Stderr, "Error: Invalid value for '--scope': '%s': not in a piste checkout.\n", scope)
		} else {
			fmt.Fprintf(a.Stderr, "Error: Invalid value for '--scope': '%s' is not one of '%s'.\n", scope, strings.Join(avail, "', '"))
		}
		return "", ExitError{2}
	}
	cwd, _ := os.Getwd()
	if root != "" {
		if n, ok := workspace.OverlayOf(root, cwd); ok {
			return scopeDir(root, n), nil
		}
	}
	switch {
	case len(avail) > 1:
		def := avail[0]
		if p.Overlay != "" {
			def = p.Overlay
		}
		a.info("Several flakes in the checkout. Pick one (or pass --scope next time):")
		if a.useForms() {
			opts := make([]huh.Option[string], len(avail))
			for i, n := range avail {
				opts[i] = huh.NewOption(fmt.Sprintf("%s  -> %s", n, scopeDir(root, n)), n)
			}
			n, err := a.pickOne("Scope", opts, def)
			if errors.Is(err, errStopped) {
				return "", ExitError{1}
			}
			if err != nil {
				return "", err
			}
			return scopeDir(root, n), nil
		}
		for _, n := range avail {
			fmt.Fprintf(a.Stdout, "  %s  -> %s\n", n, scopeDir(root, n))
		}
		in := bufio.NewReader(os.Stdin)
		for {
			fmt.Fprintf(a.Stdout, "Scope [%s] (%s): ", strings.Join(avail, "/"), def)
			line, err := in.ReadString('\n')
			line = strings.TrimSpace(line)
			if line == "" {
				if err != nil && line == "" && err.Error() == "EOF" {
					return "", ExitError{1}
				}
				return scopeDir(root, def), nil
			}
			for _, n := range avail {
				if n == line {
					return scopeDir(root, n), nil
				}
			}
			fmt.Fprintln(a.Stdout, "Please select one of the available options")
		}
	case len(avail) == 1:
		return scopeDir(root, avail[0]), nil
	}
	return p.FlakeDir, nil
}

// newPkg is `piste pkg`: package edits (they live here, not under `add`,
// which clones overlays).
func newPkg(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:    "pkg",
		Hidden: true, // pkg add is hidden until it writes the overlay
		Short:  "Manage packages in the configuration",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	c.AddCommand(newAdd(a))
	return c
}

func newAdd(a *App) *cobra.Command {
	var scope string
	c := &cobra.Command{
		Use:    "add <name> | <pkg|cask|brew|pnpm> <name>",
		Hidden: true, // hidden until it writes the overlay
		Short:  "Add a package to the configuration",
		Args:   cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			flakeDir, err := a.resolveScope(p, scope)
			if err != nil {
				return err
			}
			pkgsFile := filepath.Join(flakeDir, "modules", "packages-module.nix")
			a.info("Target flake: " + flakeDir)
			switch args[0] {
			case "pkg", "cask", "brew", "pnpm":
				if len(args) < 2 {
					a.errorf(fmt.Sprintf("Usage: piste pkg add %s <name>", args[0]))
					return ExitError{1}
				}
				return a.addTyped(p, args[0], args[1], pkgsFile, flakeDir)
			}
			return a.addAuto(p, args[0], pkgsFile)
		},
	}
	f := c.Flags()
	f.StringVar(&scope, "scope", "", "Target overlay of the checkout, by name")
	return c
}

func (a *App) addAuto(p platform.Platform, name, pkgsFile string) error {
	ctx := context.Background()
	if packages.InFile(name, pkgsFile) {
		a.warn(fmt.Sprintf("'%s' already exists in %s", name, filepath.Base(pkgsFile)))
		return nil
	}
	s := ui.For(a.Stdout)
	fmt.Fprint(a.Stdout, s.Panel("", fmt.Sprintf("Searching for '%s'...", name)))
	a.info("Checking nixpkgs...")
	r := a.runner()
	nixM := packages.SearchNixpkgs(ctx, r, name)
	exact := packages.ExactInNixpkgs(ctx, r, name)
	var brewM, caskM []string
	if p.IsDarwin {
		a.info("Checking homebrew...")
		brewM = packages.SearchBrew(ctx, r, name)
		caskM = packages.SearchBrewCask(ctx, r, name)
	}
	var rows [][]string
	if exact {
		rows = append(rows, []string{"nixpkgs", name, "exact match -- recommended"})
	}
	for i, m := range nixM {
		if i >= 5 {
			break
		}
		if m.Name != name {
			rows = append(rows, []string{"nixpkgs", fmt.Sprintf("%s (%s)", m.Name, m.Version), m.Attr})
		}
	}
	for i, m := range brewM {
		if i < 5 {
			rows = append(rows, []string{"brew", m, ""})
		}
	}
	for i, m := range caskM {
		if i < 5 {
			rows = append(rows, []string{"cask", m, "GUI app"})
		}
	}
	if len(rows) == 0 {
		a.warn(fmt.Sprintf("No matches found for '%s'", name))
		return nil
	}
	fmt.Fprintln(a.Stdout, s.Bold.Render("Search Results"))
	fmt.Fprint(a.Stdout, s.Table([]string{"Source", "Package", "Note"}, rows))
	fmt.Fprintln(a.Stdout)
	has := func(l []string) bool {
		for _, x := range l {
			if x == name {
				return true
			}
		}
		return false
	}
	switch {
	case exact:
		a.success("Recommendation: piste pkg add pkg " + name)
		fmt.Fprintln(a.Stdout, "  Add to packages-module.nix cli or dev list for cross-platform support.")
	case has(caskM):
		a.success("Recommendation: piste pkg add cask " + name)
	case has(brewM):
		a.success("Recommendation: piste pkg add brew " + name)
	default:
		a.info("Use 'piste pkg add <type> <name>' to add a specific match.")
	}
	return nil
}

func caskBrewTarget(flakeDir, list, pkgsFile string) string {
	apps := filepath.Join(flakeDir, "modules", "apps.nix")
	if b, err := os.ReadFile(apps); err == nil && strings.Contains(string(b), "homebrew."+list+" = [") {
		return apps
	}
	return pkgsFile
}

func (a *App) addTyped(p platform.Platform, typ, name, pkgsFile, flakeDir string) error {
	ctx := context.Background()
	r := a.runner()
	s := ui.For(a.Stdout)
	switch typ {
	case "pkg":
		a.info(fmt.Sprintf("Searching nixpkgs for '%s'...", name))
		res := packages.SearchNixpkgs(ctx, r, name)
		if len(res) > 0 {
			var rows [][]string
			for i, m := range res {
				if i < 10 {
					rows = append(rows, []string{m.Name, m.Version, m.Attr})
				}
			}
			fmt.Fprint(a.Stdout, s.Table([]string{"Package", "Version", "Attribute"}, rows))
		} else {
			a.warn(fmt.Sprintf("'%s' not found in nixpkgs", name))
		}
		fmt.Fprintln(a.Stdout)
		a.info("Add to packages-module.nix: cli (shell tools) or dev (development tools)")
	case "cask":
		if p.IsLinux {
			a.errorf("Homebrew casks are not available on Linux")
			return ExitError{1}
		}
		t := caskBrewTarget(flakeDir, "casks", pkgsFile)
		if packages.InFile(name, t) {
			a.warn(fmt.Sprintf("'%s' already exists in %s", name, filepath.Base(t)))
			return nil
		}
		a.info("Adding cask to " + t)
		if ok, err := packages.AddToNixList(`"`+name+`"`, t, []string{"casks", "allCasks"}); err != nil {
			return err
		} else if ok {
			a.success(fmt.Sprintf(`Added "%s" to casks in %s`, name, filepath.Base(t)))
		} else {
			a.errorf("Could not find casks list in " + filepath.Base(t))
		}
	case "brew":
		if p.IsLinux {
			a.errorf("Homebrew brews are not available on Linux")
			return ExitError{1}
		}
		a.info(fmt.Sprintf("Checking if '%s' is available in nixpkgs...", name))
		if packages.ExactInNixpkgs(ctx, r, name) {
			a.success(fmt.Sprintf("'%s' is available in nixpkgs!", name))
			fmt.Fprintln(a.Stdout)
			a.warn("Recommendation: Add to nix packages instead of homebrew brews.")
			a.info("This ensures the package works on both macOS and Linux.")
			fmt.Fprintln(a.Stdout)
			fmt.Fprintln(a.Stdout, "  cli  - shell tools, utilities")
			fmt.Fprintln(a.Stdout, "  dev  - development tools, languages")
			fmt.Fprintln(a.Stdout)
			a.info("Add to the cli list in " + pkgsFile)
			return nil
		}
		t := caskBrewTarget(flakeDir, "brews", pkgsFile)
		a.warn(fmt.Sprintf("'%s' not found in nixpkgs, adding to brews in %s...", name, filepath.Base(t)))
		if packages.InFile(name, t) {
			a.warn(fmt.Sprintf("'%s' already exists in %s", name, filepath.Base(t)))
			return nil
		}
		if ok, err := packages.AddToNixList(`"`+name+`"`, t, []string{"brews", "allBrews"}); err != nil {
			return err
		} else if ok {
			a.success(fmt.Sprintf(`Added "%s" to brews in %s`, name, filepath.Base(t)))
		} else {
			a.errorf("Could not find brews list in " + filepath.Base(t))
		}
	case "pnpm":
		ts := packages.FindPnpmGlobalsFiles(flakeDir)
		if len(ts) == 0 {
			a.errorf(fmt.Sprintf("No '%s = [ ... ]' list found under %s", packages.PnpmListName, filepath.Join(flakeDir, "modules")))
			a.info("Declare one in a bucket module (e.g. modules/js-globals.nix) first.")
			return ExitError{1}
		}
		for _, t := range ts {
			if packages.InFile(name, t) {
				a.warn(fmt.Sprintf("'%s' already exists in %s pnpm globals", name, filepath.Base(t)))
				return nil
			}
		}
		t := ts[0]
		if len(ts) > 1 {
			var others []string
			for _, o := range ts[1:] {
				others = append(others, filepath.Base(o))
			}
			a.info("pnpm globals also declared in: " + strings.Join(others, ", "))
		}
		a.info(fmt.Sprintf("Adding pnpm global to %s: %s", filepath.Base(t), name))
		if ok, err := packages.AddToPnpmGlobals(name, t); err != nil {
			return err
		} else if ok {
			a.success(fmt.Sprintf(`Added "%s" to %s in %s`, name, packages.PnpmListName, filepath.Base(t)))
			a.info("Run 'piste switch' to install it.")
		} else {
			a.errorf(fmt.Sprintf("Could not find %s list in %s", packages.PnpmListName, filepath.Base(t)))
		}
	}
	return nil
}
