package lock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/edit"
)

// Finding is one verification result.
type Finding struct {
	State  string `json:"state"` // ok | missing | edited
	What   string `json:"what"`  // owned id or kind:key
	File   string `json:"file"`
	Detail string `json:"detail,omitempty"`
}

// Current reads an owned entry back from disk: its hash now, and whether it
// is there at all.
func Current(root string, o Owned) (sum string, present bool, err error) {
	path := Abs(root, o.File)
	switch o.Kind {
	case OwnedLink:
		t, err := os.Readlink(path)
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		if err != nil {
			return "", true, nil // exists, not a link: someone replaced it
		}
		return LinkSum(t), true, nil
	case OwnedDir:
		fi, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		if err != nil || !fi.IsDir() {
			return "", true, err
		}
		s, err := TreeSum(path)
		return s, true, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	switch o.Kind {
	case OwnedFile:
		return Sum(b), true, nil
	case OwnedJSON:
		d, err := edit.ParseJSON(b)
		if err != nil {
			return "", true, fmt.Errorf("%s: %w", o.File, err)
		}
		raw, err := d.Get(o.Path)
		if errors.Is(err, edit.ErrNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", true, err
		}
		s, err := edit.Hash(raw)
		return s, true, err
	case OwnedJSONEntry:
		d, err := edit.ParseJSON(b)
		if err != nil {
			return "", true, fmt.Errorf("%s: %w", o.File, err)
		}
		if d.FindElement(o.Path, o.SHA256) >= 0 {
			return o.SHA256, true, nil
		}
		return "", false, nil
	case OwnedTOMLBlock:
		body, ok, err := edit.GetTOML(string(b), o.Path)
		if err != nil || !ok {
			return "", false, err
		}
		return SumString(body), true, nil
	case OwnedMDBlock:
		body, ok, err := edit.MDMarkers.Get(string(b))
		if err != nil || !ok {
			return "", false, err
		}
		return SumString(body), true, nil
	}
	return "", false, fmt.Errorf("unknown owned kind %q", o.Kind)
}

// VerifyOwned re-hashes every owned entry (on every check, not only at
// write time).
func (l *Lock) VerifyOwned(root string) []Finding {
	var out []Finding
	for _, o := range l.Owned {
		f := Finding{What: o.ID(), File: o.File}
		sum, present, err := Current(root, o)
		switch {
		case err != nil:
			f.State, f.Detail = "edited", err.Error()
		case !present:
			f.State = "missing"
		case sum != o.SHA256:
			f.State, f.Detail = "edited", "content differs from what piste last wrote"
		default:
			f.State = "ok"
		}
		out = append(out, f)
	}
	return out
}

// ItemSum hashes the files an item covers, relative to root.
func ItemSum(root string, files []string) (string, error) {
	var sums []string
	for _, rel := range files {
		p := Abs(root, rel)
		fi, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		var s string
		switch {
		case fi.IsDir():
			s, err = TreeSum(p)
		case fi.Mode()&fs.ModeSymlink != 0:
			var t string
			t, err = os.Readlink(p)
			s = LinkSum(t)
		default:
			s, err = FileSum(p)
		}
		if err != nil {
			return "", err
		}
		sums = append(sums, rel+" "+s)
	}
	if len(sums) == 1 {
		return strings.SplitN(sums[0], " ", 2)[1], nil
	}
	return SumString(strings.Join(sums, "\n")), nil
}

// VerifyItems re-hashes every item that records Files + SHA256 (vendored
// skills, subagents, hook scripts, bundles …). Items without files (plugins,
// marketplaces, tools) are verified against the harness by their kind's
// check, not here.
func (l *Lock) VerifyItems(root string) []Finding {
	var out []Finding
	for _, e := range l.Entries() {
		if len(e.Item.Files) == 0 || e.Item.SHA256 == "" {
			continue
		}
		f := Finding{What: string(e.Kind) + ":" + e.Item.Key(), File: strings.Join(e.Item.Files, ", ")}
		sum, err := ItemSum(root, e.Item.Files)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			f.State = "missing"
		case err != nil:
			f.State, f.Detail = "edited", err.Error()
		case sum != e.Item.SHA256:
			f.State, f.Detail = "edited", "content hash differs from the lock"
		default:
			f.State = "ok"
		}
		out = append(out, f)
	}
	return out
}

// VerifyRoots re-hashes the zone roots a render read.
func (l *Lock) VerifyRoots() []Finding {
	var out []Finding
	for _, r := range l.Roots {
		f := Finding{What: "root:" + r.Name, File: r.Root}
		if r.SHA256 == "" {
			continue
		}
		s, err := TreeSum(r.Root)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			f.State = "missing"
		case err != nil:
			f.State, f.Detail = "edited", err.Error()
		case s != r.SHA256:
			f.State, f.Detail = "edited", "zone root changed since the last render (re-run sync)"
		default:
			f.State = "ok"
		}
		out = append(out, f)
	}
	return out
}
