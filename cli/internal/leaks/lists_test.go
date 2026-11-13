package leaks

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestWords(t *testing.T) {
	if got := Words("Foo-Bar_baz 42x"); !reflect.DeepEqual(got, []string{"foo", "bar", "baz", "42x"}) {
		t.Errorf("Words = %q", got)
	}
	if got := Words("--"); len(got) != 0 {
		t.Errorf("Words(--) = %q, want none", got)
	}
}

func TestParseList(t *testing.T) {
	l, err := ParseList("list.txt", strings.NewReader(
		"plum orchard\n\nHidden-Tern\n  \nre:\\bbox[0-9]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if l.Name != "list.txt" {
		t.Errorf("Name = %q", l.Name)
	}
	for _, h := range []string{sum("plum orchard"), sum("hidden tern")} {
		if _, ok := l.terms[h]; !ok {
			t.Errorf("terms lack %s", h)
		}
	}
	if len(l.terms) != 2 || len(l.res) != 1 {
		t.Errorf("terms %d, res %d; want 2, 1", len(l.terms), len(l.res))
	}
	if !l.res[0].MatchString("BOX7") {
		t.Error("re: is not case-insensitive")
	}

	for _, c := range []struct{ in, line string }{
		{"ok\none two three four five\n", "list.txt:2:"},
		{"ok\n\nre:(\n", "list.txt:3:"},
		{"re:\n", "list.txt:1:"},
		{"ok\n-- --\n", "list.txt:2:"},
	} {
		_, err := ParseList("list.txt", strings.NewReader(c.in))
		if err == nil || !strings.Contains(err.Error(), c.line) {
			t.Errorf("ParseList(%q) err = %v, want %s", c.in, err, c.line)
		}
	}
	// The error names the line, never the term on it.
	_, err = ParseList("list.txt", strings.NewReader("one two three four secretword\n"))
	if err == nil || strings.Contains(err.Error(), "secretword") {
		t.Errorf("err echoes the term: %v", err)
	}
}

func TestParseListMaxWords(t *testing.T) {
	l, err := ParseList("l", strings.NewReader("a b\nc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if l.maxWords != 2 {
		t.Errorf("maxWords = %d, want 2", l.maxWords)
	}
}

func TestParseListComments(t *testing.T) {
	l, err := ParseList("l", strings.NewReader("# x\n  # x\nplum orchard   # why\nre:a#b\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.terms) != 1 {
		t.Fatalf("terms = %d, want 1", len(l.terms))
	}
	if _, ok := l.terms[sum("plum orchard")]; !ok {
		t.Error("the trailing comment became part of the term")
	}
	if len(l.res) != 1 || !l.res[0].MatchString("A#B") {
		t.Errorf("re:a#b not kept whole: %v", l.res)
	}
}
