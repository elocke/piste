package project

import (
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/agents/canon"
)

const manifest = `# my project
zone = "work"
harnesses = ["claude", "codex"]

[[use]]
id = "shared:skill/alpha" # keep this comment

[[use]]
id = "work:mcp/jira"
`

func TestAddRemoveUse(t *testing.T) {
	out, err := AddUse(manifest, "shared:agent/rev")
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(out)
	if err != nil || len(m.Use) != 3 || !m.Uses("shared:agent/rev") {
		t.Fatal(m, err)
	}
	back, err := RemoveUse(out, "shared:agent/rev")
	if err != nil || back != manifest {
		t.Fatalf("%v\n%q", err, back)
	}
	mid, err := RemoveUse(manifest, "shared:skill/alpha")
	if err != nil || strings.Contains(mid, "alpha") || !strings.Contains(mid, "work:mcp/jira") || !strings.HasPrefix(mid, "# my project\n") {
		t.Fatalf("%v\n%s", err, mid)
	}
	if _, err := RemoveUse(manifest, "shared:skill/none"); err == nil {
		t.Fatal("removing an unused id")
	}
	if _, err := AddUse(manifest, "shared:skill/alpha"); err == nil {
		t.Fatal("duplicate use accepted")
	}
}

func TestValidate(t *testing.T) {
	bad := []string{
		"zone = \"Home\"\n",
		"zone = \"shared\"\n",
		"zone = \"a:b\"\n",
		"zone = \"work\"\nharnesses = [\"gemini\"]\n",
		"zone = \"work\"\nharnesses = [\"codex\"]\n[policy]\nharnesses = [\"claude\"]\n",
		"zone = \"work\"\n[[use]]\nid = \"bad\"\n",
		"zone = \"work\"\nunknown = 1\n",
		"zone = \"work\"\n[[mcp]]\nname = \"x\"\nurl = \"https://h\"\nheaders = { Authorization = \"Bearer abcdefghijklmnopqrs\" }\n",
	}
	for _, s := range bad {
		if _, err := Parse(s); err == nil {
			t.Errorf("accepted:\n%s", s)
		}
	}
	m, err := Parse("zone = \"personal\"\nharnesses = [\"claude\", \"codex\"]\n[policy]\nharnesses = [\"codex\"]\n")
	if err != nil || strings.Join(m.Effective(), ",") != "codex" {
		t.Fatal(m, err)
	}
}

func TestInit(t *testing.T) {
	out, err := Init("work", []string{"claude"}, []canon.MCP{{Name: "t", URL: "https://h", BearerEnv: "T_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Parse(out)
	if err != nil || m.MCP[0].BearerEnv != "T_TOKEN" {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := Init("work", nil, []canon.MCP{{Name: "t", URL: "https://h", Headers: map[string]string{"Authorization": "Bearer abcdefghijklmnopq"}}}); err == nil {
		t.Fatal("literal secret written")
	}
}

func TestIsLocalCoversFilePointers(t *testing.T) {
	m := &Manifest{Local: []Local{{Path: ".claude/settings.json"}, {Path: "dir"}}}
	for _, id := range []string{".claude/settings.json", ".claude/settings.json#/permissions/deny/0", "dir/x"} {
		if !m.IsLocal(id) {
			t.Errorf("%s not local", id)
		}
	}
	for _, id := range []string{".claude/settings.jsonx", "dirx", ".claude/settings.json2#/a"} {
		if m.IsLocal(id) {
			t.Errorf("%s local", id)
		}
	}
}
