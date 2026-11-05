package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/elocke/piste/cli/internal/agents/adapter"
	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/registry"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

// Container bundles: a zone's presets rendered for
// the Claude Code and Codex *system* scopes of a devcontainer image, so the
// image never writes a $HOME file the harnesses rewrite at runtime. Layout
// (what piste.lib.mkDevcontainer installs):
//
//	etc/claude-code/CLAUDE.md                      managed memory (instructions)
//	etc/claude-code/.claude/skills/<name>/         managed skills
//	etc/claude-code/managed-settings.d/50-agents.json   enabledPlugins + marketplaces
//	etc/claude-code/managed-mcp.json               only with ManagedMCP (--managed-mcp)
//	etc/codex/skills/<name>/                       admin-scope skills
//	home/.codex/AGENTS.md                          seeded once (Codex has no system file)
//	manifest.json                                  zone, sources + revs, per-file sha256
//
// Hooks are not bundled yet: their scripts need interpreters the image may
// not have, so a preset naming hooks is an error rather than a broken hook.
// Third-party skills are not bundled yet either (only owned skills).

// ContainerOptions selects what a container bundle holds.
type ContainerOptions struct {
	Zone       string            // the machine / bundle zone (personal | work)
	Presets    []*registry.Item  // bundle items (presets), in order
	Targets    []string          // claude, codex
	SourceRevs map[string]string // root name → git revision (recorded, not verified)
	// ManagedMCP renders the zone's [[mcp]] into managed-mcp.json. Off by
	// default: Claude Code gives that file exclusive control of MCP (no user
	// or project servers at all), which is a zone policy choice.
	ManagedMCP bool
}

// ContainerManifest is manifest.json of a container bundle.
type ContainerManifest struct {
	Zone      string            `json:"zone"`
	Generator string            `json:"generator"`
	Presets   []string          `json:"presets"`
	Targets   []string          `json:"targets"`
	Sources   []ContainerSource `json:"sources"`
	Skills    []string          `json:"skills"`
	Plugins   []string          `json:"plugins"`
	Files     map[string]string `json:"files"` // path → sha256
}

// ContainerSource is one zone root the bundle was rendered from.
type ContainerSource struct {
	Name   string `json:"name"`
	Zone   string `json:"zone"`
	Rev    string `json:"rev,omitempty"`
	SHA256 string `json:"sha256"`
}

// ZonePolicy is the [policy] table of a zone's own agents.toml. The shared
// agents.base.toml policy never widens it: a zone's allow-list is its own.
type ZonePolicy struct {
	Allow      []string `toml:"allow"`
	Require    string   `toml:"require"`
	Unlisted   string   `toml:"unlisted"`
	Installers *bool    `toml:"installers"`
}

// Deny reports whether unlisted sources fail (the work zone).
func (p ZonePolicy) Deny() bool { return p.Unlisted == "deny" }

// Allowed matches a source against allow: a trailing * matches any suffix,
// anything else must be equal. nix: sources are built from the flake and
// always allowed.
func (p ZonePolicy) Allowed(source string) bool {
	if strings.HasPrefix(source, "nix:") {
		return true
	}
	for _, pat := range p.Allow {
		if strings.HasSuffix(pat, "*") && strings.HasPrefix(source, strings.TrimSuffix(pat, "*")) {
			return true
		}
		if pat == source {
			return true
		}
	}
	return false
}

