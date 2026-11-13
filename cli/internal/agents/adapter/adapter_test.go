// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/features/permissions/codexcli-permissions.test.ts,
// src/features/hooks/claudecode-plugin-hooks.test.ts, src/features/subagents/codexcli-subagent.test.ts,
// src/features/mcp/codexcli-mcp.test.ts (cases transcribed as golden expectations).
// Copyright (c) 2024 dyoshikawa. MIT License; see cli/THIRD_PARTY_NOTICES.md.

package adapter

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/edit"
)

func one(t *testing.T) func(Result, error) Artifact {
	return func(r Result, err error) Artifact {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Artifacts) != 1 {
			t.Fatalf("want 1 artifact, got %d: %+v", len(r.Artifacts), r.Artifacts)
		}
		return r.Artifacts[0]
	}
}

// --- instructions ---------------------------------------------------------------

func TestInstructionsUserScope(t *testing.T) {
	frags := []canon.Instruction{{Name: "shared/instructions/never.md", Text: "## Never\n\nNever do the bad thing.\n"}}
	a := one(t)(Instructions(canon.Claude, canon.User, frags))
	if a.File != ".claude/CLAUDE.md" || *a.MDBlock != "<!-- source: shared/instructions/never.md -->\n## Never\n\nNever do the bad thing." {
		t.Fatalf("%+v %q", a, *a.MDBlock)
	}
	a = one(t)(Instructions(canon.Codex, canon.User, frags))
	if a.File != ".codex/AGENTS.md" {
		t.Fatal(a.File)
	}
	a = one(t)(Instructions(canon.Claude, canon.Project, frags))
	if a.File != "CLAUDE.md" || *a.MDBlock != "@AGENTS.md" {
		t.Fatal(a.File, *a.MDBlock)
	}
	r, err := Instructions(canon.Hermes, canon.User, frags)
	if err != nil || len(r.Artifacts) != 0 || len(r.Warnings) != 1 {
		t.Fatal(r, err)
	}
}

// codex_32k_agents_fold: content over 32 KiB is split into nested
// AGENTS.md; a chain still over budget is an error.
func TestCodex32KFold(t *testing.T) {
	big := strings.Repeat("x", 12*1024)
	frags := []canon.Instruction{
		{Name: "root", Text: big},
		{Name: "web", Text: big, Subproject: "web"},
		{Name: "api", Text: big, Subproject: "services/api"},
	}
	r, err := Instructions(canon.AgentsMD, canon.Project, frags)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, a := range r.Artifacts {
		files = append(files, a.File)
	}
	sort.Strings(files)
	if !reflect.DeepEqual(files, []string{"AGENTS.md", "services/api/AGENTS.md", "web/AGENTS.md"}) {
		t.Fatal(files)
	}
	// All three on the root chain: over budget.
	frags[1].Subproject, frags[2].Subproject = "", ""
	if _, err := Instructions(canon.AgentsMD, canon.Project, frags); err == nil || !strings.Contains(err.Error(), "project_doc_max_bytes") {
		t.Fatalf("want budget error, got %v", err)
	}
	// Nested chain root + web/a over budget too.
	frags = []canon.Instruction{{Name: "r", Text: big}, {Name: "w", Text: big, Subproject: "web"}, {Name: "wa", Text: big, Subproject: "web/a"}}
	if _, err := Instructions(canon.AgentsMD, canon.Project, frags); err == nil {
		t.Fatal("chain budget not enforced")
	}
	if _, err := Instructions(canon.AgentsMD, canon.Project, []canon.Instruction{{Name: "x", Text: "y", Subproject: "../out"}}); err == nil {
		t.Fatal("escaping subproject accepted")
	}
}

// --- skills ------------------------------------------------------------------------

