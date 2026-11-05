package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A switch detaches only inside a user service of the user's manager, which
// the activation may restart; not in a scope (a terminal, an ssh session),
// a system service, its own transient unit, or under cgroup v1.
func TestUserServiceUnit(t *testing.T) {
	for _, c := range []struct{ cgroup, want string }{
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/runner.service\n", "runner.service"},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/piste-switch-1.service\n", ""},
		{"0::/user.slice/user-1000.slice/session-3.scope\n", ""},
		{"0::/system.slice/x.service\n", ""},
		{"12:pids:/user.slice\n1:name=systemd:/user.slice/user-1000.slice/user@1000.service/app.slice/runner.service\n", ""},
		{"0::/user.slice/user-1000.slice/user@1000.service/init.scope\n", ""},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/app-term-42.scope\n", ""},
		{"", ""},
	} {
		if got := userServiceUnit(c.cgroup); got != c.want {
			t.Errorf("%q: got %q, want %q", c.cgroup, got, c.want)
		}
	}
}

func TestSwitchLog(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if got, want := switchLog(getenv, "/home/user"), "/home/user/.local/state/piste/switch.log"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	env["XDG_STATE_HOME"] = "/state"
	if got, want := switchLog(getenv, "/home/user"), filepath.Join("/state", "piste", "switch.log"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// The detached switch is the same switch: its flags from runSwitch's
// arguments, the caller's environment passed by name (systemd-run reads
// the values from its own environment, so none lands in its argv).
func TestDetachArgv(t *testing.T) {
	environ := []string{"PATH=/bin", "HOME=/home/user", "BASH_FUNC_f%%=() {}", "1BAD=x", "SECRET=hunter2", "=x"}
	log := "/home/user/.local/state/piste/switch.log"
	got := detachArgv("piste-switch-1761900000", log, "/nix/store/x/bin/piste", environ, true, "o", "hh", true)
	want := strings.Fields("systemd-run --user --wait --collect --quiet --same-dir --unit piste-switch-1761900000 " +
		"-p StandardOutput=truncate:" + log + " -E PATH -E HOME -E SECRET -- /nix/store/x/bin/piste -v --overlay o switch --host hh --no-undo")
	if !slices.Equal(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	for _, a := range got {
		if strings.Contains(a, "hunter2") {
			t.Errorf("a value in the argv: %q", a)
		}
	}

	got = detachArgv("piste-switch-1", log, "/p", nil, false, "", "hh", false)
	if i := slices.Index(got, "--"); i < 0 || !slices.Equal(got[i:], []string{"--", "/p", "switch", "--host", "hh"}) {
		t.Errorf("no flags: got %q", got)
	}
	got = detachArgv("piste-switch-1", log, "/p", nil, false, "", "", false)
	if i := slices.Index(got, "--"); i < 0 || !slices.Equal(got[i:], []string{"--", "/p", "switch"}) {
		t.Errorf("no flags: got %q", got)
	}
}

// systemd-run takes a bare -E NAME from its own environment since systemd
// 250; piste reads the version to say so when the unit can't start.
func TestSystemdVersion(t *testing.T) {
	for _, c := range []struct {
		out  string
		want int
		ok   bool
	}{
		{"systemd 255 (255.4-1ubuntu8.18)\n+PAM +AUDIT +SELINUX\n", 255, true},
		{"systemd 249 (249.11-0ubuntu3.12)\n", 249, true},
		{"systemd 239 (239-78.el8)\n", 239, true},
		{"systemd 256\n", 256, true},
		{"", 0, false},
		{"systemd-run: unrecognized option\n", 0, false},
	} {
		got, ok := systemdVersion([]byte(c.out))
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got %d %v, want %d %v", c.out, got, ok, c.want, c.ok)
		}
	}
}
