package release

import "testing"

func TestLatest(t *testing.T) {
	if got := Latest([]string{"v0.1.0", "v0.2.0", "v0.10.0", "v1.0.0-rc1", "nightly", "v0.9.9"}); got != "v0.10.0" {
		t.Errorf("Latest = %q, want v0.10.0 (numeric order, pre-releases skipped)", got)
	}
	if got := Latest(nil); got != "" {
		t.Errorf("no tags: %q", got)
	}
	if got := Latest([]string{"v2.0.0-beta", "latest"}); got != "" {
		t.Errorf("no release tag: %q", got)
	}
}

func TestParseVersion(t *testing.T) {
	v, ok := ParseVersion("v1.20.3")
	if !ok || v != (Version{1, 20, 3}) || v.String() != "v1.20.3" {
		t.Errorf("ParseVersion(v1.20.3) = %v, %v", v, ok)
	}
	for _, bad := range []string{"1.2.3", "v1.2", "v1.2.3-rc1", "v1.2.3 ", ""} {
		if _, ok := ParseVersion(bad); ok {
			t.Errorf("ParseVersion(%q) is a release", bad)
		}
	}
	if !(Version{0, 9, 9}).Less(Version{0, 10, 0}) || (Version{1, 0, 0}).Less(Version{0, 99, 99}) || (Version{0, 1, 0}).Less(Version{0, 1, 0}) {
		t.Error("Less isn't numeric by major, minor, patch")
	}
}
