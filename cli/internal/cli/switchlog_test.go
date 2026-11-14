package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInSwitchUnit(t *testing.T) {
	for _, c := range []struct {
		cgroup string
		want   bool
	}{
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/piste-switch-1700000000.service\n", true},
		{"0::/user.slice/user-1000.slice/user@1000.service/app.slice/runner.service\n", false},
		{"0::/user.slice/user-1000.slice/session-3.scope\n", false},
		{"", false},
	} {
		if got := inSwitchUnit(c.cgroup); got != c.want {
			t.Errorf("%q: got %v", c.cgroup, got)
		}
	}
}

// A switch's record round-trips: the header, piste's messages and the
// switch's output, the footer.
func TestSwitchRecordRoundTrip(t *testing.T) {
	state := t.TempDir()
	var out, errOut bytes.Buffer
	a := &App{Stdout: &out, Stderr: &errOut, Env: Env{Getenv: envOf(map[string]string{"XDG_STATE_HOME": state})}, Args: []string{"switch"}}
	a.startRecord("/home/user")
	a.recordTarget("jane-mac", "/home/user/piste/overlays/mine")
	a.info("Building and switching...")
	fmt.Fprintln(a.teeing(), "Error: No available formula with the name \"oven-sh/bun/bun\".")
	a.errorf("Switch failed (exit code 1)")
	a.warn("To rollback: piste rollback")
	a.endRecord(ExitError{1})
	if a.record != nil {
		t.Fatal("record left open")
	}
	path := filepath.Join(state, "piste", "switch.log")
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("log: %v %v", fi, err)
	}
	if !strings.Contains(out.String(), "Building and switching") {
		t.Fatalf("message not on the terminal: %q", out.String())
	}
	s, ok := readSwitchLog(path)
	if !ok || s.Host != "jane-mac" || s.Flake != "/home/user/piste/overlays/mine" || !s.Finished || s.Exit != 1 || s.When == "" {
		t.Fatalf("read %+v %v", s, ok)
	}
	ex := s.excerpt()
	want := []string{`Error: No available formula with the name "oven-sh/bun/bun".`, "Switch failed (exit code 1)"}
	if strings.Join(ex, "\n") != strings.Join(want, "\n") {
		t.Fatalf("excerpt %q", ex)
	}
	if _, ok := failedSwitch(path, "jane-mac", "/home/user/piste/overlays/mine"); !ok {
		t.Fatal("failed switch not found")
	}
	for _, c := range [][2]string{{"other", "/home/user/piste/overlays/mine"}, {"jane-mac", "/elsewhere"}} {
		if _, ok := failedSwitch(path, c[0], c[1]); ok {
			t.Fatalf("matched %q", c)
		}
	}
}

func TestFailedSwitchStates(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "switch.log")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	head := "# piste switch: 2026-01-01T00:00:00Z, piste 0.1.0\n# ran: piste switch\n# host: h\n# flake: /f\n"
	if _, ok := failedSwitch(write(head+"ok\n# exit: 0\n"), "h", "/f"); ok {
		t.Fatal("a passing switch counts")
	}
	// Killed part-way: no footer.
	if s, ok := failedSwitch(write(head+"activating...\n"), "h", "/f"); !ok || s.Finished {
		t.Fatalf("an unfinished switch: %+v %v", s, ok)
	}
	if _, ok := failedSwitch(write("no header\n"), "h", "/f"); ok {
		t.Fatal("a log with no header counts")
	}
	if _, ok := failedSwitch(filepath.Join(dir, "none"), "h", "/f"); ok {
		t.Fatal("no log counts")
	}
}

func TestSwitchExcerpt(t *testing.T) {
	var body []string
	for i := range 50 {
		body = append(body, fmt.Sprintf("building line %d", i))
	}
	body = append(body,
		"\x1b[31mError:\x1b[0m something broke",
		"Error: something broke",
		strings.Repeat("x", 500)+" error",
		"To debug:    piste --verbose switch",
		"To have Claude Code fix it: piste fix",
	)
	ex := lastSwitch{Body: body}.excerpt()
	if len(ex) != 2 || ex[0] != "Error: something broke" || !strings.HasSuffix(ex[1], "…") || len(ex[1]) > excerptWidth+len("…") {
		t.Fatalf("excerpt %q", ex)
	}
	// Nothing reads as an error: the last lines.
	ex = lastSwitch{Body: body[:50]}.excerpt()
	if len(ex) != excerptLines || ex[len(ex)-1] != "building line 49" {
		t.Fatalf("tail %q", ex)
	}
}

func TestEndRecordCodes(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{{nil, "# exit: 0"}, {ExitError{3}, "# exit: 3"}, {errors.New("x"), "# exit: 1"}} {
		state := t.TempDir()
		a := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Env: Env{Getenv: envOf(map[string]string{"XDG_STATE_HOME": state})}}
		a.startRecord("/home/user")
		a.endRecord(c.err)
		b, _ := os.ReadFile(filepath.Join(state, "piste", "switch.log"))
		if !strings.HasSuffix(string(b), c.want+"\n") {
			t.Errorf("%v: %q", c.err, b)
		}
	}
}

// A detached switch's follower shows the unit's output without the log's
// header and footer, however the reads split the lines.
func TestRecordFilter(t *testing.T) {
	in := "# piste switch: 2026-01-01T00:00:00Z, piste 0.1.0\n# ran: piste switch\n# host: h\n# flake: /f\nBuilding...\n# not a record\nSwitch complete!\n# exit: 0\ntail"
	var out bytes.Buffer
	f := &recordFilter{w: &out}
	for i := 0; i < len(in); i += 7 {
		f.Write([]byte(in[i:min(i+7, len(in))]))
	}
	f.flush()
	if want := "Building...\n# not a record\nSwitch complete!\ntail"; out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}
