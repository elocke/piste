package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/plan"
	"github.com/elocke/piste/cli/internal/agents/project"
	"github.com/elocke/piste/cli/internal/agents/safety"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

// Add uses ids: zone + registry checks, then the manifest
// edit and the sync as one plan.
func (p *Project) Add(ids ...string) (*plan.Plan, error) {
	text := p.Text
	for _, id := range ids {
		if _, err := p.admit(id); err != nil {
			return nil, err
		}
		if p.Manifest.Uses(id) {
			return nil, fmt.Errorf("%s is already used", id)
		}
		var err error
		if text, err = project.AddUse(text, id); err != nil {
			return nil, err
		}
	}
	return p.withManifest(text)
}

// Remove stops using ids and removes exactly what the lock says piste wrote
// for them.
func (p *Project) Remove(ids ...string) (*plan.Plan, error) {
	// What piste wrote for the items being removed, so a refused removal can
	// show the hand edit as a diff.
	if rs, _, err := p.Render(); err == nil {
		p.expected = map[string][]byte{}
		for _, r := range rs {
			for _, a := range r.arts {
				if a.Whole != nil {
					p.expected[a.File] = a.Whole
				}
			}
		}
	}
	text := p.Text
	for _, id := range ids {
		var err error
		if text, err = project.RemoveUse(text, id); err != nil {
			return nil, err
		}
	}
	return p.withManifest(text)
}

func (p *Project) withManifest(text string) (*plan.Plan, error) {
	m, err := project.Parse(text)
	if err != nil {
		return nil, err
	}
	p.Manifest = m
	pl, err := p.Sync()
	if err != nil {
		return nil, err
	}
	if err := pl.WriteFile(project.ManifestFile, []byte(text), 0o644, "manifest"); err != nil {
		return nil, err
	}
	p.Text = text
	return pl, nil
}

