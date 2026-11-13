package scripttest

import "testing"

// A Mac's test binaries pin arm64: piste refuses x86_64-darwin, so an amd64
// Mac would fail init and setup in every script that takes the defaults.
func TestTestArch(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "arm64", "linux": "amd64"} {
		if got := testArch(goos); got != want {
			t.Errorf("testArch(%q) = %q, want %q", goos, got, want)
		}
	}
}
