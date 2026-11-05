// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/lib/sources-lock.ts, src/lib/sources.ts. Copyright (c) 2024 dyoshikawa.
// MIT License; see cli/THIRD_PARTY_NOTICES.md.

// Package lock is the agents engine's lock.
//
// One TOML shape serves three files:
//
//   - a zone's committed agents.lock (third-party items, written by
//     `piste agents update`);
//   - a project's .agents/agents.lock (the items it uses, plus the [[owned]]
//     record of every file, key and entry piste wrote);
//   - the per-machine user-scope state (~/.local/state/piste-agents/state.toml:
//     the [[owned]] record for $HOME).
//
// Item sections mirror agents.toml ([[skill]], [[claude.plugin]], [[mcp]] …)
// so a lock entry sits next to its declaration. Unknown fields are ignored
// on read, so a lock written by a newer piste still loads.
package lock

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Version of the lock format.
const Version = 1

// Kind is an artifact kind. Every kind is hashed.
type Kind string

const (
	KindSkill       Kind = "skill"
	KindSubagent    Kind = "subagent"
	KindInstruction Kind = "instruction"
	KindHook        Kind = "hook"
	KindMCP         Kind = "mcp"
	KindPlugin      Kind = "plugin"
	KindMarketplace Kind = "marketplace"
	KindInstaller   Kind = "installer"
	KindTool        Kind = "tool"
	KindBundle      Kind = "bundle"
)

// Kinds lists all ten, in lock order.
var Kinds = []Kind{KindSkill, KindSubagent, KindInstruction, KindHook, KindMCP,
	KindPlugin, KindMarketplace, KindInstaller, KindTool, KindBundle}

// Item is one locked artifact. Which fields are set depends on the kind:
// skills and marketplaces resolve to a commit (+ narHash when Nix fetches
// them); npm/uv things to an exact version + integrity; plugins to
// id@version + commit; everything to a content SHA256 of what is on disk.
type Item struct {
	Name      string   `toml:"name,omitempty"`
	ID        string   `toml:"id,omitempty"` // registry id or plugin id
	Harness   string   `toml:"harness,omitempty"`
	Source    string   `toml:"source,omitempty"`
	Path      string   `toml:"path,omitempty"`
	Ref       string   `toml:"ref,omitempty"`
	Commit    string   `toml:"commit,omitempty"`
	NarHash   string   `toml:"narHash,omitempty"`
	Version   string   `toml:"version,omitempty"`
	Integrity string   `toml:"integrity,omitempty"`
	Package   string   `toml:"package,omitempty"`      // mcp: npm:/uvx: package
	GitCommit string   `toml:"gitCommitSha,omitempty"` // claude plugin commit (installed_plugins.json)
	Installer string   `toml:"installer,omitempty"`
	SHA256    string   `toml:"sha256,omitempty"`
	Scope     string   `toml:"scope,omitempty"`
	Targets   []string `toml:"targets,omitempty"`
	Fetched   string   `toml:"fetched,omitempty"`
	// Files are the owned paths (relative to the lock's root) this item's
	// content hash covers, for items rendered into the tree.
	Files []string `toml:"files,omitempty"`
}

// Key identifies an item within its kind.
func (it Item) Key() string {
	if it.ID != "" {
		return it.ID
	}
	return it.Name
}

// Harness sections ([[claude.plugin]], [[codex.marketplace]] …).
type Harness struct {
	Marketplace []Item `toml:"marketplace,omitempty"`
	Plugin      []Item `toml:"plugin,omitempty"`
}

// ZoneRoot is an explicit zone root the render read: only these are
// inputs, and check re-hashes them.
type ZoneRoot struct {
	Name   string `toml:"name"`
	Zone   string `toml:"zone"`
	Root   string `toml:"root"`
	SHA256 string `toml:"sha256,omitempty"`
}

// OwnedKind says how an owned path is read back.
type OwnedKind string

