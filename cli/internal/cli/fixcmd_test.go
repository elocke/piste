package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/platform"
)

// onPath is a lookPath that finds exactly names, at /bin/<name>.
func onPath(names ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		for _, x := range names {
			if x == n {
				return "/bin/" + n, nil
			}
		}
		return "", errors.New("not found")
	}
}

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// binDir is a PATH directory holding scripts: name to body ("" for a
// script that does nothing).
func binDir(t *testing.T, scripts map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for n, body := range scripts {
		if body == "" {
			body = "#!/bin/sh\n"
		}
		if err := os.WriteFile(filepath.Join(d, n), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// fakeCheckout is a piste checkout with one overlay, o (its lock has a
// piste input), and a flake beside it that's in no checkout. HOME has
// neither ~/piste nor an agents manifest. Both paths are physical.
func fakeCheckout(t *testing.T) (root, solo string) {
	t.Helper()
	base := t.TempDir()
	root, solo = filepath.Join(base, "piste"), filepath.Join(base, "solo")
	flakeNix := "{ outputs = _: { }; }\n"
	for path, body := range map[string]string{
		filepath.Join(root, ".piste"):                      "",
		filepath.Join(root, "flake.nix"):                   flakeNix,
		filepath.Join(root, ".git", "HEAD"):                "ref: refs/heads/main\n",
		filepath.Join(root, "overlays", "o", "flake.nix"):  flakeNix,
		filepath.Join(root, "overlays", "o", "flake.lock"): `{"nodes": {"root": {"inputs": {"piste": "piste"}}, "piste": {"locked": {"lastModified": 4102444800}}}, "root": "root", "version": 7}`,
		filepath.Join(solo, "flake.nix"):                   flakeNix,
		filepath.Join(base, "home", ".keep"):               "",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", filepath.Join(base, "home"))
	return physical(root), physical(solo)
}

func TestFixAgent(t *testing.T) {
	for _, c := range []struct {
		name     string
		env      map[string]string
		path     []string
		want     string
		wantWhy  string
		wantPath string
	}{
		{"unset, both", nil, []string{"claude", "codex"}, "claude", "", "/bin/claude"},
		{"unset, claude", nil, []string{"claude"}, "claude", "", "/bin/claude"},
		{"unset, codex", nil, []string{"codex"}, "codex", "", "/bin/codex"},
		{"unset, neither", nil, nil, "", "piste fix hands this to Claude Code or Codex, and neither is on PATH", ""},
		{"claude missing", map[string]string{"PISTE_AI": "claude"}, []string{"codex"}, "", "PISTE_AI=claude, and claude isn't on PATH", ""},
		{"codex", map[string]string{"PISTE_AI": "codex"}, []string{"claude", "codex"}, "codex", "", "/bin/codex"},
		{"none", map[string]string{"PISTE_AI": "none"}, []string{"claude", "codex"}, "", "", ""},
		{"case and space", map[string]string{"PISTE_AI": " Claude "}, []string{"claude"}, "claude", "", "/bin/claude"},
		{"unknown", map[string]string{"PISTE_AI": "gemini"}, []string{"claude", "codex"}, "", "PISTE_AI=gemini: piste fix starts claude or codex, or none for no AI", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			ai, why := fixAgent(onPath(c.path...), envOf(c.env))
			if ai.Name != c.want || why != c.wantWhy || ai.Path != c.wantPath {
				t.Fatalf("fixAgent = %+v, %q; want %q at %q, %q", ai, why, c.want, c.wantPath, c.wantWhy)
			}
			label := map[string]string{"claude": "Claude Code", "codex": "Codex", "": ""}[c.want]
			if ai.Label != label {
				t.Fatalf("Label = %q, want %q", ai.Label, label)
			}
		})
	}
}

func TestFixLogPath(t *testing.T) {
	if got := fixLogPath(envOf(map[string]string{"XDG_STATE_HOME": "/x"}), "/home/user"); got != "/x/piste/fix.log" {
		t.Fatalf("with XDG_STATE_HOME: %q", got)
	}
	if got := fixLogPath(envOf(nil), "/home/user"); got != "/home/user/.local/state/piste/fix.log" {
		t.Fatalf("default: %q", got)
	}
}

// A log left 0644 (an older run, or by hand) is 0600 and empty once opened.
func TestOpenFixLogMode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "piste", "fix.log")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("old secret-ish output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openFixLog(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 || fi.Size() != 0 {
		t.Fatalf("mode %v size %d, want 0600 and empty", fi.Mode().Perm(), fi.Size())
	}
	// A fresh one, its directory made too.
	q := filepath.Join(t.TempDir(), "new", "piste", "fix.log")
	g, err := openFixLog(q)
	if err != nil {
		t.Fatal(err)
	}
	g.Close()
	if fi, _ := os.Stat(q); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("new log: %v", fi)
	}
}

