// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/features/subagents/{claudecode,claudecode-plugin,codexcli}-subagent.ts.
// Copyright (c) 2024 dyoshikawa. MIT License; see cli/THIRD_PARTY_NOTICES.md.

package adapter

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/lock"
)

// pluginForbidden: Claude Code refuses these on plugin-shipped agents "for
// security reasons", so emitting them would suggest a constraint that isn't
// enforced (claudecode-plugin-subagent.ts).
var pluginForbidden = []string{"hooks", "mcpServers", "permissionMode"}

// Subagent renders one subagent:
//   - claude: .claude/agents/<n>.md, as authored
//   - claude-plugin: agents/<n>.md without hooks/mcpServers/permissionMode,
//     and isolation only if "worktree"
//   - codex: .codex/agents/<n>.toml with name, description,
//     developer_instructions (the body), plus model / model_reasoning_effort /
//     sandbox_mode from a `codex:` frontmatter section
//
// Hermes subagents are not rendered (rulesync generates Python plugins for
// them; we don't port that).
func Subagent(harness string, a canon.Subagent) (Result, error) {
	var r Result
	switch harness {
	case canon.Claude, canon.ClaudePlugin:
		fm := map[string]any{}
		for k, v := range a.Frontmatter {
			fm[k] = v
		}
		dir := ".claude/agents"
		if harness == canon.ClaudePlugin {
			dir = "agents"
			var dropped []string
			for _, k := range pluginForbidden {
				if _, ok := fm[k]; ok {
					delete(fm, k)
					dropped = append(dropped, k)
				}
			}
			if iso, ok := fm["isolation"]; ok && iso != "worktree" {
				delete(fm, "isolation")
				dropped = append(dropped, fmt.Sprintf("isolation %q", iso))
			}
			if len(dropped) > 0 {
				r.warn(fmt.Sprintf("claude-plugin: subagent %s: dropped %s (not supported for plugin agents)", a.Name, strings.Join(dropped, ", ")))
			}
		}
		delete(fm, "codex")
		md, err := canon.JoinFrontmatter(fm, a.Order, a.Body)
		if err != nil {
			return r, err
		}
		r.add(Artifact{Harness: harness, Kind: lock.KindSubagent, Item: a.Name, File: dir + "/" + a.Name + ".md", Whole: []byte(md)})
		return r, nil
	case canon.Codex:
		b, err := CodexSubagentTOML(a)
		if err != nil {
			return r, err
		}
		r.add(Artifact{Harness: harness, Kind: lock.KindSubagent, Item: a.Name, File: ".codex/agents/" + a.Name + ".toml", Whole: b})
		return r, nil
	case canon.Hermes:
		r.warn("hermes: subagent " + a.Name + " not rendered (Hermes subagents are profiles)")
		return r, nil
	}
	return r, fmt.Errorf("subagents: unknown harness %q", harness)
}

// CodexSubagentTOML renders the Codex agent file. A multi-line body is a
// ”' literal string unless it contains ”' itself (stringifyCodexCliSubagentToml).
func CodexSubagentTOML(a canon.Subagent) ([]byte, error) {
	head := map[string]any{"name": a.Name}
	if d, ok := a.Frontmatter["description"].(string); ok && d != "" {
		head["description"] = d
	}
	if sec, ok := a.Frontmatter["codex"].(map[string]any); ok {
		for _, k := range []string{"model", "model_reasoning_effort", "sandbox_mode"} {
			if v, ok := sec[k].(string); ok && v != "" {
				head[k] = v
			}
		}
	}
	// Stable key order: name, description, then the rest alphabetically.
	var b strings.Builder
	for _, k := range []string{"name", "description", "model", "model_reasoning_effort", "sandbox_mode"} {
		v, ok := head[k]
		if !ok {
			continue
		}
		line, err := toml.Marshal(map[string]any{k: v})
		if err != nil {
			return nil, err
		}
		b.Write(line)
	}
	body := strings.Trim(a.Body, "\n")
	if body != "" {
		if strings.Contains(body, "\n") && !strings.Contains(body, "'''") {
			b.WriteString("developer_instructions = '''\n" + body + "\n'''\n")
		} else {
			line, err := toml.Marshal(map[string]any{"developer_instructions": body})
			if err != nil {
				return nil, err
			}
			b.Write(line)
		}
	}
	out := []byte(b.String())
	var check map[string]any
	if err := toml.Unmarshal(out, &check); err != nil {
		return nil, fmt.Errorf("codex subagent %s: rendered TOML does not parse: %w", a.Name, err)
	}
	return out, nil
}
