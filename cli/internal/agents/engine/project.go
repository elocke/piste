package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/adapter"
	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/plan"
	"github.com/elocke/piste/cli/internal/agents/project"
	"github.com/elocke/piste/cli/internal/agents/registry"
	"github.com/elocke/piste/cli/internal/agents/safety"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

// Project is one project-scope session.
type Project struct {
	Root     string
	Manifest *project.Manifest
	Text     string // manifest text as read
	Lock     *lock.Lock
	Reg      *registry.Registry
	Force    bool
	Warnings []string

	// Adopt: owned ids Sync may take over (init --adopt).
	Adopt map[string]bool

	next     *lock.Lock        // the lock Sync computed, written by Commit
	expected map[string][]byte // what piste last wrote, for refusal diffs
}

// Open loads the manifest and lock of root against a registry.
func Open(root string, reg *registry.Registry) (*Project, error) {
	m, text, err := project.Load(root)
	if err != nil {
		return nil, err
	}
	l, err := lock.Read(filepath.Join(root, project.LockFile))
	if err != nil {
		return nil, err
	}
	if l.Journal != nil {
		return nil, fmt.Errorf("%s records an interrupted apply (%s): %s; check the files, then delete the [journal] table",
			project.LockFile, l.Journal.Started, strings.Join(l.Journal.Changes, ", "))
	}
	return &Project{Root: root, Manifest: m, Text: text, Lock: l, Reg: reg}, nil
}

// admit resolves a used id against the zone boundary and the registry.
func (p *Project) admit(id string) (*registry.Item, error) {
	zid, err := zone.ParseID(id)
	if err != nil {
		return nil, err
	}
	if err := zone.Admit(p.Manifest.Zone, zid); err != nil {
		return nil, err
	}
	it, ok := p.Reg.Get(id)
	if !ok {
		return nil, fmt.Errorf("%s is not in this machine's registry (zone roots: %s)", id, rootNames(p.Reg))
	}
	// A third-party item must be allowed by its zone's [policy] (and it is
	// locked, or the registry would not offer it): a project can't bring
	// in a new third-party source.
	if it.ThirdParty != nil && p.Reg.Meta(it).AllowedBy == "" {
		return nil, fmt.Errorf("%s: %s is not allowed by the %s zone [policy] (refused)", id, it.ThirdParty.Source, it.ID.Zone)
	}
	return it, nil
}

func rootNames(r *registry.Registry) string {
	if r.Dir != "" {
		return "registry " + r.Dir
	}
	var n []string
	for _, x := range r.Roots {
		n = append(n, x.Name+"="+x.Zone)
	}
	if len(n) == 0 {
		return "none; pass --zone-root or a manifest"
	}
	return strings.Join(n, ", ")
}

type rendered struct {
	item string
	arts []adapter.Artifact
}

