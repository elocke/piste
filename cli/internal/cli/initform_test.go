package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"charm.land/huh/v2"

	"github.com/elocke/piste/cli/internal/overlay"
)

func linuxAns() overlay.Answers {
	return overlay.Answers{User: "jane", System: "x86_64-linux", Hostname: "devbox", Zone: "personal",
		GitName: "Jane Doe", GitEmail: "jane@example.com"}
}

var allQs = initQs{user: true, system: true, host: true, roles: true, zone: true, name: true, gitName: true, gitEmail: true, sops: true, extras: true}

func validName(n string) error { return overlay.ValidName(n) }

func enters(n int) []any {
	s := make([]any, n)
	for i := range s {
		s[i] = enter
	}
	return s
}

func steps(parts ...any) []any {
	var out []any
	for _, p := range parts {
		if s, ok := p.([]any); ok {
			out = append(out, s...)
		} else {
			out = append(out, p)
		}
	}
	return out
}

// viewHas checks the form's current screen for s (or its absence).
func viewHas(t *testing.T, s string, want bool) func(*huh.Form) {
	return func(f *huh.Form) {
		t.Helper()
		if strings.Contains(f.View(), s) != want {
			t.Errorf("screen has %q = %v, want %v:\n%s", s, !want, want, f.View())
		}
	}
}

// errorIs checks the current screen shows err's message.
func errorIs(t *testing.T, err error) func(*huh.Form) {
	return func(f *huh.Form) {
		t.Helper()
		for _, e := range f.Errors() {
			if e.Error() == err.Error() {
				return
			}
		}
		t.Errorf("screen errors %v, want %q", f.Errors(), err)
	}
}

// Enter through every question and Create: the defaults.
func TestInitFormDefaults(t *testing.T) {
	ans := linuxAns()
	if err := formApp(t, enters(11)).initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	want := linuxAns()
	want.Roles, want.Name = []string{"dev"}, "personal"
	if !reflect.DeepEqual(ans, want) {
		t.Errorf("answers %+v\nwant %+v", ans, want)
	}
}

func TestInitFormRefusesBadUser(t *testing.T) {
	ans := linuxAns()
	run := steps(ctrlU, text("Bad User"), enter, errorIs(t, overlay.CheckUser("Bad User")),
		ctrlU, text("bob"), enter, enters(10))
	if err := formApp(t, run).initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	if ans.User != "bob" {
		t.Errorf("user %q, want bob", ans.User)
	}
}

// A flag's answer isn't asked, and is on the summary.
func TestInitFormFlagAnswerOnSummary(t *testing.T) {
	ans := linuxAns()
	ans.User = "flaguser"
	q := allQs
	q.user = false
	run := steps(viewHas(t, "username", false), enters(9), viewHas(t, "flaguser", true), enter)
	if err := formApp(t, run).initForm(&ans, q, validName); err != nil {
		t.Fatal(err)
	}
	if ans.User != "flaguser" {
		t.Errorf("user %q", ans.User)
	}
}

// Change something: the form again from the start, every answer kept.
func TestInitFormChangeSomething(t *testing.T) {
	ans := linuxAns()
	d := drive(t, steps(enters(10), right, enter), steps(enter, enter, ctrlU, text("box2"), enters(9)))
	a := formApp(t)
	a.runForm = d.run
	if err := a.initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	if ans.Hostname != "box2" || d.n != 2 {
		t.Errorf("hostname %q after %d runs, want box2 after 2", ans.Hostname, d.n)
	}
}

func TestInitFormDesktop(t *testing.T) {
	ans := linuxAns()
	run := steps(enters(3), down, enters(8))
	if err := formApp(t, run).initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ans.Roles, []string{"desktop"}) {
		t.Errorf("roles %v, want [desktop]", ans.Roles)
	}
}

// A Mac brings desktop: no roles question.
func TestInitFormMacNoRoles(t *testing.T) {
	ans := linuxAns()
	ans.System, ans.Hostname = "aarch64-darwin", "jane-mac"
	q := allQs
	q.system = false
	run := steps(enters(2), viewHas(t, "Roles", false), enters(6), viewHas(t, "desktop (every Mac)", true), enter)
	if err := formApp(t, run).initForm(&ans, q, validName); err != nil {
		t.Fatal(err)
	}
	if ans.Roles != nil {
		t.Errorf("roles %v, want none", ans.Roles)
	}
}

func TestInitFormMacPicked(t *testing.T) {
	ans := linuxAns()
	run := steps(enter, up, enter, enter, viewHas(t, "Roles", false), enters(7))
	if err := formApp(t, run).initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	if ans.System != "aarch64-darwin" || ans.Roles != nil {
		t.Errorf("system %q roles %v, want aarch64-darwin and none", ans.System, ans.Roles)
	}
}

func TestInitFormMacOnSecondPassRefusesDottedHost(t *testing.T) {
	ans := linuxAns()
	first := steps(enter, enter, ctrlU, text("box.lan"), enters(8), right, enter)
	second := steps(enter, up, enter, enter, errorIs(t, overlay.CheckHostname("box.lan", "aarch64-darwin")),
		ctrlU, text("box"), enters(8))
	d := drive(t, first, second)
	a := formApp(t)
	a.runForm = d.run
	if err := a.initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	if ans.Hostname != "box" || ans.System != "aarch64-darwin" {
		t.Errorf("host %q system %q", ans.Hostname, ans.System)
	}
}