func skillFixture(t *testing.T, fm string) canon.Skill {
	t.Helper()
	d := filepath.Join(t.TempDir(), "web", "alpha")
	os.MkdirAll(filepath.Join(d, "scripts"), 0o755)
	os.WriteFile(filepath.Join(d, "SKILL.md"), []byte("---\n"+fm+"---\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(d, "scripts", "run.sh"), []byte("#!/bin/sh\n"), 0o755)
	s, err := canon.LoadSkill(d)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSkillPathsAndModes(t *testing.T) {
	s := skillFixture(t, "name: alpha\ndescription: Use when alpha.\nallowed-tools: Bash\n")
	a := one(t)(Skill(canon.Claude, canon.User, s, Link, "/src/alpha"))
	if a.File != ".claude/skills/alpha" || a.Link != "/src/alpha" {
		t.Fatal(a)
	}
	a = one(t)(Skill(canon.Codex, canon.User, s, Link, "/src/alpha"))
	if a.File != ".agents/skills/alpha" {
		t.Fatal(a.File)
	}
	// A codex copy drops Claude-only keys, with a warning; the script keeps +x.
	r, err := Skill(canon.AgentsMD, canon.Project, s, Copy, "")
	a = one(t)(r, err)
	md := string(a.Tree["SKILL.md"].Data)
	if strings.Contains(md, "allowed-tools") || !strings.Contains(md, "description: Use when alpha.") || !strings.HasSuffix(md, "Body.\n") {
		t.Fatalf("%q", md)
	}
	if len(r.Warnings) != 1 || a.Tree["scripts/run.sh"].Mode&0o111 == 0 {
		t.Fatal(r.Warnings, a.Tree["scripts/run.sh"].Mode)
	}
	// A claude copy is verbatim.
	a = one(t)(Skill(canon.Claude, canon.Project, s, Copy, ""))
	if !strings.Contains(string(a.Tree["SKILL.md"].Data), "allowed-tools: Bash") {
		t.Fatal("claude copy changed")
	}
	// Hermes: bundle only, category nesting.
	a = one(t)(Skill(canon.Hermes, canon.Bundle, s, Copy, ""))
	if a.File != "skills/web/alpha" {
		t.Fatal(a.File)
	}
	if _, err := Skill(canon.Hermes, canon.User, s, Copy, ""); err == nil {
		t.Fatal("hermes user scope accepted")
	}
}

// --- subagents ----------------------------------------------------------------------

func TestCodexSubagent(t *testing.T) {
	a := canon.Subagent{Name: "reviewer", Frontmatter: map[string]any{
		"name": "reviewer", "description": "Reviews diffs.", "tools": "Read",
		"codex": map[string]any{"model": "gpt-5.5", "model_reasoning_effort": "high"},
	}, Body: "You review.\nBe terse.\n"}
	art := one(t)(Subagent(canon.Codex, a))
	want := "name = 'reviewer'\ndescription = 'Reviews diffs.'\nmodel = 'gpt-5.5'\nmodel_reasoning_effort = 'high'\n" +
		"developer_instructions = '''\nYou review.\nBe terse.\n'''\n"
	if art.File != ".codex/agents/reviewer.toml" || string(art.Whole) != want {
		t.Fatalf("%s\n%s", art.File, art.Whole)
	}
	// A body containing ''' falls back to a basic string.
	a.Body = "has ''' inside\nsecond"
	art = one(t)(Subagent(canon.Codex, a))
	var v map[string]any
	if err := toml.Unmarshal(art.Whole, &v); err != nil || v["developer_instructions"] != "has ''' inside\nsecond" {
		t.Fatalf("%v %v", err, v)
	}
}

func TestPluginSubagentDropsForbidden(t *testing.T) {
	a := canon.Subagent{Name: "x", Frontmatter: map[string]any{
		"name": "x", "description": "d", "hooks": map[string]any{}, "mcpServers": []any{}, "permissionMode": "plan", "isolation": "none",
	}, Order: []string{"name", "description"}, Body: "b\n"}
	r, err := Subagent(canon.ClaudePlugin, a)
	art := one(t)(r, err)
	s := string(art.Whole)
	for _, k := range []string{"hooks", "mcpServers", "permissionMode", "isolation"} {
		if strings.Contains(s, k) {
			t.Fatalf("%s kept:\n%s", k, s)
		}
	}
	if art.File != "agents/x.md" || len(r.Warnings) != 1 {
		t.Fatal(art.File, r.Warnings)
	}
}

// --- hooks ----------------------------------------------------------------------------

var p5Hooks = []canon.Hook{
	{Name: "rtk-rewrite", Event: "preToolUse", Matcher: "Bash", Script: "rtk-rewrite.sh", Timeout: 10, Targets: []string{"claude", "codex"}},
	{Name: "guard-read", Event: "preToolUse", Matcher: "Read|Grep", Script: "piste-guard.py", Timeout: 10, Targets: []string{"claude"}},
	{Name: "note", Event: "userPromptSubmit", Matcher: "ignored", Command: "./hooks/note.sh --x", Targets: []string{"claude"}},
}

func installed(h canon.Hook) (string, error) {
	if h.Script != "" {
		return "~/.local/share/piste-agents/hooks/bin/" + strings.TrimSuffix(h.Script, filepath.Ext(h.Script)), nil
	}
	return h.Command, nil
}

func TestHooksClaudeCodex(t *testing.T) {
	r, err := Hooks(canon.Claude, canon.User, p5Hooks, installed)
	if err != nil || len(r.Artifacts) != 3 {
		t.Fatal(r, err)
	}
	a := r.Artifacts[0]
	if a.File != ".claude/settings.json" || a.JSONArray != "/hooks/PreToolUse" ||
		string(a.JSONEntry) != `{"hooks":[{"command":"~/.local/share/piste-agents/hooks/bin/rtk-rewrite","timeout":10,"type":"command"}],"matcher":"Bash"}` {
		t.Fatalf("%+v %s", a, a.JSONEntry)
	}
	// UserPromptSubmit takes no matcher.
	if r.Artifacts[2].JSONArray != "/hooks/UserPromptSubmit" || strings.Contains(string(r.Artifacts[2].JSONEntry), "matcher") {
		t.Fatalf("%s", r.Artifacts[2].JSONEntry)
	}
	r, _ = Hooks(canon.Codex, canon.User, p5Hooks, installed)
	if len(r.Artifacts) != 1 || r.Artifacts[0].File != ".codex/hooks.json" {
		t.Fatal(r.Artifacts)
	}
	if _, err := Hooks(canon.Codex, canon.User, []canon.Hook{{Name: "n", Event: "notification", Command: "x"}}, installed); err == nil {
		t.Fatal("unsupported codex event accepted")
	}
	if _, err := Hooks(canon.Claude, canon.User, []canon.Hook{{Name: "n", Event: "stop", Command: "a\nb"}}, installed); err == nil {
		t.Fatal("newline in command accepted")
	}
}

// Plugin hooks resolve ./-relative commands against the plugin root
// (claudecode-plugin-hooks.ts: "$CLAUDE_PLUGIN_ROOT"/scripts/x.sh).
func TestHooksCarrierPluginRoot(t *testing.T) {
	a := one(t)(Hooks(canon.ClaudePlugin, canon.Bundle, p5Hooks, func(h canon.Hook) (string, error) {
		if h.Script != "" {
			return "./hooks/bin/" + h.Script, nil
		}
		return h.Command, nil
	}))
	s := string(a.Whole)
	for _, want := range []string{`"command": "\"$CLAUDE_PLUGIN_ROOT\"/hooks/bin/rtk-rewrite.sh"`, `"\"$CLAUDE_PLUGIN_ROOT\"/hooks/note.sh --x"`, `"UserPromptSubmit"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in\n%s", want, s)
		}
	}
	if applyProjectDir("npx prettier", "$V") != "npx prettier" || applyProjectDir("/abs/x", "$V") != "/abs/x" ||
		applyProjectDir(`"./my hook.sh"`, "$V") != `"$V"/"my hook.sh"` {
		t.Fatal("prefix rules")
	}
}

// --- MCP ------------------------------------------------------------------------------

var servers = []canon.MCP{
	{Name: "gh", Command: "github-mcp", Args: []string{"stdio"}, Env: map[string]string{"LOG": "info"}, SecretEnv: []string{"GITHUB_TOKEN"}},
	{Name: "data-feed", URL: "https://mcp.example/data-feed", BearerEnv: "DATA_FEED_TOKEN", Headers: map[string]string{"X-Profile": "jane", "X-Key": "${DATA_FEED_KEY}"}},
}

// stdio_secret_three_harnesses: secretEnv renders correctly
// for Claude, Codex and Hermes, and never as a value.
func TestStdioSecretThreeHarnesses(t *testing.T) {
	r, err := MCPServers(canon.Claude, canon.Project, servers)
	if err != nil {
		t.Fatal(err)
	}
	// Sorted by name: data-feed, then gh.
	var gh map[string]any
	json.Unmarshal(r.Artifacts[1].JSONValue, &gh)
	if gh["env"].(map[string]any)["GITHUB_TOKEN"] != "${GITHUB_TOKEN}" || r.Artifacts[1].JSONPointer != "/mcpServers/gh" {
		t.Fatalf("claude: %s", r.Artifacts[1].JSONValue)
	}
	var df map[string]any
	json.Unmarshal(r.Artifacts[0].JSONValue, &df)
	if df["headers"].(map[string]any)["Authorization"] != "Bearer ${DATA_FEED_TOKEN}" || df["type"] != "http" {
		t.Fatalf("claude remote: %s", r.Artifacts[0].JSONValue)
	}

	a := one(t)(MCPServers(canon.Codex, canon.User, servers))
	var doc map[string]map[string]map[string]any
	if err := toml.Unmarshal([]byte(a.TOMLBody), &doc); err != nil {
		t.Fatalf("%v\n%s", err, a.TOMLBody)
	}
	g := doc["mcp_servers"]["gh"]
	if !reflect.DeepEqual(g["env_vars"], []any{"GITHUB_TOKEN"}) || g["env"].(map[string]any)["LOG"] != "info" {
		t.Fatalf("codex stdio: %v", g)
	}
	if _, leaked := g["env"].(map[string]any)["GITHUB_TOKEN"]; leaked {
		t.Fatal("codex env carries the secret name as a value")
	}
	c := doc["mcp_servers"]["data-feed"]
	if c["bearer_token_env_var"] != "DATA_FEED_TOKEN" || c["env_http_headers"].(map[string]any)["X-Key"] != "DATA_FEED_KEY" ||
		c["http_headers"].(map[string]any)["X-Profile"] != "jane" {
		t.Fatalf("codex remote: %v", c)
	}

	h := one(t)(MCPServers(canon.Hermes, canon.Bundle, servers))
	if !strings.Contains(string(h.Whole), "GITHUB_TOKEN: ${GITHUB_TOKEN}") {
		t.Fatalf("hermes:\n%s", h.Whole)
	}
	if strings.Contains(a.TOMLBody+string(h.Whole), "ghp_") {
		t.Fatal("value leaked")
	}
}

func TestMCPRefusesLiteralSecrets(t *testing.T) {
	bad := []canon.MCP{
		{Name: "a", URL: "https://h", Headers: map[string]string{"Authorization": "Bearer abcdefghijklmnopqrstu"}},
		{Name: "b", Command: "x", Env: map[string]string{"API_KEY": "hunter2hunter2"}},
		{Name: "c", Command: "x", URL: "https://h"},
		{Name: "d", Command: "x", SecretEnv: []string{"not a var"}},
	}
	for _, m := range bad {
		if _, err := MCPServers(canon.Claude, canon.Project, []canon.MCP{m}); err == nil {
			t.Errorf("%s accepted", m.Name)
		}
	}
	if _, err := MCPServers(canon.Claude, canon.User, servers); err == nil {
		t.Fatal("claude user-scope file render accepted")
	}
}

// The codex block splices into a config that already has its own
// [mcp_servers] and [mcp_servers.foreign] tables.
func TestCodexBlockCoexists(t *testing.T) {
	a := one(t)(MCPServers(canon.Codex, canon.User, servers))
	doc := "[mcp_servers]\n\n[mcp_servers.foreign]\ncommand = \"f\"\n"
	if _, err := edit.SetTOML(doc, a.TOMLBlock, a.TOMLBody); err != nil {
		t.Fatalf("%v\n%s", err, a.TOMLBody)
	}
	if _, err := edit.SetTOML("[mcp_servers.gh]\ncommand = \"mine\"\n", a.TOMLBlock, a.TOMLBody); err == nil {
		t.Fatal("hand-written duplicate not caught")
	}
}

// no_injected_keys: only declared keys appear.
func TestNoInjectedKeys(t *testing.T) {
	a := one(t)(MCPServers(canon.Codex, canon.User, []canon.MCP{{Name: "p", Command: "playwright-mcp"}}))
	if strings.TrimSpace(a.TOMLBody) != "[mcp_servers.p]\ncommand = 'playwright-mcp'" {
		t.Fatalf("%q", a.TOMLBody)
	}
	for _, k := range []string{"approval_policy", "sandbox_mode", "default_permissions", "enabled", "trust"} {
		if strings.Contains(a.TOMLBody, k) {
			t.Fatal(k)
		}
	}
	c := one(t)(MCPServers(canon.Claude, canon.Project, []canon.MCP{{Name: "p", Command: "playwright-mcp"}}))
	if string(c.JSONValue) != `{"command":"playwright-mcp"}` {
		t.Fatalf("%s", c.JSONValue)
	}
}

// --- permissions ------------------------------------------------------------------

func TestParseCommandPattern(t *testing.T) {
	cases := []struct {
		in     string
		toks   string
		prefix bool
	}{
		{"rm -rf *", "rm -rf", true},
		{"Bash(rm -rf:*)", "rm -rf", true},
		{"git status", "git status", false},
		{"Bash(git push:*)", "git push", true},
		{"  npm  run   test  ", "npm run test", false},
	}
	for _, c := range cases {
		cp, err := ParseCommandPattern(c.in)
		if err != nil || strings.Join(cp.Tokens, " ") != c.toks || cp.Prefix != c.prefix {
			t.Errorf("%q → %+v %v", c.in, cp, err)
		}
	}
	for _, bad := range []string{"rm * -rf", "*", "Bash(:*)", "git pu?h", "ls [ab]"} {
		if _, err := ParseCommandPattern(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestCodexRulesGolden(t *testing.T) {
	b, err := CodexRules([]canon.Permission{
		{Pattern: "rm -rf *", Decision: canon.Deny},
		{Pattern: "git status", Decision: canon.Allow},
		{Pattern: "git push *", Decision: canon.Ask},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"prefix_rule(\n    pattern = [\"rm\", \"-rf\"],\n    decision = \"forbidden\",",
		"pattern = [\"git\", \"status\"],\n    decision = \"allow\",",
		"pattern = [\"git\", \"push\"],\n    decision = \"prompt\",",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, `"*"`) {
		t.Fatal("literal * token emitted (the rulesync fail-open)")
	}
}

// codex_deny_midwildcard_errors: render error, nothing produced.
func TestCodexMidWildcardErrors(t *testing.T) {
	r, err := Permissions(canon.Codex, []canon.Permission{{Pattern: "git status", Decision: canon.Allow}, {Pattern: "rm * -rf", Decision: canon.Deny}})
	if err == nil || len(r.Artifacts) != 0 {
		t.Fatalf("%v %v", r.Artifacts, err)
	}
	if _, err := Permissions(canon.Hermes, []canon.Permission{{Pattern: "rm -rf *", Decision: canon.Deny}}); err == nil {
		t.Fatal("inexpressible deny accepted (must fail closed)")
	}
}

func TestClaudePermissions(t *testing.T) {
	r, err := Permissions(canon.Claude, []canon.Permission{{Pattern: "rm -rf *", Decision: canon.Deny}, {Pattern: "Bash(git status)", Decision: canon.Allow}})
	if err != nil || len(r.Artifacts) != 2 {
		t.Fatal(err)
	}
	if r.Artifacts[0].JSONArray != "/permissions/deny" || string(r.Artifacts[0].JSONEntry) != `"Bash(rm -rf:*)"` ||
		r.Artifacts[1].JSONArray != "/permissions/allow" || string(r.Artifacts[1].JSONEntry) != `"Bash(git status)"` {
		t.Fatalf("%+v", r.Artifacts)
	}
}

// codex_deny_prefix: the real `codex execpolicy
// check`. Runs when PISTE_TINT=1 and codex is on PATH.
func TestCodexExecpolicyInt(t *testing.T) {
	if os.Getenv("PISTE_TINT") != "1" {
		t.Skip("integration test: set PISTE_TINT=1 with codex on PATH")
	}
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not on PATH")
	}
	b, err := CodexRules([]canon.Permission{
		{Pattern: "rm -rf *", Decision: canon.Deny},
		{Pattern: "git status", Decision: canon.Allow},
		{Pattern: "git push *", Decision: canon.Ask},
	})
	if err != nil {
		t.Fatal(err)
	}
	rules := filepath.Join(t.TempDir(), "piste.rules")
	os.WriteFile(rules, b, 0o644)
	check := func(want string, argv ...string) {
		t.Helper()
		out, err := exec.Command(codex, append([]string{"execpolicy", "check", "--rules", rules}, argv...)...).CombinedOutput()
		if err != nil && !strings.Contains(string(out), want) {
			t.Fatalf("codex execpolicy check %v: %v\n%s", argv, err, out)
		}
		if !strings.Contains(string(out), want) {
			t.Fatalf("%v: want %s in\n%s", argv, want, out)
		}
	}
	check(`"decision":"forbidden"}`, "rm", "-rf", "/")
	check(`"decision":"allow"}`, "git", "status")
	check(`"decision":"prompt"}`, "git", "push", "origin", "main")
}
