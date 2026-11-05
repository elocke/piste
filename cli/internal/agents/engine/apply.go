// Package engine turns adapter artifacts into a plan with ownership records:
// every write is a set of one owned path, recorded in
// the lock with its hash, and a path that exists but isn't ours is never
// taken over.
package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/adapter"
	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/fsx"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/plan"
)

// ErrForeign: the path exists and isn't ours.
var ErrForeign = errors.New("exists and is not owned by piste")

// Desired is an owned entry a render wants, with how to write it.
type Desired struct {
	Owned lock.Owned
	Art   adapter.Artifact
}

// Owner applies artifacts to a plan against the previous lock.
type Owner struct {
	Plan  *plan.Plan
	Prev  *lock.Lock // what piste wrote last time
	Force bool
	// Adopt lists owned ids that may be taken over although piste didn't write
	// them (init --adopt: the entries it imported).
	Adopt map[string]bool
}

func (o *Owner) prev(id string) (lock.Owned, bool) {
	for _, e := range o.Prev.Owned {
		if e.ID() == id {
			return e, true
		}
	}
	return lock.Owned{}, false
}

// check decides whether an owned path may be (re)written: it is new, or
// ours and unedited, or already equal to what we want. Otherwise refuse
// (a hand edit, or someone else's path) unless Force.
func (o *Owner) check(want lock.Owned) (skip bool, err error) {
	cur, present, err := lock.Current(o.Plan.Root, want)
	if err != nil && !o.Force {
		return false, err
	}
	if present && cur == want.SHA256 {
		return true, nil // already right
	}
	prev, ours := o.prev(want.ID())
	switch {
	case !present:
		return false, nil
	case !ours:
		if want.Kind == lock.OwnedJSONEntry {
			return false, nil // a new entry beside others
		}
		if o.Force || o.Adopt[want.ID()] {
			return false, nil
		}
		return false, fmt.Errorf("%s: %w (remove it, or --force to take it over)", want.ID(), ErrForeign)
	case cur != prev.SHA256 && !o.Force:
		return false, fmt.Errorf("%s: %w; not overwriting (--force to discard the edit)", want.ID(), plan.ErrEdited)
	}
	return false, nil
}

// created computes what piste is about to create for an owned entry: the file
// (if new) and every missing ancestor of ptr (plus ptr itself when
// withSelf, for arrays we append into).
func (o *Owner) created(file string, d *edit.JSON, ptr string, withSelf bool) []string {
	var out []string
	if !o.Plan.Exists(file) {
		out = append(out, "#file")
	}
	if d == nil {
		return out
	}
	toks, _ := edit.Split(ptr)
	for i := 1; i < len(toks); i++ {
		if p := edit.Pointer(toks[:i]...); !d.Has(p) {
			out = append(out, p)
		}
	}
	if withSelf && !d.Has(ptr) {
		out = append(out, ptr)
	}
	return out
}

// keepCreated carries the previous record's Created forward on a no-op.
func (o *Owner) keepCreated(w *lock.Owned) {
	if prev, ok := o.prev(w.ID()); ok {
		w.Created = prev.Created
	}
}

