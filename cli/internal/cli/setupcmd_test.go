package cli

import "testing"

// withFlakes adds to a NIX_CONFIG the person may have set, and stands alone
// when there is none.
func TestWithFlakes(t *testing.T) {
	const on = "extra-experimental-features = nix-command flakes"
	for _, c := range []struct{ in, want string }{
		{"", on},
		{" \n", on},
		{"max-jobs = 4", "max-jobs = 4\n" + on},
		{"experimental-features = nix-command\nmax-jobs = 4", "experimental-features = nix-command\nmax-jobs = 4\n" + on},
	} {
		if got := withFlakes(c.in); got != c.want {
			t.Errorf("withFlakes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
