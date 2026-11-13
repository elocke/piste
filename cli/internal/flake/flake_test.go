package flake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elocke/piste/cli/internal/execx"
)

// The home-manager profile of a list of homeConfigurations names.
func TestPickProfile(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profiles []string
		want     string
	}{
		{"exact match", []string{"jane@devbox", "jane@other", "x@devbox"}, "jane@devbox"},
		{"single user profile", []string{"jane@other", "x@devbox"}, "jane@other"},
		{"fallback to user@linux", []string{"x@devbox"}, "jane@linux"},
		{"ambiguous falls back", []string{"jane@a", "jane@b"}, "jane@linux"},
		{"none", nil, "jane@linux"},
	} {
		if got := PickProfile(tc.profiles, "jane", "devbox"); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ConfigurationNames reads an output's names from the flake's outputs, with
// the engine override builds pass: a flake without the output has none (rc
// 0, no error text to read), an eval that fails is an *EvalError with nix's
// stderr.
func TestConfigurationNames(t *testing.T) {
	reply := func(stdout, stderr string, code int) *execx.Recorder {
		return &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {Stdout: []byte(stdout), Stderr: []byte(stderr), ExitCode: code}}}
	}
	ctx := context.Background()
	override := []string{"--override-input", "piste", "git+file:///ck"}
	r := reply(`["box"]`, "", 0)
	if got, err := ConfigurationNames(ctx, r, "/f", "nixosConfigurations", override); err != nil || len(got) != 1 || got[0] != "box" {
		t.Errorf("names: %q %v", got, err)
	}
	want := "nix eval --json /f#. --override-input piste git+file:///ck --apply 'o: builtins.attrNames (o.nixosConfigurations or { })' --no-write-lock-file"
	if got := execx.Quote(r.Calls[0]); got != want {
		t.Errorf("argv %s\nwant %s", got, want)
	}
	if got, err := ConfigurationNames(ctx, reply("[]", "", 0), "/f", "nixosConfigurations", nil); err != nil || len(got) != 0 {
		t.Errorf("none: %q %v", got, err)
	}
	_, err := ConfigurationNames(ctx, reply("", "error: boom\n", 1), "/f", "nixosConfigurations", nil)
	var ee *EvalError
	if !errors.As(err, &ee) || string(ee.Stderr) != "error: boom\n" {
		t.Errorf("failed eval: %v", err)
	}
	if _, err := ConfigurationNames(ctx, reply("not json", "", 0), "/f", "nixosConfigurations", nil); err == nil {
		t.Error("not json: no error")
	}
}

// piste build's stale-nixpkgs panel names the command that moves nixpkgs:
// an engine input, it moves with releases (piste update), and `piste update
// nixpkgs` exits 1.
func TestStaleWarningNamesUpdate(t *testing.T) {
	d := t.TempDir()
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	old := now.Add(-40 * 24 * time.Hour).Unix()
	lock := fmt.Sprintf(`{"nodes": {"root": {"inputs": {"nixpkgs": "nixpkgs"}}, "nixpkgs": {"locked": {"lastModified": %d}}}, "root": "root", "version": 7}`, old)
	if err := os.WriteFile(filepath.Join(d, "flake.lock"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func(n func() time.Time) { Now = n }(Now)
	Now = func() time.Time { return now }
	if got, want := StaleWarning(d), "nixpkgs is 40 days old. Run 'piste update' for the newest release."; got != want {
		t.Errorf("StaleWarning = %q, want %q", got, want)
	}
}
