// Package adapter renders the canonical model (package canon) for each
// harness: claude, claude-plugin (the piste-harness carrier), codex, hermes and
// agentsmd.
//
// Adapters never write files. They return Artifacts: whole files, trees,
// links, or one owned path inside a shared file (a JSON pointer, an array
// entry, a TOML managed block, a Markdown managed block). The writer (package
// plan) applies them and records each in the lock, so piste only ever touches
// what it wrote.
package adapter

import (
	"os"
	"sort"

	"github.com/elocke/piste/cli/internal/agents/lock"
)

// Artifact is one thing a render produces. Exactly one payload is set.
type Artifact struct {
	Harness string
	Kind    lock.Kind
	Item    string // the canonical item name
	File    string // path relative to the scope root

	Whole []byte      // the whole file is ours
	Mode  os.FileMode // for Whole (0 = default)

	Tree map[string]TreeFile // a directory we own (skill copies)

	Link string // a symlink to this (relative) target

	JSONPointer string // one value we own in a JSON/JSONC file
	JSONValue   []byte

	JSONArray string // one entry we own in the array at this pointer
	JSONEntry []byte

	TOMLBlock string // a piste managed block in a TOML file
	TOMLBody  string

	MDBlock *string // the piste-agents block of a Markdown file
}

// TreeFile is one file of a Tree.
type TreeFile struct {
	Data []byte
	Mode os.FileMode
}

// Result is a render: artifacts plus warnings for anything lossy.
type Result struct {
	Artifacts []Artifact
	Warnings  []string
}

func (r *Result) add(a Artifact) { r.Artifacts = append(r.Artifacts, a) }
func (r *Result) warn(s string)  { r.Warnings = append(r.Warnings, s) }
func (r *Result) merge(o Result) {
	r.Artifacts = append(r.Artifacts, o.Artifacts...)
	r.Warnings = append(r.Warnings, o.Warnings...)
}
func (r *Result) sortByFile() *Result {
	sort.SliceStable(r.Artifacts, func(i, j int) bool { return r.Artifacts[i].File < r.Artifacts[j].File })
	return r
}

func str(s string) *string { return &s }