// LoadZonePolicy reads [policy] from the agents.toml of the roots in zone z.
// One root sets the zone's policy; another (an overlay's own agents/ beside
// its team layer's) may say extends = true and add allow entries, nothing
// else (lib/agents.nix zonePolicy, userscope.NewZone).
func LoadZonePolicy(roots []zone.Root, z string) (ZonePolicy, error) {
	var pol ZonePolicy
	var adds []string
	found := 0
	for _, r := range roots {
		if r.Zone != z {
			continue
		}
		b, err := os.ReadFile(filepath.Join(r.Dir, "agents.toml"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return pol, err
		}
		var m struct {
			Policy *ZonePolicy `toml:"policy"`
		}
		if err := toml.Unmarshal(b, &m); err != nil {
			return pol, fmt.Errorf("%s/agents.toml: %w", r.Name, err)
		}
		var raw struct {
			Policy map[string]any `toml:"policy"`
		}
		if err := toml.Unmarshal(b, &raw); err != nil {
			return pol, fmt.Errorf("%s/agents.toml: %w", r.Name, err)
		}
		if ext, _ := raw.Policy["extends"].(bool); ext {
			for k := range raw.Policy {
				if k != "extends" && k != "allow" {
					return pol, fmt.Errorf("%s/agents.toml: [policy] extends = true only adds allow entries to the %s zone's policy; %s is for the root that sets it", r.Name, z, k)
				}
			}
			adds = append(adds, m.Policy.Allow...)
			continue
		}
		if m.Policy != nil {
			pol = *m.Policy
			found++
		}
	}
	if found > 1 {
		return pol, fmt.Errorf("more than one %s zone root declares [policy]; another root of the zone adds to it with [policy] extends = true", z)
	}
	pol.Allow = append(pol.Allow, adds...)
	return pol, nil
}

// PolicyViolations lists every declared third-party item the policy refuses
// (the runtime twin of the eval-time guard in lib/agents.nix), marketplaces
// only when used (UsedMarketplace). Empty unless the policy is deny-unlisted.
func PolicyViolations(reg *registry.Registry, pol ZonePolicy) []string {
	if !pol.Deny() {
		return nil
	}
	var out []string
	for _, id := range reg.IDs() {
		it := reg.Items[id]
		switch it.ID.Kind {
		case "marketplace":
			src, _ := it.Decl["source"].(string)
			if UsedMarketplace(reg, it) && !pol.Allowed(src) {
				out = append(out, fmt.Sprintf("%s: source %q is not in [policy] allow", id, src))
			}
		case "plugin":
			pid, _ := it.Decl["id"].(string)
			_, mkt, _ := strings.Cut(pid, "@")
			h, _ := it.Decl["harness"].(string)
			if h == "codex" && !pol.Allowed("codex:"+mkt) {
				out = append(out, fmt.Sprintf("%s: marketplace codex:%s is not in [policy] allow", id, mkt))
			}
			if h == "claude" && !declaredMarketplace(reg, mkt) && !pol.Allowed("claude:"+mkt) {
				out = append(out, fmt.Sprintf("%s: marketplace %s is neither declared nor allowed", id, mkt))
			}
		case "tool":
			src, _ := it.Decl["source"].(string)
			if !pol.Allowed(src) {
				out = append(out, fmt.Sprintf("%s: source %q is not in [policy] allow", id, src))
			}
		case "installer":
			src, _ := it.Decl["installer"].(string)
			if pol.Installers == nil || !*pol.Installers {
				out = append(out, fmt.Sprintf("%s: installer %q: this zone allows no installers", id, src))
			} else if !pol.Allowed(src) {
				out = append(out, fmt.Sprintf("%s: installer %q is not in [policy] allow", id, src))
			}
		case "mcp":
			if src, ver := mcpPackage(*it.MCP); src != "" {
				if !pol.Allowed(src) {
					out = append(out, fmt.Sprintf("%s: package %s is not in [policy] allow", id, src))
				} else if ver == "" || ver == "latest" {
					out = append(out, fmt.Sprintf("%s: package %s has no exact version", id, src))
				}
			}
		}
	}
	return out
}

// UsedMarketplace reports whether a marketplace item is its zone's: one a
// zone declares in its own agents.toml always is; one from the shared
// catalog (agents.base.toml) only when a plugin of the same harness in the
// registry names it (x@<name>). An unused catalog marketplace is neither
// policy-checked nor rendered (lib/agents.nix usedMarketplaces, the same
// rule; a bundle's registry holds only its zone's roots).
func UsedMarketplace(reg *registry.Registry, it *registry.Item) bool {
	if it.Source != "agents.base.toml" {
		return true
	}
	h, _ := it.Decl["harness"].(string)
	name, _ := it.Decl["name"].(string)
	for _, id := range reg.IDs() {
		p := reg.Items[id]
		if p.ID.Kind != "plugin" {
			continue
		}
		if ph, _ := p.Decl["harness"].(string); ph != h {
			continue
		}
		pid, _ := p.Decl["id"].(string)
		if _, m, ok := strings.Cut(pid, "@"); ok && m == name {
			return true
		}
	}
	return false
}

func declaredMarketplace(reg *registry.Registry, name string) bool {
	for _, it := range reg.Find("marketplace", name) {
		if h, _ := it.Decl["harness"].(string); h == "claude" {
			return true
		}
	}
	return false
}

// mcpPackage: the npm / pypi package an npx / uvx server runs, and its
// pinned version ("" if none).
func mcpPackage(m canon.MCP) (source, version string) {
	cmd := filepath.Base(m.Command)
	switch cmd {
	case "npx", "pnpx", "bunx":
		for _, a := range m.Args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			i := strings.LastIndex(a, "@")
			if i <= 0 {
				return "npm:" + a, ""
			}
			return "npm:" + a[:i], a[i+1:]
		}
	case "uvx":
		for i := 0; i < len(m.Args); i++ {
			a := m.Args[i]
			switch a {
			case "--with", "--from", "--python", "-p", "--index", "--index-url", "--extra-index-url":
				i++
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			name, ver, _ := strings.Cut(a, "==")
			return "pypi:" + name, ver
		}
	}
	return "", ""
}

// RenderContainer renders the container bundle tree.
func RenderContainer(reg *registry.Registry, o ContainerOptions) (map[string]adapter.TreeFile, ContainerManifest, []string, error) {
	tree := map[string]adapter.TreeFile{}
	cm := ContainerManifest{Zone: o.Zone, Generator: "piste agents bundle", Targets: o.Targets, Files: map[string]string{}}
	var warnings []string
	fail := func(format string, a ...any) (map[string]adapter.TreeFile, ContainerManifest, []string, error) {
		return nil, cm, nil, fmt.Errorf(format, a...)
	}
	if err := zone.Validate(o.Zone, reg.Roots); err != nil {
		return fail("%v", err)
	}
	for _, t := range o.Targets {
		if t != canon.Claude && t != canon.Codex {
			return fail("container bundles target claude and codex, not %q", t)
		}
	}
	pol, err := LoadZonePolicy(reg.Roots, o.Zone)
	if err != nil {
		return fail("%v", err)
	}
	if v := PolicyViolations(reg, pol); len(v) > 0 {
		return fail("the %s zone policy refuses:\n  %s", o.Zone, strings.Join(v, "\n  "))
	}

	var skills, instrNames []string
	seenSkill, seenInstr := map[string]bool{}, map[string]bool{}
	for _, p := range o.Presets {
		if p.Bundle == nil {
			return fail("%s is not a bundle (preset)", p.ID)
		}
		if p.ID.Zone != zone.Shared && p.ID.Zone != o.Zone {
			return fail("%s is %s content; this is a %s bundle", p.ID, p.ID.Zone, o.Zone)
		}
		if len(p.Bundle.Hooks) > 0 {
			return fail("%s names hooks; container bundles do not carry hooks yet", p.ID)
		}
		if len(p.Bundle.Agents) > 0 {
			return fail("%s names subagents; container bundles do not carry them yet", p.ID)
		}
		cm.Presets = append(cm.Presets, p.ID.String())
		for _, s := range p.Bundle.Skills {
			if !seenSkill[s] {
				seenSkill[s] = true
				skills = append(skills, s)
			}
		}
		for _, n := range p.Bundle.Instructions {
			if !seenInstr[n] {
				seenInstr[n] = true
				instrNames = append(instrNames, n)
			}
		}
	}

	// Skills: owned only, from shared or this zone.
	sort.Strings(skills)
	for _, name := range skills {
		var found *registry.Item
		for _, c := range reg.Find("skill", name) {
			if c.ID.Zone == zone.Shared || c.ID.Zone == o.Zone {
				if found != nil {
					return fail("skill %s is in two zones", name)
				}
				found = c
			}
		}
		if found == nil {
			return fail("skill %s is not an owned skill of the %s or shared zone (third-party skills are not bundled yet)", name, o.Zone)
		}
		for _, h := range o.Targets {
			sr, err := adapter.Skill(h, canon.Bundle, *found.Skill, adapter.Copy, "")
			if err != nil {
				return fail("%v", err)
			}
			warnings = append(warnings, sr.Warnings...)
			dir := "etc/codex/skills/" + name
			if h == canon.Claude {
				dir = "etc/claude-code/.claude/skills/" + name
			}
			for n, f := range sr.Artifacts[0].Tree {
				tree[dir+"/"+n] = f
			}
		}
		cm.Skills = append(cm.Skills, name)
	}

	// Instructions: Claude's managed CLAUDE.md, Codex's seeded AGENTS.md.
	for _, h := range o.Targets {
		block, err := containerInstructions(reg, o.Zone, instrNames, h)
		if err != nil {
			return fail("%v", err)
		}
		if block == "" {
			continue
		}
		text, err := edit.MDMarkers.Set("", block)
		if err != nil {
			return fail("%v", err)
		}
		file := "home/.codex/AGENTS.md"
		if h == canon.Claude {
			file = "etc/claude-code/CLAUDE.md"
		}
		tree[file] = adapter.TreeFile{Data: []byte(text), Mode: 0o644}
	}

	if contains(o.Targets, canon.Claude) {
		settings, plugins := claudeManagedSettings(reg, o.Zone)
		cm.Plugins = plugins
		if settings != nil {
			b, _ := json.MarshalIndent(settings, "", "  ")
			tree["etc/claude-code/managed-settings.d/50-agents.json"] = adapter.TreeFile{Data: append(b, '\n'), Mode: 0o644}
		}
		// managed-mcp.json takes exclusive control of MCP servers in Claude
		// Code, so it is written only when asked for (ManagedMCP) and the
		// zone declares servers.
		var servers []canon.MCP
		for _, id := range reg.IDs() {
			it := reg.Items[id]
			if it.ID.Kind == "mcp" && (it.ID.Zone == o.Zone || it.ID.Zone == zone.Shared) {
				servers = append(servers, *it.MCP)
			}
		}
		if len(servers) > 0 && !o.ManagedMCP {
			names := make([]string, 0, len(servers))
			for _, m := range servers {
				names = append(names, m.Name)
			}
			sort.Strings(names)
			warnings = append(warnings, fmt.Sprintf("MCP server(s) %s declared but not bundled: managed-mcp.json is opt-in (--managed-mcp)", strings.Join(names, ", ")))
		}
		if len(servers) > 0 && o.ManagedMCP {
			r, err := adapter.MCPServers(canon.ClaudePlugin, canon.Bundle, servers)
			if err != nil {
				return fail("%v", err)
			}
			for _, a := range r.Artifacts {
				tree["etc/claude-code/managed-mcp.json"] = adapter.TreeFile{Data: a.Whole, Mode: 0o644}
			}
		}
	}

	for _, r := range reg.Roots {
		s, err := lock.TreeSum(r.Dir)
		if err != nil {
			return fail("zone root %s: %v", r.Name, err)
		}
		cm.Sources = append(cm.Sources, ContainerSource{Name: r.Name, Zone: r.Zone, Rev: o.SourceRevs[r.Name], SHA256: s})
	}
	for p, f := range tree {
		cm.Files[p] = lock.Sum(f.Data)
	}
	if cm.Skills == nil {
		cm.Skills = []string{}
	}
	if cm.Plugins == nil {
		cm.Plugins = []string{}
	}
	mb, _ := json.MarshalIndent(cm, "", "  ")
	tree["manifest.json"] = adapter.TreeFile{Data: append(mb, '\n'), Mode: 0o644}
	return tree, cm, warnings, nil
}

// containerInstructions composes the fragments for one harness the way
// lib/agents.nix does: frontmatter targets + order, a source marker each,
// and for Codex (no @imports) the include inlined.
func containerInstructions(reg *registry.Registry, z string, names []string, harness string) (string, error) {
	type frag struct {
		order int
		name  string
		text  canon.Instruction
	}
	var frags []frag
	for _, n := range names {
		var found *registry.Item
		for _, c := range reg.Find("instruction", n) {
			if c.ID.Zone == zone.Shared || c.ID.Zone == z {
				if found != nil {
					return "", fmt.Errorf("instruction fragment %s is in two zones", n)
				}
				found = c
			}
		}
		if found == nil {
			return "", fmt.Errorf("instruction fragment %s is not in the %s or shared zone", n, z)
		}
		fm, _, body, err := canon.SplitFrontmatter(found.Instruction.Text)
		if err != nil {
			return "", fmt.Errorf("instruction %s: %w", n, err)
		}
		targets, _ := fm["targets"].([]any)
		if !containsAny(targets, harness) {
			continue
		}
		order := 0
		switch v := fm["order"].(type) {
		case int:
			order = v
		case uint64:
			order = int(v)
		case float64:
			order = int(v)
		}
		body = strings.TrimSpace(body)
		if harness == canon.Codex {
			root := rootDir(reg, found.Root)
			body, err = inlineImports(root, harness, body)
			if err != nil {
				return "", fmt.Errorf("instruction %s: %w", n, err)
			}
		}
		frags = append(frags, frag{order, n, canon.Instruction{Name: found.Instruction.Name, Text: body}})
	}
	sort.Slice(frags, func(i, j int) bool {
		if frags[i].order == frags[j].order {
			return frags[i].name < frags[j].name
		}
		return frags[i].order < frags[j].order
	})
	var ins []canon.Instruction
	for _, f := range frags {
		ins = append(ins, f.text)
	}
	return adapter.ComposeBlock(ins), nil
}

func inlineImports(root, harness, body string) (string, error) {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "@") || strings.ContainsAny(l, " \t") {
			continue
		}
		file := l[1:]
		var b []byte
		var err error
		for _, c := range []string{filepath.Join(root, "instructions/include", harness, file), filepath.Join(root, "instructions/include", file)} {
			if b, err = os.ReadFile(c); err == nil {
				break
			}
		}
		if err != nil {
			return "", fmt.Errorf("'@%s' has no include for %s (instructions/include/{%s/,})", file, harness, harness)
		}
		lines[i] = strings.TrimSpace(string(b))
	}
	return strings.Join(lines, "\n"), nil
}

