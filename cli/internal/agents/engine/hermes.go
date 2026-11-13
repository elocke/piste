package engine

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/adapter"
	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/fsx"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/registry"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

// HermesFormat marks a manifest.json in the hermes-skills layout:
//
//	tree/<name>/…                          every selected skill once
//	profiles/<profile>/<name> -> ../../tree/<name>
//	manifest.json                          sources, per-profile lists, sha256 per file
const HermesFormat = "hermes-skills/1"

// HermesPresetPrefix names the presets that are Hermes profiles:
// presets/hermes-<profile>.toml.
const HermesPresetPrefix = "hermes-"

// HermesManifest is manifest.json of a hermes-skills bundle.
type HermesManifest struct {
	Format   string                 `json:"format"`
	Zone     string                 `json:"zone"`
	Sources  []HermesSource         `json:"sources"`
	Profiles map[string][]string    `json:"profiles"`
	Skills   map[string]HermesSkill `json:"skills"`
}

// HermesSource is one zone root the bundle was rendered from.
type HermesSource struct {
	Name   string `json:"name"`
	Zone   string `json:"zone"`
	Commit string `json:"commit,omitempty"` // the git commit, when the build knows it
	SHA256 string `json:"sha256"`           // lock.TreeSum of the root
}

// HermesSkill is one skill of the tree.
type HermesSkill struct {
	Source string            `json:"source"` // registry id
	Tree   string            `json:"tree"`   // lock.MemTreeSum
	Files  map[string]string `json:"files"`  // path within the skill → sha256
}

// HermesProfiles lists the hermes-<profile> presets visible to zone z,
// profile name → preset item.
func HermesProfiles(reg *registry.Registry, z string) (map[string]*registry.Item, error) {
	out := map[string]*registry.Item{}
	for _, id := range reg.IDs() {
		it := reg.Items[id]
		if it.Bundle == nil || !strings.HasPrefix(it.ID.Name, HermesPresetPrefix) {
			continue
		}
		if it.ID.Zone != zone.Shared && it.ID.Zone != z {
			continue
		}
		p := strings.TrimPrefix(it.ID.Name, HermesPresetPrefix)
		if _, err := (adapter.HermesLayout{Home: "/"}).ProfileDir(p); err != nil {
			return nil, err
		}
		if prev, ok := out[p]; ok {
			return nil, fmt.Errorf("hermes profile %s: two presets (%s, %s)", p, prev.ID, it.ID)
		}
		out[p] = it
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no %s<profile> presets in the registry", HermesPresetPrefix)
	}
	return out, nil
}

