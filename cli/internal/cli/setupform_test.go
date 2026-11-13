package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupChoiceCreate(t *testing.T) {
	url, err := formApp(t, []any{enter}).setupChoice()
	if url != "" || err != nil {
		t.Errorf("%q, %v; want create", url, err)
	}
}

func TestSetupChoiceAdd(t *testing.T) {
	url, err := formApp(t, steps(down, enter, text("https://example.com/o.git"), enter)).setupChoice()
	if url != "https://example.com/o.git" || err != nil {
		t.Errorf("%q, %v", url, err)
	}
}

// Add one I already have, then no URL: the choice again, Add selected.
func TestSetupChoiceEmptyURL(t *testing.T) {
	d := drive(t, steps(down, enter, enter), steps(enter, text("u"), enter))
	a := formApp(t)
	a.runForm = d.run
	url, err := a.setupChoice()
	if url != "u" || err != nil || d.n != 2 {
		t.Errorf("%q, %v after %d forms", url, err, d.n)
	}
}

// Shift+Tab from the empty URL goes back to the choice.
func TestSetupChoiceBackFromURL(t *testing.T) {
	url, err := formApp(t, steps(down, enter, shiftTab, up, enter)).setupChoice()
	if url != "" || err != nil {
		t.Errorf("%q, %v; want create", url, err)
	}
}

func TestSetupChoiceStopped(t *testing.T) {
	if _, err := formApp(t, []any{ctrlC}).setupChoice(); !errors.Is(err, errStopped) {
		t.Errorf("Ctrl+C: %v, want errStopped", err)
	}
}

// Later unless picked; Ctrl+C is Later too.
func TestOfferSwitch(t *testing.T) {
	for _, c := range []struct {
		steps []any
		want  bool
	}{
		{[]any{enter}, false},
		{[]any{left, enter}, true},
		{[]any{ctrlC}, false},
	} {
		got, err := formApp(t, c.steps).offerSwitch()
		if err != nil || got != c.want {
			t.Errorf("%d steps: %v, %v; want %v", len(c.steps), got, err, c.want)
		}
	}
}

// Ctrl+C in the init setup runs: setup's message, not init's (the
// checkout may be a fresh clone).
func TestSetupInitStoppedMessage(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "overlays"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".piste"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", ".piste"},
		{"-c", "commit.gpgsign=false", "commit", "-q", "-m", "piste"},
		{"remote", "add", "origin", "https://github.com/example/piste.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(root)
	var errb bytes.Buffer
	d := drive(t, []any{enter}, []any{ctrlC})
	a := &App{Stdout: &bytes.Buffer{}, Stderr: &errb, Env: Env{Getenv: os.Getenv}, forms: ptr(true), runForm: d.run}
	if _, _, _, err := a.setupOverlay(nil, root); err != (ExitError{1}) {
		t.Errorf("exit %v, want ExitError{1}", err)
	}
	if d.n != 2 {
		t.Errorf("%d forms ran, want setup's and init's", d.n)
	}
	if s := errb.String(); !strings.Contains(s, "No answer: stopped. Run piste setup again.") || strings.Contains(s, "nothing written") {
		t.Errorf("stderr %q", s)
	}
}