func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"nix", "build", `/a b#homeConfigurations."p".activationPackage`, "--override-input", "piste", "git+file:///home/user/piste"})
	want := `nix build '/a b#homeConfigurations."p".activationPackage' --override-input piste git+file:///home/user/piste`
	if got != want {
		t.Fatalf("shellJoin =\n  %s\nwant\n  %s", got, want)
	}
	if got := shellJoin([]string{"echo", "it's"}); got != `echo 'it'\''s'` {
		t.Fatalf("quote in arg: %s", got)
	}
}

// The deny rules are the checkout's .claude/settings.json, not this argv: a
// plain `claude` or `piste claude` at the root gets them too.
func TestFixExecClaude(t *testing.T) {
	ai := fixAI{"claude", "Claude Code", "/bin/claude"}
	argv, _ := fixExec(ai, "/home/user/piste", []string{"a", "b"}, "/home/user/.local/state/piste", "the prompt")
	want := []string{"claude", "the prompt",
		"--add-dir", "/home/user/piste/overlays/a", "--add-dir", "/home/user/piste/overlays/b",
		"--add-dir", "/home/user/.local/state/piste"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv =\n  %q\nwant\n  %q", argv, want)
	}
}

func TestFixExecCodex(t *testing.T) {
	argv, _ := fixExec(fixAI{"codex", "Codex", "/bin/codex"}, "/home/user/piste", []string{"a"}, "/s", "the prompt")
	if !slices.Equal(argv, []string{"codex", "the prompt"}) {
		t.Fatalf("argv = %q", argv)
	}
}

// The session runs with exactly PISTE_AI=none, whatever the shell had: no
// hint inside it, and no nested AI from its own piste fix.
func TestFixExecEnv(t *testing.T) {
	t.Setenv("PISTE_AI", "claude")
	t.Setenv("PWD", "/elsewhere")
	t.Setenv("CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD", "")
	os.Unsetenv("CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD")
	for _, ai := range []fixAI{{"claude", "Claude Code", "/bin/claude"}, {"codex", "Codex", "/bin/codex"}} {
		_, env := fixExec(ai, "/home/user/piste", nil, "/s", "p")
		count := func(prefix string) (n int, last string) {
			for _, kv := range env {
				if strings.HasPrefix(kv, prefix) {
					n++
					last = kv
				}
			}
			return
		}
		if n, kv := count("PISTE_AI="); n != 1 || kv != "PISTE_AI=none" {
			t.Fatalf("%s: %d PISTE_AI entries, last %q", ai.Name, n, kv)
		}
		if n, kv := count("PWD="); n != 1 || kv != "PWD=/home/user/piste" {
			t.Fatalf("%s: %d PWD entries, last %q", ai.Name, n, kv)
		}
		n, _ := count("CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1")
		if (ai.Name == "claude") != (n == 1) {
			t.Fatalf("%s: %d CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD entries", ai.Name, n)
		}
	}
}