// RenderHermes renders every hermes-<profile> preset whole: files are
// the tree/ and manifest.json contents, links the profiles/ symlinks.
// commits maps a zone root name to its git commit (optional, recorded only).
func RenderHermes(reg *registry.Registry, z string, commits map[string]string) (files map[string]adapter.TreeFile, links map[string]string, m HermesManifest, warnings []string, err error) {
	files, links = map[string]adapter.TreeFile{}, map[string]string{}
	m = HermesManifest{Format: HermesFormat, Zone: z, Profiles: map[string][]string{}, Skills: map[string]HermesSkill{}}
	profiles, err := HermesProfiles(reg, z)
	if err != nil {
		return
	}
	snap, err := zone.Snapshot(reg.Roots)
	if err != nil {
		return
	}
	for _, s := range snap {
		m.Sources = append(m.Sources, HermesSource{Name: s.Name, Zone: s.Zone, Commit: commits[s.Name], SHA256: s.SHA256})
	}
	for n := range commits {
		found := false
		for _, s := range snap {
			found = found || s.Name == n
		}
		if !found {
			err = fmt.Errorf("--source-commit %s: no such zone root", n)
			return
		}
	}
	for p, it := range profiles {
		names := append([]string(nil), it.Bundle.Skills...)
		sort.Strings(names)
		for i, name := range names {
			if i > 0 && names[i-1] == name {
				err = fmt.Errorf("preset %s lists skill %s twice", it.ID, name)
				return
			}
			if _, done := m.Skills[name]; !done {
				var sk HermesSkill
				var w []string
				sk, w, err = renderHermesSkill(reg, z, it, name, files)
				if err != nil {
					return
				}
				warnings = append(warnings, w...)
				m.Skills[name] = sk
			}
			links["profiles/"+p+"/"+name] = "../../tree/" + name
		}
		m.Profiles[p] = names
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	files["manifest.json"] = adapter.TreeFile{Data: append(mb, '\n'), Mode: 0o644}
	return
}

func renderHermesSkill(reg *registry.Registry, z string, preset *registry.Item, name string, files map[string]adapter.TreeFile) (HermesSkill, []string, error) {
	var found *registry.Item
	for _, c := range reg.Find("skill", name) {
		if c.ID.Zone == zone.Shared || c.ID.Zone == z {
			if found != nil {
				return HermesSkill{}, nil, fmt.Errorf("bundle %s: skill %s is in two zones", preset.ID, name)
			}
			found = c
		}
	}
	if found == nil {
		return HermesSkill{}, nil, fmt.Errorf("bundle %s: skill %s is not in the registry", preset.ID, name)
	}
	sr, err := adapter.Skill(canon.Hermes, canon.Bundle, *found.Skill, adapter.Copy, "")
	if err != nil {
		return HermesSkill{}, nil, err
	}
	sk := HermesSkill{Source: found.ID.String(), Files: map[string]string{}}
	mem := map[string]lock.MemFile{}
	for rel, f := range sr.Artifacts[0].Tree {
		files["tree/"+name+"/"+rel] = f
		sk.Files[rel] = lock.Sum(f.Data)
		mem[rel] = lock.MemFile{Data: f.Data, Exec: f.Mode&0o111 != 0}
	}
	sk.Tree = lock.MemTreeSum(mem)
	return sk, sr.Warnings, nil
}

// WriteHermesBundle renders the hermes-skills layout into out, replacing it
// whole (an existing out must itself be a bundle), then verifies the result.
func WriteHermesBundle(reg *registry.Registry, z string, commits map[string]string, out string) (HermesManifest, []string, error) {
	files, links, m, warnings, err := RenderHermes(reg, z, commits)
	if err != nil {
		return m, warnings, err
	}
	if err := replaceTree(out, files, links); err != nil {
		return m, warnings, err
	}
	if probs, err := CheckBundle(out); err != nil || len(probs) > 0 {
		return m, warnings, fmt.Errorf("the written bundle does not verify: %v %v", err, probs)
	}
	return m, warnings, nil
}

// replaceTree writes files and links into a fresh sibling of out, then swaps
// it in (the WriteBundle rules: never the Nix store, never through a
// symlink, never over a directory that is not a bundle).
func replaceTree(out string, files map[string]adapter.TreeFile, links map[string]string) error {
	out = filepath.Clean(out)
	if strings.HasPrefix(out+"/", fsx.StorePrefix) {
		return fsx.Refusedf("%s is in the Nix store", out)
	}
	if fi, err := os.Lstat(out); err == nil {
		if fi.Mode()&fs.ModeSymlink != 0 {
			return fsx.Refusedf("%s is a symlink", out)
		}
		if _, err := os.Stat(filepath.Join(out, "manifest.json")); err != nil {
			return fsx.Refusedf("%s exists and is not a bundle (no manifest.json); not replacing it", out)
		}
	}
	tmp := out + ".piste-new"
	os.RemoveAll(tmp)
	for rel, f := range files {
		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(p, f.Data, mode); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	for rel, target := range links {
		p := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(target, p); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	old := out + ".piste-old"
	os.RemoveAll(old)
	if _, err := os.Lstat(out); err == nil {
		if err := os.Rename(out, old); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, out); err != nil {
		os.Rename(old, out)
		return err
	}
	os.RemoveAll(old)
	return nil
}

// checkHermes verifies a hermes-skills tree against its manifest: every file
// under tree/ hashed, no extra or missing file or skill, and profiles/ holding
// exactly the manifest's links (the contract a Hermes image's skill check relies on).
func checkHermes(dir string, m HermesManifest) []string {
	var probs []string
	top, _ := os.ReadDir(dir)
	for _, e := range top {
		switch e.Name() {
		case "manifest.json", "tree", "profiles":
		default:
			probs = append(probs, "extra file "+e.Name())
		}
	}
	tree := filepath.Join(dir, "tree")
	seen := map[string]bool{}
	skills, _ := os.ReadDir(tree)
	for _, s := range skills {
		want, ok := m.Skills[s.Name()]
		if !ok || !s.IsDir() {
			probs = append(probs, "extra skill tree/"+s.Name()+" (not in manifest.json)")
			continue
		}
		seen[s.Name()] = true
		got := map[string]bool{}
		root := filepath.Join(tree, s.Name())
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			label := "tree/" + s.Name() + "/" + rel
			sum, ok := want.Files[rel]
			switch {
			case !d.Type().IsRegular():
				probs = append(probs, "not a regular file "+label)
			case !ok:
				probs = append(probs, "extra file "+label)
			default:
				got[rel] = true
				if h, err := lock.FileSum(p); err != nil || h != sum {
					probs = append(probs, "changed file "+label)
				}
			}
			return nil
		})
		for rel := range want.Files {
			if !got[rel] {
				probs = append(probs, "missing file tree/"+s.Name()+"/"+rel)
			}
		}
	}
	for n := range m.Skills {
		if !seen[n] {
			probs = append(probs, "missing skill tree/"+n)
		}
	}
	profiles := filepath.Join(dir, "profiles")
	pds, _ := os.ReadDir(profiles)
	gotP := map[string]bool{}
	for _, pd := range pds {
		names, ok := m.Profiles[pd.Name()]
		if !ok || !pd.IsDir() {
			probs = append(probs, "extra profile profiles/"+pd.Name())
			continue
		}
		gotP[pd.Name()] = true
		want := map[string]bool{}
		for _, n := range names {
			want[n] = true
		}
		es, _ := os.ReadDir(filepath.Join(profiles, pd.Name()))
		have := map[string]bool{}
		for _, e := range es {
			label := "profiles/" + pd.Name() + "/" + e.Name()
			t, err := os.Readlink(filepath.Join(profiles, pd.Name(), e.Name()))
			switch {
			case !want[e.Name()]:
				probs = append(probs, "extra skill "+label)
			case err != nil || t != "../../tree/"+e.Name():
				probs = append(probs, "bad link "+label)
			default:
				have[e.Name()] = true
			}
		}
		for n := range want {
			if !have[n] {
				probs = append(probs, "missing skill profiles/"+pd.Name()+"/"+n)
			}
		}
	}
	for p := range m.Profiles {
		if !gotP[p] {
			probs = append(probs, "missing profile profiles/"+p)
		}
	}
	sort.Strings(probs)
	return probs
}
