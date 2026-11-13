package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func symlink(t *testing.T, target, link string) {
	t.Helper()
	_ = os.Remove(link)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// A profile is <name> -> <name>-<n>-link -> <store path>, as nix-env keeps
// it; anything else has no generation, so the switch isn't checked.
func TestActiveGeneration(t *testing.T) {
	dir := t.TempDir()
	gen41 := filepath.Join(dir, "gens", "41")
	if err := os.MkdirAll(gen41, 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(gen41)
	if err != nil {
		t.Fatal(err)
	}
	sys := filepath.Join(dir, "system")
	symlink(t, gen41, filepath.Join(dir, "system-41-link"))

	if _, ok := activeGeneration(sys); ok {
		t.Error("no profile link: want no generation")
	}

	symlink(t, "system-41-link", sys)
	g, ok := activeGeneration(sys)
	if !ok || g.num != 41 || g.path != resolved {
		t.Errorf("system -> system-41-link: got %+v, %v; want {41 %s}", g, ok, resolved)
	}

	symlink(t, gen41, sys)
	if g, ok := activeGeneration(sys); ok {
		t.Errorf("a profile straight at a store path: got %+v, want no generation", g)
	}

	symlink(t, "system-42-link", sys)
	symlink(t, filepath.Join(dir, "gens", "42"), filepath.Join(dir, "system-42-link"))
	if g, ok := activeGeneration(sys); ok {
		t.Errorf("a dangling generation link: got %+v, want no generation", g)
	}

	hm := filepath.Join(dir, "home-manager")
	symlink(t, gen41, filepath.Join(dir, "home-manager-7-link"))
	symlink(t, "home-manager-7-link", hm)
	if g, ok := activeGeneration(hm); !ok || g.num != 7 {
		t.Errorf("home-manager -> home-manager-7-link: got %+v, %v; want 7", g, ok)
	}
}

// home-manager keeps its profile in the user's nix state when that profiles
// directory exists, else in the per-user profiles (home-manager's
// setHomeManagerPathVariables).
func TestHMProfile(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	users := filepath.Join(dir, "per-user")
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }

	if got, want := hmProfile(getenv, home, "tester", users), filepath.Join(users, "tester", "home-manager"); got != want {
		t.Errorf("no user profiles dir: got %s, want %s", got, want)
	}

	if err := os.MkdirAll(filepath.Join(home, ".local", "state", "nix", "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := hmProfile(getenv, home, "tester", users), filepath.Join(home, ".local", "state", "nix", "profiles", "home-manager"); got != want {
		t.Errorf("~/.local/state/nix/profiles: got %s, want %s", got, want)
	}

	state := filepath.Join(dir, "state")
	env["XDG_STATE_HOME"] = state
	if got, want := hmProfile(getenv, home, "tester", users), filepath.Join(users, "tester", "home-manager"); got != want {
		t.Errorf("$XDG_STATE_HOME without nix/profiles: got %s, want %s", got, want)
	}
	if err := os.MkdirAll(filepath.Join(state, "nix", "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := hmProfile(getenv, home, "tester", users), filepath.Join(state, "nix", "profiles", "home-manager"); got != want {
		t.Errorf("$XDG_STATE_HOME/nix/profiles: got %s, want %s", got, want)
	}
}

// fakeShell writes a #!/bin/sh script; @SLEEP@ in body is sleep's absolute
// path (the check's PATH has no sleep in the Nix sandbox).
func fakeShell(t *testing.T, body string) string {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep on PATH")
	}
	p := filepath.Join(t.TempDir(), "fake-shell")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+strings.ReplaceAll(body, "@SLEEP@", sleep)), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// alive: the process exists and isn't a zombie (an orphan whose reaper
// hasn't got to it yet is dead).
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	if runtime.GOOS == "linux" {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return false
		}
		if i := strings.LastIndexByte(string(b), ')'); i > 0 && i+2 < len(b) && b[i+2] == 'Z' {
			return false
		}
	}
	return true
}