func rootDir(reg *registry.Registry, name string) string {
	for _, r := range reg.Roots {
		if r.Name == name {
			return r.Dir
		}
	}
	return ""
}

// claudeManagedSettings: enabledPlugins for every declared Claude plugin of
// the shared + zone roots, and extraKnownMarketplaces for the used
// (UsedMarketplace) GitHub marketplaces (not the reserved
// claude-plugins-official).
func claudeManagedSettings(reg *registry.Registry, z string) (map[string]any, []string) {
	enabled := map[string]any{}
	markets := map[string]any{}
	var ids []string
	for _, id := range reg.IDs() {
		it := reg.Items[id]
		if it.ID.Zone != zone.Shared && it.ID.Zone != z {
			continue
		}
		if h, _ := it.Decl["harness"].(string); h != "claude" {
			continue
		}
		switch it.ID.Kind {
		case "plugin":
			pid, _ := it.Decl["id"].(string)
			enabled[pid] = true
			ids = append(ids, pid)
		case "marketplace":
			name, _ := it.Decl["name"].(string)
			src, _ := it.Decl["source"].(string)
			if name == "claude-plugins-official" || !strings.HasPrefix(src, "github:") || !UsedMarketplace(reg, it) {
				continue
			}
			markets[name] = map[string]any{"source": map[string]any{"source": "github", "repo": strings.TrimPrefix(src, "github:")}}
		}
	}
	if len(enabled) == 0 {
		return nil, nil
	}
	sort.Strings(ids)
	s := map[string]any{"enabledPlugins": enabled}
	if len(markets) > 0 {
		s["extraKnownMarketplaces"] = markets
	}
	return s, ids
}

func containsAny(xs []any, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// WriteContainerBundle renders and writes a container bundle to out,
// replaced whole (the same refusal rules as the Hermes bundle).
func WriteContainerBundle(reg *registry.Registry, o ContainerOptions, out string) (ContainerManifest, []string, error) {
	tree, cm, warnings, err := RenderContainer(reg, o)
	if err != nil {
		return cm, nil, err
	}
	return cm, warnings, writeTreeWhole(tree, out)
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
