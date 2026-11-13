package leaks

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

// ip builds an address at run time, so no private address sits in this file
// (the engine's own scan would flag it).
func ip(a, b, c, d byte) string { return netip.AddrFrom4([4]byte{a, b, c, d}).String() }

func prefix(a, b, c, d byte, bits int) string {
	return netip.PrefixFrom(netip.AddrFrom4([4]byte{a, b, c, d}), bits).String()
}

func list(t *testing.T, name, text string) *List {
	t.Helper()
	l, err := ParseList(name, strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestMatcherTerms(t *testing.T) {
	m := NewMatcher("", []*List{
		list(t, "p.txt", "plum orchard\none two three four\n"),
	})
	for line, text := range map[string]string{
		"the Plum-Orchard gate":        "Plum-Orchard",
		"x plum_orchard y":             "plum_orchard",
		"PLUM ORCHARD":                 "PLUM ORCHARD",
		"zero One-two three_FOUR five": "One-two three_FOUR",
	} {
		want := []string{`"` + text + `" (p.txt)`}
		if got := m.Line(line); !reflect.DeepEqual(got, want) {
			t.Errorf("Line(%q) = %q, want %q", line, got, want)
		}
	}
	for _, line := range []string{"plumorchard", "plum, then orchard", "orchard plum"} {
		if got := m.Line(line); len(got) != 0 {
			t.Errorf("Line(%q) = %q, want none", line, got)
		}
	}
}

func TestMatcherRegex(t *testing.T) {
	m := NewMatcher("", []*List{list(t, "r.txt", `re:\bbox[0-9]`+"\n")})
	if got, want := m.Line("on BOX7 now"), []string{`"BOX7" (r.txt)`}; !reflect.DeepEqual(got, want) {
		t.Errorf("Line = %q, want %q", got, want)
	}
	if got := m.Line("inbox7"); len(got) != 0 {
		t.Errorf("Line(inbox7) = %q", got)
	}
}

func TestMatcherPrivate(t *testing.T) {
	m := NewMatcher("", nil)
	for addr, pfx := range map[string]string{
		ip(10, 1, 2, 3):       prefix(10, 0, 0, 0, 8),
		ip(172, 16, 0, 1):     prefix(172, 16, 0, 0, 12),
		ip(172, 31, 255, 255): prefix(172, 16, 0, 0, 12),
		ip(192, 168, 1, 1):    prefix(192, 168, 0, 0, 16),
	} {
		want := []string{`"` + addr + `" a private address (` + pfx + `)`}
		if got := m.Line("host = " + addr + ";"); !reflect.DeepEqual(got, want) {
			t.Errorf("Line(%s) = %q, want %q", addr, got, want)
		}
	}
	for _, line := range []string{ip(192, 0, 2, 1), ip(172, 32, 0, 1), "1" + ip(10, 0, 0, 1), "v1.2.3",
		"10.01.0.1"} { // leading zeros: not an address
		if got := m.Line(line); len(got) != 0 {
			t.Errorf("Line(%q) = %q, want none", line, got)
		}
	}
}

func TestMatcherHome(t *testing.T) {
	m := NewMatcher("/home/user", nil)
	for _, line := range []string{"cd /home/user/src", "/home/user", "at /home/user."} {
		if got, want := m.Line(line), []string{`"/home/user" your home directory`}; !reflect.DeepEqual(got, want) {
			t.Errorf("Line(%q) = %q, want %q", line, got, want)
		}
	}
	if got := m.Line("/home/username"); len(got) != 0 {
		t.Errorf("Line(/home/username) = %q", got)
	}
	mac := NewMatcher("/Users/jane", nil)
	if got := mac.Line("/users/jane/x"); len(got) != 1 {
		t.Errorf("any case: Line = %q", got)
	}
	for _, home := range []string{"/tmp/h", "/home/", "/home", "/homeless-shelter", ""} {
		if got := NewMatcher(home, nil).Line(home + "/x " + home); len(got) != 0 {
			t.Errorf("home %q: Line = %q, want none", home, got)
		}
	}
}

func TestMatcherDedupe(t *testing.T) {
	m := NewMatcher("", []*List{
		list(t, "a.txt", "plum orchard\nre:\\bbox[0-9]\n"),
		list(t, "b.txt", "Plum-Orchard\nre:\\bbox[0-9]\n"),
	})
	want := []string{`"plum orchard" (a.txt)`, `"box1" (a.txt)`}
	if got := m.Line("plum orchard box1"); !reflect.DeepEqual(got, want) {
		t.Errorf("Line = %q, want %q", got, want)
	}
	// The same text twice on a line is one hit.
	if got := m.Line("plum orchard and plum orchard"); len(got) != 1 {
		t.Errorf("Line = %q, want one hit", got)
	}
}