func has(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Render produces every artifact the manifest asks for, grouped by owning
// item (aggregates like the AGENTS.md block own under "project:<what>").
func (p *Project) Render() ([]rendered, []lock.Entry, error) {
	hs := p.Manifest.Effective()
	claude, codex := has(hs, canon.Claude), has(hs, canon.Codex)
	var out []rendered
	var entries []lock.Entry
	var frags []canon.Instruction
	var mcps []canon.MCP
	var perms []canon.Permission
	var errs []error
	add := func(item string, r adapter.Result, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", item, err))
			return
		}
		p.Warnings = append(p.Warnings, r.Warnings...)
		if len(r.Artifacts) > 0 {
			out = append(out, rendered{item, r.Artifacts})
		}
	}
	for _, u := range p.Manifest.Use {
		it, err := p.admit(u.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		li := lock.Item{ID: u.ID, Name: it.ID.Name, Source: it.Root + ":" + it.Source, SHA256: it.SHA256}
		kind := registry.Kinds[it.ID.Kind]
		switch it.ID.Kind {
		case "skill", "3p-skill":
			if safety.HasDeceptiveHidden(it.Skill.Body) || safety.HasDeceptiveHidden(it.Skill.Description()) {
				errs = append(errs, fmt.Errorf("%s: SKILL.md contains hidden or bidi control characters", u.ID))
				continue
			}
			// .agents/skills/<n> is the one copy: Codex reads it directly and
			// Claude through the link. With Claude in the project, it keeps
			// Claude's frontmatter (Codex ignores unknown keys); otherwise
			// Claude-only keys are dropped.
			render := canon.AgentsMD
			if claude {
				render = canon.Claude
			}
			r, err := adapter.Skill(render, canon.Project, *it.Skill, adapter.Copy, "")
			if err == nil && claude {
				r.Artifacts[0].File = ".agents/skills/" + it.Skill.Name
				r.Artifacts[0].Harness = canon.AgentsMD
			}
			if err == nil && claude {
				l, lerr := adapter.Skill(canon.Claude, canon.Project, *it.Skill, adapter.Link, "../../.agents/skills/"+it.Skill.Name)
				r.Artifacts = append(r.Artifacts, l.Artifacts...)
				err = lerr
			}
			add(u.ID, r, err)
		case "agent":
			if claude {
				r, err := adapter.Subagent(canon.Claude, *it.Agent)
				add(u.ID, r, err)
			}
			if codex {
				r, err := adapter.Subagent(canon.Codex, *it.Agent)
				add(u.ID, r, err)
			}
		case "instruction":
			frags = append(frags, *it.Instruction)
		case "hook":
			h := *it.Hook
			var r adapter.Result
			if h.Script != "" {
				r.Artifacts = append(r.Artifacts, adapter.Artifact{Kind: lock.KindHook, Item: h.Name,
					File: ".agents/hooks/" + h.Script, Whole: it.HookScript, Mode: 0o755})
			}
			cmd := func(h canon.Hook) (string, error) {
				if h.Script != "" {
					return "./.agents/hooks/" + h.Script, nil
				}
				return h.Command, nil
			}
			for _, harness := range []string{canon.Claude, canon.Codex} {
				if !has(hs, harness) {
					continue
				}
				hr, err := adapter.Hooks(harness, canon.Project, []canon.Hook{h}, cmd)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", u.ID, err))
					continue
				}
				r.Artifacts = append(r.Artifacts, hr.Artifacts...)
				r.Warnings = append(r.Warnings, hr.Warnings...)
			}
			add(u.ID, r, nil)
		case "permission":
			perms = append(perms, *it.Permission)
			if claude {
				r, err := adapter.Permissions(canon.Claude, []canon.Permission{*it.Permission})
				add(u.ID, r, err)
			}
		case "mcp":
			mcps = append(mcps, *it.MCP)
			if claude {
				r, err := adapter.MCPServers(canon.Claude, canon.Project, []canon.MCP{*it.MCP})
				add(u.ID, r, err)
			}
		case "plugin":
			r, err := p.pluginArtifacts(it)
			add(u.ID, r, err)
			li.Harness = declString(it.Decl, "harness")
			li.Name = pluginID(it)
		case "marketplace":
			r, err := p.marketplaceArtifacts(it)
			add(u.ID, r, err)
			li.Harness = declString(it.Decl, "harness")
		case "tool", "installer":
			// Recorded in the lock only: installed by Nix or the zone's
			// pinned installer, never by a project.
			li.Version = declString(it.Decl, "version")
		case "bundle":
			r, err := p.bundleArtifacts(it)
			add(u.ID, r, err)
		default:
			errs = append(errs, fmt.Errorf("%s: kind %q can't be used in a project", u.ID, it.ID.Kind))
			continue
		}
		entries = append(entries, lock.Entry{Kind: kind, Item: li})
	}
	for _, s := range p.Manifest.MCP {
		mcps = append(mcps, s)
		if claude {
			r, err := adapter.MCPServers(canon.Claude, canon.Project, []canon.MCP{s})
			add("local:mcp/"+s.Name, r, err)
		}
		b, _ := json.Marshal(s)
		entries = append(entries, lock.Entry{Kind: lock.KindMCP, Item: lock.Item{ID: "local:mcp/" + s.Name, Name: s.Name, Source: project.ManifestFile, SHA256: lock.Sum(b)}})
	}
	// Aggregates: one owned block / file for all items of a kind.
	if len(frags) > 0 {
		r, err := adapter.Instructions(canon.AgentsMD, canon.Project, frags)
		add("project:instructions", r, err)
		if claude {
			r, err := adapter.Instructions(canon.Claude, canon.Project, frags)
			add("project:instructions", r, err)
		}
	}
	if codex && len(mcps) > 0 {
		r, err := adapter.MCPServers(canon.Codex, canon.Project, mcps)
		add("project:codex-mcp", r, err)
	}
	if codex && len(perms) > 0 {
		r, err := adapter.Permissions(canon.Codex, perms)
		add("project:codex-rules", r, err)
	}
	return out, entries, errors.Join(errs...)
}

