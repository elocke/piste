// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/features/skills/{claudecode,codexcli,hermesagent,agentsmd}-skill.ts,
// src/constants/{claudecode,codexcli,agentsmd,hermesagent}-paths.ts. Copyright (c) 2024 dyoshikawa.
// MIT License; see cli/THIRD_PARTY_NOTICES.md.

package adapter

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/lock"
)

// SkillDir is where each harness discovers skills, relative to the scope
// root. Codex reads the cross-tool ~/.agents/skills (rulesync
// CODEXCLI_SKILLS_DIR_PATH), not ~/.codex/skills.
func SkillDir(harness string, scope canon.Scope) (string, error) {
	switch harness {
	case canon.Claude:
		return ".claude/skills", nil
	case canon.Codex, canon.AgentsMD:
		return ".agents/skills", nil
	case canon.ClaudePlugin:
		return "skills", nil
	case canon.Hermes:
		if scope != canon.Bundle {
			return "", fmt.Errorf("hermes skills are delivered only through the hermes-skills bundle")
		}
		return "skills", nil
	}
	return "", fmt.Errorf("skills: unknown harness %q", harness)
}

// claudeOnlyKeys are SKILL.md frontmatter fields only Claude Code reads
// (claudecode-skill.ts). Other harnesses get them dropped, with a warning,
// so a copy never advertises a constraint it can't enforce.
var claudeOnlyKeys = []string{
	"allowed-tools", "disallowed-tools", "model", "argument-hint", "context", "agent",
	"shell", "disable-model-invocation", "user-invocable", "paths", "when_to_use", "hooks",
}

// SkillMode is how a skill reaches a harness.
type SkillMode int

const (
	// Link: a symlink to the source (workstations; live edits, no copy).
	Link SkillMode = iota
	// Copy: a tree we own (projects, bundles, the carrier).
	Copy
)

// Skill renders one skill. linkTarget is used for Link (relative to the link's
// directory, or absolute).
func Skill(harness string, scope canon.Scope, s canon.Skill, mode SkillMode, linkTarget string) (Result, error) {
	var r Result
	dir, err := SkillDir(harness, scope)
	if err != nil {
		return r, err
	}
	rel := dir + "/" + s.Name
	if harness == canon.Hermes && s.Category != "" && s.Category != "skills" {
		rel = dir + "/" + s.Category + "/" + s.Name // Hermes category nesting
	}
	if mode == Link {
		if linkTarget == "" {
			return r, fmt.Errorf("skill %s: link mode needs a target", s.Name)
		}
		r.add(Artifact{Harness: harness, Kind: lock.KindSkill, Item: s.Name, File: rel, Link: linkTarget})
		return r, nil
	}
	tree, err := readTree(s.Dir)
	if err != nil {
		return r, err
	}
	if harness != canon.Claude && harness != canon.ClaudePlugin {
		fm := map[string]any{}
		for k, v := range s.Frontmatter {
			fm[k] = v
		}
		var dropped []string
		for _, k := range claudeOnlyKeys {
			if _, ok := fm[k]; ok {
				delete(fm, k)
				dropped = append(dropped, k)
			}
		}
		if len(dropped) > 0 {
			md, err := canon.JoinFrontmatter(fm, s.Order, s.Body)
			if err != nil {
				return r, err
			}
			tree["SKILL.md"] = TreeFile{Data: []byte(md), Mode: tree["SKILL.md"].Mode}
			r.warn(fmt.Sprintf("%s: skill %s: dropped Claude-only frontmatter %s", harness, s.Name, strings.Join(dropped, ", ")))
		}
	}
	r.add(Artifact{Harness: harness, Kind: lock.KindSkill, Item: s.Name, File: rel, Tree: tree})
	return r, nil
}

// readTree loads a skill directory. Symlinks inside a skill are refused:
// a copy must be self-contained (rulesync assertTreeContainsNoSymlinks).
func readTree(root string) (map[string]TreeFile, error) {
	out := map[string]TreeFile{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: symlinks inside a skill are not copied", p)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = TreeFile{Data: b, Mode: fi.Mode().Perm()}
		return nil
	})
	return out, err
}

// TreeNames lists a tree's files, sorted.
func TreeNames(t map[string]TreeFile) []string {
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
