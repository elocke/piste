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

// The built registry: an index and a content tree,
//
//	registry.json                          the index: machine zone, roots, every item's Meta
//	content/<zone>/<kind>/<name>@<hash12>/ the item's payload
//
// built for one machine zone (N + P, or N + W), so a work registry has no
// personal item to offer. Nix hosts link it at ~/.local/share/piste-agents/
// registry; devcontainers carry it in the zone bundle; Nix-free machines
// pull it. Load re-hashes every payload against the index.

// IndexFile is the registry index's name.
const IndexFile = "registry.json"

// FormatVersion of registry.json.
const FormatVersion = 1

// Index is registry.json.
type Index struct {
	Version   int          `json:"version"`
	Zone      string       `json:"zone"`
	Generator string       `json:"generator"`
	Roots     []IndexRoot  `json:"roots"`
	Items     []IndexEntry `json:"items"`
}

// IndexRoot is one zone root the registry was built from.
type IndexRoot struct {
	Name   string `json:"name"`
	Zone   string `json:"zone"`
	Commit string `json:"commit,omitempty"`
}

// IndexEntry is one item: its metadata and where its payload is.
type IndexEntry struct {
	Meta
	Path       string      `json:"path"` // the source path within its root
	Category   string      `json:"category,omitempty"`
	ThirdParty *ThirdParty `json:"third_party,omitempty"`
	Content    string      `json:"content"` // relative to the registry dir
}

// declFile holds a declared item's canonical form in its content dir.
const declFile = "item.json"

type declPayload struct {
	Hook       *canon.Hook       `json:"hook,omitempty"`
	Permission *canon.Permission `json:"permission,omitempty"`
	MCP        *canon.MCP        `json:"mcp,omitempty"`
	Decl       map[string]any    `json:"decl,omitempty"`
}

// Write builds the registry artifact for machine zone z at out (replaced
// whole, through a sibling temporary directory).
func (r *Registry) Write(z, out string) error {
	if err := zone.ValidMachine(z); err != nil {
		return fmt.Errorf("registry %w", err)
	}
	tmp := out + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	idx := Index{Version: FormatVersion, Zone: z, Generator: "piste agents registry build"}
	for _, root := range r.Roots {
		idx.Roots = append(idx.Roots, IndexRoot{Name: root.Name, Zone: root.Zone, Commit: r.Revs[root.Name]})
	}
	for _, id := range r.IDs() {
		it := r.Items[id]
		if it.ID.Zone != zone.Shared && it.ID.Zone != z {
			return fmt.Errorf("%s is %s content; this is a %s registry", id, it.ID.Zone, z)
		}
		rel := filepath.ToSlash(filepath.Join("content", it.ID.Zone, it.ID.Kind, it.ID.Name+"@"+it.SHA256[:12]))
		e := IndexEntry{Meta: r.Meta(it), Path: it.Source, ThirdParty: it.ThirdParty, Content: rel}
		if it.Skill != nil {
			e.Category = it.Skill.Category
		}
		if err := writePayload(it, filepath.Join(tmp, rel)); err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		idx.Items = append(idx.Items, e)
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, IndexFile), append(b, '\n'), 0o644); err != nil {
		return err
	}
	// Verify what was written before it replaces anything.
	if _, err := Load(tmp); err != nil {
		return fmt.Errorf("built registry does not load back: %w", err)
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

func writePayload(it *Item, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	switch {
	case it.Skill != nil:
		return copyTree(it.Skill.Dir, filepath.Join(dir, it.Skill.Name))
	case it.raw != nil:
		return os.WriteFile(filepath.Join(dir, filepath.Base(it.Source)), it.raw, 0o644)
	case it.Hook != nil:
		if it.Hook.Script != "" {
			if err := os.WriteFile(filepath.Join(dir, "script"), it.HookScript, 0o755); err != nil {
				return err
			}
		}
		return writeJSON(filepath.Join(dir, declFile), declPayload{Hook: it.Hook})
	case it.Permission != nil:
		return writeJSON(filepath.Join(dir, declFile), declPayload{Permission: it.Permission})
	case it.MCP != nil:
		return writeJSON(filepath.Join(dir, declFile), declPayload{MCP: it.MCP})
	case it.Decl != nil:
		return writeJSON(filepath.Join(dir, declFile), declPayload{Decl: it.Decl})
	}
	return errors.New("item has no payload")
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// copyTree copies regular files (keeping the executable bit) and symlinks
// (as links), skipping .git, so TreeSum of the copy equals the source's.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			if d.Name() == ".git" && p != src {
				return filepath.SkipDir
			}
			return os.MkdirAll(to, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			t, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(t, to)
		case d.Type().IsRegular():
			fi, err := d.Info()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if fi.Mode()&0o111 != 0 {
				mode = 0o755
			}
			return os.WriteFile(to, b, mode)
		}
		return nil
	})
}

