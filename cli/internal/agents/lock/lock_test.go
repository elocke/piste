package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/agents/edit"
)

// The zone lock shape: [[skill]] next to its
// agents.toml declaration, with commit + narHash; unknown fields ignored.
func TestParseZoneLock(t *testing.T) {
	src := `
[[skill]]
name = "paseo"
source = "github:getpaseo/paseo"
path = "skills/paseo"
ref = "v0.9.2"
commit = "0123456789abcdef0123456789abcdef01234567"
narHash = "sha256-AAAA"
fetched = "2026-09-30"
future_field = "ignored"

[[claude.plugin]]
id = "codex@openai-codex"
version = "1.0.6"
commit = "db52e28"

[[hook]]
name = "hindsight"
installer = "npm:@vectorize-io/hindsight-coding-agents"
version = "0.7.0"

[[mcp]]
name = "playwright"
version = "0.0.83"
integrity = "sha512-x"
`
	l, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	es := l.Entries()
	kinds := map[Kind]int{}
	for _, e := range es {
		kinds[e.Kind]++
	}
	if kinds[KindSkill] != 1 || kinds[KindPlugin] != 1 || kinds[KindInstaller] != 1 || kinds[KindMCP] != 1 {
		t.Fatalf("%v", kinds)
	}
	if l.Skill[0].NarHash != "sha256-AAAA" || es[len(es)-1].Kind == "" {
		t.Fatal(l.Skill[0])
	}
	for _, e := range es {
		if e.Kind == KindPlugin && e.Item.Harness != "claude" {
			t.Fatal("harness not filled")
		}
	}
}

func TestRoundTripAllKinds(t *testing.T) {
	l := &Lock{Version: Version, Zone: "personal"}
	for _, k := range Kinds {
		it := Item{Name: "n-" + string(k), SHA256: "s"}
		if k == KindPlugin || k == KindMarketplace {
			it.Harness = "codex"
		}
		l.Put(k, it)
	}
	l.Own(Owned{File: ".mcp.json", Kind: OwnedJSON, Path: "/mcpServers/x", SHA256: "h"})
	l.Journal = &Journal{State: "applying", Changes: []string{"update .mcp.json"}}
	b, err := l.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	l2, err := Parse(b)
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	seen := map[Kind]bool{}
	for _, e := range l2.Entries() {
		seen[e.Kind] = true
	}
	if len(seen) != len(Kinds) {
		t.Fatalf("kinds after round trip: %v\n%s", seen, b)
	}
	if len(l2.Owned) != 1 || l2.Journal == nil {
		t.Fatalf("%s", b)
	}
	b2, _ := l2.Marshal()
	if string(b) != string(b2) {
		t.Fatalf("not deterministic:\n%s\n---\n%s", b, b2)
	}
	if !l2.Drop(KindPlugin, "codex", "n-plugin") || l2.Drop(KindPlugin, "codex", "n-plugin") {
		t.Fatal("drop")
	}
}

func TestNewerVersionRefused(t *testing.T) {
	if _, err := Parse([]byte("version = 99\n")); err == nil {
		t.Fatal("want error")
	}
}