const (
	OwnedFile      OwnedKind = "file"       // whole file we created
	OwnedLink      OwnedKind = "link"       // symlink we created (SHA256 = LinkSum(target))
	OwnedDir       OwnedKind = "dir"        // directory tree we copied (TreeSum)
	OwnedJSON      OwnedKind = "json"       // one value at a JSON pointer
	OwnedJSONEntry OwnedKind = "json-entry" // one element of the array at Path, found by its hash
	OwnedTOMLBlock OwnedKind = "toml-block" // piste managed block Path in a TOML file
	OwnedMDBlock   OwnedKind = "md-block"   // piste-agents managed block in a Markdown file
)

// Owned is one thing piste wrote: removal and drift only ever
// consider these, and only while their hash still matches.
type Owned struct {
	File   string    `toml:"file"` // relative to the lock's root
	Kind   OwnedKind `toml:"kind"`
	Path   string    `toml:"path,omitempty"` // JSON pointer, TOML block id
	Item   string    `toml:"item,omitempty"` // the item it belongs to (kind:key)
	SHA256 string    `toml:"sha256"`
	Target string    `toml:"target,omitempty"` // for links
	// Created lists what piste created along with this entry: JSON pointers of
	// parent objects/arrays, and "#file" when the file itself was new. On
	// removal each is deleted again, but only if it is empty by then.
	Created []string `toml:"created,omitempty"`
}

// ID is the owned entry's identity.
func (o Owned) ID() string {
	if o.Path == "" {
		return o.File
	}
	return o.File + "#" + o.Path + "@" + string(o.Kind)
}

// Journal records an apply in progress. A leftover journal means an
// apply was interrupted; the next run reports it before doing anything.
type Journal struct {
	State   string   `toml:"state"` // "applying"
	Started string   `toml:"started"`
	Changes []string `toml:"changes"`
}

// Lock is the whole file.
type Lock struct {
	Version     int        `toml:"version"`
	Zone        string     `toml:"zone,omitempty"` // the project's / machine's zone
	Roots       []ZoneRoot `toml:"root,omitempty"`
	Skill       []Item     `toml:"skill,omitempty"`
	Subagent    []Item     `toml:"subagent,omitempty"`
	Instruction []Item     `toml:"instruction,omitempty"`
	Hook        []Item     `toml:"hook,omitempty"`
	MCP         []Item     `toml:"mcp,omitempty"`
	Claude      Harness    `toml:"claude,omitempty"`
	Codex       Harness    `toml:"codex,omitempty"`
	Installer   []Item     `toml:"installer,omitempty"`
	Tool        []Item     `toml:"tool,omitempty"`
	Bundle      []Item     `toml:"bundle,omitempty"`
	Owned       []Owned    `toml:"owned,omitempty"`
	Journal     *Journal   `toml:"journal,omitempty"`
}

// Entry is an item with its kind.
type Entry struct {
	Kind Kind
	Item Item
}

// Entries flattens every item section. [[hook]] items that name an
// installer are installers.
func (l *Lock) Entries() []Entry {
	var out []Entry
	add := func(k Kind, items []Item) {
		for _, it := range items {
			kk := k
			if k == KindHook && it.Installer != "" {
				kk = KindInstaller
			}
			out = append(out, Entry{kk, it})
		}
	}
	add(KindSkill, l.Skill)
	add(KindSubagent, l.Subagent)
	add(KindInstruction, l.Instruction)
	add(KindHook, l.Hook)
	add(KindMCP, l.MCP)
	add(KindPlugin, withHarness(l.Claude.Plugin, "claude"))
	add(KindPlugin, withHarness(l.Codex.Plugin, "codex"))
	add(KindMarketplace, withHarness(l.Claude.Marketplace, "claude"))
	add(KindMarketplace, withHarness(l.Codex.Marketplace, "codex"))
	add(KindInstaller, l.Installer)
	add(KindTool, l.Tool)
	add(KindBundle, l.Bundle)
	return out
}

func withHarness(items []Item, h string) []Item {
	out := make([]Item, len(items))
	for i, it := range items {
		if it.Harness == "" {
			it.Harness = h
		}
		out[i] = it
	}
	return out
}

