// Package canon is the agents engine's canonical model: what a zone declares,
// independent of any harness. Adapters (package adapter) render it per
// harness.
package canon

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// Harness names.
const (
	Claude       = "claude"
	ClaudePlugin = "claude-plugin" // the piste-harness plugin carrier
	Codex        = "codex"
	Hermes       = "hermes"
	AgentsMD     = "agentsmd" // .agents/ + AGENTS.md, shared by Codex and others
)

// Scope of a render.
type Scope string

const (
	User    Scope = "user"    // $HOME
	Project Scope = "project" // a repo root
	Bundle  Scope = "bundle"  // a self-contained tree (Hermes, devcontainers)
)

// Skill is one skill directory with a SKILL.md.
type Skill struct {
	Name        string
	Dir         string         // absolute source directory
	Category    string         // Hermes category (the parent dir name in zone layouts)
	Frontmatter map[string]any // decoded YAML
	Order       []string       // frontmatter key order, for re-rendering
	Body        string
}

// Description from the frontmatter.
func (s Skill) Description() string {
	d, _ := s.Frontmatter["description"].(string)
	return d
}

var frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

// SplitFrontmatter separates YAML frontmatter from a Markdown body.
func SplitFrontmatter(text string) (fm map[string]any, order []string, body string, err error) {
	m := frontmatterRe.FindStringSubmatchIndex(text)
	if m == nil {
		return nil, nil, text, errors.New("no YAML frontmatter")
	}
	raw := text[m[2]:m[3]]
	var ms yaml.MapSlice
	if err := yaml.Unmarshal([]byte(raw), &ms); err != nil {
		return nil, nil, "", fmt.Errorf("frontmatter: %w", err)
	}
	fm = map[string]any{}
	for _, it := range ms {
		k := fmt.Sprint(it.Key)
		fm[k] = it.Value
		order = append(order, k)
	}
	return fm, order, text[m[1]:], nil
}

// JoinFrontmatter renders frontmatter (keys in order, then any others
// sorted) and body back into a Markdown document.
func JoinFrontmatter(fm map[string]any, order []string, body string) (string, error) {
	seen := map[string]bool{}
	var ms yaml.MapSlice
	for _, k := range order {
		if v, ok := fm[k]; ok && !seen[k] {
			ms = append(ms, yaml.MapItem{Key: k, Value: v})
			seen[k] = true
		}
	}
	var rest []string
	for k := range fm {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		ms = append(ms, yaml.MapItem{Key: k, Value: fm[k]})
	}
	b, err := yaml.MarshalWithOptions(ms, yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return "", err
	}
	return "---\n" + string(bytes.TrimRight(b, "\n")) + "\n---\n" + body, nil
}

// LoadSkill reads dir/SKILL.md. The name must match the directory.
func LoadSkill(dir string) (Skill, error) {
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return Skill{}, err
	}
	fm, order, body, err := SplitFrontmatter(string(b))
	if err != nil {
		return Skill{}, fmt.Errorf("%s: %w", dir, err)
	}
	s := Skill{Name: filepath.Base(dir), Dir: dir, Frontmatter: fm, Order: order, Body: body,
		Category: filepath.Base(filepath.Dir(dir))}
	if n, _ := fm["name"].(string); n != s.Name {
		return s, fmt.Errorf("%s: frontmatter name %q != directory %q", dir, n, s.Name)
	}
	if s.Description() == "" {
		return s, fmt.Errorf("%s: frontmatter missing description", dir)
	}
	return s, nil
}

// Subagent is a Claude-style agent Markdown file.
type Subagent struct {
	Name        string
	Frontmatter map[string]any
	Order       []string
	Body        string
}

// LoadSubagent reads an agents/<name>.md file.
func LoadSubagent(path string) (Subagent, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Subagent{}, err
	}
	fm, order, body, err := SplitFrontmatter(string(b))
	if err != nil {
		return Subagent{}, fmt.Errorf("%s: %w", path, err)
	}
	name, _ := fm["name"].(string)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".md")
	}
	return Subagent{Name: name, Frontmatter: fm, Order: order, Body: body}, nil
}

// Instruction is one fragment of the composed instruction block.
type Instruction struct {
	Name string
	Text string
	// Subproject, when set, scopes the fragment to a directory of the
	// project (Codex reads one AGENTS.md per directory on the chain).
	Subproject string
}

// Hook is one owned hook (agents/hooks.toml).
type Hook struct {
	Name    string   `toml:"name"`
	Event   string   `toml:"event"`
	Matcher string   `toml:"matcher"`
	Script  string   `toml:"script"`  // a file in the zone's hooks/ dir
	Command string   `toml:"command"` // or a command line
	Timeout int      `toml:"timeout"`
	Targets []string `toml:"targets"`
}

// MCP is one MCP server (agents.toml [[mcp]]).
type MCP struct {
	Name    string            `toml:"name"`
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	// SecretEnv names variables the server needs from the environment. They
	// are never written as values anywhere.
	SecretEnv []string          `toml:"secretEnv"`
	URL       string            `toml:"url"`
	Headers   map[string]string `toml:"headers"`
	// BearerEnv: Authorization: Bearer ${BearerEnv}.
	BearerEnv      string   `toml:"bearerEnv"`
	StartupTimeout int      `toml:"startupTimeoutSec"`
	Targets        []string `toml:"targets"`
}

// Stdio reports whether the server is launched as a process.
func (m MCP) Stdio() bool { return m.Command != "" }

// Decision of a permission rule.
type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

// Permission is one shell-command rule (canonical `rm -rf *` style, or
// Claude's `Bash(cmd:*)`).
type Permission struct {
	Pattern  string   `toml:"pattern"`
	Decision Decision `toml:"decision"`
}