func TestTreeSumStableAndSensitive(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "s", "scripts"), 0o755)
	os.WriteFile(filepath.Join(d, "s", "SKILL.md"), []byte("---\nname: s\n---\n"), 0o644)
	os.WriteFile(filepath.Join(d, "s", "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	a, _ := TreeSum(filepath.Join(d, "s"))
	b, _ := TreeSum(filepath.Join(d, "s"))
	if a != b {
		t.Fatal("unstable")
	}
	os.Chmod(filepath.Join(d, "s", "scripts", "run.sh"), 0o644)
	if c, _ := TreeSum(filepath.Join(d, "s")); c == a {
		t.Fatal("exec bit ignored")
	}
}

// Every kind of owned entry is re-hashed; a flipped byte is named.
func TestVerifyOwnedDetectsTamper(t *testing.T) {
	root := t.TempDir()
	w := func(rel, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755)
		os.WriteFile(filepath.Join(root, rel), []byte(s), 0o644)
	}
	w(".agents/skills/ours/SKILL.md", "skill body")
	w(".claude/agents/rev.md", "subagent")
	w(".mcp.json", `{"mcpServers":{"piste-a":{"command":"a"},"foreign":{}}}`)
	w(".claude/settings.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"x"}]}]}}`)
	w("AGENTS.md", "top\n<!-- piste-agents:begin -->\nblock\n<!-- piste-agents:end -->\n")
	w(".codex/config.toml", "a = 1\n\n# >>> piste:managed mcp\n[mcp_servers.piste-a]\ncommand = \"a\"\n# <<< piste:managed mcp\n")
	os.Symlink("../../.agents/skills/ours", filepath.Join(root, ".claude/skills-link"))

	l := &Lock{}
	tree, _ := TreeSum(filepath.Join(root, ".agents/skills/ours"))
	sub, _ := FileSum(filepath.Join(root, ".claude/agents/rev.md"))
	mcp, _ := edit.Hash([]byte(`{"command":"a"}`))
	hook, _ := edit.Hash([]byte(`{"hooks":[{"type":"command","command":"x"}]}`))
	l.Own(Owned{File: ".agents/skills/ours", Kind: OwnedDir, SHA256: tree})
	l.Own(Owned{File: ".claude/agents/rev.md", Kind: OwnedFile, SHA256: sub})
	l.Own(Owned{File: ".mcp.json", Kind: OwnedJSON, Path: "/mcpServers/piste-a", SHA256: mcp})
	l.Own(Owned{File: ".claude/settings.json", Kind: OwnedJSONEntry, Path: "/hooks/Stop", SHA256: hook})
	l.Own(Owned{File: "AGENTS.md", Kind: OwnedMDBlock, SHA256: SumString("block")})
	l.Own(Owned{File: ".codex/config.toml", Kind: OwnedTOMLBlock, Path: "mcp", SHA256: SumString("[mcp_servers.piste-a]\ncommand = \"a\"")})
	l.Own(Owned{File: ".claude/skills-link", Kind: OwnedLink, SHA256: LinkSum("../../.agents/skills/ours")})

	for _, f := range l.VerifyOwned(root) {
		if f.State != "ok" {
			t.Fatalf("clean tree: %+v", f)
		}
	}
	w(".agents/skills/ours/SKILL.md", "skill bodY")
	w(".claude/agents/rev.md", "subagenT")
	w(".mcp.json", `{"mcpServers":{"piste-a":{"command":"b"},"foreign":{}}}`)
	w(".claude/settings.json", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"y"}]}]}}`)
	w("AGENTS.md", "top\n<!-- piste-agents:begin -->\nblocK\n<!-- piste-agents:end -->\n")
	var bad []string
	for _, f := range l.VerifyOwned(root) {
		if f.State != "ok" {
			bad = append(bad, f.File)
		}
	}
	want := ".agents/skills/ours,.claude/agents/rev.md,.mcp.json,.claude/settings.json,AGENTS.md"
	if strings.Join(bad, ",") != want {
		t.Fatalf("flagged %v", bad)
	}
}

func TestVerifyItems(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "v/x"), 0o755)
	os.WriteFile(filepath.Join(root, "v/x/SKILL.md"), []byte("a"), 0o644)
	s, _ := ItemSum(root, []string{"v/x"})
	l := &Lock{Skill: []Item{{Name: "x", Files: []string{"v/x"}, SHA256: s}, {Name: "gone", Files: []string{"v/gone"}, SHA256: "z"}}}
	fs := l.VerifyItems(root)
	if fs[0].State != "ok" || fs[1].State != "missing" {
		t.Fatalf("%+v", fs)
	}
	os.WriteFile(filepath.Join(root, "v/x/SKILL.md"), []byte("b"), 0o644)
	if l.VerifyItems(root)[0].State != "edited" {
		t.Fatal("tamper not detected")
	}
}

func TestMemTreeSumMatchesDisk(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "a"), 0o755)
	os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(d, "a", "run"), []byte("y"), 0o755)
	disk, _ := TreeSum(d)
	mem := MemTreeSum(map[string]MemFile{"SKILL.md": {Data: []byte("x")}, "a/run": {Data: []byte("y"), Exec: true}})
	if disk != mem {
		t.Fatal("mem and disk tree sums differ")
	}
}

// The zone lock `piste agents update` writes, read back field for field.
func TestParseUpdateLock(t *testing.T) {
	src := `# agents.lock: machine-written by ` + "`piste agents update`" + `; do not edit by hand.
version = 1
zone = "personal"

[[skill]]
name    = "paseo"
source  = "github:getpaseo/paseo"
path    = "skills/paseo"
ref     = "v0.9.2"
commit  = "1111111111111111111111111111111111111111"
narHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
fetched = "2026-09-30"

[[claude.marketplace]]
name    = "superpowers-extended-cc-marketplace"
source  = "github:pcvelz/superpowers"
commit  = "abc"

[[claude.plugin]]
id           = "codex@openai-codex"
version      = "1.0.6"
gitCommitSha = "db52e28"

[[codex.plugin]]
id      = "github@openai-curated"
version = "0.1.0"

[[mcp]]
name      = "playwright"
package   = "npm:@playwright/mcp"
version   = "0.0.83"
integrity = "sha512-x"

[[hook]]
name      = "hindsight"
installer = "npm:@vectorize-io/hindsight-coding-agents"
version   = "0.7.0"

[[tool]]
name    = "githits"
source  = "npm:githits"
version = "0.6.6"
`
	l, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[Kind]int{}
	for _, e := range l.Entries() {
		kinds[e.Kind]++
	}
	want := map[Kind]int{KindSkill: 1, KindMarketplace: 1, KindPlugin: 2, KindMCP: 1, KindInstaller: 1, KindTool: 1}
	for k, n := range want {
		if kinds[k] != n {
			t.Fatalf("%s: %d (%v)", k, kinds[k], kinds)
		}
	}
	if l.Zone != "personal" || l.Claude.Plugin[0].GitCommit != "db52e28" || l.MCP[0].Package != "npm:@playwright/mcp" || l.Skill[0].Ref != "v0.9.2" {
		t.Fatalf("%+v", l)
	}
}