// Load reads a built registry and re-hashes every payload against its
// index entry: a changed or missing payload is an error, never a silent
// substitute.
func Load(dir string) (*Registry, error) {
	b, err := os.ReadFile(filepath.Join(dir, IndexFile))
	if err != nil {
		return nil, err
	}
	var idx Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, fmt.Errorf("%s: %w", IndexFile, err)
	}
	if idx.Version != FormatVersion {
		return nil, fmt.Errorf("%s: version %d; this piste reads %d", IndexFile, idx.Version, FormatVersion)
	}
	if err := zone.ValidMachine(idx.Zone); err != nil {
		return nil, fmt.Errorf("%s: %w", IndexFile, err)
	}
	r := &Registry{Items: map[string]*Item{}, Dir: dir, Zone: idx.Zone, Revs: map[string]string{}}
	for _, root := range idx.Roots {
		r.Revs[root.Name] = root.Commit
	}
	for i := range idx.Items {
		e := idx.Items[i]
		it, err := loadEntry(dir, e)
		if err != nil {
			return nil, fmt.Errorf("registry %s: %s: %w", dir, e.ID, err)
		}
		if it.ID.Zone != zone.Shared && it.ID.Zone != idx.Zone {
			return nil, fmt.Errorf("registry %s: %s is %s content in a %s registry", dir, e.ID, it.ID.Zone, idx.Zone)
		}
		if _, dup := r.Items[e.ID]; dup {
			return nil, fmt.Errorf("registry %s: %s listed twice", dir, e.ID)
		}
		r.Items[e.ID] = it
	}
	return r, nil
}

func loadEntry(dir string, e IndexEntry) (*Item, error) {
	id, err := zone.ParseID(e.ID)
	if err != nil {
		return nil, err
	}
	if _, ok := Kinds[id.Kind]; !ok {
		return nil, fmt.Errorf("unknown kind %q", id.Kind)
	}
	if strings.Contains(e.Content, "..") || filepath.IsAbs(e.Content) {
		return nil, fmt.Errorf("content path %q leaves the registry", e.Content)
	}
	cdir := filepath.Join(dir, filepath.FromSlash(e.Content))
	meta := e.Meta
	it := &Item{ID: id, Root: e.Root, Source: e.Path, Desc: e.Description, ThirdParty: e.ThirdParty, meta: &meta}
	var sum string
	switch id.Kind {
	case "skill", "3p-skill":
		sd := filepath.Join(cdir, id.Name)
		sk, err := loadSkillAs(sd, id.Name, e.Category)
		if err != nil {
			return nil, err
		}
		it.Skill = &sk
		if sum, err = lock.TreeSum(sd); err != nil {
			return nil, err
		}
	case "agent", "instruction", "bundle":
		p := filepath.Join(cdir, filepath.Base(e.Path))
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		it.raw, sum = b, lock.Sum(b)
		switch id.Kind {
		case "agent":
			a, err := canon.LoadSubagent(p)
			if err != nil {
				return nil, err
			}
			it.Agent = &a
		case "instruction":
			it.Instruction = &canon.Instruction{Name: e.Root + "/" + e.Path, Text: string(b)}
		case "bundle":
			var pr Preset
			if err := toml.Unmarshal(b, &pr); err != nil {
				return nil, err
			}
			it.Bundle = &pr
		}
	default:
		b, err := os.ReadFile(filepath.Join(cdir, declFile))
		if err != nil {
			return nil, err
		}
		var d declPayload
		if err := json.Unmarshal(b, &d); err != nil {
			return nil, err
		}
		switch {
		case d.Hook != nil:
			it.Hook = d.Hook
			if d.Hook.Script != "" {
				if it.HookScript, err = os.ReadFile(filepath.Join(cdir, "script")); err != nil {
					return nil, err
				}
			}
			sum = hookSum(*d.Hook, it.HookScript)
		case d.Permission != nil:
			it.Permission, sum = d.Permission, jsonSum(*d.Permission)
		case d.MCP != nil:
			it.MCP, sum = d.MCP, jsonSum(*d.MCP)
		case d.Decl != nil:
			it.Decl, sum = d.Decl, jsonSum(d.Decl)
		default:
			return nil, errors.New("empty " + declFile)
		}
	}
	if sum != e.ContentHash {
		return nil, fmt.Errorf("payload hash %s != index %s (the registry was changed after it was built)", short(sum), short(e.ContentHash))
	}
	it.SHA256 = sum
	return it, nil
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// Summary counts items per kind, for build and pull output.
func (r *Registry) Summary() string {
	n := map[string]int{}
	for _, it := range r.Items {
		n[it.ID.Kind]++
	}
	ks := make([]string, 0, len(n))
	for k := range n {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, 0, len(ks))
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("%d %s", n[k], k))
	}
	return strings.Join(parts, ", ")
}
