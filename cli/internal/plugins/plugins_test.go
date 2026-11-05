package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mk(t *testing.T, dir, name string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestFind(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	mk(t, a, "piste-pkgs", 0o755)
	mk(t, b, "piste-pkgs", 0o755)
	mk(t, b, "piste-secrets-rotate", 0o755)
	mk(t, a, "piste-noexec", 0o644)
	path := strings.Join([]string{a, b}, string(os.PathListSeparator))

	if got := Find([]string{"pkgs"}, path); got != filepath.Join(a, "piste-pkgs") {
		t.Errorf("first PATH entry should win, got %q", got)
	}
	if got := Find([]string{"secrets", "rotate"}, path); got != filepath.Join(b, "piste-secrets-rotate") {
		t.Errorf("group plugin: got %q", got)
	}
	for _, p := range [][]string{{"noexec"}, {"missing"}, {"../x"}, {"-v"}, nil} {
		if got := Find(p, path); got != "" {
			t.Errorf("Find(%v) = %q, want none", p, got)
		}
	}
	got := List(path)
	if strings.Join(got, ",") != "pkgs,secrets-rotate" {
		t.Errorf("List = %v", got)
	}
}

// TestFindUsesPistePrefix: plugins are piste-<cmd>; a differently prefixed
// executable on PATH is not a plugin.
func TestFindUsesPistePrefix(t *testing.T) {
	dir := t.TempDir()
	mk(t, dir, "piste-foo", 0o755)
	mk(t, dir, "other-bar", 0o755)
	if got := Find([]string{"foo"}, dir); got != filepath.Join(dir, "piste-foo") {
		t.Errorf("Find(foo) = %q, want %s/piste-foo", got, dir)
	}
	if got := Find([]string{"bar"}, dir); got != "" {
		t.Errorf("Find(bar) = %q, want none (other-bar is not a piste plugin)", got)
	}
}
