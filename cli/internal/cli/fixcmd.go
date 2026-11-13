package cli

// `piste fix`. After a failed build or switch it rebuilds what the switch
// builds, never activating, and opens Claude Code or Codex at the checkout
// with the error in hand and the piste-fix project skill. In the agent's own
// shell (no terminal) it only rebuilds and reports.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/envvar"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/workspace"
)

func init() {
	register("fix", newFix)
}

// fixAI is the AI CLI piste fix starts: Name is "claude" or "codex" ("" for
// none), Label how piste names it, Path where lookPath found it.
type fixAI struct{ Name, Label, Path string }

var fixLabels = map[string]string{"claude": "Claude Code", "codex": "Codex"}

// fixAgent is the AI piste fix would start, from $PISTE_AI:
// unset, claude on PATH, else codex; claude or codex, that one; none, no AI.
// why says why there's none, "" when PISTE_AI=none asked for that.
func fixAgent(lookPath func(string) (string, error), getenv func(string) string) (ai fixAI, why string) {
	want := strings.ToLower(strings.TrimSpace(envvar.Get(getenv, "AI")))
	switch want {
	case "":
		for _, n := range []string{"claude", "codex"} {
			if p, err := lookPath(n); err == nil {
				return fixAI{n, fixLabels[n], p}, ""
			}
		}
		return fixAI{}, "piste fix hands this to Claude Code or Codex, and neither is on PATH"
	case "none":
		return fixAI{}, ""
	case "claude", "codex":
		if p, err := lookPath(want); err == nil {
			return fixAI{want, fixLabels[want], p}, ""
		}
		return fixAI{}, fmt.Sprintf("PISTE_AI=%s, and %s isn't on PATH", want, want)
	}
	return fixAI{}, fmt.Sprintf("PISTE_AI=%s: piste fix starts claude or codex, or none for no AI", want)
}

// fixLogPath is where piste fix keeps the build's output:
// ${XDG_STATE_HOME:-~/.local/state}/piste/fix.log.
func fixLogPath(getenv func(string) string, home string) string {
	state := getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "piste", "fix.log")
}

// openFixLog opens the log for a new run: its directory made, the file
// emptied and only the person's (0600), whatever mode it had before.
func openFixLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./#-]+$`)

