package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/drift"
	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/outdated"
	"github.com/elocke/piste/cli/internal/platform"
)

func TestRepoURL(t *testing.T) {
	for _, c := range []struct{ name, ref, want string }{
		{"github", "github:example/tools/abc", "https://github.com/example/tools"},
		{"git+https", "git+https://example.com/team/layer.git?rev=abc", "https://example.com/team/layer.git"},
		{"gitlab", "gitlab:example/tools/abc", "https://gitlab.com/example/tools"},
		{"sourcehut", "sourcehut:~jane/tools/abc", "https://git.sr.ht/~jane/tools"},
		{"other ref", "tarball+https://example.com/x.tar.gz", "tarball+https://example.com/x.tar.gz"},
		{"empty", "", ""},
	} {
		if got := repoURL(outdated.Input{Ref: c.ref}); got != c.want {
			t.Errorf("%s: repoURL(%q) = %q, want %q", c.name, c.ref, got, c.want)
		}
	}
	if got := layerHeading(outdated.Input{Name: "layer", Path: "./layer"}); got != "Layer layer (in this overlay: ./layer)" {
		t.Errorf("path layer heading = %q", got)
	}
	if got := layerHeading(outdated.Input{Name: "x"}); got != "Layer x" {
		t.Errorf("no url heading = %q", got)
	}
	if got := layerHeading(outdated.Input{Name: "x", Ref: "github:example/x/abc"}); got != "Layer x (a change is a PR to https://github.com/example/x)" {
		t.Errorf("github heading = %q", got)
	}
}

// pkgsApp is an App whose external programs are rec, away from any checkout.
func pkgsApp(t *testing.T, rec *execx.Recorder) (*App, *bytes.Buffer) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USER", "jane")
	t.Setenv("PATH", t.TempDir())
	t.Chdir(home)
	var out bytes.Buffer
	return &App{Stdout: &out, Stderr: &out, execRunner: rec}, &out
}

func TestBrewSection(t *testing.T) {
	old := brewManifest
	defer func() { brewManifest = old }()
	// brew's v2 shape: a formula by its full name, a cask by its token.
	rec := &execx.Recorder{Replies: map[string]execx.Result{"brew outdated": {Stdout: []byte(`{
	  "formulae": [{"name":"example/tap/tool","installed_versions":["1.0"],"current_version":"1.1"}],
	  "casks": [{"name":"app","installed_versions":["2.0"],"current_version":"2.1"}]}`)}}}
	a, out := pkgsApp(t, rec)
	brewManifest = filepath.Join(t.TempDir(), "homebrew.json")
	if err := os.WriteFile(brewManifest, []byte(`{"brews":["example/tap/tool"],"casks":["example/tap/app"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.pkgsReport(platform.Platform{FlakeDir: t.TempDir(), IsDarwin: true})
	for _, w := range []string{"Homebrew:", "example/tap/tool", "formula", "1.0 → 1.1", "example/tap/app", "cask", "2.0 → 2.1"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report lacks %q:\n%s", w, out)
		}
	}

	// No manifest: no section, no error, brew never asked.
	rec2 := &execx.Recorder{}
	a, out = pkgsApp(t, rec2)
	brewManifest = filepath.Join(t.TempDir(), "missing.json")
	a.pkgsReport(platform.Platform{FlakeDir: t.TempDir(), IsDarwin: true})
	if strings.Contains(out.String(), "Homebrew") {
		t.Errorf("Homebrew section without a manifest:\n%s", out)
	}
	for _, c := range rec2.Calls {
		if c[0] == "brew" {
			t.Errorf("brew was run: %v", c)
		}
	}
	if _, state, err := a.brewOutdated(false); state != brewNoList || err != nil {
		t.Errorf("brewOutdated without a manifest = state %v, err %v", state, err)
	}
}

func TestPnpmOutdatedRunnerError(t *testing.T) {
	rec := &execx.Recorder{}
	a, out := pkgsApp(t, rec)
	bin := filepath.Join(os.Getenv("HOME"), ".nix-profile", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(bin, "piste-pnpm")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec.Replies = map[string]execx.Result{tool + " outdated": {ExitCode: 1}}
	items, declared, err := a.pnpmOutdated()
	if !declared || err == nil || len(items) != 0 {
		t.Fatalf("pnpmOutdated = %v, %v, %v; want declared with an error", items, declared, err)
	}
	// The report carries on past it, with a warning, not "up to date".
	a.pkgsReport(platform.Platform{FlakeDir: t.TempDir()})
	if !strings.Contains(out.String(), "pnpm globals: piste-pnpm outdated failed") || strings.Contains(out.String(), "up to date") {
		t.Errorf("report doesn't name the pnpm failure:\n%s", out)
	}

	// Its stderr line is the reason given.
	out.Reset()
	rec.Replies = map[string]execx.Result{tool + " outdated": {ExitCode: 1, Stderr: []byte("piste-pnpm: pnpm isn't on PATH\n")}}
	a.pkgsReport(platform.Platform{FlakeDir: t.TempDir()})
	if !strings.Contains(out.String(), "pnpm globals: piste-pnpm outdated failed: pnpm isn't on PATH") {
		t.Errorf("report doesn't say why pnpm failed:\n%s", out)
	}
}

// A pnpm global piste.packages.pnpm declares (read from the switched
// profile's piste-pnpm) isn't untracked to piste drift and adopt: pinned or
// not, it is matched by name.
func TestPnpmOptionNotUntracked(t *testing.T) {
	rec := &execx.Recorder{}
	a, _ := pkgsApp(t, rec)
	if got := a.pnpmDeclared(); got != nil {
		t.Errorf("pnpmDeclared without a piste-pnpm = %q", got)
	}
	bin := filepath.Join(os.Getenv("HOME"), ".nix-profile", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(bin, "piste-pnpm")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec.Replies = map[string]execx.Result{tool + " declared": {Stdout: []byte("tsx\nserve@14.2.0\n")}}
	js := drift.ReadJS(t.TempDir(), a.pnpmDeclared())
	finds := drift.Classify(drift.Installs{drift.Pnpm: {"tsx", "serve", "left"}}, drift.Declarations{}, js)
	for _, f := range finds {
		if want := f.Name == "left"; (f.Class == drift.Untracked) != want {
			t.Errorf("%s: class %v, untracked should be %v", f.Name, f.Class, want)
		}
	}
}
