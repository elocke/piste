// Package registry is the catalogue of what the machine's zones offer, read from
// explicit zone roots only. Item ids are <zone>:<kind>/<name>.
//
// A zone root is an agents/ tree:
//
//	skills/<category>/<name>/SKILL.md   skill
//	agents/<name>.md                    agent (subagent)
//	instructions/<name>.md              instruction fragment
//	hooks.toml [[hook]] + hooks/<script> hook
//	permissions.toml [[permission]]     permission (shell rules)
//	agents.toml / agents.base.toml:
//	  [[mcp]]                           mcp
//	  [[claude.plugin]] / [[codex.plugin]]   plugin
//	  [[claude.marketplace]] / [[codex.marketplace]] marketplace
//	  [[tool]]                          tool
//	  [[hook]] with installer           installer
//	presets/<name>.toml                 bundle (a Hermes / container skill set)
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

// Kinds of registry item (the id's kind segment) and their lock kind.
var Kinds = map[string]lock.Kind{
	"skill": lock.KindSkill, "agent": lock.KindSubagent, "instruction": lock.KindInstruction,
	"hook": lock.KindHook, "permission": lock.KindHook, "mcp": lock.KindMCP,
	"plugin": lock.KindPlugin, "marketplace": lock.KindMarketplace, "tool": lock.KindTool,
	"installer": lock.KindInstaller, "bundle": lock.KindBundle,
	"3p-skill": lock.KindSkill,
}

// Item is one registry entry.
type Item struct {
	ID     zone.ID
	Root   string // the zone root's name
	Source string // path within the root (or the declaring file)
	SHA256 string // content hash of the source
	Desc   string

	Skill       *canon.Skill
	Agent       *canon.Subagent
	Instruction *canon.Instruction
	Hook        *canon.Hook
	HookScript  []byte // the hook's script, if it has one
	Permission  *canon.Permission
	MCP         *canon.MCP
	Decl        map[string]any // plugin / marketplace / tool / installer declaration
	Bundle      *Preset
	ThirdParty  *ThirdParty // 3p-skill: the zone agents.lock entry it was fetched from

	meta *Meta  // set when loaded from an artifact
	raw  []byte // agent / instruction / bundle: the source file's bytes
}

// Preset is presets/<name>.toml.
type Preset struct {
	Targets      []string `toml:"targets"`
	Skills       []string `toml:"skills"`
	Agents       []string `toml:"agents"`
	Instructions []string `toml:"instructions"`
	Hooks        []string `toml:"hooks"`
}

// Registry is every item of the given roots, or of a built registry
// artifact (Load).
type Registry struct {
	Roots []zone.Root
	Items map[string]*Item // by id string

	// Dir and Zone are set when the registry was loaded from an artifact:
	// its directory and the machine zone it was built for.
	Dir  string
	Zone string
	// Revs are the zone roots' commits, when known (Meta's commit).
	Revs map[string]string
	// Warnings from the scan (e.g. a locked third-party skill with no
	// fetched content on this machine).
	Warnings []string

	policy map[string][]string // root name → its [policy] allow list
}

// Build scans the roots. Nothing else is read.
func Build(roots []zone.Root) (*Registry, error) {
	r := &Registry{Roots: roots, Items: map[string]*Item{}}
	for _, root := range roots {
		if err := r.scan(root); err != nil {
			return nil, fmt.Errorf("zone root %s: %w", root.Name, err)
		}
	}
	return r, nil
}

// Get returns an item by id.
func (r *Registry) Get(id string) (*Item, bool) {
	it, ok := r.Items[id]
	return it, ok
}