func TestInitFormNameTaken(t *testing.T) {
	ans := linuxAns()
	taken := func(n string) error {
		if n == "personal" {
			return fmt.Errorf("overlays/%s exists", n)
		}
		return overlay.ValidName(n)
	}
	run := steps(enters(5), enter, errorIs(t, errors.New("overlays/personal exists")), text("work2"), enters(6))
	if err := formApp(t, run).initForm(&ans, allQs, taken); err != nil {
		t.Fatal(err)
	}
	if ans.Name != "work2" {
		t.Errorf("name %q, want work2", ans.Name)
	}
}

func TestInitFormNothingAsked(t *testing.T) {
	ans := linuxAns()
	a := formApp(t)
	a.runForm = func(*huh.Form) error { t.Fatal("a form with nothing to ask"); return nil }
	if err := a.initForm(&ans, initQs{}, validName); err != nil {
		t.Fatal(err)
	}
}

func TestInitFormStopped(t *testing.T) {
	ans := linuxAns()
	if err := formApp(t, []any{ctrlC}).initForm(&ans, allQs, validName); !errors.Is(err, errStopped) {
		t.Errorf("Ctrl+C: %v, want errStopped", err)
	}
}

// piste init, Ctrl+C at its form: the message, exit 1, nothing written.
func TestInitStoppedMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "overlays"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".piste"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	var errb bytes.Buffer
	a := &App{Stdout: &bytes.Buffer{}, Stderr: &errb, Env: Env{Getenv: os.Getenv}, forms: ptr(true),
		runForm: drive(t, []any{ctrlC}).run}
	c := newInit(a)
	c.SetArgs([]string{"--engine", "github:example/piste"})
	c.SilenceErrors, c.SilenceUsage = true, true
	if err := c.Execute(); err != (ExitError{1}) {
		t.Errorf("exit %v, want ExitError{1}", err)
	}
	if !strings.Contains(errb.String(), "Stopped: nothing written.") {
		t.Errorf("stderr %q", errb.String())
	}
	if es, _ := os.ReadDir(filepath.Join(root, "overlays")); len(es) != 0 {
		t.Errorf("overlays/ has %d entries", len(es))
	}
}

// Shift+Tab off a screen's first answer while it's refused (a fresh
// machine's empty git name): refused there, not a frozen form.
func TestInitFormBackFromEmptyGitName(t *testing.T) {
	ans := linuxAns()
	ans.GitName = ""
	run := steps(enters(6), shiftTab, errorIs(t, overlay.CheckGit("git name", "")), text("Jo"), enters(5))
	if err := formApp(t, run).initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	if ans.GitName != "Jo" {
		t.Errorf("git name %q, want Jo", ans.GitName)
	}
}

// The summary shows answers as typed (huh's notes read _ and * as markup).
func TestInitFormSummaryShowsAnswersAsTyped(t *testing.T) {
	ans := linuxAns()
	ans.User, ans.GitEmail = "j_doe", "first_last@corp.com"
	run := steps(enters(10), viewHas(t, "x86_64-linux", true), viewHas(t, "j_doe", true),
		viewHas(t, "first_last@corp.com", true), enter)
	if err := formApp(t, run).initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
}

// "Start minimal or with everything?": minimal by default (Enter), on the
// summary either way.
func TestInitFormStartMinimalByDefault(t *testing.T) {
	ans := linuxAns()
	run := steps(enters(9), viewHas(t, "Start minimal or with everything?", true), enter,
		viewHas(t, "start     minimal", true), enter)
	if err := formApp(t, run).initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	if ans.Extras || !reflect.DeepEqual(ans.Roles, []string{"dev"}) {
		t.Errorf("extras %v roles %v, want minimal and [dev]", ans.Extras, ans.Roles)
	}
}

// Everything: the roles question's answer stays, and extras goes beside it.
func TestInitFormStartEverything(t *testing.T) {
	ans := linuxAns()
	run := steps(enters(9), down, enter, viewHas(t, "start     everything", true), enter)
	if err := formApp(t, run).initForm(&ans, allQs, validName); err != nil {
		t.Fatal(err)
	}
	if !ans.Extras || !reflect.DeepEqual(ans.Roles, []string{"dev"}) {
		t.Errorf("extras %v roles %v, want everything and [dev]", ans.Extras, ans.Roles)
	}
}

// On a Mac (no roles question) the question is still asked.
func TestInitFormMacStartEverything(t *testing.T) {
	ans := linuxAns()
	ans.System, ans.Hostname = "aarch64-darwin", "jane-mac"
	q := allQs
	q.system = false
	run := steps(enters(7), viewHas(t, "Start minimal or with everything?", true), down, enter,
		viewHas(t, "start     everything", true), enter)
	if err := formApp(t, run).initForm(&ans, q, validName); err != nil {
		t.Fatal(err)
	}
	if !ans.Extras || ans.Roles != nil {
		t.Errorf("extras %v roles %v, want everything and none", ans.Extras, ans.Roles)
	}
}

// Answered by --extras: not asked, on the summary.
func TestInitFormStartFromFlag(t *testing.T) {
	ans := linuxAns()
	ans.Extras = true
	q := allQs
	q.extras = false
	run := steps(enters(9), viewHas(t, "Start minimal", false), viewHas(t, "start     everything", true), enter)
	if err := formApp(t, run).initForm(&ans, q, validName); err != nil {
		t.Fatal(err)
	}
	if !ans.Extras {
		t.Error("extras dropped")
	}
}