func TestFixPrompt(t *testing.T) {
	argv := []string{"nix", "build", "/home/user/piste/overlays/mine#homeConfigurations.\"jane@devbox\".activationPackage", "--show-trace"}
	var out strings.Builder
	for i := range 200 {
		fmt.Fprintf(&out, "building line %d of a long log\n", i)
	}
	out.WriteString("error: attribute 'tmuxx' missing\n       at /nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-source/hosts/devbox.nix:20:5:\n")
	tail := stderrTail([]byte(out.String()), 3)
	recheck := "piste fix --overlay work --host other-mac"
	failed := fixPrompt(false, "jane@devbox", tail, "/home/user/.local/state/piste/fix.log", argv, recheck, nil)
	for _, s := range []string{tail, "/home/user/.local/state/piste/fix.log", shellJoin(argv), "Follow .agents/skills/piste-fix/SKILL.md", "check it with:\n\n" + recheck + "\n"} {
		if !strings.Contains(failed, s) {
			t.Fatalf("failed prompt lacks %q:\n%s", s, failed)
		}
	}
	built := fixPrompt(true, "jane@devbox", "", "/home/user/.local/state/piste/fix.log", argv, recheck, nil)
	for _, s := range []string{"it builds now", "ask the person for the error they saw", "/home/user/.local/state/piste/fix.log", ".agents/skills/piste-fix/SKILL.md", "check it with:\n\n" + recheck + "\n"} {
		if !strings.Contains(built, s) {
			t.Fatalf("passing prompt lacks %q:\n%s", s, built)
		}
	}
	if strings.Contains(built, "tmuxx") {
		t.Fatalf("passing prompt has the error:\n%s", built)
	}
	// It builds, and the last switch's log has the failure: no asking.
	sw := &switchFailure{Path: "/home/user/.local/state/piste/switch.log", When: "2026-01-01T00:00:00Z", Lines: []string{"Error: brew bundle failed"}}
	fromLog := fixPrompt(true, "jane@devbox", "", "/home/user/.local/state/piste/fix.log", argv, recheck, sw)
	for _, s := range []string{"Error: brew bundle failed", sw.Path, sw.When, ".agents/skills/piste-fix/SKILL.md", "check it with:\n\n" + recheck + "\n"} {
		if !strings.Contains(fromLog, s) {
			t.Fatalf("switch-log prompt lacks %q:\n%s", s, fromLog)
		}
	}
	if strings.Contains(fromLog, "ask the person") {
		t.Fatalf("switch-log prompt asks the person:\n%s", fromLog)
	}
	for _, p := range []string{failed, built, fromLog} {
		if len(p) > 4096 || strings.Contains(p, "building line 100") {
			t.Fatalf("prompt carries log text (%d bytes):\n%s", len(p), p)
		}
	}
}

func TestClaudeAddDirs(t *testing.T) {
	args, env := claudeAddDirs("/home/user/piste", []string{"a", "b"})
	if !slices.Equal(args, []string{"--add-dir", "/home/user/piste/overlays/a", "--add-dir", "/home/user/piste/overlays/b"}) {
		t.Fatalf("args = %q", args)
	}
	if !slices.Equal(env, []string{"CLAUDE_CODE_ADDITIONAL_DIRECTORIES_CLAUDE_MD=1"}) {
		t.Fatalf("env = %q", env)
	}
}

