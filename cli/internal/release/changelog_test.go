package release

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const preamble = "# Changelog\n\nWhat each release changed.\n\n"

const released = "## v0.2.0 (2026-02-01)\n" +
	"- Second.\n" +
	"\n" +
	"### Upgrading\n" +
	"- Rename x.\n" +
	"\n" +
	"## v0.1.0 (2026-01-01)\n" +
	"- First.\n"

func TestSections(t *testing.T) {
	text := preamble +
		"## Unreleased\n" +
		"- Pending change.\n" +
		"### Upgrading\n" +
		"- Pending break.\n" +
		"\n" + released
	got := Sections(text)
	want := []Section{
		{Changes: []string{"- Pending change."}, Upgrading: []string{"- Pending break."}},
		{Version: "v0.2.0", Date: "2026-02-01", Changes: []string{"- Second."}, Upgrading: []string{"- Rename x."}},
		{Version: "v0.1.0", Date: "2026-01-01", Changes: []string{"- First."}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Sections:\n%#v\nwant\n%#v", got, want)
	}
}

func TestBetween(t *testing.T) {
	text := preamble + "## Unreleased\n\n## v0.3.0 (2026-03-01)\n- Third.\n\n" + released
	var got []string
	for _, s := range Between(text, "v0.1.0", "v0.3.0") {
		got = append(got, s.Version)
	}
	if !reflect.DeepEqual(got, []string{"v0.2.0", "v0.3.0"}) {
		t.Errorf("Between(v0.1.0, v0.3.0) = %v, want [v0.2.0 v0.3.0] (oldest first)", got)
	}
	if got := Between(text, "v0.3.0", "v0.3.0"); len(got) != 0 {
		t.Errorf("Between(v0.3.0, v0.3.0) = %v, want none", got)
	}
}

func TestNotes(t *testing.T) {
	text := preamble + "## Unreleased\n\n" + released
	got, ok := Notes(text, "v0.2.0")
	if want := "- Second.\n\n### Upgrading\n- Rename x.\n"; !ok || got != want {
		t.Errorf("Notes(v0.2.0) = %q, %v; want %q", got, ok, want)
	}
	if got, ok := Notes(text, "v0.1.0"); !ok || got != "- First.\n" {
		t.Errorf("Notes(v0.1.0) = %q, %v", got, ok)
	}
	if _, ok := Notes(text, "v9.9.9"); ok {
		t.Error("Notes of an unknown version")
	}
}

func TestCut(t *testing.T) {
	const date = "2026-11-02"
	cases := []struct {
		name, older, unreleased, kind, note, version, section string
	}{
		{"first minor", "", "- A.\n### Upgrading\n", "minor", "", "v0.1.0", "- A.\n"},
		{"first major", "", "- A.\n", "major", "", "v1.0.0", "- A.\n"},
		{"patch with a note", "## v0.1.0 (2026-01-01)\n- First.\n", "- Fix.\n", "patch", "Inputs updated (flake.lock).", "v0.1.1",
			"- Fix.\n- Inputs updated (flake.lock).\n"},
		{"the note alone", "## v0.1.0 (2026-01-01)\n- First.\n", "", "patch", "Inputs updated (flake.lock).", "v0.1.1",
			"- Inputs updated (flake.lock).\n"},
		{"minor over a patch", "## v0.1.1 (2026-01-08)\n- Inputs updated (flake.lock).\n\n## v0.1.0 (2026-01-01)\n- First.\n",
			"- New.\n\n### Upgrading\n- Rename x.\n", "minor", "", "v0.2.0", "- New.\n\n### Upgrading\n- Rename x.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text := preamble + "## Unreleased\n" + c.unreleased + "\n" + c.older
			out, v, err := Cut(text, c.kind, c.note, date)
			if err != nil {
				t.Fatal(err)
			}
			if v != c.version {
				t.Errorf("version %s, want %s", v, c.version)
			}
			head := preamble + "## Unreleased\n\n## " + c.version + " (" + date + ")\n" + c.section
			if !strings.HasPrefix(out, head) {
				t.Errorf("cut:\n%s\nwant it to start:\n%s", out, head)
			}
			if c.older != "" && !strings.HasSuffix(out, "\n\n"+c.older) {
				t.Errorf("the older releases changed:\n%s", out)
			}
			if strings.Count(out, "### Upgrading") != strings.Count(c.section+c.older, "### Upgrading") {
				t.Errorf("an empty ### Upgrading was carried into the release:\n%s", out)
			}
			if Top(out) != c.version {
				t.Errorf("Top after the cut = %q", Top(out))
			}
		})
	}
}

func TestCutRefused(t *testing.T) {
	one := "## v0.1.0 (2026-01-01)\n- First.\n"
	for name, c := range map[string]struct{ text, kind, note string }{
		"a patch with Upgrading notes": {preamble + "## Unreleased\n- A.\n### Upgrading\n- Break.\n\n" + one, "patch", ""},
		"a patch before any release":   {preamble + "## Unreleased\n- A.\n", "patch", "Inputs updated (flake.lock)."},
		"nothing to release":           {preamble + "## Unreleased\n\n" + one, "minor", ""},
		"only an empty Upgrading":      {preamble + "## Unreleased\n### Upgrading\n\n" + one, "minor", ""},
	} {
		if _, _, err := Cut(c.text, c.kind, c.note, "2026-11-02"); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: err %v, want ErrRefused", name, err)
		}
	}
	if _, _, err := Cut(preamble+one, "minor", "x", "2026-11-02"); err == nil || errors.Is(err, ErrRefused) {
		t.Errorf("no ## Unreleased: err %v, want an error that isn't a refusal", err)
	}
	if _, _, err := Cut(preamble+"## Unreleased\n- A.\n", "huge", "", "2026-11-02"); err == nil || errors.Is(err, ErrRefused) {
		t.Errorf("an unknown kind: err %v, want an error that isn't a refusal", err)
	}
}

func TestPending(t *testing.T) {
	text := preamble + "## Unreleased\n\n" + released
	if got := Pending(text, []string{"v0.1.0", "v0.3.0-rc1"}); got != "v0.2.0" {
		t.Errorf("untagged top: %q, want v0.2.0", got)
	}
	if got := Pending(text, []string{"v0.1.0", "v0.2.0"}); got != "" {
		t.Errorf("tagged top: %q", got)
	}
	if got := Pending(text, []string{"v0.1.0", "v0.4.0"}); got != "" {
		t.Errorf("a higher tag: %q", got)
	}
	if got := Pending(preamble+"## Unreleased\n- A.\n", nil); got != "" {
		t.Errorf("no release heading: %q", got)
	}
}
