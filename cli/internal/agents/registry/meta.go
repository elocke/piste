package registry

import (
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/adapter"
	"github.com/elocke/piste/cli/internal/agents/canon"
)

// Meta is an item's generated metadata: never hand-kept,
// always derived from the item, its zone and the zone's lock and policy.
type Meta struct {
	ID          string              `json:"id"`
	Kind        string              `json:"kind"`
	Zone        string              `json:"zone"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Trust       string              `json:"trust"`                // first-party | declared | third-party
	AllowedBy   string              `json:"allowed_by,omitempty"` // third-party: the zone [policy] allow pattern
	Harnesses   []string            `json:"harnesses,omitempty"`
	Lossy       map[string][]string `json:"lossy,omitempty"` // harness → fields it drops
	Root        string              `json:"root"`
	Source      string              `json:"source"` // root:path, or the upstream for third-party
	Commit      string              `json:"commit,omitempty"`
	Version     string              `json:"version,omitempty"`
	ContentHash string              `json:"content_hash"`
	Requires    []string            `json:"requires,omitempty"`
	License     string              `json:"license,omitempty"`
}

// Meta returns the item's metadata.
func (r *Registry) Meta(it *Item) Meta {
	if it.meta != nil {
		return *it.meta
	}
	m := Meta{
		ID: it.ID.String(), Kind: it.ID.Kind, Zone: it.ID.Zone, Name: it.ID.Name,
		Description: it.Desc, Trust: "first-party", Root: it.Root,
		Source: it.Root + ":" + it.Source, ContentHash: it.SHA256, Commit: r.Revs[it.Root],
	}
	fm := map[string]any{}
	switch {
	case it.Skill != nil:
		fm = it.Skill.Frontmatter
		m.Harnesses = []string{canon.Claude, canon.Codex, canon.Hermes}
		var dropped []string
		for _, k := range adapter.ClaudeOnlySkillKeys() {
			if _, ok := fm[k]; ok {
				dropped = append(dropped, k)
			}
		}
		if c, _ := fm["context"].(string); c == "fork" {
			// A forked-context skill only makes sense where it can fork.
			m.Harnesses = []string{canon.Claude}
		} else if len(dropped) > 0 {
			m.Lossy = map[string][]string{canon.Codex: dropped, canon.Hermes: dropped}
		}
	case it.Agent != nil:
		fm = it.Agent.Frontmatter
		m.Harnesses = []string{canon.Claude, canon.Codex}
		var dropped []string
		for k := range fm {
			if k != "name" && k != "description" && k != "codex" {
				dropped = append(dropped, k)
			}
		}
		if len(dropped) > 0 {
			sort.Strings(dropped)
			m.Lossy = map[string][]string{canon.Codex: dropped}
		}
	case it.Instruction != nil, it.Permission != nil:
		m.Harnesses = []string{canon.Claude, canon.Codex}
	case it.Hook != nil:
		m.Harnesses = targetsOr(it.Hook.Targets, canon.Claude, canon.Codex)
		if it.Hook.Command != "" {
			m.Requires = append(m.Requires, "command: "+it.Hook.Command)
		}
	case it.MCP != nil:
		m.Trust = "declared"
		m.Harnesses = targetsOr(it.MCP.Targets, canon.Claude, canon.Codex, canon.Hermes)
		if it.MCP.Command != "" {
			m.Requires = append(m.Requires, "command: "+it.MCP.Command)
		}
		for _, e := range it.MCP.SecretEnv {
			m.Requires = append(m.Requires, "env: "+e)
		}
		if it.MCP.BearerEnv != "" {
			m.Requires = append(m.Requires, "env: "+it.MCP.BearerEnv)
		}
	case it.Decl != nil:
		m.Trust = "declared"
		if h, _ := it.Decl["harness"].(string); h != "" {
			m.Harnesses = []string{h}
		}
		m.Version, _ = it.Decl["version"].(string)
		if s, _ := it.Decl["source"].(string); s != "" {
			m.Source = s
		}
	case it.Bundle != nil:
		m.Harnesses = it.Bundle.Targets
	}
	if it.ThirdParty != nil {
		tp := it.ThirdParty
		m.Trust = "third-party"
		m.Source = tp.Source
		if tp.Path != "" && tp.Path != "." {
			m.Source += "/" + tp.Path
		}
		m.Commit, m.Version = tp.Commit, tp.Ref
		m.AllowedBy = allowedBy(r.policy[it.Root], tp.Source)
	}
	if l, _ := fm["license"].(string); l != "" {
		m.License = l
	}
	switch req := fm["requires"].(type) {
	case string:
		m.Requires = append(m.Requires, req)
	case []any:
		for _, x := range req {
			m.Requires = append(m.Requires, fmt.Sprint(x))
		}
	}
	return m
}

// RequiredIDs are the registry ids an item's `requires` names (the rest of
// its requirements are commands and environment variables).
func (r *Registry) RequiredIDs(it *Item) []string {
	var out []string
	for _, q := range r.Meta(it).Requires {
		if _, ok := r.Items[q]; ok {
			out = append(out, q)
		}
	}
	return out
}

func targetsOr(t []string, def ...string) []string {
	if len(t) > 0 {
		return t
	}
	return def
}

// allowedBy returns the zone [policy] allow pattern that admits source.
func allowedBy(allow []string, source string) string {
	for _, p := range allow {
		if ok, _ := path.Match(p, source); ok {
			return p
		}
	}
	return ""
}

// readPolicy reads the root's agents.toml [policy] allow list.
func (r *Registry) readPolicy(dir, name string) error {
	var m struct {
		Policy struct {
			Allow []string `toml:"allow"`
		} `toml:"policy"`
	}
	if _, ok, err := readTOML(filepath.Join(dir, "agents.toml"), &m); err != nil || !ok {
		return err
	}
	if r.policy == nil {
		r.policy = map[string][]string{}
	}
	r.policy[name] = m.Policy.Allow
	return nil
}

// Search returns the items matching every word of text (in the id, name or
// description, case-insensitive) and the filters (empty = any).
func (r *Registry) Search(text, kind, harness, zoneName string) []Meta {
	words := strings.Fields(strings.ToLower(text))
	var out []Meta
	for _, id := range r.IDs() {
		it := r.Items[id]
		m := r.Meta(it)
		if kind != "" && m.Kind != kind {
			continue
		}
		if zoneName != "" && m.Zone != zoneName {
			continue
		}
		if harness != "" && !contains(m.Harnesses, harness) {
			continue
		}
		hay := strings.ToLower(m.ID + " " + m.Description)
		match := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				match = false
				break
			}
		}
		if match {
			out = append(out, m)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
