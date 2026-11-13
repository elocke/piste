// Package project reads and edits a project's .agents/agents.toml.
// The file is human-edited, so it is never re-serialised: add and
// remove splice [[use]] tables as text, and the result is re-parsed before
// anything is written.
package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/safety"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

// Paths inside a project.
const (
	Dir          = ".agents"
	ManifestFile = ".agents/agents.toml"
	LockFile     = ".agents/agents.lock"
)

// Use is one [[use]] entry.
type Use struct {
	ID string `toml:"id"`
}

// Manifest is .agents/agents.toml.
type Manifest struct {
	Zone      string      `toml:"zone"`
	Harnesses []string    `toml:"harnesses"`
	Use       []Use       `toml:"use"`
	MCP       []canon.MCP `toml:"mcp"` // project-local servers (init --adopt)
	// Local declares hand-made agent content the project keeps as its own
	// (init --adopt): audit labels it local, drift doesn't call it unknown.
	Local  []Local `toml:"local"`
	Policy struct {
		// Harnesses narrows the harnesses (tighten-only).
		Harnesses []string `toml:"harnesses"`
	} `toml:"policy"`
}

// Local is one [[local]] entry: a path relative to the project root.
type Local struct {
	Path string `toml:"path"`
	Note string `toml:"note,omitempty"`
}

// IsLocal reports whether rel (a project-relative path) is declared local,
// itself, under a declared directory, or a JSON pointer into a declared file
// (path#/ptr).
func (m *Manifest) IsLocal(rel string) bool {
	for _, l := range m.Local {
		if rel == l.Path || strings.HasPrefix(rel, l.Path+"/") || strings.HasPrefix(rel, l.Path+"#") {
			return true
		}
	}
	return false
}

// Root finds the project root: the nearest ancestor with a .git entry.
func Root(start string) (string, error) {
	p, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			return p, nil
		}
		if p == filepath.Dir(p) {
			return "", errors.New("not inside a git repository (use --root)")
		}
		p = filepath.Dir(p)
	}
}

// Load reads and validates the manifest at root.
func Load(root string) (*Manifest, string, error) {
	b, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", fmt.Errorf("no %s in %s (run `piste agents init`)", ManifestFile, root)
	}
	if err != nil {
		return nil, "", err
	}
	m, err := Parse(string(b))
	return m, string(b), err
}

// Parse decodes and validates manifest text.
func Parse(text string) (*Manifest, error) {
	var m Manifest
	dec := toml.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		var sm *toml.StrictMissingError
		if errors.As(err, &sm) {
			return nil, fmt.Errorf("%s: %s", ManifestFile, sm.String())
		}
		return nil, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	return &m, m.Validate()
}

var knownHarness = map[string]bool{canon.Claude: true, canon.Codex: true, canon.AgentsMD: true}

// Validate checks the zone, harnesses, ids and that no literal secret is in
// the file.
func (m *Manifest) Validate() error {
	if err := zone.ValidMachine(m.Zone); err != nil {
		return fmt.Errorf("%s: zone = %q: %w", ManifestFile, m.Zone, err)
	}
	for _, h := range m.Harnesses {
		if !knownHarness[h] {
			return fmt.Errorf("%s: unknown harness %q (claude, codex, agentsmd)", ManifestFile, h)
		}
	}
	for _, h := range m.Policy.Harnesses {
		if !contains(m.Harnesses, h) {
			return fmt.Errorf("%s: [policy] harnesses may only narrow harnesses (%q is not in it)", ManifestFile, h)
		}
	}
	seen := map[string]bool{}
	for _, u := range m.Use {
		if _, err := zone.ParseID(u.ID); err != nil {
			return fmt.Errorf("%s: %w", ManifestFile, err)
		}
		if seen[u.ID] {
			return fmt.Errorf("%s: %s is used twice", ManifestFile, u.ID)
		}
		seen[u.ID] = true
	}
	for _, l := range m.Local {
		c := filepath.ToSlash(filepath.Clean(l.Path))
		if l.Path == "" || c != l.Path || filepath.IsAbs(l.Path) || c == ".." || strings.HasPrefix(c, "../") {
			return fmt.Errorf("%s: [[local]] path %q: want a clean path inside the project", ManifestFile, l.Path)
		}
	}
	for _, s := range m.MCP {
		for k, v := range s.Env {
			if safety.LooksSecret(k, v) {
				return fmt.Errorf("%s: mcp %s env %s holds a literal secret", ManifestFile, s.Name, k)
			}
		}
		for k, v := range s.Headers {
			if safety.LooksSecret(k, v) {
				return fmt.Errorf("%s: mcp %s header %s holds a literal secret", ManifestFile, s.Name, k)
			}
		}
	}
	return nil
}

