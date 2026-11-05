// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/lib/orphan-sweep.ts, src/lib/shared-file-derive.ts. Copyright (c) 2024 dyoshikawa.
// MIT License; see cli/THIRD_PARTY_NOTICES.md.

// Package plan is plan-then-apply for every agents command (the structure
// of rulesync's orphan-sweep).
//
// A command first describes every change on a Plan: edits are computed in
// memory, every target is guarded (fsx.Guard), and every removal is checked
// against the lock. Any error there aborts with nothing written. Apply then
// stages every file (temp + fsync), journals, and renames them into place
// in order; a failure while staging removes the temp files, so either all
// files change or none do.
package plan

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aymanbagabas/go-udiff"

	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/fsx"
	"github.com/elocke/piste/cli/internal/agents/lock"
)

// ErrEdited: an owned path changed since piste wrote it.
var ErrEdited = errors.New("edited since piste wrote it")

// Change is one line of the plan, for display.
type Change struct {
	Op   string `json:"op"` // create | update | delete | link | unlink
	Path string `json:"path"`
	What string `json:"what,omitempty"`
}

type pending struct {
	rel    string
	abs    string
	orig   []byte
	exists bool
	data   []byte
	mode   os.FileMode
	what   []string
}

type linkOp struct{ rel, abs, target string }

type removeOp struct {
	rel, abs string
	dir      bool
}

// Plan accumulates changes under one root.
type Plan struct {
	Root  string
	Lock  *lock.Lock
	Force bool // remove owned paths even if edited (--force)
	// Expected is what piste last wrote for an owned file (rel -> bytes), when
	// the command still has it (e.g. the registry copy); a refused removal
	// shows the diff against it.
	Expected map[string][]byte

	files   map[string]*pending
	order   []string
	links   []linkOp
	removes []removeOp
	own     []lock.Owned
	disown  []string
}

// New starts a plan for root, recording ownership in l.
func New(root string, l *lock.Lock) *Plan {
	return &Plan{Root: filepath.Clean(root), Lock: l, files: map[string]*pending{}}
}

func (p *Plan) abs(rel string) string { return lock.Abs(p.Root, rel) }

func (p *Plan) guard(abs string) error {
	return fsx.Guard(p.Root, abs, func(link string) bool { return p.Lock.IsOwnedLink(p.Root, link) })
}

// file loads (once) the pending state of rel.
func (p *Plan) file(rel string) (*pending, error) {
	if f, ok := p.files[rel]; ok {
		return f, nil
	}
	abs := p.abs(rel)
	if err := p.guard(abs); err != nil {
		return nil, err
	}
	f := &pending{rel: rel, abs: abs}
	b, err := os.ReadFile(abs)
	switch {
	case err == nil:
		f.orig, f.exists = b, true
	case errors.Is(err, fs.ErrNotExist):
	default:
		return nil, err
	}
	f.data = f.orig
	p.files[rel] = f
	p.order = append(p.order, rel)
	return f, nil
}

// WriteFile sets rel's whole content (a file we own outright).
func (p *Plan) WriteFile(rel string, data []byte, mode os.FileMode, what string) error {
	f, err := p.file(rel)
	if err != nil {
		return err
	}
	f.data, f.mode = data, mode
	f.what = append(f.what, what)
	return nil
}

// EditText transforms rel's text (Markdown / TOML managed blocks).
func (p *Plan) EditText(rel, what string, fn func(string) (string, error)) error {
	f, err := p.file(rel)
	if err != nil {
		return err
	}
	out, err := fn(string(f.data))
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	f.data = []byte(out)
	f.what = append(f.what, what)
	return nil
}

// EditJSON applies fn to rel parsed as JSON/JSONC.
func (p *Plan) EditJSON(rel, what string, fn func(*edit.JSON) error) error {
	f, err := p.file(rel)
	if err != nil {
		return err
	}
	d, err := edit.ParseJSON(f.data)
	if err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	if err := fn(d); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	f.data = d.Bytes()
	if !f.exists && len(f.data) > 0 && f.data[len(f.data)-1] != '\n' {
		f.data = append(f.data, '\n')
	}
	f.what = append(f.what, what)
	return nil
}

// Link makes rel a symlink to target. An existing path that is not a link
// we wrote is refused (never replaced).
func (p *Plan) Link(rel, target string) error {
	abs := p.abs(rel)
	if err := p.guard(abs); err != nil {
		return err
	}
	fi, err := os.Lstat(abs)
	if err == nil {
		if fi.Mode()&fs.ModeSymlink != 0 {
			if cur, _ := os.Readlink(abs); cur == target {
				return nil
			}
			if !p.Lock.IsOwnedLink(p.Root, abs) {
				return fsx.Refusedf("%s is a symlink not written by piste", rel)
			}
		} else {
			return fsx.Refusedf("%s exists and is not ours; not replacing it", rel)
		}
	}
	p.links = append(p.links, linkOp{rel, abs, target})
	return nil
}