// Finding is one drift / audit row.
type Finding struct {
	State  string `json:"state"`
	What   string `json:"what"`
	File   string `json:"file,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Healthy drift states (unknown is reported, never "clean", but it isn't
// drift piste can fix).
var Healthy = map[string]bool{"ok": true, "local": true, "unknown": true}

// Drift is the three-way comparison: lock ↔ disk (every owned
// entry re-hashed), lock ↔ registry (outdated, gone, policy), manifest ↔
// lock (unsynced), plus the unknown entries in harness files.
func (p *Project) Drift() []Finding {
	var out []Finding
	for _, f := range p.Lock.VerifyOwned(p.Root) {
		out = append(out, Finding{State: f.State, What: f.What, File: f.File, Detail: f.Detail})
	}
	locked := map[string]bool{}
	for _, e := range p.Lock.Entries() {
		id := e.Item.ID
		locked[id] = true
		if strings.HasPrefix(id, "local:") {
			continue
		}
		zid, err := zone.ParseID(id)
		if err != nil {
			out = append(out, Finding{State: "policy", What: id, Detail: err.Error()})
			continue
		}
		if err := zone.Admit(p.Manifest.Zone, zid); err != nil {
			out = append(out, Finding{State: "policy", What: id, Detail: err.Error()})
			continue
		}
		it, ok := p.Reg.Get(id)
		switch {
		case !ok:
			out = append(out, Finding{State: "gone", What: id, Detail: "no longer in the registry"})
		case it.SHA256 != e.Item.SHA256:
			out = append(out, Finding{State: "outdated", What: id, Detail: "the registry has a newer version (piste agents sync)"})
		default:
			out = append(out, Finding{State: "ok", What: id})
		}
	}
	for _, u := range p.Manifest.Use {
		if !locked[u.ID] {
			out = append(out, Finding{State: "unsynced", What: u.ID, Detail: "used but not in the lock (piste agents sync)"})
		}
	}
	for _, s := range p.Manifest.MCP {
		if !locked["local:mcp/"+s.Name] {
			out = append(out, Finding{State: "unsynced", What: "local:mcp/" + s.Name})
		}
	}
	for id := range locked {
		if !strings.HasPrefix(id, "local:") && !p.Manifest.Uses(id) {
			out = append(out, Finding{State: "unsynced", What: id, Detail: "in the lock but no longer used (piste agents sync)"})
		}
	}
	for _, f := range p.unknown() {
		if p.Manifest.IsLocal(f.What) {
			f.State, f.Detail = "local", "declared [[local]]"
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].State != out[j].State {
			return out[i].State < out[j].State
		}
		return out[i].What < out[j].What
	})
	return out
}

// unknown lists what the harnesses see here that no lock entry covers: it
// is reported, never deleted, and never called clean.
func (p *Project) unknown() []Finding {
	var out []Finding
	ownedFile := map[string]bool{}
	ownedPtr := map[string]bool{}
	ownedEntry := map[string]map[string]bool{}
	for _, o := range p.Lock.Owned {
		ownedFile[o.File] = true
		switch o.Kind {
		case lock.OwnedJSON:
			ownedPtr[o.File+"#"+o.Path] = true
		case lock.OwnedJSONEntry:
			k := o.File + "#" + o.Path
			if ownedEntry[k] == nil {
				ownedEntry[k] = map[string]bool{}
			}
			ownedEntry[k][o.SHA256] = true
		}
	}
	for _, dir := range []string{".agents/skills", ".claude/skills", ".claude/agents", ".codex/agents", ".agents/hooks"} {
		es, _ := os.ReadDir(filepath.Join(p.Root, dir))
		for _, e := range es {
			rel := dir + "/" + e.Name()
			if !ownedFile[rel] {
				out = append(out, Finding{State: "unknown", What: rel, File: rel, Detail: "not written by piste"})
			}
		}
	}
	// Instruction files: owned when piste manages a block in them.
	for _, f := range []string{"AGENTS.md", "CLAUDE.md", ".claude/CLAUDE.md"} {
		if _, err := os.Lstat(filepath.Join(p.Root, f)); err == nil && !ownedFile[f] {
			out = append(out, Finding{State: "unknown", What: f, File: f, Detail: "not written by piste"})
		}
	}
	jsonKeys := []struct{ file, ptr string }{
		{".mcp.json", "/mcpServers"}, {".claude/settings.json", "/enabledPlugins"}, {".claude/settings.json", "/extraKnownMarketplaces"},
	}
	for _, jk := range jsonKeys {
		d := readJSON(filepath.Join(p.Root, jk.file))
		if d == nil {
			continue
		}
		raw, err := d.Get(jk.ptr)
		if err != nil {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for k := range m {
			ptr := jk.ptr + edit.Pointer(k)
			if !ownedPtr[jk.file+"#"+ptr] {
				out = append(out, Finding{State: "unknown", What: jk.file + "#" + ptr, File: jk.file, Detail: "not written by piste"})
			}
		}
	}
	for _, f := range []string{".claude/settings.json", ".codex/hooks.json"} {
		d := readJSON(filepath.Join(p.Root, f))
		if d == nil {
			continue
		}
		for _, base := range []string{"/hooks", "/permissions"} {
			raw, err := d.Get(base)
			if err != nil {
				continue
			}
			var m map[string]json.RawMessage
			if json.Unmarshal(raw, &m) != nil {
				continue
			}
			for k, v := range m {
				var arr []json.RawMessage
				if json.Unmarshal(v, &arr) != nil {
					continue
				}
				ptr := base + edit.Pointer(k)
				for i, el := range arr {
					h, _ := edit.Hash(el)
					if !ownedEntry[f+"#"+ptr][h] {
						out = append(out, Finding{State: "unknown", What: f + "#" + ptr + "/" + strconv.Itoa(i), File: f, Detail: "not written by piste"})
					}
				}
			}
		}
	}
	return out
}

func readJSON(path string) *edit.JSON {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	d, err := edit.ParseJSON(b)
	if err != nil {
		return nil
	}
	return d
}

// Audit is the inventory with attribution: registry (in the
// lock, or a copy matching a registry item), third-party (likewise, a
// locked third-party item), local (the manifest's own [[mcp]] and
// [[local]]), hand-made (tracked in git with no lock record) or unknown.
func (p *Project) Audit() []Finding {
	var out []Finding
	for _, o := range p.Lock.Owned {
		label := "registry"
		if strings.HasPrefix(o.Item, "local:") {
			label = "local"
		}
		out = append(out, Finding{State: label, What: o.ID(), File: o.File, Detail: o.Item})
	}
	tracked := gitTracked(p.Root)
	for _, f := range p.unknown() {
		out = append(out, p.attribute(f, tracked))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].What < out[j].What })
	return out
}

func gitTracked(root string) map[string]bool {
	out := map[string]bool{}
	cmd := exec.Command("git", "-C", root, "ls-files", "-z")
	b, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, f := range strings.Split(string(b), "\x00") {
		if f != "" {
			out[f] = true
		}
	}
	return out
}

func trackedUnder(t map[string]bool, dir string) bool {
	for f := range t {
		if strings.HasPrefix(f, dir+"/") {
			return true
		}
	}
	return false
}

// Adopted is what `init --adopt` imported, with its warnings.
type Adopted struct {
	MCP      []canon.MCP
	Warnings []string
}

// AdoptMCP imports .mcp.json servers as project-local [[mcp]] entries,
// redacting literal secrets into environment references. The result
// never contains a literal: the manifest is validated before any write.
func AdoptMCP(root string) (Adopted, error) {
	var a Adopted
	b, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	std, err := edit.Canonical(b)
	if err != nil {
		return a, fmt.Errorf(".mcp.json: %w", err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(std, &doc); err != nil {
		return a, fmt.Errorf(".mcp.json: %w", err)
	}
	names := make([]string, 0, len(doc.MCPServers))
	for n := range doc.MCPServers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := doc.MCPServers[n]
		m := canon.MCP{Name: n, Command: s.Command, Args: s.Args, URL: s.URL}
		for k, v := range s.Env {
			if env, ok := envOnly(v); ok {
				m.SecretEnv = append(m.SecretEnv, env)
				continue
			}
			if safety.LooksSecret(k, v) {
				m.SecretEnv = append(m.SecretEnv, k)
				a.Warnings = append(a.Warnings, fmt.Sprintf("mcp %s: env %s held a literal secret; now secretEnv %s (set it in your environment)", n, k, k))
				continue
			}
			if m.Env == nil {
				m.Env = map[string]string{}
			}
			m.Env[k] = v
		}
		for k, v := range s.Headers {
			if strings.EqualFold(k, "authorization") {
				if env, ok := bearerOnly(v); ok {
					m.BearerEnv = env
					continue
				}
			}
			nv, name, red := safety.Redact(n, k, v)
			if red {
				if strings.EqualFold(k, "authorization") {
					m.BearerEnv = name
				} else {
					if m.Headers == nil {
						m.Headers = map[string]string{}
					}
					m.Headers[k] = nv
				}
				a.Warnings = append(a.Warnings, fmt.Sprintf("mcp %s: header %s held a literal secret; now ${%s} (set it in your environment)", n, k, name))
				continue
			}
			if m.Headers == nil {
				m.Headers = map[string]string{}
			}
			m.Headers[k] = v
		}
		sort.Strings(m.SecretEnv)
		a.MCP = append(a.MCP, m)
	}
	return a, nil
}

func envOnly(v string) (string, bool) {
	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") && !strings.ContainsAny(v[2:len(v)-1], ":-${}") {
		return v[2 : len(v)-1], true
	}
	return "", false
}

func bearerOnly(v string) (string, bool) {
	if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return envOnly(strings.TrimSpace(v[7:]))
	}
	return "", false
}
