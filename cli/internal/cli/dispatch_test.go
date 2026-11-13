package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeEnv map[string]string

func (f fakeEnv) env(pathDirs ...string) Env {
	f["PATH"] = strings.Join(pathDirs, string(os.PathListSeparator))
	return Env{
		Getenv: func(k string) string { return f[k] },
	}
}

func exe(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}
func TestResolve(t *testing.T) {
	bin := t.TempDir()
	exe(t, bin, "piste-tidy")
	exe(t, bin, "piste-switch") // must never shadow the built-in
	exe(t, bin, "piste-secrets-rotate")
	goCmds := goNames()

	for _, c := range []struct {
		args []string
		impl Impl
		path string
		argv string
	}{
		{nil, ImplGo, "", ""},
		{[]string{"--help"}, ImplGo, "", ""},
		{[]string{"--version"}, ImplGo, "", ""},
		{[]string{"completion", "zsh"}, ImplGo, "", ""},
		{[]string{"switch", "--host", "x"}, ImplGo, "", ""},
		{[]string{"-v", "switch"}, ImplGo, "", ""},
		{[]string{"update-overlays"}, ImplGo, "", ""},
		{[]string{"secrets", "init"}, ImplGo, "", ""},
		// the Go group execs piste-secrets-<sub> itself (newSecrets)
		{[]string{"secrets", "rotate", "--all"}, ImplGo, "", ""},
		{[]string{"remote", "move", "pb", "abcd"}, ImplGo, "", ""},
		{[]string{"tidy", "list"}, ImplPlugin, filepath.Join(bin, "piste-tidy"), "piste-tidy list"},
		{[]string{"nosuchcmd"}, ImplUnknown, "", ""},
	} {
		check(t, c.args, goCmds, fakeEnv{}, bin, c.impl, c.path, c.argv)
	}
}

func check(t *testing.T, args []string, goCmds map[string]bool, env fakeEnv, bin string, impl Impl, path, argv string) {
	t.Helper()
	r := Resolve(args, goCmds, env.env(bin))
	if r.Impl != impl || r.Path != path || strings.Join(r.Argv, " ") != argv {
		t.Errorf("Resolve(%v, %v) = %+v; want %s %s %q", args, env, r, impl, path, argv)
	}
}
