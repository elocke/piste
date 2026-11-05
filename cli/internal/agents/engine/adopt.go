package engine

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/project"
	"github.com/elocke/piste/cli/internal/agents/registry"
)

// Survey opens a project that has no .agents/agents.toml yet, so audit can
// inventory it before init (audit first, then init adopts
// what it found). Nothing is written through it.
func Survey(root string, reg *registry.Registry, machineZone string) (*Project, error) {
	l, err := lock.Read(filepath.Join(root, project.LockFile))
	if err != nil {
		return nil, err
	}
	m := &project.Manifest{Zone: machineZone, Harnesses: []string{"claude", "codex"}}
	return &Project{Root: root, Manifest: m, Lock: l, Reg: reg}, nil
}

// skillDirs are where a project's skills live, per harness.
var skillDirs = []string{".agents/skills/", ".claude/skills/"}

// match finds the registry skill whose content hash equals the skill
// directory at rel (a copy of a registry item, not yet in the lock).
func (p *Project) match(rel string) *registry.Item {
	var name string
	for _, d := range skillDirs {
		if strings.HasPrefix(rel, d) {
			name = strings.TrimPrefix(rel, d)
		}
	}
	if name == "" || strings.Contains(name, "/") {
		return nil
	}
	abs := filepath.Join(p.Root, rel)
	if fi, err := os.Lstat(abs); err != nil || !fi.IsDir() {
		return nil
	}
	sum, err := lock.TreeSum(abs)
	if err != nil {
		return nil
	}
	for _, kind := range []string{"skill", "3p-skill"} {
		for _, it := range p.Reg.Find(kind, name) {
			if it.SHA256 == sum {
				return it
			}
		}
	}
	return nil
}

// leaves reports whether rel is a symlink that resolves outside the root.
func (p *Project) leaves(rel string) bool {
	abs := filepath.Join(p.Root, rel)
	fi, err := os.Lstat(abs)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	t, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return true // dangling: nothing in the repo backs it
	}
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		root = p.Root
	}
	r, err := filepath.Rel(root, t)
	return err != nil || r == ".." || strings.HasPrefix(r, "../")
}

// attribute labels an entry no lock record covers (attribution):
// registry / third-party when its content matches a registry item, local
// when the manifest declares it, hand-made when git tracks it, else
// unknown.
func (p *Project) attribute(f Finding, tracked map[string]bool) Finding {
	switch {
	case p.leaves(f.File):
		f.State, f.Detail = "unknown", "a symlink leaving the repo"
	case p.Manifest.IsLocal(f.What):
		f.State, f.Detail = "local", "declared [[local]] in "+project.ManifestFile
	default:
		if it := p.match(f.What); it != nil {
			f.State = "registry"
			if it.ThirdParty != nil {
				f.State = "third-party"
			}
			f.Detail = "matches " + it.ID.String() + " (not in the lock; init --adopt uses it)"
		} else if tracked[f.File] || trackedUnder(tracked, f.File) {
			f.State, f.Detail = "hand-made", "tracked in git, not in the lock (piste agents init --adopt declares it local)"
		}
	}
	return f
}

// Adoption is what init --adopt makes of an audit.
type Adoption struct {
	Uses   []string        // registry ids whose copies are already here
	Locals []project.Local // hand-made content the project keeps
	Adopt  map[string]bool // owned ids sync may take over (the matched copies)
	// Moves are matched copies in .claude/skills to move to .agents/skills
	// (the one copy; .claude gets the link) before the first sync.
	Moves [][2]string
}

// Relocate performs the moves. Each source was verified identical to its
// registry item, which is what sync would write at the destination.
func (a Adoption) Relocate(root string) error {
	for _, m := range a.Moves {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, m[1])), 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(root, m[0]), filepath.Join(root, m[1])); err != nil {
			return err
		}
	}
	return nil
}

// Adoption turns the audit into manifest entries: registry matches become
// [[use]] (and their paths may be taken over by the first sync), hand-made
// items [[local]]. .mcp.json servers are AdoptMCP's; unknown entries stay
// unknown (they are reported, never claimed).
func (p *Project) Adoption() Adoption {
	a := Adoption{Adopt: map[string]bool{}}
	uses := map[string]bool{}
	for _, f := range p.Audit() {
		if f.File == ".mcp.json" {
			continue
		}
		switch f.State {
		case "registry", "third-party":
			it := p.match(f.What)
			if it == nil || strings.HasPrefix(f.What, "local:") {
				continue
			}
			if !uses[it.ID.String()] {
				uses[it.ID.String()] = true
				a.Uses = append(a.Uses, it.ID.String())
			}
			for _, d := range skillDirs {
				a.Adopt[d+it.Skill.Name] = true
			}
			to := skillDirs[0] + it.Skill.Name
			if f.What != to {
				if _, err := os.Lstat(filepath.Join(p.Root, to)); err != nil {
					a.Moves = append(a.Moves, [2]string{f.What, to})
				}
			}
		case "hand-made":
			a.Locals = append(a.Locals, project.Local{Path: localPath(f)})
		}
	}
	sort.Strings(a.Uses)
	sort.SliceStable(a.Locals, func(i, j int) bool { return a.Locals[i].Path < a.Locals[j].Path })
	// One [[local]] per path: array entries collapse to their array.
	seen := map[string]bool{}
	out := a.Locals[:0]
	for _, l := range a.Locals {
		if !seen[l.Path] {
			seen[l.Path] = true
			out = append(out, l)
		}
	}
	a.Locals = out
	return a
}

// localPath is what a [[local]] entry names for a finding: the directory
// or file, or for a JSON array element its array (indices shift).
func localPath(f Finding) string {
	if i := strings.Index(f.What, "#"); i >= 0 {
		ptr := f.What[i+1:]
		if j := strings.LastIndex(ptr, "/"); j > 0 && isIndex(ptr[j+1:]) {
			return f.What[:i+1] + ptr[:j]
		}
	}
	return f.What
}

func isIndex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
