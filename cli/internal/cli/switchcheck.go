package cli

// A switch that undoes itself. This file holds what a switch reads:
// the active generation of the profile it moves, and the check that a new
// login shell starts and runs piste. switchundo.go has when a switch is
// checked and how it is undone; switchdetach.go, when it runs as its own
// unit.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/platform"
)

// generation is a profile's active generation: the number of its
// <name>-<n>-link and the store path it resolves to.
type generation struct {
	num  int
	path string
}

var generationLink = regexp.MustCompile(`^(.+)-(\d+)-link$`)

// activeGeneration reads profile's link. ok is false when there is no
// generation to go back to: no link, a link that isn't
// <base(profile)>-<n>-link, or one that doesn't resolve.
func activeGeneration(profile string) (generation, bool) {
	target, err := os.Readlink(profile)
	if err != nil {
		return generation{}, false
	}
	m := generationLink.FindStringSubmatch(filepath.Base(target))
	if m == nil || m[1] != filepath.Base(profile) {
		return generation{}, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return generation{}, false
	}
	path, err := filepath.EvalSymlinks(profile)
	if err != nil {
		return generation{}, false
	}
	return generation{num: n, path: path}, true
}

// hmProfile is home-manager's profile, found as home-manager finds it:
// ${XDG_STATE_HOME:-~/.local/state}/nix/profiles when that directory
// exists, else the per-user profiles.
func hmProfile(getenv func(string) string, home, user, userProfiles string) string {
	state := getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	if fi, err := os.Stat(filepath.Join(state, "nix", "profiles")); err == nil && fi.IsDir() {
		return filepath.Join(state, "nix", "profiles", "home-manager")
	}
	return filepath.Join(userProfiles, user, "home-manager")
}

// switchProfile is the profile a switch of kind on this machine moves: the
// system profile for a nix-darwin or NixOS system, home-manager's for a home.
func (a *App) switchProfile(p platform.Platform, kind Kind) string {
	if kind != KindHome {
		return platform.SystemProfile
	}
	return hmProfile(a.Env.Getenv, p.HomeDir, p.User, platform.UserProfiles)
}

// --- the check ---------------------------------------------------------------

// switchCheckTimeout is how long the new login shell has to run piste:
// generous for a slow first start. A variable for the unit tests.
var switchCheckTimeout = 30 * time.Second

// checkEnv is exactly the environment the check's shell gets. Anything
// inherited would hide the generation under test: __HM_SESS_VARS_SOURCED,
// __NIX_DARWIN_SET_ENVIRONMENT_DONE or __ETC_ZSHENV_SOURCED make the shell
// skip the files that set it up, and the old PATH finds the old piste. PATH
// is sshd's default; ZELLIJ_SKIP stops the engine's opt-in zellij autostart
// from exec'ing zellij with no terminal.
func checkEnv(home, user, shell string) []string {
	return []string{
		"HOME=" + home, "USER=" + user, "LOGNAME=" + user, "SHELL=" + shell,
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TERM=dumb", "ZELLIJ_SKIP=1",
	}
}

// shellCheck is one run of the check.
type shellCheck struct {
	code     int   // the exit code; -1 when it didn't start, didn't finish or died by a signal
	err      error // it couldn't start
	timedOut bool
	state    string // how it ended, when by a signal ("signal: killed")
	elapsed  time.Duration
	output   []byte // stdout and stderr, as they came
}

func (c shellCheck) ok() bool { return c.err == nil && !c.timedOut && c.code == 0 }

// why is the failure, for "…: <why>."
func (c shellCheck) why() string {
	switch {
	case c.err != nil:
		return "the shell couldn't start: " + c.err.Error()
	case c.timedOut:
		return fmt.Sprintf("the shell didn't finish in %gs", switchCheckTimeout.Seconds())
	case c.code >= 0:
		return fmt.Sprintf("the shell exited %d after %.1fs", c.code, c.elapsed.Seconds())
	}
	return fmt.Sprintf("the shell died (%s) after %.1fs", c.state, c.elapsed.Seconds())
}

// tail is the output's last n non-empty lines.
func (c shellCheck) tail(n int) []string {
	var lines []string
	for _, l := range strings.Split(string(c.output), "\n") {
		if l = strings.TrimRight(l, " \t\r"); strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// checkShell runs `<shell> -l -i -c 'piste --version'`: a login,
// interactive shell reads every startup file a new terminal or ssh login
// reads. It runs in its own session (no terminal to take or draw on), with
// stdin /dev/null and its output captured. Its process group is killed
// when it exits or times out, and WaitDelay keeps a leftover child holding
// the output from holding the check.
func checkShell(shell string, env []string) shellCheck {
	ctx, cancel := context.WithTimeout(context.Background(), switchCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-i", "-c", "piste --version")
	cmd.Env = env
	for _, kv := range env { // in $HOME, as a login starts
		if h, ok := strings.CutPrefix(kv, "HOME="); ok {
			if fi, err := os.Stat(h); err == nil && fi.IsDir() {
				cmd.Dir = h
			}
		}
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	c := shellCheck{code: -1}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		c.err = err
		return c
	}
	err := cmd.Wait()
	c.elapsed = time.Since(start)
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // what it left running
	c.output = out.Bytes()
	if ctx.Err() == context.DeadlineExceeded {
		c.timedOut = true
		return c
	}
	var ee *exec.ExitError
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) && !errors.As(err, &ee) {
		c.err = err
		return c
	}
	if st := cmd.ProcessState; st != nil {
		c.code = st.ExitCode()
		if c.code < 0 {
			c.state = st.String()
		}
	}
	return c
}

// dsclShell is the shell in `dscl . -read /Users/<user> UserShell`'s
// output ("UserShell: /bin/zsh", or the value on the next line when it has
// a space).
func dsclShell(out []byte) string {
	lines := strings.Split(string(out), "\n")
	for i, l := range lines {
		if v, ok := strings.CutPrefix(l, "UserShell:"); ok {
			if v = strings.TrimSpace(v); v == "" && i+1 < len(lines) {
				v = strings.TrimSpace(lines[i+1])
			}
			return v
		}
	}
	return ""
}

// getentShell is field 7 of `getent passwd <user>`.
func getentShell(out []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	f := strings.Split(line, ":")
	if len(f) < 7 {
		return ""
	}
	return strings.TrimSpace(f[6])
}

// loginShell is the shell a new terminal or ssh login starts: the user
// database's, so a switch that changes it is checked with the new one;
// else $SHELL; else /bin/sh.
func (a *App) loginShell(p platform.Platform) string {
	argv, parse := []string{"getent", "passwd", p.User}, getentShell
	if p.IsDarwin {
		argv, parse = []string{"dscl", ".", "-read", "/Users/" + p.User, "UserShell"}, dsclShell
	}
	if res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: argv, Timeout: 10 * time.Second}); err == nil {
		if sh := parse(res.Stdout); sh != "" {
			return sh
		}
	}
	if sh := a.Env.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}
