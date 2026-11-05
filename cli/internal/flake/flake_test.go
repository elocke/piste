package flake

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elocke/piste/cli/internal/execx"
)

// Home-manager profile detection and discovery.
func TestDetectProfile(t *testing.T) {
	names := func(stdout string, code int) *execx.Recorder {
		return &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {Stdout: []byte(stdout), ExitCode: code}}}
	}
	for _, tc := range []struct {
		name string
		r    *execx.Recorder
		want string
	}{
		{"exact match", names(`["jane@devbox", "jane@other", "x@devbox"]`, 0), "jane@devbox"},
		{"single user profile", names(`["jane@other", "x@devbox"]`, 0), "jane@other"},
		{"fallback to user@linux", names(`["x@devbox"]`, 0), "jane@linux"},
		{"ambiguous falls back", names(`["jane@a", "jane@b"]`, 0), "jane@linux"},
		{"eval failure", names("", 1), "jane@linux"},
		{"not json", names("nope", 0), "jane@linux"},
	} {
		if got := DetectProfile(context.Background(), tc.r, "/f", "jane", "devbox"); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestHMProfilesArgv(t *testing.T) {
	r := &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {Stdout: []byte(`["a@b"]`)}}}
	if got := HMProfiles(context.Background(), r, "/f"); len(got) != 1 || got[0] != "a@b" {
		t.Fatal(got)
	}
	want := "nix eval --json /f#homeConfigurations --apply builtins.attrNames --no-write-lock-file"
	if got := execx.Quote(r.Calls[0]); got != want {
		t.Fatalf("argv %q, want %q", got, want)
	}
	if got := HMProfiles(context.Background(), &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {ExitCode: 1}}}, "/f"); got != nil {
		t.Fatal("failure should be empty")
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