// Put adds or replaces an item (matched by kind + harness + key).
func (l *Lock) Put(k Kind, it Item) {
	slot := l.slot(k, it.Harness)
	for i := range *slot {
		if (*slot)[i].Key() == it.Key() {
			(*slot)[i] = it
			return
		}
	}
	*slot = append(*slot, it)
}

// Drop removes an item; it reports whether one was there.
func (l *Lock) Drop(k Kind, harness, key string) bool {
	slot := l.slot(k, harness)
	for i := range *slot {
		if (*slot)[i].Key() == key {
			*slot = append((*slot)[:i], (*slot)[i+1:]...)
			return true
		}
	}
	return false
}

func (l *Lock) slot(k Kind, harness string) *[]Item {
	switch k {
	case KindSkill:
		return &l.Skill
	case KindSubagent:
		return &l.Subagent
	case KindInstruction:
		return &l.Instruction
	case KindHook:
		return &l.Hook
	case KindMCP:
		return &l.MCP
	case KindPlugin:
		if harness == "codex" {
			return &l.Codex.Plugin
		}
		return &l.Claude.Plugin
	case KindMarketplace:
		if harness == "codex" {
			return &l.Codex.Marketplace
		}
		return &l.Claude.Marketplace
	case KindInstaller:
		return &l.Installer
	case KindTool:
		return &l.Tool
	default:
		return &l.Bundle
	}
}

// Own records (or refreshes) an owned entry.
func (l *Lock) Own(o Owned) {
	for i := range l.Owned {
		if l.Owned[i].ID() == o.ID() {
			l.Owned[i] = o
			return
		}
	}
	l.Owned = append(l.Owned, o)
}

// Disown forgets an owned entry.
func (l *Lock) Disown(id string) {
	for i := range l.Owned {
		if l.Owned[i].ID() == id {
			l.Owned = append(l.Owned[:i], l.Owned[i+1:]...)
			return
		}
	}
}

// OwnedBy returns the owned entries of one item.
func (l *Lock) OwnedBy(item string) []Owned {
	var out []Owned
	for _, o := range l.Owned {
		if o.Item == item {
			out = append(out, o)
		}
	}
	return out
}

// IsOwnedLink reports whether abs (resolved against root) is a link we wrote.
func (l *Lock) IsOwnedLink(root, abs string) bool {
	for _, o := range l.Owned {
		if o.Kind == OwnedLink && Abs(root, o.File) == abs {
			return true
		}
	}
	return false
}

// Read loads a lock; a missing file is an empty lock.
func Read(path string) (*Lock, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Lock{Version: Version}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse decodes lock TOML.
func Parse(b []byte) (*Lock, error) {
	l := &Lock{}
	if err := toml.Unmarshal(b, l); err != nil {
		return nil, fmt.Errorf("agents.lock: %w", err)
	}
	if l.Version == 0 {
		l.Version = Version
	}
	if l.Version > Version {
		return nil, fmt.Errorf("agents.lock: version %d is newer than this piste understands (%d)", l.Version, Version)
	}
	return l, nil
}

// Marshal encodes the lock deterministically (sections sorted by key).
func (l *Lock) Marshal() ([]byte, error) {
	c := *l
	for _, s := range [][]Item{c.Skill, c.Subagent, c.Instruction, c.Hook, c.MCP, c.Claude.Plugin,
		c.Claude.Marketplace, c.Codex.Plugin, c.Codex.Marketplace, c.Installer, c.Tool, c.Bundle} {
		sort.SliceStable(s, func(i, j int) bool { return s[i].Key() < s[j].Key() })
	}
	sort.SliceStable(c.Owned, func(i, j int) bool { return c.Owned[i].ID() < c.Owned[j].ID() })
	sort.SliceStable(c.Roots, func(i, j int) bool { return c.Roots[i].Name < c.Roots[j].Name })
	var b bytes.Buffer
	b.WriteString("# Machine-written by `piste agents`; do not edit by hand.\n")
	enc := toml.NewEncoder(&b)
	enc.SetIndentTables(false)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Abs resolves a lock-relative path.
func Abs(root, rel string) string {
	if strings.HasPrefix(rel, "/") {
		return rel
	}
	return root + "/" + rel
}
