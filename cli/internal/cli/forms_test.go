package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"charm.land/huh/v2"
)

// Forms only for a person at a terminal who hasn't asked for plain output.
func TestFormsWanted(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	for i, c := range []struct {
		term bool
		cols int
		env  func(string) string
		want bool
	}{
		{false, 80, env(), false},
		{true, 80, env(), true},
		{true, 80, env("NO_COLOR", "1"), false},
		{true, 80, env("TERM", "dumb"), false},
		{true, 80, env("TERM", "xterm-256color"), true},
		{true, 80, env("ACCESSIBLE", "1"), false},
		// A terminal that reports no width (script(1), some CI and editor
		// shells): huh can't lay a form out in it, and panics.
		{true, 0, env(), false},
	} {
		if got := formsWanted(c.term, c.cols, c.env); got != c.want {
			t.Errorf("case %d: formsWanted = %v, want %v", i, got, c.want)
		}
	}
}

// A buffer for stdin is never a terminal; the answer is kept for the run.
func TestUseFormsNotATerminal(t *testing.T) {
	a := &App{Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Env: Env{Getenv: func(string) string { return "" }}}
	if a.useForms() {
		t.Fatal("useForms with a buffer for stdin")
	}
	if a.forms == nil {
		t.Fatal("the decision wasn't kept")
	}
}

func formApp(t *testing.T, runs ...[]any) *App {
	return &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, forms: ptr(true), runForm: drive(t, runs...).run}
}

func TestAskYesNo(t *testing.T) {
	for _, c := range []struct {
		def   bool
		steps []any
		want  bool
	}{
		{false, []any{enter}, false},
		{false, []any{left, enter}, true},
		{true, []any{enter}, true},
	} {
		got, err := formApp(t, c.steps).askYesNo("Go?", "Yes", "No", c.def)
		if err != nil || got != c.want {
			t.Errorf("def %v, %d steps: %v, %v; want %v", c.def, len(c.steps), got, err, c.want)
		}
	}
	if _, err := formApp(t, []any{ctrlC}).askYesNo("Go?", "Yes", "No", false); !errors.Is(err, errStopped) {
		t.Errorf("Ctrl+C: %v, want errStopped", err)
	}
}

func TestPickOne(t *testing.T) {
	opts := huh.NewOptions("a", "b", "c")
	for _, c := range []struct {
		steps []any
		want  string
	}{
		{[]any{enter}, "b"},
		{[]any{down, enter}, "c"},
	} {
		got, err := formApp(t, c.steps).pickOne("Which", opts, "b")
		if err != nil || got != c.want {
			t.Errorf("%d steps: %q, %v; want %q", len(c.steps), got, err, c.want)
		}
	}
	if _, err := formApp(t, []any{ctrlC}).pickOne("Which", opts, "b"); !errors.Is(err, errStopped) {
		t.Errorf("Ctrl+C: %v, want errStopped", err)
	}
}

// Options and buttons in the terminal's own colour: huh assumes a light
// background (it never asks) and then draws them in near-white 252.
func TestFormColoursReadableEitherWay(t *testing.T) {
	check := func(f *huh.Form) {
		if v := f.View(); strings.Contains(v, "38;5;252") || strings.Contains(v, "38;5;235") {
			t.Errorf("a fixed grey on the screen:\n%q", v)
		}
	}
	if _, err := formApp(t, steps(check, enter)).pickOne("Which", huh.NewOptions("a", "b", "c"), "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := formApp(t, steps(check, enter)).askYesNo("Go?", "Yes", "No", false); err != nil {
		t.Fatal(err)
	}
}