// Delete plans the removal of one file inside a directory piste owns (a file
// dropped from a re-rendered tree). The caller has verified the directory.
func (p *Plan) Delete(rel string) error {
	abs := p.abs(rel)
	if err := p.guard(abs); err != nil {
		return err
	}
	p.removes = append(p.removes, removeOp{rel: rel, abs: abs})
	return nil
}

// Own schedules an ownership record, applied with the plan.
func (p *Plan) Own(o lock.Owned) { p.own = append(p.own, o) }

// Remove plans the removal of an owned entry: only if the lock
// records it and its current hash equals the last written hash, unless
// Force. Returns ErrEdited with a diff otherwise.
func (p *Plan) Remove(o lock.Owned) error {
	sum, present, err := lock.Current(p.Root, o)
	if err != nil && !p.Force {
		return err
	}
	if !present {
		p.disown = append(p.disown, o.ID())
		return nil
	}
	if sum != o.SHA256 && !p.Force {
		return fmt.Errorf("%s: %w; not removing it (use --force)%s", o.ID(), ErrEdited, p.editDiff(o))
	}
	abs := p.abs(o.File)
	switch o.Kind {
	case lock.OwnedFile, lock.OwnedLink:
		if err := p.guardRemove(abs, o.Kind == lock.OwnedLink); err != nil {
			return err
		}
		p.removes = append(p.removes, removeOp{rel: o.File, abs: abs})
	case lock.OwnedDir:
		if err := p.guardRemove(abs, false); err != nil {
			return err
		}
		p.removes = append(p.removes, removeOp{rel: o.File, abs: abs, dir: true})
	case lock.OwnedJSON:
		if err := p.EditJSON(o.File, "remove "+o.Path, func(d *edit.JSON) error {
			if err := d.Delete(o.Path); err != nil {
				return err
			}
			return pruneCreated(d, o.Created)
		}); err != nil {
			return err
		}
		p.deleteIfEmpty(o, "{}")
	case lock.OwnedJSONEntry:
		if err := p.EditJSON(o.File, "remove entry of "+o.Path, func(d *edit.JSON) error {
			if i := d.FindElement(o.Path, o.SHA256); i >= 0 {
				if err := d.Delete(o.Path + "/" + strconv.Itoa(i)); err != nil {
					return err
				}
			}
			return pruneCreated(d, o.Created)
		}); err != nil {
			return err
		}
		p.deleteIfEmpty(o, "{}")
	case lock.OwnedTOMLBlock:
		if err := p.EditText(o.File, "remove block "+o.Path, func(s string) (string, error) { return edit.RemoveTOML(s, o.Path) }); err != nil {
			return err
		}
		p.deleteIfEmpty(o, "")
	case lock.OwnedMDBlock:
		if err := p.EditText(o.File, "remove managed block", edit.MDMarkers.Remove); err != nil {
			return err
		}
		p.deleteIfEmpty(o, "")
	}
	p.disown = append(p.disown, o.ID())
	return nil
}

// pruneCreated deletes parents piste created, deepest first, while empty.
func pruneCreated(d *edit.JSON, created []string) error {
	ptrs := append([]string(nil), created...)
	sort.Slice(ptrs, func(i, j int) bool { return len(ptrs[i]) > len(ptrs[j]) })
	for _, ptr := range ptrs {
		if ptr == "#file" || !d.Has(ptr) || d.Len(ptr) > 0 {
			continue
		}
		raw, err := d.Get(ptr)
		if err != nil || (string(raw) != "{}" && string(raw) != "[]") {
			continue
		}
		if err := d.Delete(ptr); err != nil {
			return err
		}
	}
	return nil
}

// deleteIfEmpty turns a pending edit into a removal when piste created the
// file and nothing but an empty document (empty = "{}" for JSON, blank for
// text) is left.
func (p *Plan) deleteIfEmpty(o lock.Owned, empty string) {
	created := false
	for _, c := range o.Created {
		created = created || c == "#file"
	}
	f, ok := p.files[o.File]
	if !created || !ok {
		return
	}
	content := strings.TrimSpace(string(f.data))
	if empty == "{}" {
		if c, err := edit.Canonical(f.data); err == nil {
			content = string(c)
		}
	}
	if content != empty {
		return
	}
	f.data = f.orig
	for _, r := range p.removes {
		if r.rel == o.File {
			return
		}
	}
	p.removes = append(p.removes, removeOp{rel: o.File, abs: f.abs})
}

func (p *Plan) guardRemove(abs string, link bool) error {
	if link {
		// The link itself is ours; its parents must still be real dirs.
		return fsx.Guard(p.Root, abs, func(string) bool { return true })
	}
	return p.guard(abs)
}

// editDiff shows what changed in an edited owned file, when the command
// told us what piste wrote.
func (p *Plan) editDiff(o lock.Owned) string {
	want, ok := p.Expected[o.File]
	if !ok || o.Kind != lock.OwnedFile {
		return ""
	}
	b, err := os.ReadFile(p.abs(o.File))
	if err != nil {
		return ""
	}
	return "\n" + strings.TrimRight(udiff.Unified(o.File+" (piste)", o.File+" (now)", string(want), string(b)), "\n")
}

