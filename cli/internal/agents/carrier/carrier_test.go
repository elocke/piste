package carrier

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/agents/adapter"
	"github.com/elocke/piste/cli/internal/agents/canon"
)

func build(t *testing.T) map[string]adapter.TreeFile {
	t.Helper()
	d := filepath.Join(t.TempDir(), "hello")
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\nname: hello\ndescription: Use when greeting.\n---\nSay hello.\n"), 0o644)
	s, err := canon.LoadSkill(d)
	if err != nil {
		t.Fatal(err)
	}
	var rs []adapter.Result
	add := func(r adapter.Result, err error) {
		if err != nil {
			t.Fatal(err)
		}
		rs = append(rs, r)
	}
	add(adapter.Skill(canon.ClaudePlugin, canon.Bundle, s, adapter.Copy, ""))
	add(adapter.Subagent(canon.ClaudePlugin, canon.Subagent{Name: "rev", Frontmatter: map[string]any{"name": "rev", "description": "Reviews."}, Order: []string{"name", "description"}, Body: "Review.\n"}))
	add(adapter.Hooks(canon.ClaudePlugin, canon.Bundle, []canon.Hook{{Name: "hi", Event: "sessionStart", Script: "hi.sh"}},
		func(h canon.Hook) (string, error) { return "./hooks/bin/" + h.Script, nil }))
	add(adapter.MCPServers(canon.ClaudePlugin, canon.Bundle, []canon.MCP{{Name: "echo", Command: "echo-mcp"}}))
	tree, err := Build(Meta{Plugin: "piste-harness", Marketplace: "piste-local", Version: "0.1.0"}, rs,
		map[string]adapter.TreeFile{"hooks/bin/hi.sh": {Data: []byte("#!/bin/sh\necho '{}'\n"), Mode: 0o755}})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// carrier layout: the rulesync plugin directory plus
// plugin.json and a one-plugin marketplace.json.
func TestLayout(t *testing.T) {
	tree := build(t)
	want := []string{
		".claude-plugin/marketplace.json",
		"piste-harness/.claude-plugin/plugin.json",
		"piste-harness/.mcp.json",
		"piste-harness/agents/rev.md",
		"piste-harness/hooks/bin/hi.sh",
		"piste-harness/hooks/hooks.json",
		"piste-harness/skills/hello/SKILL.md",
	}
	if got := strings.Join(Files(tree), "\n"); got != strings.Join(want, "\n") {
		t.Fatalf("layout:\n%s", got)
	}
	if !strings.Contains(string(tree["piste-harness/hooks/hooks.json"].Data), `\"$CLAUDE_PLUGIN_ROOT\"/hooks/bin/hi.sh`) {
		t.Fatal(string(tree["piste-harness/hooks/hooks.json"].Data))
	}
	if !strings.Contains(string(tree[".claude-plugin/marketplace.json"].Data), `"source": "./piste-harness"`) {
		t.Fatal("marketplace source")
	}
}

func TestRejects(t *testing.T) {
	if _, err := Build(Meta{Plugin: "Bad Name", Marketplace: "m", Version: "1"}, nil, nil); err == nil {
		t.Fatal("bad name")
	}
	r := adapter.Result{Artifacts: []adapter.Artifact{{Harness: canon.Claude, File: "x"}}}
	if _, err := Build(Meta{Plugin: "p", Marketplace: "m", Version: "1"}, []adapter.Result{r}, nil); err == nil {
		t.Fatal("non-plugin artifact accepted")
	}
	r = adapter.Result{Artifacts: []adapter.Artifact{{Harness: canon.ClaudePlugin, File: "settings.json", JSONPointer: "/x"}}}
	if _, err := Build(Meta{Plugin: "p", Marketplace: "m", Version: "1"}, []adapter.Result{r}, nil); err == nil {
		t.Fatal("key edit accepted in carrier")
	}
	if _, err := Build(Meta{Plugin: "p", Marketplace: "m", Version: "1"}, nil, map[string]adapter.TreeFile{"../x": {}}); err == nil {
		t.Fatal("escape accepted")
	}
}

// carrier_layout_validate: `claude plugin validate`
// accepts the marketplace and the plugin. PISTE_TINT=1 with claude on PATH.
func TestClaudeValidateInt(t *testing.T) {
	if os.Getenv("PISTE_TINT") != "1" {
		t.Skip("integration test: set PISTE_TINT=1 with claude on PATH")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not on PATH")
	}
	dir := t.TempDir()
	for rel, f := range build(t) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		os.WriteFile(p, f.Data, mode)
	}
	home := t.TempDir() // no login, no user config
	for _, target := range []string{dir, filepath.Join(dir, "piste-harness")} {
		cmd := exec.Command(claude, "plugin", "validate", "--strict", target)
		cmd.Env = append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR="+filepath.Join(home, ".claude"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("claude plugin validate %s: %v\n%s", target, err, out)
		}
	}
}
