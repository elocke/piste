package edit

import (
	"errors"
	"strings"
	"testing"
)

// The piste-agents splice cases.
func TestMDBlock(t *testing.T) {
	B, E := MDMarkers.Begin, MDMarkers.End
	cases := []struct{ in, block, want string }{
		{"", "rule one", B + "\nrule one\n" + E + "\n"},
		{"# mine\nkeep me", "rule one", "# mine\nkeep me\n\n" + B + "\nrule one\n" + E + "\n"},
		{"# mine\n", "r", "# mine\n\n" + B + "\nr\n" + E + "\n"},
		{"top\n" + B + "\nold\n" + E + "\nbottom <!-- a comment -->\n", "new", "top\n" + B + "\nnew\n" + E + "\nbottom <!-- a comment -->\n"},
	}
	for _, c := range cases {
		got, err := MDMarkers.Set(c.in, c.block)
		if err != nil || got != c.want {
			t.Errorf("Set(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
		if b, ok, _ := MDMarkers.Get(got); !ok || b != c.block {
			t.Errorf("Get = %q %v", b, ok)
		}
	}
	for _, bad := range []string{B + "\n" + B + "\n" + E, E + "\n" + B, B + "\nx"} {
		if _, err := MDMarkers.Set(bad, "x"); !errors.Is(err, ErrAmbiguous) {
			t.Errorf("Set(%q): want ambiguous, got %v", bad, err)
		}
	}
	orig := "# mine\nkeep me\n"
	set, _ := MDMarkers.Set(orig, "b")
	back, _ := MDMarkers.Remove(set)
	if back != orig {
		t.Errorf("Remove: %q", back)
	}
}

// A 30-comment config.toml in the shape of the evaluation.
func codexConfig() string {
	var b strings.Builder
	b.WriteString("# Codex config\n# Added by bosun\nmodel = \"gpt-5\"\n\n")
	for i := 0; i < 28; i++ {
		b.WriteString("# comment line " + strings.Repeat("x", i%5) + "\n")
	}
	b.WriteString("[mcp_servers.foreign]\ncommand = \"foreign\" # trailing comment\n\n[projects.\"/home/u/p\"]\ntrust_level = \"trusted\"\n")
	return b.String()
}

// Every line outside the managed block is byte-identical after sync.
func TestTOMLCommentsPreserved(t *testing.T) {
	doc := codexConfig()
	body := "[mcp_servers.piste-demo]\ncommand = \"demo\"\nargs = [\"a\"]\n"
	out, err := SetTOML(doc, "mcp", body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, doc) {
		t.Fatalf("original bytes changed:\n%s", out)
	}
	if got, ok, _ := GetTOML(out, "mcp"); !ok || got != strings.TrimSpace(body) {
		t.Fatalf("block %q", got)
	}
	// Idempotent and removable.
	again, _ := SetTOML(out, "mcp", body)
	if again != out {
		t.Fatal("not idempotent")
	}
	back, err := RemoveTOML(out, "mcp")
	if err != nil || back != doc {
		t.Fatalf("remove: %v\n%q", err, back)
	}
}

// A duplicate key aborts with no change.
func TestTOMLDuplicateAborts(t *testing.T) {
	doc := codexConfig()
	_, err := SetTOML(doc, "mcp", "[mcp_servers.foreign]\ncommand = \"ours\"\n")
	if err == nil {
		t.Fatal("duplicate table accepted")
	}
	if _, err := SetTOML(doc, "mcp", "model = \"x\"\n"); err == nil {
		t.Fatal("bare key block accepted")
	}
	if _, err := SetTOML("a = \n", "mcp", "[x]\n"); err == nil {
		t.Fatal("invalid existing doc accepted")
	}
}