func TestFixHint(t *testing.T) {
	root, solo := fakeCheckout(t)
	hint := func(dir string, scripts []string, env map[string]string, overlay, host string) string {
		m := map[string]string{}
		for _, n := range scripts {
			m[n] = ""
		}
		t.Setenv("PATH", binDir(t, m))
		t.Chdir(dir)
		var out strings.Builder
		a := &App{Stdout: &out, Stderr: &out, Env: Env{Getenv: envOf(env)}, Overlay: overlay}
		a.fixHint(host)
		return out.String()
	}
	o := filepath.Join(root, "overlays", "o")
	both := []string{"claude", "piste"}
	if got := hint(o, both, nil, "o", "hh"); got != "To have Claude Code fix it: piste fix --overlay o --host hh\n" {
		t.Fatalf("both: %q", got)
	}
	if got := hint(o, both, nil, "", ""); got != "To have Claude Code fix it: piste fix\n" {
		t.Fatalf("none: %q", got)
	}
	if got := hint(o, []string{"codex"}, nil, "", ""); got != "To have Codex fix it: nix run .#piste -- fix\n" {
		t.Fatalf("codex, no piste on PATH: %q", got)
	}
	if got := hint(o, both, map[string]string{"PISTE_AI": "none"}, "o", "hh"); got != "" {
		t.Fatalf("PISTE_AI=none: %q", got)
	}
	if got := hint(o, []string{"piste"}, nil, "", ""); got != "" {
		t.Fatalf("no AI: %q", got)
	}
	// No checkout: piste fix would start no AI, so no line promises one.
	if got := hint(solo, both, nil, "", ""); got != "" {
		t.Fatalf("no checkout: %q", got)
	}

	// piste setup through nix run from elsewhere: it moved into the checkout,
	// and the person's shell didn't, so the line starts with the cd there
	// (~ for HOME). Once piste is on PATH, no cd.
	moved := func(scripts []string) string {
		m := map[string]string{}
		for _, n := range scripts {
			m[n] = ""
		}
		t.Setenv("PATH", binDir(t, m))
		t.Chdir(root)
		var out strings.Builder
		a := &App{Stdout: &out, Stderr: &out, Env: Env{Getenv: envOf(map[string]string{"HOME": filepath.Dir(root)})}, Overlay: "o", movedTo: root}
		a.fixHint("hh")
		return out.String()
	}
	if got := moved([]string{"codex"}); got != "To have Codex fix it: cd ~/piste && nix run .#piste -- fix --overlay o --host hh\n" {
		t.Fatalf("setup moved, no piste on PATH: %q", got)
	}
	if got := moved(both); got != "To have Claude Code fix it: piste fix --overlay o --host hh\n" {
		t.Fatalf("setup moved, piste on PATH: %q", got)
	}
}

