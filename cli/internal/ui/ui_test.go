package ui

import (
	"reflect"
	"testing"
)

// Golden values are rich 15's rendering of the `piste status` value column at
// the default console width of 80 (50 cells for the value).
func TestFit(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"master (clean)", []string{"master (clean)"}},
		{"", []string{""}},
		{"iad20-fj917-1619a46e-4172-4ceb-b0c8-abf05f80eb01-1E15B1840096",
			[]string{"iad20-fj917-1619a46e-4172-4ceb-b0c8-abf05f80eb01-…"}},
		{"a very long commit message that is sixty chars long exactly..",
			[]string{"a very long commit message that is sixty chars", "long exactly.."}},
		{"x /private/var/folders/aa/bbbbbbbbbbbbbbbbbbbbbb/T/nix-build-piste-go-3.0.0-pre.drv-0/x/flake",
			[]string{"x", "/private/var/folders/aa/bbbbbbbbbbbbbbbbbbbbbb/T/…"}},
	} {
		if got := Fit(tc.in, 50); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Fit(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWidthColumns(t *testing.T) {
	t.Setenv("COLUMNS", "132")
	if w := Width(); w != 132 {
		t.Fatalf("Width() = %d, want 132", w)
	}
}
