package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/platform"
)

// switch's and rollback's yes/no: No unless picked; Ctrl+C is click's abort.
func TestConfirmForm(t *testing.T) {
	for _, c := range []struct {
		steps []any
		want  bool
	}{
		{[]any{enter}, false},
		{[]any{left, enter}, true},
	} {
		got, err := formApp(t, c.steps).confirm("Activate previous generation?")
		if err != nil || got != c.want {
			t.Errorf("%d steps: %v, %v; want %v", len(c.steps), got, err, c.want)
		}
	}
	if _, err := formApp(t, []any{ctrlC}).confirm("Activate previous generation?"); !errors.Is(err, errAborted) {
		t.Errorf("Ctrl+C: %v, want errAborted", err)
	}
}

// secrets edit's file, when there are several.
func TestChooseFileForm(t *testing.T) {
	root := t.TempDir()
	files := []string{filepath.Join(root, "a.sops.yaml"), filepath.Join(root, "b.sops.yaml")}
	for _, c := range []struct {
		steps []any
		want  string
	}{
		{[]any{enter}, files[0]},
		{[]any{down, enter}, files[1]},
	} {
		got, err := formApp(t, c.steps).chooseFile(root, files)
		if err != nil || got != c.want {
			t.Errorf("%d steps: %q, %v; want %q", len(c.steps), got, err, c.want)
		}
	}
	if _, err := formApp(t, []any{ctrlC}).chooseFile(root, files); !errors.Is(err, errAborted) {
		t.Errorf("Ctrl+C: %v, want errAborted", err)
	}
}

// pkg add's scope, outside any overlay of a checkout with several.
func TestResolveScopeForm(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{".piste", "overlays/one/flake.nix", "overlays/two/flake.nix"} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	p := platform.Platform{Checkout: root}
	for _, c := range []struct {
		steps []any
		want  string
	}{
		{[]any{enter}, filepath.Join(root, "overlays", "one")},
		{[]any{down, enter}, filepath.Join(root, "overlays", "two")},
	} {
		a := formApp(t, c.steps)
		got, err := a.resolveScope(p, "")
		if err != nil || got != c.want {
			t.Errorf("%d steps: %q, %v; want %q", len(c.steps), got, err, c.want)
		}
		out := a.Stdout.(*bytes.Buffer).String()
		if !strings.Contains(out, "Several flakes in the checkout") || strings.Contains(out, " -> ") {
			t.Errorf("stdout: %q; want the Several flakes line and no list", out)
		}
	}
	a := formApp(t, []any{ctrlC})
	if _, err := a.resolveScope(p, ""); err != (ExitError{1}) {
		t.Errorf("Ctrl+C: %v, want ExitError{1}", err)
	}
}
