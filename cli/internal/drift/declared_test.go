package drift

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
)

// Only names Nix can take as an attribute reach the expression, once each,
// in order; the expression is one line with no comment in it.
func TestApplyExpr(t *testing.T) {
	got := applyExpr([]string{"ripgrep", "@scope/pkg", "python@3.12", "example/tap/foo", "ripgrep", "g++"})
	if !strings.HasPrefix(got, "(") || !strings.HasSuffix(got, `) [ "ripgrep" "g++" ]`) {
		t.Errorf("got %q", got)
	}
	if strings.ContainsAny(got, "\n#") {
		t.Errorf("not one comment-free line: %q", got)
	}
	if e := applyExpr(nil); !strings.HasSuffix(e, ") [ ]") {
		t.Errorf("no names: %q", e)
	}
}

func TestDeclared(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {Stdout: []byte(`{
  "nix": ["git", "ripgrep"], "casks": ["firefox"], "brews": ["mas"], "exclude": ["docker"],
  "files": {"homebrew.casks": ["/nix/store/x-source/modules/apps.nix"]},
  "pkgs": {"ripgrep": {"pname": "ripgrep", "version": "15.2.0"}}}`)}}}
	d, err := Declared(context.Background(), rec, `f#darwinConfigurations."h"`, []string{"--override-input", "piste", "X"}, []string{"ripgrep"})
	if err != nil {
		t.Fatal(err)
	}
	want := Declarations{
		Nix: []string{"git", "ripgrep"}, Casks: []string{"firefox"}, Brews: []string{"mas"}, Exclude: []string{"docker"},
		Files: map[string][]string{"homebrew.casks": {"/nix/store/x-source/modules/apps.nix"}},
		Pkgs:  map[string]Package{"ripgrep": {Pname: "ripgrep", Version: "15.2.0"}},
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("got %+v", d)
	}
	argv := rec.Calls[0]
	if strings.Join(argv[:8], " ") != `nix eval --json f#darwinConfigurations."h" --override-input piste X --apply` ||
		len(argv) != 9 || argv[8] != applyExpr([]string{"ripgrep"}) {
		t.Errorf("argv %q", argv)
	}
}

func TestDeclaredEvalFails(t *testing.T) {
	stderr := []byte("error: attribute 'h' missing\n")
	rec := &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {ExitCode: 1, Stderr: stderr}}}
	_, err := Declared(context.Background(), rec, "f#x", nil, nil)
	var ee *EvalError
	if !errors.As(err, &ee) || string(ee.Stderr) != string(stderr) {
		t.Errorf("failed eval: %v", err)
	}
	rec = &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {Stdout: []byte("not json")}}}
	if _, err := Declared(context.Background(), rec, "f#x", nil, nil); !errors.As(err, &ee) {
		t.Errorf("bad JSON: %v", err)
	}
}
