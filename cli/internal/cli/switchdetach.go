package cli

// When the activation would restart the switch's caller (Linux).
// home-manager restarts each user unit whose file changed (sd-switch); a
// switch run inside one (an agent's runner service) would be killed with
// it, part-way through the activation, with no check and no undo. So
// there, piste runs the switch as its own transient unit and follows its
// log.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/platform"
)

var (
	userManagerUnit = regexp.MustCompile(`^user@\d+\.service$`)
	// systemdEnvName: the variable names systemd-run's -E accepts.
	systemdEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// userServiceUnit is the user service a process runs in, from its cgroup
// file (cgroup v2's "0::" line): the innermost component under
// user@<uid>.service/ that is a .service. "" for none: a scope (a terminal,
// an ssh session), a system service, a detached switch's own
// piste-switch-* unit, or cgroup v1.
func userServiceUnit(cgroup string) string {
	for _, line := range strings.Split(cgroup, "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		under, unit := false, ""
		for _, c := range strings.Split(path, "/") {
			if under && strings.HasSuffix(c, ".service") {
				unit = c
			}
			if userManagerUnit.MatchString(c) {
				under = true
			}
		}
		if strings.HasPrefix(unit, "piste-switch-") {
			return ""
		}
		return unit
	}
	return ""
}

// switchCaller is the user service a switch on Linux runs inside ("" for
// none, and always on a Mac: no known launchd job runs piste).
func switchCaller() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	b, err := os.ReadFile(platform.CgroupFile)
	if err != nil {
		return ""
	}
	return userServiceUnit(string(b))
}

// switchLog is where a detached switch writes, overwritten each time.
func switchLog(getenv func(string) string, home string) string {
	state := getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "piste", "switch.log")
}

// detachArgv runs `piste switch` as the transient user unit name, its
// output (stderr too) in log. The caller's environment goes by name:
// systemd-run takes the values from its own environment, so none lands in
// its argv. The switch's flags come from runSwitch's arguments, not
// os.Args, so setup's switch detaches as a switch.
func detachArgv(name, log, exe string, environ []string, verbose bool, overlay, host string, noUndo, force bool) []string {
	argv := []string{"systemd-run", "--user", "--wait", "--collect", "--quiet", "--same-dir",
		"--unit", name, "-p", "StandardOutput=truncate:" + log}
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); systemdEnvName.MatchString(k) {
			argv = append(argv, "-E", k)
		}
	}
	argv = append(argv, "--", exe)
	if verbose {
		argv = append(argv, "-v")
	}
	if overlay != "" {
		argv = append(argv, "--overlay", overlay)
	}
	argv = append(argv, "switch")
	if host != "" {
		argv = append(argv, "--host", host)
	}
	if noUndo {
		argv = append(argv, "--no-undo")
	}
	if force {
		argv = append(argv, "--force")
	}
	return argv
}

// detachSwitch runs the switch as its own unit and copies its log here as
// it grows, then exits with the unit's code (systemd-run --wait passes it
// on). If the activation restarts caller and takes this piste down, the
// switch, the check and any undo carry on, and the log has the result.
// systemd-run's own errors go to stderr, with no fallback to switching in
// place.
func (a *App) detachSwitch(caller, host string, noUndo, force bool) error {
	home := a.Env.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	log := switchLog(a.Env.Getenv, home)
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		return err
	}
	// Empty before the unit starts, so following from the top shows only
	// this switch.
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	name := fmt.Sprintf("piste-switch-%d", time.Now().Unix())
	shown := log
	if rest, ok := strings.CutPrefix(log, home+"/"); ok && home != "" {
		shown = "~/" + rest
	}
	a.info(fmt.Sprintf("This switch runs inside %s, which the switch may restart.", caller))
	a.info(fmt.Sprintf("Running it as %s.service. Log: %s", name, shown))
	// Ctrl-C stops this piste and systemd-run, not the unit.
	a.info(fmt.Sprintf("Ctrl-C leaves it running; to stop it: systemctl --user stop %s.service", name))

	argv := detachArgv(name, log, exe, os.Environ(), a.Verbose, a.Overlay, host, noUndo, force)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdout, cmd.Stderr = a.Stderr, a.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(a.Stderr, "piste: systemd-run: %v\n", err)
		a.cantDetach(caller)
		return ExitError{1}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	r, err := os.Open(log)
	if err != nil {
		return err
	}
	defer r.Close()
	var copied int64
	follow := func() {
		n, _ := io.Copy(a.Stdout, r)
		copied += n
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			follow()
			var ee *exec.ExitError
			switch {
			case err == nil:
				return nil
			case errors.As(err, &ee) && ee.ExitCode() > 0:
				// Nothing in the log: the unit never ran.
				if copied == 0 {
					a.cantDetach(caller)
				}
				return ExitError{ee.ExitCode()}
			}
			fmt.Fprintf(a.Stderr, "piste: systemd-run: %v\n", err)
			return ExitError{1}
		case <-tick.C:
			follow()
		}
	}
}

// systemdEnvByName is the first systemd whose systemd-run takes a bare
// -E NAME's value from its own environment; an older one passes "NAME" on
// and the manager refuses it ("Invalid environment block").
const systemdEnvByName = 250

// cantDetach follows systemd-run's own error when the switch's unit never
// ran: there's no switching in place, so it says how to switch outside the
// service, and names a systemd too old for the unit.
func (a *App) cantDetach(caller string) {
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"systemd-run", "--version"}, Timeout: 10 * time.Second})
	if v, ok := systemdVersion(res.Stdout); err == nil && ok && v < systemdEnvByName {
		a.errorf(fmt.Sprintf("piste needs systemd %d or newer to run the switch as its own unit; this is systemd %d.", systemdEnvByName, v))
	}
	a.warn(fmt.Sprintf("To switch anyway, run %s switch outside %s: from a terminal or ssh, or under systemd-run --user --scope.", a.piste(), caller))
}

// systemdVersion is the number in `systemd-run --version`'s first line
// ("systemd 255 (255.4-1ubuntu8.18)").
func systemdVersion(out []byte) (int, bool) {
	line, _, _ := strings.Cut(string(out), "\n")
	f := strings.Fields(line)
	if len(f) < 2 || f[0] != "systemd" {
		return 0, false
	}
	v, err := strconv.Atoi(f[1])
	return v, err == nil
}