// Exists reports whether rel existed before this plan.
func (p *Plan) Exists(rel string) bool {
	if f, ok := p.files[rel]; ok {
		return f.exists
	}
	_, err := os.Lstat(p.abs(rel))
	return err == nil
}

// Changes lists what Apply would do.
func (p *Plan) Changes() []Change {
	var out []Change
	for _, rel := range p.order {
		f := p.files[rel]
		if string(f.data) == string(f.orig) && f.exists {
			continue
		}
		op := "update"
		if !f.exists {
			op = "create"
		}
		out = append(out, Change{op, rel, strings.Join(f.what, "; ")})
	}
	for _, l := range p.links {
		out = append(out, Change{"link", l.rel, "-> " + l.target})
	}
	for _, r := range p.removes {
		out = append(out, Change{"delete", r.rel, ""})
	}
	return out
}

// Diff is a unified diff of every file edit, for --dry-run.
func (p *Plan) Diff() string {
	var b strings.Builder
	for _, rel := range p.order {
		f := p.files[rel]
		if string(f.data) != string(f.orig) {
			b.WriteString(udiff.Unified("a/"+rel, "b/"+rel, string(f.orig), string(f.data)))
		}
	}
	return b.String()
}

// Empty reports whether the plan changes nothing on disk.
func (p *Plan) Empty() bool { return len(p.Changes()) == 0 }

// Apply writes the plan. journal is called with the journal before the
// first rename and with nil after the last one (the caller persists the
// lock both times). Ownership records are updated in p.Lock on success.
func (p *Plan) Apply(journal func(*lock.Journal) error) error {
	changes := p.Changes()
	var staged []*fsx.Staged
	abort := func(err error) error {
		for _, s := range staged {
			s.Abort()
		}
		return err
	}
	n := 0
	for _, rel := range p.order {
		f := p.files[rel]
		if string(f.data) == string(f.orig) && f.exists {
			continue
		}
		n++
		if fsx.Fault == "stage:"+strconv.Itoa(n) {
			return abort(fmt.Errorf("injected fault staging %s (write %d)", rel, n))
		}
		s, err := fsx.Stage(f.abs, f.data, f.mode)
		if err != nil {
			return abort(fmt.Errorf("%s: %w", rel, err))
		}
		staged = append(staged, s)
	}
	if len(changes) > 0 && journal != nil {
		var lines []string
		for _, c := range changes {
			lines = append(lines, c.Op+" "+c.Path)
		}
		if err := journal(&lock.Journal{State: "applying", Started: time.Now().UTC().Format(time.RFC3339), Changes: lines}); err != nil {
			return abort(err)
		}
	}
	for i, s := range staged {
		if err := s.Commit(); err != nil {
			for _, rest := range staged[i+1:] {
				rest.Abort()
			}
			return fmt.Errorf("%s: %w (apply interrupted; the journal lists the planned changes)", s.Path, err)
		}
	}
	for _, l := range p.links {
		if err := fsx.Symlink(l.target, l.abs); err != nil {
			return fmt.Errorf("%s: %w", l.rel, err)
		}
	}
	for _, r := range p.removes {
		if err := remove(r); err != nil {
			return fmt.Errorf("%s: %w", r.rel, err)
		}
		p.pruneDirs(r.rel)
	}
	for _, id := range p.disown {
		p.Lock.Disown(id)
	}
	for _, o := range p.own {
		p.Lock.Own(o)
	}
	sort.SliceStable(p.Lock.Owned, func(i, j int) bool { return p.Lock.Owned[i].ID() < p.Lock.Owned[j].ID() })
	if journal != nil && len(changes) > 0 {
		return journal(nil)
	}
	return nil
}

// pruneDirs removes the now-empty parents of a removed entry inside the
// harness directories (.agents, .claude, .codex), deepest first, so the
// last remove leaves no empty directory tree behind. Anything non-empty
// stays (.agents keeps agents.toml).
func (p *Plan) pruneDirs(rel string) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return
	}
	switch parts[0] {
	case ".agents", ".claude", ".codex":
	default:
		return
	}
	for i := len(parts) - 1; i >= 1; i-- {
		dir := p.abs(strings.Join(parts[:i], "/"))
		if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
			return
		}
		if os.Remove(dir) != nil { // fails when not empty
			return
		}
	}
}

func remove(r removeOp) error {
	if !r.dir {
		err := os.Remove(r.abs)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	// Rename first so the tree vanishes atomically, then delete it.
	tmp := filepath.Join(filepath.Dir(r.abs), "."+filepath.Base(r.abs)+".piste-rm")
	os.RemoveAll(tmp)
	if err := os.Rename(r.abs, tmp); err != nil {
		return err
	}
	return os.RemoveAll(tmp)
}
