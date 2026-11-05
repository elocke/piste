package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

// ThirdParty is where a 3p-skill came from: its zone agents.lock entry.
// The content itself is the locked fetch (a store path on Nix
// hosts, the registry artifact elsewhere).
type ThirdParty struct {
	Source string `json:"source"` // github:owner/repo
	Path   string `json:"path,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// BuildWith is Build plus the zones' locked third-party skills: fetched is
// skill name → the directory holding its fetched content (the Nix fetch of
// the locked commit). A locked skill with no fetched content is not offered
// (a warning), so the registry never carries an unpinned copy.
func BuildWith(roots []zone.Root, fetched map[string]string) (*Registry, error) {
	r, err := Build(roots)
	if err != nil {
		return nil, err
	}
	for _, root := range roots {
		if err := r.scanLock(root, fetched); err != nil {
			return nil, fmt.Errorf("zone root %s: %w", root.Name, err)
		}
	}
	return r, nil
}

func (r *Registry) scanLock(root zone.Root, fetched map[string]string) error {
	var l struct {
		Skill []lock.Item `toml:"skill"`
	}
	if _, ok, err := readTOML(filepath.Join(root.Dir, "agents.lock"), &l); err != nil || !ok {
		return err
	}
	sort.SliceStable(l.Skill, func(i, j int) bool { return l.Skill[i].Name < l.Skill[j].Name })
	for _, e := range l.Skill {
		dir, ok := fetched[e.Name]
		if !ok || !exists(filepath.Join(dir, "SKILL.md")) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s:3p-skill/%s: locked, but no fetched content on this machine (not offered)", root.Zone, e.Name))
			continue
		}
		sk, err := loadSkillAs(dir, e.Name, "third-party")
		if err != nil {
			return err
		}
		sum, err := lock.TreeSum(dir)
		if err != nil {
			return err
		}
		tp := &ThirdParty{Source: e.Source, Path: e.Path, Ref: e.Ref, Commit: e.Commit}
		if err := r.add(root, "3p-skill", e.Name, "agents.lock#"+e.Name, sum, &Item{Skill: &sk, Desc: sk.Description(), ThirdParty: tp}); err != nil {
			return err
		}
	}
	return nil
}

// loadSkillAs reads dir/SKILL.md as the skill name (a fetched directory is
// named after its store path, not the skill; the frontmatter must agree).
func loadSkillAs(dir, name, category string) (canon.Skill, error) {
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return canon.Skill{}, err
	}
	fm, order, body, err := canon.SplitFrontmatter(string(b))
	if err != nil {
		return canon.Skill{}, fmt.Errorf("%s: %w", dir, err)
	}
	s := canon.Skill{Name: name, Dir: dir, Category: category, Frontmatter: fm, Order: order, Body: body}
	if n, _ := fm["name"].(string); n != name {
		return s, fmt.Errorf("%s: frontmatter name %q != locked name %q", dir, n, name)
	}
	if s.Description() == "" {
		return s, fmt.Errorf("%s: frontmatter missing description", dir)
	}
	return s, nil
}
