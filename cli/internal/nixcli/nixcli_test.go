package nixcli

import (
	"context"
	"reflect"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
)

func TestAttrNames(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{
		"nix eval --json path:/f#darwinConfigurations": {Stdout: []byte(`["Janes-MacBook-Pro"]`)},
	}}
	n := Nix{Run: rec, Extra: []string{"--show-trace"}}
	got, err := n.AttrNames(context.Background(), "path:/f#darwinConfigurations")
	if err != nil || !reflect.DeepEqual(got, []string{"Janes-MacBook-Pro"}) {
		t.Fatal(got, err)
	}
	want := []string{"nix", "eval", "--json", "path:/f#darwinConfigurations", "--apply", "builtins.attrNames", "--show-trace"}
	if !reflect.DeepEqual(rec.Calls[0], want) {
		t.Fatalf("argv %v", rec.Calls[0])
	}
}

func TestEvalError(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {ExitCode: 1, Stderr: []byte("error: boom\n")}}}
	var v any
	if err := (Nix{Run: rec}).EvalJSON(context.Background(), "x", "", &v); err == nil {
		t.Fatal("want error")
	}
}

func TestBuildOutPaths(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"nix build": {Stdout: []byte("/nix/store/a\n/nix/store/b\n")}}}
	got, err := (Nix{Run: rec}).BuildOutPaths(context.Background(), ".#a", ".#b")
	if err != nil || !reflect.DeepEqual(got, []string{"/nix/store/a", "/nix/store/b"}) {
		t.Fatal(got, err)
	}
}