func declString(d map[string]any, k string) string {
	s, _ := d[k].(string)
	return s
}

func pluginID(it *registry.Item) string {
	if id := declString(it.Decl, "id"); id != "" {
		return id
	}
	return it.ID.Name
}

// Plugins: .claude/settings.json enabledPlugins["<id>"] = true.
func (p *Project) pluginArtifacts(it *registry.Item) (adapter.Result, error) {
	var r adapter.Result
	if declString(it.Decl, "harness") != "claude" {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s: codex plugins have no project scope; recorded in the lock only", it.ID))
		return r, nil
	}
	if !has(p.Manifest.Effective(), canon.Claude) {
		return r, nil
	}
	r.Artifacts = append(r.Artifacts, adapter.Artifact{Harness: canon.Claude, Kind: lock.KindPlugin, Item: it.ID.String(),
		File: ".claude/settings.json", JSONPointer: edit.Pointer("enabledPlugins", pluginID(it)), JSONValue: []byte("true")})
	return r, nil
}

// Marketplaces: .claude/settings.json extraKnownMarketplaces.
func (p *Project) marketplaceArtifacts(it *registry.Item) (adapter.Result, error) {
	var r adapter.Result
	if declString(it.Decl, "harness") != "claude" || !has(p.Manifest.Effective(), canon.Claude) {
		return r, nil
	}
	src := declString(it.Decl, "source")
	var source map[string]string
	switch {
	case strings.HasPrefix(src, "github:"):
		source = map[string]string{"source": "github", "repo": strings.TrimPrefix(src, "github:")}
	case strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "git+"):
		source = map[string]string{"source": "git", "url": strings.TrimPrefix(src, "git+")}
	default:
		return r, fmt.Errorf("marketplace source %q: want github:owner/repo or a git URL", src)
	}
	v, _ := json.Marshal(map[string]any{"source": source})
	r.Artifacts = append(r.Artifacts, adapter.Artifact{Harness: canon.Claude, Kind: lock.KindMarketplace, Item: it.ID.String(),
		File: ".claude/settings.json", JSONPointer: edit.Pointer("extraKnownMarketplaces", it.ID.Name), JSONValue: v})
	return r, nil
}

// Bundles (vendor): a preset's skills rendered whole for Hermes under
// .agents/bundles/<name>/, with a manifest.json listing exactly what is in
// it. A skill removed from the preset is simply absent.
func (p *Project) bundleArtifacts(it *registry.Item) (adapter.Result, error) {
	r, _, err := RenderBundle(p.Reg, p.Manifest.Zone, it)
	if err != nil {
		return r, err
	}
	for i := range r.Artifacts {
		r.Artifacts[i].File = ".agents/bundles/" + it.ID.Name + "/" + r.Artifacts[i].File
	}
	return r, nil
}

// BundleManifest is manifest.json of a bundle.
type BundleManifest struct {
	Bundle string            `json:"bundle"`
	Skills map[string]string `json:"skills"` // name → tree sha256
}

