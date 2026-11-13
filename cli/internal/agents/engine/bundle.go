package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/adapter"
	"github.com/elocke/piste/cli/internal/agents/fsx"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/registry"
)

// WriteBundle renders the bundle whole into out (Hermes gets whole
// renders, never merges, so a skill removed from the preset is simply
// absent). out is replaced atomically; an existing out must be a bundle
// (have a manifest.json), never an arbitrary directory.
func WriteBundle(reg *registry.Registry, z string, it *registry.Item, out string) (int, error) {
	r, bm, err := RenderBundle(reg, z, it)
	if err != nil {
		return 0, err
	}
	if err := writeTreeWhole(r.Artifacts[0].Tree, out); err != nil {
		return 0, err
	}
	return len(bm.Skills), nil
}

// writeTreeWhole writes tree to out, replaced atomically. An existing out
// must be a bundle (have a manifest.json), never an arbitrary directory.
func writeTreeWhole(tree map[string]adapter.TreeFile, out string) error {
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
	for rel, f := range tree {
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

// CheckBundle verifies a bundle tree against its manifest.json: any extra
// file or skill, a missing skill, or a changed one is a problem (the
// contract a Hermes image's skill check relies on). Both layouts are
// understood: one preset (skills/<category>/<name>) and hermes-skills
// (tree/ + profiles/).
func CheckBundle(dir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var hm HermesManifest
	if err := json.Unmarshal(b, &hm); err == nil && hm.Format == HermesFormat {
		return checkHermes(dir, hm), nil
	}
	var bm BundleManifest
	if err := json.Unmarshal(b, &bm); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	var probs []string
	seen := map[string]bool{}
	skills := filepath.Join(dir, "skills")
	cats, _ := os.ReadDir(skills)
	for _, c := range cats {
		if !c.IsDir() {
			probs = append(probs, "extra file skills/"+c.Name())
			continue
		}
		es, _ := os.ReadDir(filepath.Join(skills, c.Name()))
		for _, e := range es {
			rel := "skills/" + c.Name() + "/" + e.Name()
			want, ok := bm.Skills[e.Name()]
			if !ok {
				probs = append(probs, "extra skill "+rel+" (not in manifest.json)")
				continue
			}
			seen[e.Name()] = true
			got, err := lock.TreeSum(filepath.Join(dir, rel))
			if err != nil || got != want {
				probs = append(probs, "changed skill "+rel)
			}
		}
	}
	for n := range bm.Skills {
		if !seen[n] {
			probs = append(probs, "missing skill "+n)
		}
	}
	top, _ := os.ReadDir(dir)
	for _, e := range top {
		if e.Name() != "manifest.json" && e.Name() != "skills" {
			probs = append(probs, "extra file "+e.Name())
		}
	}
	sort.Strings(probs)
	if len(probs) == 0 && errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return probs, nil
}