// IDs lists the ids, sorted.
func (r *Registry) IDs() []string {
	out := make([]string, 0, len(r.Items))
	for k := range r.Items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Find returns the items with this kind and name across zones.
func (r *Registry) Find(kind, name string) []*Item {
	var out []*Item
	for _, id := range r.IDs() {
		it := r.Items[id]
		if it.ID.Kind == kind && it.ID.Name == name {
			out = append(out, it)
		}
	}
	return out
}

func (r *Registry) add(root zone.Root, kind, name, src, sum string, it *Item) error {
	id := zone.ID{Zone: root.Zone, Kind: kind, Name: name}
	if _, err := zone.ParseID(id.String()); err != nil {
		return err
	}
	if prev, dup := r.Items[id.String()]; dup {
		return fmt.Errorf("%s is defined in both %s (%s) and %s (%s)", id, prev.Root, prev.Source, root.Name, src)
	}
	it.ID, it.Root, it.Source, it.SHA256 = id, root.Name, src, sum
	r.Items[id.String()] = it
	return nil
}

func (r *Registry) scan(root zone.Root) error {
	if fi, err := os.Stat(root.Dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", root.Dir)
	}
	if err := r.readPolicy(root.Dir, root.Name); err != nil {
		return err
	}
	// skills/<category>/<name>/SKILL.md
	cats, _ := os.ReadDir(filepath.Join(root.Dir, "skills"))
	for _, c := range cats {
		if !c.IsDir() || strings.HasPrefix(c.Name(), ".") {
			continue
		}
		skills, _ := os.ReadDir(filepath.Join(root.Dir, "skills", c.Name()))
		for _, s := range skills {
			dir := filepath.Join(root.Dir, "skills", c.Name(), s.Name())
			if !s.IsDir() || !exists(filepath.Join(dir, "SKILL.md")) {
				continue
			}
			sk, err := canon.LoadSkill(dir)
			if err != nil {
				return err
			}
			sum, err := lock.TreeSum(dir)
			if err != nil {
				return err
			}
			if err := r.add(root, "skill", sk.Name, "skills/"+c.Name()+"/"+s.Name(), sum, &Item{Skill: &sk, Desc: sk.Description()}); err != nil {
				return err
			}
		}
	}
	// agents/<name>.md
	if err := eachFile(filepath.Join(root.Dir, "agents"), ".md", func(p string, b []byte) error {
		a, err := canon.LoadSubagent(p)
		if err != nil {
			return err
		}
		d, _ := a.Frontmatter["description"].(string)
		return r.add(root, "agent", a.Name, "agents/"+filepath.Base(p), lock.Sum(b), &Item{Agent: &a, Desc: d, raw: b})
	}); err != nil {
		return err
	}
	// instructions/<name>.md (not include/)
	if err := eachFile(filepath.Join(root.Dir, "instructions"), ".md", func(p string, b []byte) error {
		name := strings.TrimSuffix(filepath.Base(p), ".md")
		src := "instructions/" + filepath.Base(p)
		in := canon.Instruction{Name: root.Name + "/" + src, Text: string(b)}
		return r.add(root, "instruction", name, src, lock.Sum(b), &Item{Instruction: &in, raw: b})
	}); err != nil {
		return err
	}
	// hooks.toml
	var hooks struct {
		Hook []canon.Hook `toml:"hook"`
	}
	if b, ok, err := readTOML(filepath.Join(root.Dir, "hooks.toml"), &hooks); err != nil {
		return err
	} else if ok {
		_ = b
		for i := range hooks.Hook {
			h := hooks.Hook[i]
			it := &Item{Hook: &h}
			if h.Script != "" {
				sb, err := os.ReadFile(filepath.Join(root.Dir, "hooks", h.Script))
				if err != nil {
					return fmt.Errorf("hook %s: %w", h.Name, err)
				}
				it.HookScript = sb
			}
			if err := r.add(root, "hook", h.Name, "hooks.toml", hookSum(h, it.HookScript), it); err != nil {
				return err
			}
		}
	}
	// permissions.toml
	var perms struct {
		Permission []struct {
			Name     string         `toml:"name"`
			Pattern  string         `toml:"pattern"`
			Decision canon.Decision `toml:"decision"`
		} `toml:"permission"`
	}
	if _, ok, err := readTOML(filepath.Join(root.Dir, "permissions.toml"), &perms); err != nil {
		return err
	} else if ok {
		for _, p := range perms.Permission {
			pp := canon.Permission{Pattern: p.Pattern, Decision: p.Decision}
			if err := r.add(root, "permission", p.Name, "permissions.toml", jsonSum(pp), &Item{Permission: &pp}); err != nil {
				return err
			}
		}
	}
	// agents.toml / agents.base.toml
	for _, f := range []string{"agents.base.toml", "agents.toml"} {
		var m manifestFile
		if _, ok, err := readTOML(filepath.Join(root.Dir, f), &m); err != nil {
			return err
		} else if !ok {
			continue
		}
		for i := range m.MCP {
			mc := m.MCP[i]
			if err := r.add(root, "mcp", mc.Name, f, jsonSum(mc), &Item{MCP: &mc}); err != nil {
				return err
			}
		}
		decls := []struct {
			kind, harness string
			list          []map[string]any
		}{
			{"plugin", "claude", m.Claude.Plugin}, {"plugin", "codex", m.Codex.Plugin},
			{"marketplace", "claude", m.Claude.Marketplace}, {"marketplace", "codex", m.Codex.Marketplace},
			{"tool", "", m.Tool},
		}
		for _, d := range decls {
			for _, x := range d.list {
				name := declName(x)
				if name == "" {
					continue
				}
				if d.harness != "" {
					x["harness"] = d.harness
				}
				if err := r.add(root, d.kind, name, f, jsonSum(x), &Item{Decl: x}); err != nil {
					return err
				}
			}
		}
		for _, x := range m.Hook {
			if _, ok := x["installer"]; !ok {
				continue
			}
			if err := r.add(root, "installer", declName(x), f, jsonSum(x), &Item{Decl: x}); err != nil {
				return err
			}
		}
	}
	// presets/<name>.toml → bundles
	return eachFile(filepath.Join(root.Dir, "presets"), ".toml", func(p string, b []byte) error {
		var pr Preset
		if err := toml.Unmarshal(b, &pr); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		name := strings.TrimSuffix(filepath.Base(p), ".toml")
		return r.add(root, "bundle", name, "presets/"+filepath.Base(p), lock.Sum(b), &Item{Bundle: &pr, raw: b})
	})
}

type manifestFile struct {
	MCP    []canon.MCP `toml:"mcp"`
	Claude struct {
		Plugin      []map[string]any `toml:"plugin"`
		Marketplace []map[string]any `toml:"marketplace"`
	} `toml:"claude"`
	Codex struct {
		Plugin      []map[string]any `toml:"plugin"`
		Marketplace []map[string]any `toml:"marketplace"`
	} `toml:"codex"`
	Tool []map[string]any `toml:"tool"`
	Hook []map[string]any `toml:"hook"`
}

// declName: a plugin's id before the @ ("codex@openai-codex" → "codex"),
// else name.
func declName(x map[string]any) string {
	if id, ok := x["id"].(string); ok && id != "" {
		return strings.SplitN(id, "@", 2)[0]
	}
	n, _ := x["name"].(string)
	return n
}

// hookSum is a hook's content hash: its declaration and its script.
func hookSum(h canon.Hook, script []byte) string {
	decl, _ := json.Marshal(h)
	parts := []string{string(decl)}
	if h.Script != "" {
		parts = append(parts, lock.Sum(script))
	}
	return lock.SumString(strings.Join(parts, "\n"))
}

// jsonSum hashes a declaration by its JSON encoding.
func jsonSum(v any) string {
	b, _ := json.Marshal(v)
	return lock.Sum(b)
}

func readTOML(path string, v any) ([]byte, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := toml.Unmarshal(b, v); err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	return b, true, nil
}

func eachFile(dir, ext string, fn func(string, []byte) error) error {
	es, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range es {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ext) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := fn(p, b); err != nil {
			return err
		}
	}
	return nil
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