// Effective harnesses after the policy narrowing.
func (m *Manifest) Effective() []string {
	if len(m.Policy.Harnesses) > 0 {
		return m.Policy.Harnesses
	}
	return m.Harnesses
}

// Uses reports whether id is used.
func (m *Manifest) Uses(id string) bool {
	for _, u := range m.Use {
		if u.ID == id {
			return true
		}
	}
	return false
}

// AddUse appends a [[use]] table to the manifest text.
func AddUse(text, id string) (string, error) {
	if !strings.HasSuffix(text, "\n") && text != "" {
		text += "\n"
	}
	out := text + "\n[[use]]\nid = " + strconv.Quote(id) + "\n"
	_, err := Parse(out)
	return out, err
}

var tableHeader = regexp.MustCompile(`^\s*\[`)

// RemoveUse deletes the [[use]] table whose id is id (and the blank line
// before it), leaving every other byte.
func RemoveUse(text, id string) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	want := regexp.MustCompile(`^\s*id\s*=\s*` + regexp.QuoteMeta(strconv.Quote(id)) + `\s*(#.*)?$`)
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "[[use]]" {
			continue
		}
		end := i + 1
		match := false
		for end < len(lines) && !tableHeader.MatchString(lines[end]) {
			if want.MatchString(strings.TrimRight(lines[end], "\n")) {
				match = true
			}
			end++
		}
		if !match {
			continue
		}
		// Trailing blank lines stay with the next table.
		for end > i+1 && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		start := i
		if start > 0 && strings.TrimSpace(lines[start-1]) == "" {
			start--
		}
		out := strings.Join(lines[:start], "") + strings.Join(lines[end:], "")
		_, err := Parse(out)
		return out, err
	}
	return "", fmt.Errorf("%s does not use %s", ManifestFile, id)
}

// Init renders a new manifest.
func Init(z string, harnesses []string, mcp []canon.MCP) (string, error) {
	return InitWith(z, harnesses, mcp, nil, nil)
}

// InitWith renders a new manifest that also uses registry ids and declares
// local paths (what init --adopt found).
func InitWith(z string, harnesses []string, mcp []canon.MCP, uses []string, locals []Local) (string, error) {
	var b strings.Builder
	b.WriteString("# Agent content for this project (piste agents).\n")
	b.WriteString("zone = " + strconv.Quote(z) + "\n")
	q := make([]string, len(harnesses))
	for i, h := range harnesses {
		q[i] = strconv.Quote(h)
	}
	b.WriteString("harnesses = [" + strings.Join(q, ", ") + "]\n")
	for _, s := range mcp {
		b.WriteString("\n# Adopted from .mcp.json (project-local).\n")
		t, err := mcpTable(s)
		if err != nil {
			return "", err
		}
		b.WriteString(t)
	}
	if len(uses) > 0 {
		b.WriteString("\n# Adopted: this content matched the registry.\n")
	}
	for i, id := range uses {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[[use]]\nid = " + strconv.Quote(id) + "\n")
	}
	if len(locals) > 0 {
		b.WriteString("\n# Adopted: hand-made content this project keeps as its own.\n")
	}
	for i, l := range locals {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("[[local]]\npath = " + strconv.Quote(l.Path) + "\n")
		if l.Note != "" {
			b.WriteString("note = " + strconv.Quote(l.Note) + "\n")
		}
	}
	out := b.String()
	if _, err := Parse(out); err != nil {
		return "", err
	}
	return out, nil
}

func mcpTable(s canon.MCP) (string, error) {
	type row struct {
		Name      string            `toml:"name"`
		Command   string            `toml:"command,omitempty"`
		Args      []string          `toml:"args,omitempty"`
		Env       map[string]string `toml:"env,omitempty"`
		SecretEnv []string          `toml:"secretEnv,omitempty"`
		URL       string            `toml:"url,omitempty"`
		Headers   map[string]string `toml:"headers,omitempty"`
		BearerEnv string            `toml:"bearerEnv,omitempty"`
	}
	b, err := toml.Marshal(map[string][]row{"mcp": {{s.Name, s.Command, s.Args, s.Env, s.SecretEnv, s.URL, s.Headers, s.BearerEnv}}})
	return string(b), err
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
