// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/features/*/claudecode-plugin-*.ts, src/constants/plugin-paths.ts,
// src/utils/plugin-root.ts. Copyright (c) 2024 dyoshikawa. MIT License; see cli/THIRD_PARTY_NOTICES.md.

// Package carrier builds the piste-harness Claude plugin:
// the zone's skills, subagents, hooks and MCP servers delivered as one local
// plugin, so piste never edits ~/.claude/settings.json or ~/.claude.json for
// them. rulesync generates the plugin directory; we add what it doesn't —
// .claude-plugin/plugin.json and a one-plugin marketplace.json, so the
// directory can be added with `claude plugin marketplace add <dir>`.
package carrier

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/adapter"
	"github.com/elocke/piste/cli/internal/agents/canon"
)

// Meta names the plugin and its marketplace.
type Meta struct {
	Plugin      string // e.g. piste-harness
	Marketplace string // e.g. piste-local
	Version     string
	Description string
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Build lays out the marketplace directory:
//
//	.claude-plugin/marketplace.json
//	<plugin>/.claude-plugin/plugin.json
//	<plugin>/{skills,agents,hooks,…}   from claude-plugin artifacts
//	<plugin>/.mcp.json
//
// extra adds plugin files that aren't artifacts (hook scripts), keyed by
// path inside the plugin.
func Build(m Meta, results []adapter.Result, extra map[string]adapter.TreeFile) (map[string]adapter.TreeFile, error) {
	if !nameRe.MatchString(m.Plugin) || !nameRe.MatchString(m.Marketplace) {
		return nil, fmt.Errorf("carrier: plugin and marketplace names must be kebab-case")
	}
	if m.Version == "" {
		return nil, fmt.Errorf("carrier: version required")
	}
	out := map[string]adapter.TreeFile{}
	put := func(rel string, f adapter.TreeFile) error {
		rel = path.Clean(rel)
		if strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
			return fmt.Errorf("carrier: %s escapes the plugin", rel)
		}
		if _, dup := out[rel]; dup {
			return fmt.Errorf("carrier: two artifacts write %s", rel)
		}
		out[rel] = f
		return nil
	}
	for _, r := range results {
		for _, a := range r.Artifacts {
			if a.Harness != canon.ClaudePlugin {
				return nil, fmt.Errorf("carrier: %s artifact %s is not a claude-plugin render", a.Harness, a.File)
			}
			if c := path.Clean(a.File); c == ".." || strings.HasPrefix(c, "../") || path.IsAbs(c) {
				return nil, fmt.Errorf("carrier: %s escapes the plugin", a.File)
			}
			base := m.Plugin + "/" + a.File
			switch {
			case a.Whole != nil:
				if err := put(base, adapter.TreeFile{Data: a.Whole, Mode: a.Mode}); err != nil {
					return nil, err
				}
			case a.Tree != nil:
				for _, n := range adapter.TreeNames(a.Tree) {
					if err := put(base+"/"+n, a.Tree[n]); err != nil {
						return nil, err
					}
				}
			default:
				return nil, fmt.Errorf("carrier: %s must be a whole file or tree (the plugin is entirely ours)", a.File)
			}
		}
	}
	for rel, f := range extra {
		if c := path.Clean(rel); c == ".." || strings.HasPrefix(c, "../") || path.IsAbs(c) {
			return nil, fmt.Errorf("carrier: %s escapes the plugin", rel)
		}
		if err := put(m.Plugin+"/"+rel, f); err != nil {
			return nil, err
		}
	}
	desc := m.Description
	if desc == "" {
		desc = "Skills, agents, hooks and MCP servers rendered by piste agents from the piste zones."
	}
	plugin, _ := json.MarshalIndent(map[string]any{
		"name": m.Plugin, "version": m.Version, "description": desc,
		"author": map[string]string{"name": "piste agents"},
	}, "", "  ")
	if err := put(m.Plugin+"/.claude-plugin/plugin.json", adapter.TreeFile{Data: append(plugin, '\n')}); err != nil {
		return nil, err
	}
	market, _ := json.MarshalIndent(map[string]any{
		"name":        m.Marketplace,
		"description": "Local marketplace holding the piste agents carrier plugin.",
		"owner":       map[string]string{"name": "piste agents"},
		"plugins": []map[string]string{{
			"name": m.Plugin, "source": "./" + m.Plugin, "version": m.Version, "description": desc,
		}},
	}, "", "  ")
	if err := put(".claude-plugin/marketplace.json", adapter.TreeFile{Data: append(market, '\n')}); err != nil {
		return nil, err
	}
	return out, nil
}

// Files lists the layout, sorted.
func Files(t map[string]adapter.TreeFile) []string {
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// InstallID is the id Claude uses: <plugin>@<marketplace>.
func (m Meta) InstallID() string { return m.Plugin + "@" + m.Marketplace }
