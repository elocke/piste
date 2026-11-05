// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/features/rules/{claudecode-rule,codexcli-rule,agentsmd-rule,hermesagent-rule}.ts,
// src/features/rules/rules-processor.ts, src/constants/codexcli-paths.ts. Copyright (c) 2024 dyoshikawa.
// MIT License; see cli/THIRD_PARTY_NOTICES.md.

package adapter

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/lock"
)

// CodexProjectDocMaxBytes is Codex's project_doc_max_bytes default: it stops
// reading the AGENTS.md files on the root-to-cwd chain once their combined
// size reaches this, silently.
const CodexProjectDocMaxBytes = 32 * 1024

// ComposeBlock joins fragments the way lib/agents.nix does for the
// managed block: a source marker per fragment, blank lines between.
func ComposeBlock(frags []canon.Instruction) string {
	parts := make([]string, 0, len(frags))
	for _, f := range frags {
		parts = append(parts, "<!-- source: "+f.Name+" -->\n"+strings.Trim(f.Text, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// Instructions renders the instruction fragments for one harness and scope.
// They always go into the managed block of a file, never a whole file (the
// user and the harness write the rest), and nothing is injected beyond the
// fragments themselves.
func Instructions(harness string, scope canon.Scope, frags []canon.Instruction) (Result, error) {
	var r Result
	if len(frags) == 0 {
		return r, nil
	}
	block := func(file string, fs []canon.Instruction) {
		r.add(Artifact{Harness: harness, Kind: lock.KindInstruction, Item: "instructions", File: file, MDBlock: str(ComposeBlock(fs))})
	}
	switch harness {
	case canon.Claude:
		if scope == canon.User {
			block(".claude/CLAUDE.md", frags)
			return r, nil
		}
		// Project: the instructions live in AGENTS.md (agentsmd); CLAUDE.md
		// only imports it.
		r.add(Artifact{Harness: harness, Kind: lock.KindInstruction, Item: "instructions", File: "CLAUDE.md", MDBlock: str("@AGENTS.md")})
		return r, nil
	case canon.Codex:
		if scope == canon.User {
			block(".codex/AGENTS.md", frags)
			return r, nil
		}
		return agentsMD(harness, frags)
	case canon.AgentsMD:
		if scope == canon.User {
			return r, fmt.Errorf("agentsmd has no user scope (Codex reads ~/.codex/AGENTS.md; use the codex target)")
		}
		return agentsMD(harness, frags)
	case canon.Hermes:
		// Hermes has no global rules file; per-profile SOUL/instructions are
		// rendered by the Hermes bundle, not here.
		r.warn("hermes: instruction fragments are not rendered (Hermes has no global rules file)")
		return r, nil
	case canon.ClaudePlugin:
		r.warn("claude-plugin: plugins cannot carry CLAUDE.md instructions; rendered for claude instead")
		return r, nil
	}
	return r, fmt.Errorf("instructions: unknown harness %q", harness)
}

// agentsMD folds root fragments into AGENTS.md and directory-scoped ones
// into <subproject>/AGENTS.md (the rulesync v22 codexcli "fold" policy).
// Codex reads the root file plus one per directory down to the cwd, sharing
// one 32 KiB budget per chain; over budget is an error here rather than
// rulesync's warning, since Codex would drop the tail silently.
func agentsMD(harness string, frags []canon.Instruction) (Result, error) {
	var r Result
	bySub := map[string][]canon.Instruction{}
	for _, f := range frags {
		sub := path.Clean(strings.Trim(f.Subproject, "/"))
		if sub == "." {
			sub = ""
		}
		if strings.HasPrefix(sub, "..") {
			return r, fmt.Errorf("instruction %s: subproject %q leaves the project", f.Name, f.Subproject)
		}
		bySub[sub] = append(bySub[sub], f)
	}
	subs := make([]string, 0, len(bySub))
	for s := range bySub {
		subs = append(subs, s)
	}
	sort.Strings(subs)
	sizes := map[string]int{}
	for _, s := range subs {
		file := "AGENTS.md"
		if s != "" {
			file = s + "/AGENTS.md"
		}
		b := ComposeBlock(bySub[s])
		sizes[s] = len(b)
		r.add(Artifact{Harness: harness, Kind: lock.KindInstruction, Item: "instructions", File: file, MDBlock: str(b)})
	}
	// Every chain root → … → s must fit the budget.
	for _, s := range subs {
		total := 0
		for _, anc := range chain(s) {
			total += sizes[anc]
		}
		if total > CodexProjectDocMaxBytes {
			where := "AGENTS.md"
			if s != "" {
				where = s + "/AGENTS.md"
			}
			return r, fmt.Errorf("instructions for %s total %d bytes on the AGENTS.md chain, over Codex's %d-byte project_doc_max_bytes; "+
				"scope fragments to subprojects (subproject = \"dir\") or trim them", where, total, CodexProjectDocMaxBytes)
		}
	}
	return r, nil
}

// chain lists "" and every ancestor directory of s, then s.
func chain(s string) []string {
	out := []string{""}
	if s == "" {
		return out
	}
	parts := strings.Split(s, "/")
	for i := range parts {
		out = append(out, strings.Join(parts[:i+1], "/"))
	}
	return out
}