// The command the session checks a fix with rebuilds what this run built,
// from the checkout root it starts at: the overlay named even when the cwd
// picked it, and a flake that isn't the checkout's from its own directory.
func TestFixRecheck(t *testing.T) {
	t.Setenv("PATH", binDir(t, map[string]string{"piste": ""}))
	const root = "/home/user/piste"
	work := platform.Platform{FlakeDir: root + "/overlays/work", Checkout: root, Overlay: "work"}
	for _, c := range []struct {
		name, overlay string
		p             platform.Platform
		host          string
		want          string
	}{
		{"the overlay the cwd picked", "", work, "other-mac", "piste fix --overlay work --host other-mac"},
		{"--overlay, and a host", "work", work, "devbox", "piste fix --overlay work --host devbox"},
		{"the engine, no overlays", "", platform.Platform{FlakeDir: root, Checkout: root}, "", "piste fix"},
		{"a flake in no checkout", "", platform.Platform{FlakeDir: "/home/user/my flake"}, "devbox", "cd '/home/user/my flake' && piste fix --host devbox"},
		{"a flake in the checkout that isn't an overlay", "", platform.Platform{FlakeDir: root + "/templates/default", Checkout: root}, "", "cd /home/user/piste/templates/default && piste fix"},
	} {
		a := &App{Overlay: c.overlay}
		if got := a.fixRecheck(c.p, root, c.host); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// runFix's hand-off, with the terminal and the exec stood in: at a
// terminal with an AI and a checkout it becomes the AI (by the path it
// found) at the checkout root, with the prompt and PISTE_AI=none; without a
// checkout or a terminal it only reports, and says why there's no session.
func TestRunFixLaunch(t *testing.T) {
	root, solo := fakeCheckout(t)
	fail := "#!/bin/sh\necho \"error: attribute 'tmuxx' missing\" >&2\nexit ${FAKE_EXIT:-1}\n"
	bin := binDir(t, map[string]string{"nix": fail, "darwin-rebuild": fail, "claude": ""})
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, k := range []string{"PISTE_AI", "PISTE_ENGINE", "PISTE_FLAKE", "FAKE_EXIT"} {
		t.Setenv(k, "")
	}
	t.Setenv("USER", "jane")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	term := true
	type call struct {
		path, cwd string
		argv, env []string
	}
	var calls []call
	oldInteractive, oldExec := interactive, execFn
	t.Cleanup(func() { interactive, execFn = oldInteractive, oldExec })
	interactive = func(io.Writer) bool { return term }
	execFn = func(path string, argv, env []string) error {
		cwd, _ := os.Getwd()
		calls = append(calls, call{path, physical(cwd), argv, env})
		return nil
	}
	run := func(dir string) (string, error) {
		calls = nil
		t.Chdir(dir)
		var out, errOut strings.Builder
		a := &App{Stdout: &out, Stderr: &errOut, Env: Env{Getenv: os.Getenv}}
		err := a.runFix("testhost")
		return out.String(), err
	}
	o := filepath.Join(root, "overlays", "o")

	out, err := run(o)
	if err != nil || len(calls) != 1 {
		t.Fatalf("terminal, AI, checkout: err %v, %d execs; stdout:\n%s", err, len(calls), out)
	}
	c := calls[0]
	if c.path != filepath.Join(bin, "claude") || c.argv[0] != "claude" || c.cwd != root {
		t.Fatalf("exec %q %q at %q, want %s at %s", c.path, c.argv[0], c.cwd, filepath.Join(bin, "claude"), root)
	}
	for _, s := range []string{"error: attribute 'tmuxx' missing", "fix --overlay o --host testhost\n"} {
		if !strings.Contains(c.argv[1], s) {
			t.Fatalf("prompt lacks %q:\n%s", s, c.argv[1])
		}
	}
	if !slices.Contains(c.env, "PISTE_AI=none") || !slices.Contains(c.env, "PWD="+root) {
		t.Fatalf("env lacks PISTE_AI=none or PWD=%s", root)
	}
	if !strings.Contains(out, "Starting Claude Code at "+root+" with this error...") {
		t.Fatalf("stdout:\n%s", out)
	}

	t.Setenv("FAKE_EXIT", "0")
	if out, err := run(o); err != nil || len(calls) != 1 || !strings.Contains(calls[0].argv[1], "it builds now") {
		t.Fatalf("a passing build at a terminal: err %v, %d execs; stdout:\n%s", err, len(calls), out)
	}
	t.Setenv("FAKE_EXIT", "")

	if out, err := run(solo); !errors.Is(err, ExitError{1}) || len(calls) != 0 || strings.Contains(out, "Starting") ||
		!strings.Contains(out, "piste fix starts Claude Code at a piste checkout, and found none") {
		t.Fatalf("no checkout: err %v, %d execs; stdout:\n%s", err, len(calls), out)
	}

	t.Setenv("PISTE_AI", "none")
	if out, err := run(o); !errors.Is(err, ExitError{1}) || len(calls) != 0 {
		t.Fatalf("PISTE_AI=none: err %v, %d execs; stdout:\n%s", err, len(calls), out)
	}
	t.Setenv("PISTE_AI", "")

	term = false
	if out, err := run(o); !errors.Is(err, ExitError{1}) || len(calls) != 0 || strings.Contains(out, "found none") {
		t.Fatalf("no terminal: err %v, %d execs; stdout:\n%s", err, len(calls), out)
	}
}