func gone(t *testing.T, pidFile string) {
	t.Helper()
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(2 * time.Second); alive(pid); {
		if time.Now().After(end) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the shell's child %d outlived the check", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func shortTimeout(t *testing.T, d time.Duration) {
	old := switchCheckTimeout
	switchCheckTimeout = d
	t.Cleanup(func() { switchCheckTimeout = old })
}

func TestCheckEnv(t *testing.T) {
	got := checkEnv("/home/user", "user", "/bin/zsh")
	want := []string{
		"HOME=/home/user", "USER=user", "LOGNAME=user", "SHELL=/bin/zsh",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb", "ZELLIJ_SKIP=1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// The shell sees exactly checkEnv's variables (nothing leaks from piste's,
// such as __HM_SESS_VARS_SOURCED or the old PATH), and is asked for a login,
// interactive shell running piste --version.
func TestCheckShellEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("__HM_SESS_VARS_SOURCED", "1")
	sh := fakeShell(t, "export -p > "+dir+"/env\nprintf '%s\\n' \"$@\" > "+dir+"/args\n")
	env := checkEnv("/home/user", "user", sh)
	c := checkShell(sh, env)
	if !c.ok() {
		t.Fatalf("check failed: %s", c.why())
	}
	b, err := os.ReadFile(filepath.Join(dir, "env"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		l = strings.TrimPrefix(l, "export ")
		name, _, _ := strings.Cut(l, "=")
		switch name {
		case "PWD", "OLDPWD", "SHLVL", "_": // sh's own
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	want := []string{"HOME", "LOGNAME", "PATH", "SHELL", "TERM", "USER", "ZELLIJ_SKIP"}
	if !slices.Equal(names, want) {
		t.Errorf("the shell saw %q, want %q", names, want)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if got := strings.Split(strings.TrimSpace(string(args)), "\n"); !slices.Equal(got, []string{"-l", "-i", "-c", "piste --version"}) {
		t.Errorf("the shell's arguments: got %q", got)
	}
}

func TestCheckShellExit127(t *testing.T) {
	sh := fakeShell(t, "echo 'zsh: command not found: piste' >&2\nexit 127\n")
	c := checkShell(sh, checkEnv("/home/user", "user", sh))
	if c.ok() || c.code != 127 {
		t.Fatalf("got ok=%v code=%d, want a failure with 127", c.ok(), c.code)
	}
	if !regexp.MustCompile(`^the shell exited 127 after [0-9]+\.[0-9]s$`).MatchString(c.why()) {
		t.Errorf("why: %q", c.why())
	}
	if !slices.Contains(c.tail(3), "zsh: command not found: piste") {
		t.Errorf("tail: %q", c.tail(3))
	}
}

func TestCheckShellMissing(t *testing.T) {
	sh := filepath.Join(t.TempDir(), "no-such-shell")
	c := checkShell(sh, checkEnv("/home/user", "user", sh))
	if c.ok() {
		t.Fatal("a missing shell passed")
	}
	if !strings.HasPrefix(c.why(), "the shell couldn't start: ") {
		t.Errorf("why: %q", c.why())
	}
}

// A shell that doesn't finish is killed at the timeout, with what it
// started. The timeout leaves the shell time to start its child and write
// the child's pid first, even on a loaded machine: inside a darwin build
// sandbox, with every package's tests running, 300ms wasn't enough.
func TestCheckShellTimeout(t *testing.T) {
	shortTimeout(t, 2*time.Second)
	dir := t.TempDir()
	sh := fakeShell(t, "@SLEEP@ 30 &\necho $! > "+dir+"/child\n@SLEEP@ 30\n")
	start := time.Now()
	c := checkShell(sh, checkEnv("/home/user", "user", sh))
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("the check took %s", d)
	}
	if c.ok() || !c.timedOut {
		t.Fatalf("got ok=%v timedOut=%v", c.ok(), c.timedOut)
	}
	if c.why() != "the shell didn't finish in 2s" {
		t.Errorf("why: %q", c.why())
	}
	gone(t, filepath.Join(dir, "child"))
}

// A child left running in the background, holding the output, doesn't
// hold the check (WaitDelay), and is killed with the shell's group.
func TestCheckShellBackgroundChild(t *testing.T) {
	dir := t.TempDir()
	sh := fakeShell(t, "@SLEEP@ 30 &\necho $! > "+dir+"/child\nexit 0\n")
	start := time.Now()
	c := checkShell(sh, checkEnv("/home/user", "user", sh))
	if d := time.Since(start); d > 10*time.Second { // well short of the 30s sleep; slack for a loaded machine
		t.Fatalf("the check took %s", d)
	}
	if !c.ok() {
		t.Fatalf("check failed: %s (%v)", c.why(), c.err)
	}
	gone(t, filepath.Join(dir, "child"))
}

func TestLoginShellParse(t *testing.T) {
	for _, c := range []struct {
		got, want string
	}{
		{dsclShell([]byte("UserShell: /bin/zsh\n")), "/bin/zsh"},
		{dsclShell([]byte("UserShell:\n /opt/my shell/zsh\n")), "/opt/my shell/zsh"},
		{dsclShell(nil), ""},
		{getentShell([]byte("user:x:1000:1000::/home/user:/usr/bin/fish\n")), "/usr/bin/fish"},
		{getentShell([]byte("user:x:1000\n")), ""},
		{getentShell(nil), ""},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}