// RenderBundle renders a bundle (preset) for Hermes: skills/<category>/<n>
// trees plus manifest.json, as one owned directory.
func RenderBundle(reg *registry.Registry, z string, it *registry.Item) (adapter.Result, BundleManifest, error) {
	var r adapter.Result
	bm := BundleManifest{Bundle: it.ID.String(), Skills: map[string]string{}}
	tree := map[string]adapter.TreeFile{}
	for _, name := range it.Bundle.Skills {
		var found *registry.Item
		for _, c := range reg.Find("skill", name) {
			if c.ID.Zone == zone.Shared || c.ID.Zone == z {
				if found != nil {
					return r, bm, fmt.Errorf("bundle %s: skill %s is in two zones", it.ID, name)
				}
				found = c
			}
		}
		if found == nil {
			return r, bm, fmt.Errorf("bundle %s: skill %s is not in the registry", it.ID, name)
		}
		sr, err := adapter.Skill(canon.Hermes, canon.Bundle, *found.Skill, adapter.Copy, "")
		if err != nil {
			return r, bm, err
		}
		r.Warnings = append(r.Warnings, sr.Warnings...)
		a := sr.Artifacts[0]
		mem := map[string]lock.MemFile{}
		for n, f := range a.Tree {
			tree[a.File+"/"+n] = f
			mem[n] = lock.MemFile{Data: f.Data, Exec: f.Mode&0o111 != 0}
		}
		bm.Skills[name] = lock.MemTreeSum(mem)
	}
	mb, _ := json.MarshalIndent(bm, "", "  ")
	tree["manifest.json"] = adapter.TreeFile{Data: append(mb, '\n'), Mode: 0o644}
	r.Artifacts = append(r.Artifacts, adapter.Artifact{Harness: canon.Hermes, Kind: lock.KindBundle, Item: it.ID.String(), File: "", Tree: tree})
	return r, bm, nil
}

// Sync plans the project to match its manifest.
func (p *Project) Sync() (*plan.Plan, error) {
	rs, entries, err := p.Render()
	if err != nil {
		return nil, err
	}
	pl := plan.New(p.Root, p.Lock)
	pl.Force = p.Force
	pl.Expected = map[string][]byte{}
	for k, v := range p.expected {
		pl.Expected[k] = v
	}
	o := &Owner{Plan: pl, Prev: p.Lock, Force: p.Force, Adopt: p.Adopt}
	want := map[string]bool{}
	var errs []error
	for _, r := range rs {
		for _, a := range r.arts {
			a.File = strings.TrimSuffix(a.File, "/")
			if a.Whole != nil {
				pl.Expected[a.File] = a.Whole
			}
			owned, err := o.Put(a, r.item)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, w := range owned {
				want[w.ID()] = true
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if err := o.Prune(want); err != nil {
		return nil, err
	}
	// The lock's items are exactly what the manifest uses now. Each item's
	// SHA256 is its registry source hash (update detection); what was
	// written is covered by the [[owned]] entries, which name their item.
	next := &lock.Lock{Version: lock.Version, Zone: p.Manifest.Zone, Owned: p.Lock.Owned}
	for _, e := range entries {
		next.Put(e.Kind, e.Item)
	}
	// No zone roots in a project lock: it is committed, so it must not
	// depend on where this machine's registry lives; the items' hashes pin
	// the content.
	p.next = next
	return pl, nil
}

// Commit applies a Sync plan and writes the lock (journalled).
func (p *Project) Commit(pl *plan.Plan) error {
	next := p.next
	journal := func(j *lock.Journal) error {
		next.Journal = j
		next.Owned = p.Lock.Owned
		return WriteLock(p.Root, project.LockFile, next)
	}
	if err := pl.Apply(journal); err != nil {
		return err
	}
	next.Owned, next.Journal = p.Lock.Owned, nil
	// Item hashes of rendered files are verified through the owned entries;
	// the item's own SHA256 stays the registry source hash.
	return WriteLock(p.Root, project.LockFile, next)
}