// Put plans one artifact (already rendered for this root) and returns the
// owned entries it creates.
func (o *Owner) Put(a adapter.Artifact, item string) ([]lock.Owned, error) {
	switch {
	case a.Whole != nil:
		w := lock.Owned{File: a.File, Kind: lock.OwnedFile, Item: item, SHA256: lock.Sum(a.Whole)}
		skip, err := o.check(w)
		if err != nil {
			return nil, err
		}
		if !skip {
			if err := o.Plan.WriteFile(a.File, a.Whole, a.Mode, item); err != nil {
				return nil, err
			}
		}
		o.Plan.Own(w)
		return []lock.Owned{w}, nil
	case a.Tree != nil:
		mem := map[string]lock.MemFile{}
		for n, f := range a.Tree {
			mem[n] = lock.MemFile{Data: f.Data, Exec: f.Mode&0o111 != 0}
		}
		w := lock.Owned{File: a.File, Kind: lock.OwnedDir, Item: item, SHA256: lock.MemTreeSum(mem)}
		skip, err := o.check(w)
		if err != nil {
			return nil, err
		}
		if !skip {
			for _, n := range adapter.TreeNames(a.Tree) {
				f := a.Tree[n]
				if err := o.Plan.WriteFile(a.File+"/"+n, f.Data, f.Mode, item); err != nil {
					return nil, err
				}
			}
			// Files the new render dropped (the directory is ours).
			stale, err := extraFiles(filepath.Join(o.Plan.Root, a.File), a.Tree)
			if err != nil {
				return nil, err
			}
			for _, rel := range stale {
				if err := o.Plan.Delete(a.File + "/" + rel); err != nil {
					return nil, err
				}
			}
		}
		o.Plan.Own(w)
		return []lock.Owned{w}, nil
	case a.Link != "":
		w := lock.Owned{File: a.File, Kind: lock.OwnedLink, Item: item, SHA256: lock.LinkSum(a.Link), Target: a.Link}
		// Plan.Lock is the previous lock, so a link we wrote may be replaced;
		// a foreign one never is, even with --force.
		if err := o.Plan.Link(a.File, a.Link); err != nil {
			return nil, err
		}
		o.Plan.Own(w)
		return []lock.Owned{w}, nil
	case a.JSONPointer != "":
		sum, err := edit.Hash(a.JSONValue)
		if err != nil {
			return nil, err
		}
		w := lock.Owned{File: a.File, Kind: lock.OwnedJSON, Path: a.JSONPointer, Item: item, SHA256: sum}
		skip, err := o.check(w)
		if err != nil {
			return nil, err
		}
		if !skip {
			if err := o.Plan.EditJSON(a.File, item, func(d *edit.JSON) error {
				w.Created = o.created(a.File, d, a.JSONPointer, false)
				return d.Set(a.JSONPointer, a.JSONValue)
			}); err != nil {
				return nil, err
			}
		} else {
			o.keepCreated(&w)
		}
		o.Plan.Own(w)
		return []lock.Owned{w}, nil
	case a.JSONArray != "":
		sum, err := edit.Hash(a.JSONEntry)
		if err != nil {
			return nil, err
		}
		w := lock.Owned{File: a.File, Kind: lock.OwnedJSONEntry, Path: a.JSONArray, Item: item, SHA256: sum}
		// Identity is (array, hash): present means already right.
		if _, present, err := lock.Current(o.Plan.Root, w); err != nil {
			return nil, err
		} else if !present {
			if err := o.Plan.EditJSON(a.File, item, func(d *edit.JSON) error {
				if d.FindElement(a.JSONArray, sum) >= 0 {
					return nil // already added earlier in this plan
				}
				w.Created = o.created(a.File, d, a.JSONArray, true)
				_, err := d.Append(a.JSONArray, a.JSONEntry)
				return err
			}); err != nil {
				return nil, err
			}
		} else {
			o.keepCreated(&w)
		}
		w.Path = a.JSONArray
		o.Plan.Own(w)
		return []lock.Owned{w}, nil
	case a.TOMLBlock != "":
		body := strings.Trim(a.TOMLBody, "\n")
		w := lock.Owned{File: a.File, Kind: lock.OwnedTOMLBlock, Path: a.TOMLBlock, Item: item, SHA256: lock.SumString(body)}
		skip, err := o.check(w)
		if err != nil {
			return nil, err
		}
		if !skip {
			w.Created = o.created(a.File, nil, "", false)
			if err := o.Plan.EditText(a.File, item, func(s string) (string, error) { return edit.SetTOML(s, a.TOMLBlock, body+"\n") }); err != nil {
				return nil, err
			}
		} else {
			o.keepCreated(&w)
		}
		o.Plan.Own(w)
		return []lock.Owned{w}, nil
	case a.MDBlock != nil:
		body := strings.Trim(*a.MDBlock, "\n")
		w := lock.Owned{File: a.File, Kind: lock.OwnedMDBlock, Item: item, SHA256: lock.SumString(body)}
		skip, err := o.check(w)
		if err != nil {
			return nil, err
		}
		if !skip {
			w.Created = o.created(a.File, nil, "", false)
			if err := o.Plan.EditText(a.File, item, func(s string) (string, error) { return edit.MDMarkers.Set(s, body) }); err != nil {
				return nil, err
			}
		} else {
			o.keepCreated(&w)
		}
		o.Plan.Own(w)
		return []lock.Owned{w}, nil
	}
	return nil, fmt.Errorf("artifact %s has no payload", a.File)
}

// Prune plans the removal of every previously owned entry the new render
// doesn't want (lock-recorded paths only, hash-checked by plan.Remove).
func (o *Owner) Prune(want map[string]bool) error {
	var ids []string
	byID := map[string]lock.Owned{}
	for _, e := range o.Prev.Owned {
		if !want[e.ID()] {
			ids = append(ids, e.ID())
			byID[e.ID()] = e
		}
	}
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		if err := o.Plan.Remove(byID[id]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func extraFiles(dir string, tree map[string]adapter.TreeFile) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return filepath.SkipAll
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if _, ok := tree[rel]; !ok {
			out = append(out, rel)
		}
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	sort.Strings(out)
	return out, err
}

// WriteLock persists l at path if its bytes changed (idempotent sync).
func WriteLock(root, path string, l *lock.Lock) error {
	abs := lock.Abs(root, path)
	if len(l.Entries()) == 0 && len(l.Owned) == 0 && len(l.Roots) == 0 && l.Journal == nil {
		// Nothing is used or owned: no lock, so removing the last item
		// leaves the tree as it was before the first add.
		if _, err := os.Lstat(abs); err != nil {
			return nil
		}
		if err := fsx.Guard(root, abs, nil); err != nil {
			return err
		}
		return os.Remove(abs)
	}
	b, err := l.Marshal()
	if err != nil {
		return err
	}
	if cur, err := os.ReadFile(abs); err == nil && string(cur) == string(b) {
		return nil
	}
	if err := fsx.Guard(root, abs, nil); err != nil {
		return err
	}
	return fsx.WriteAtomic(abs, b, 0o644)
}