// shellJoin is argv as a shell would need it typed: an argument with any
// other character single-quoted.
func shellJoin(argv []string) string {
	out := make([]string, len(argv))
	for i, a := range argv {
		if shellSafe.MatchString(a) {
			out[i] = a
		} else {
			out[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(out, " ")
}

// shellPath is path as typed at a shell: under home as ~/…, unless the rest
// would need quoting (a quoted ~ isn't expanded), else quoted as shellJoin.
func shellPath(home, path string) string {
	if t := tilde(home, path); t != path && (t == "~" || shellSafe.MatchString(t[1:])) {
		return t
	}
	return shellJoin([]string{path})
}

func newFix(a *App) *cobra.Command {
	var host string
	c := &cobra.Command{
		Use:   "fix",
		Short: "Rebuild after a failed build or switch, and hand the error to Claude Code or Codex",
		Long: "Rebuild what `piste switch` builds (the checkout's engine, --show-trace), without\n" +
			"activating, and keep the output in ${XDG_STATE_HOME:-~/.local/state}/piste/fix.log.\n" +
			"At a terminal, then start Claude Code (else Codex) at the piste checkout with the\n" +
			"error and the piste-fix skill. PISTE_AI=claude|codex picks one; PISTE_AI=none, no AI.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runFix(host)
		},
	}
	hostFlags(c, &host)
	return c
}

// fixHint is the line after a failed build or switch that names piste fix
// and the AI it would start, with the failed command's --overlay and --host.
// No AI (or PISTE_AI=none), or no checkout for piste fix to
// start it at: no line.
func (a *App) fixHint(host string) {
	ai, _ := fixAgent(exec.LookPath, a.Env.Getenv)
	if ai.Name == "" {
		return
	}
	if _, err := checkoutRoot(); err != nil {
		return
	}
	a.warn("To have " + ai.Label + " fix it: " + a.fixCmd(a.Overlay, host))
}

// fixCmd is piste fix with each of --overlay and --host given.
func (a *App) fixCmd(overlay, host string) string {
	cmd := a.piste() + " fix"
	for _, f := range [][2]string{{"--overlay", overlay}, {"--host", host}} {
		if f[1] != "" {
			cmd += " " + f[0] + " " + shellJoin(f[1:])
		}
	}
	return cmd
}

// fixRecheck is how the session, at the checkout root, rebuilds what this
// run built: piste fix with the overlay it resolved named (the root may
// pick another), or, for a flake that isn't the root's or one of its
// overlays, run from that flake.
func (a *App) fixRecheck(p platform.Platform, root, host string) string {
	switch {
	case p.Checkout == root && p.Overlay != "":
		return a.fixCmd(p.Overlay, host)
	case p.FlakeDir != root:
		return "cd " + shellJoin([]string{p.FlakeDir}) + " && " + a.fixCmd(a.Overlay, host)
	}
	return a.fixCmd(a.Overlay, host)
}

// runFix is `piste fix`: host as for build and switch.
func (a *App) runFix(host string) error {
	p, err := a.plat()
	if err != nil {
		return err
	}
	o, err := a.engineOverride(p)
	if err != nil {
		return err
	}
	a.announceFlake(p, &o)
	t, err := a.targetFor(p, host)
	if err != nil {
		return err
	}
	name := t.Name
	argv, err := a.buildCmd(p, t, false, o)
	if err != nil {
		return err
	}
	if !slices.Contains(argv, "--show-trace") {
		argv = append(argv, "--show-trace")
	}

	a.info(fmt.Sprintf("Rebuilding %s as piste switch would, without activating...", name))
	logPath := fixLogPath(a.Env.Getenv, p.HomeDir)
	log, err := openFixLog(logPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "piste: fix: %v\n", err)
		return ExitError{1}
	}
	fmt.Fprintf(log, "# piste fix: %s, piste %s\n# ran: %s\n# build: %s\n\n",
		time.Now().UTC().Format(time.RFC3339), Version, shellJoin(append([]string{"piste"}, a.Args...)), shellJoin(argv))
	rc, out, err := a.passTee(argv, log)
	log.Close()
	if err != nil {
		return err
	}

	built, summary := rc == 0, ""
	if built {
		a.success(fmt.Sprintf("✓ %s builds, so the failure was in activation, or it's gone.", name))
	} else {
		if summary = stderrTail(out, 3); summary == "" {
			summary = fmt.Sprintf("Build failed (exit code %d)", rc)
		}
		a.errorf(summary)
	}
	a.info("Full log: " + logPath)

	ai, why := fixAgent(exec.LookPath, a.Env.Getenv)
	root, rootErr := checkoutRoot()
	switch {
	case why != "" && rootErr == nil:
		why += ". Common errors: " + filepath.Join(root, ".agents", "skills", "piste-fix", "SKILL.md")
	case ai.Name != "" && rootErr != nil:
		why = "piste fix starts " + ai.Label + " at a piste checkout, and found none: run it inside one, or clone one to ~/piste"
	}
	if why != "" {
		a.warn(why)
	}
	// Typing piste fix at a terminal is the request: after a passing build
	// too, the session starts and asks what the person saw.
	if ai.Name != "" && rootErr == nil && interactive(a.Stdout) {
		prompt := fixPrompt(built, name, summary, logPath, argv, a.fixRecheck(p, root, host))
		return a.startFix(ai, root, prompt, filepath.Dir(logPath), built)
	}
	if !built {
		return ExitError{1}
	}
	return nil
}

// startFix replaces piste with the AI's session at the checkout root.
func (a *App) startFix(ai fixAI, root, prompt, logDir string, built bool) error {
	overlays, _ := workspace.Overlays(root)
	argv, env := fixExec(ai, root, overlays, logDir, prompt)
	if built {
		a.info(fmt.Sprintf("Starting %s at %s to ask what you saw...", ai.Label, root))
	} else {
		a.info(fmt.Sprintf("Starting %s at %s with this error...", ai.Label, root))
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintf(a.Stderr, "piste: fix: %v\n", err)
		return ExitError{1}
	}
	if err := execFn(ai.Path, argv, env); err != nil {
		fmt.Fprintf(a.Stderr, "piste: fix: exec %s: %v\n", ai.Path, err)
		return ExitError{1}
	}
	return nil
}

// fixPrompt is the session's first message: the error in one line, where
// the full output is, the build piste ran, the skill to follow, and the
// command that checks a fix (recheck: the same config, from the checkout
// root). No log text, so it stays far below the limit on one argument.
func fixPrompt(built bool, name, summary, logPath string, argv []string, recheck string) string {
	head := fmt.Sprintf("piste fix rebuilt %s as piste switch builds it, without activating", name)
	ran := fmt.Sprintf("The build ran:\n\n%s\n\n", shellJoin(argv))
	check := "check it with:\n\n" + recheck + "\n"
	if built {
		return head + ", and it builds now: the failure was in activation, or it's gone. " +
			"Its output is in " + logPath + ". " + ran +
			"First ask the person for the error they saw, then follow .agents/skills/piste-fix/SKILL.md. " +
			"Once you've fixed it, " + check
	}
	return head + ", and the build failed:\n\n" + summary + "\n\n" +
		"The full output is in " + logPath + ". " + ran +
		"Follow .agents/skills/piste-fix/SKILL.md to find the cause, fix it with the person, and " + check
}

// fixExec is the session's argv and environment. Claude: the prompt first
// (--add-dir takes several values and would swallow it), each overlay and
// the log's directory added; the deny rules are the checkout's
// .claude/settings.json, which a session at its root reads. Codex:
// the prompt alone (the overlays are under the checkout, and its sandbox
// reads anywhere). Both run with PISTE_AI=none: no hint after a failure
// inside the session, and no nested AI from its own piste fix.
func fixExec(ai fixAI, root string, overlays []string, logDir, prompt string) (argv, env []string) {
	kv := []string{"PWD=" + root, "PISTE_AI=none"}
	if ai.Name == "claude" {
		dirs, dirsEnv := claudeAddDirs(root, overlays)
		argv = append(append([]string{"claude", prompt}, dirs...), "--add-dir", logDir)
		kv = append(kv, dirsEnv...)
	} else {
		argv = []string{"codex", prompt}
	}
	return argv, withEnv(os.Environ(), kv...)
}

// withEnv is base with each KEY=value of kv set: earlier entries of the
// same keys dropped (a C getenv reads the first), kv appended.
func withEnv(base []string, kv ...string) []string {
	keys := map[string]bool{}
	for _, e := range kv {
		k, _, _ := strings.Cut(e, "=")
		keys[k] = true
	}
	var out []string
	for _, e := range base {
		if k, _, _ := strings.Cut(e, "="); !keys[k] {
			out = append(out, e)
		}
	}
	return append(out, kv...)
}

// passTee runs argv with its stdout and stderr through one writer, to the
// terminal (stderr) and log at once, and returns its exit code and output.
// One writer that isn't a terminal: nix prints plain lines, no progress bar.
func (a *App) passTee(argv []string, log io.Writer) (int, []byte, error) {
	if argv[0] == "nix" {
		if err := a.checkNix(); err != nil {
			return 1, nil, err
		}
	}
	var buf bytes.Buffer
	w := io.MultiWriter(a.Stderr, log, &buf)
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, w, w
	err := c.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, buf.Bytes(), nil
	case errors.As(err, &ee):
		return ee.ExitCode(), buf.Bytes(), nil
	}
	fmt.Fprintf(a.Stderr, "piste: %s: %v\n", argv[0], err)
	return 1, nil, ExitError{1}
}
