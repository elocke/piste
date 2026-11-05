package edit

import (
	"strings"
	"testing"
)

const settings = `{
  // user comment, must survive
  "permissions": {
    "allow": [
      "Bash(ls:*)",
      "Bash(git status)"
    ]
  },
  "mcpServers": {
    "foreign": { "command": "x", "args": ["--y"] }
  },
  "hooks": {}
}
`

func mustParse(t *testing.T, s string) *JSON {
	t.Helper()
	d, err := ParseJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSetAddsMemberWithSiblingIndent(t *testing.T) {
	d := mustParse(t, settings)
	if err := d.Set("/mcpServers/piste-demo", []byte(`{"command":"demo","args":["a"]}`)); err != nil {
		t.Fatal(err)
	}
	want := `  "mcpServers": {
    "foreign": { "command": "x", "args": ["--y"] },
    "piste-demo": {
      "command": "demo",
      "args": [
        "a"
      ]
    }
  },`
	if got := string(d.Bytes()); !strings.Contains(got, want) {
		t.Fatalf("got:\n%s", got)
	}
	if !strings.Contains(string(d.Bytes()), "// user comment, must survive") {
		t.Fatal("comment lost")
	}
}

func TestSetThenDeleteIsByteIdentical(t *testing.T) {
	d := mustParse(t, settings)
	d.Set("/mcpServers/piste-demo", []byte(`{"command":"demo"}`))
	d2 := mustParse(t, string(d.Bytes()))
	if err := d2.Delete("/mcpServers/piste-demo"); err != nil {
		t.Fatal(err)
	}
	if got := string(d2.Bytes()); got != settings {
		t.Fatalf("not byte-identical:\n%s", got)
	}
}

func TestAppendAndRemoveByHash(t *testing.T) {
	d := mustParse(t, settings)
	ptr, err := d.Append("/permissions/allow", []byte(`"Bash(piste agents:*)"`))
	if err != nil || ptr != "/permissions/allow/2" {
		t.Fatal(ptr, err)
	}
	want := "      \"Bash(git status)\",\n      \"Bash(piste agents:*)\"\n    ]"
	if !strings.Contains(string(d.Bytes()), want) {
		t.Fatalf("got:\n%s", d.Bytes())
	}
	h, _ := Hash([]byte(`"Bash(piste agents:*)"`))
	i := d.FindElement("/permissions/allow", h)
	if i != 2 {
		t.Fatal(i)
	}
	d.Delete("/permissions/allow/2")
	if string(d.Bytes()) != settings {
		t.Fatalf("got:\n%s", d.Bytes())
	}
}

func TestSetCreatesParents(t *testing.T) {
	d := mustParse(t, settings)
	if _, err := d.Append("/hooks/SessionStart", []byte(`{"hooks":[{"type":"command","command":"echo hi"}]}`)); err != nil {
		t.Fatal(err)
	}
	d.Set("/a/b/c", []byte(`1`))
	got := string(d.Bytes())
	for _, s := range []string{`"SessionStart": [`, `"command": "echo hi"`, `"a": {`, `"c": 1`} {
		if !strings.Contains(got, s) {
			t.Fatalf("missing %s in\n%s", s, got)
		}
	}
	if _, err := Canonical(d.Bytes()); err != nil {
		t.Fatalf("result is not valid JSON(C): %v\n%s", err, got)
	}
}

func TestEmptyDocument(t *testing.T) {
	d := mustParse(t, "")
	d.Set("/mcpServers/x", []byte(`{"url":"http://h"}`))
	c, err := Canonical(d.Bytes())
	if err != nil || string(c) != `{"mcpServers":{"x":{"url":"http://h"}}}` {
		t.Fatalf("%s %v\n%s", c, err, d.Bytes())
	}
}

func TestReplaceKeepsNeighbours(t *testing.T) {
	d := mustParse(t, settings)
	d.Set("/mcpServers/foreign", []byte(`{"command":"z"}`))
	if !strings.Contains(string(d.Bytes()), `"hooks": {}`) {
		t.Fatal(string(d.Bytes()))
	}
}

func TestPointerEscaping(t *testing.T) {
	p := Pointer("a/b", "c~d")
	if p != "/a~1b/c~0d" {
		t.Fatal(p)
	}
	toks, _ := Split(p)
	if toks[0] != "a/b" || toks[1] != "c~d" {
		t.Fatal(toks)
	}
}

func TestHashIgnoresFormatting(t *testing.T) {
	a, _ := Hash([]byte(`{"b":1,"a":[1,2]}`))
	b, _ := Hash([]byte("{\n  // c\n  \"a\": [1, 2],\n  \"b\": 1,\n}"))
	if a != b {
		t.Fatal("reformat changed the hash")
	}
	c, _ := Hash([]byte(`{"b":2,"a":[1,2]}`))
	if a == c {
		t.Fatal("value edit kept the hash")
	}
}

func TestTrailingCommaStyleKept(t *testing.T) {
	src := "{\n  \"a\": 1,\n}\n"
	d := mustParse(t, src)
	d.Set("/b", []byte(`2`))
	if got := string(d.Bytes()); got != "{\n  \"a\": 1,\n  \"b\": 2,\n}\n" {
		t.Fatalf("%q", got)
	}
	d.Delete("/b")
	if got := string(d.Bytes()); got != src {
		t.Fatalf("%q", got)
	}
}
