package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// find is the command at path (["agents", "bundle"]) in root.
func find(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	c, _, err := root.Find(path)
	if err != nil || c == root {
		t.Fatalf("no command %q: %v", strings.Join(path, " "), err)
	}
	return c
}

// v0.1.0's help lists what is promised: maintainer tools and the plumbing
// the Nix modules call work, and are hidden.
func TestHiddenCommands(t *testing.T) {
	root := docsRoot()
	for _, p := range [][]string{
		{"sync"}, {"update-overlays"}, {"remote"}, {"diff"}, {"edit"}, {"pkg"}, {"pkg", "add"},
		{"agents", "bundle"}, {"agents", "budget"}, {"agents", "registry"},
	} {
		if c := find(t, root, p...); !c.Hidden {
			t.Errorf("piste %s is listed in --help", strings.Join(p, " "))
		}
	}
	for _, p := range [][]string{
		{"switch"}, {"build"}, {"check"}, {"update"}, {"agents", "apply"}, {"agents", "sync"}, {"secrets", "edit"},
	} {
		if c := find(t, root, p...); c.Hidden {
			t.Errorf("piste %s is hidden", strings.Join(p, " "))
		}
	}
}

// The deleted flags stay deleted: no AI pkg add, no shared scope, no claude
// --auto (--permission-mode auto passes through), one --host, no --profile,
// and clean's --dry-run like every other command's.
func TestDeletedFlags(t *testing.T) {
	root := docsRoot()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, name := range []string{"profile", "ai", "shared"} {
			if c.Flags().Lookup(name) != nil {
				t.Errorf("piste %s has --%s", c.CommandPath(), name)
			}
		}
		for _, s := range c.Commands() {
			walk(s)
		}
	}
	walk(root)
	if strings.Contains(find(t, root, "claude").Use, "--auto") {
		t.Errorf("piste claude still documents --auto")
	}
	if c := find(t, root, "clean"); c.Flags().Lookup("dry") != nil || c.Flags().Lookup("dry-run") == nil {
		t.Errorf("clean's flags: want --dry-run and no --dry")
	}
	var usage string
	for _, name := range []string{"build", "switch", "dry", "gendiff", "fix"} {
		f := find(t, root, name).Flags().Lookup("host")
		if f == nil {
			t.Errorf("piste %s has no --host", name)
			continue
		}
		if usage == "" {
			usage = f.Usage
		} else if f.Usage != usage {
			t.Errorf("piste %s --host means %q, others %q", name, f.Usage, usage)
		}
	}
}

// --host is a hostname: a user@host is refused before anything is looked up.
func TestHostIsAHostname(t *testing.T) {
	for _, name := range []string{"build", "switch", "dry", "gendiff", "fix"} {
		a := &App{Stdout: io.Discard, Stderr: io.Discard, Env: Env{Getenv: func(string) string { return "" }}}
		root := a.newRoot()
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{name, "--host", "jane@box"})
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "--host takes a hostname") {
			t.Errorf("piste %s --host jane@box: %v", name, err)
		}
	}
}
