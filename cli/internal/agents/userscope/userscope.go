// Package userscope is `piste agents apply` / `piste agents check` for $HOME.
// Output, exit codes, messages and the ledger format are pinned by the
// frozen goldens in cli/internal/agents/testdata/golden.
//
// The Nix module decides everything and writes a manifest; this only splices
// each composed instruction block into its marked section of
// ~/.claude/CLAUDE.md / ~/.codex/AGENTS.md (apply, at activation) and
// compares the manifest with the disk (check, read-only). Home-manager owns
// the skill links.
package userscope

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/fsx"
	"github.com/elocke/piste/cli/internal/agents/lock"
)

// ManifestVersion is the manifest format the module writes.
const ManifestVersion = 2

// Markers of the managed block.
const (
	Begin = "<!-- piste-agents:begin -->"
	End   = "<!-- piste-agents:end -->"
)

// Link is one declared skill link (home-manager owns it; we only check it).
type Link struct {
	Target  string `json:"target"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Harness string `json:"harness"`
	Repo    string `json:"repo"`
	Source  string `json:"source"`
}

// Instruction is one declared managed block.
type Instruction struct {
	Target    string   `json:"target"`
	Harness   string   `json:"harness"`
	Fragments []string `json:"fragments"`
	Block     string   `json:"block"` // store path of the composed block
	SHA256    string   `json:"sha256"`
}

// Manifest is ~/.config/piste-agents/manifest.json.
type Manifest struct {
	Version      any           `json:"version"`
	Links        []Link        `json:"links"`
	Instructions []Instruction `json:"instructions"`
	Deferred     []string      `json:"deferred"`

	// Raw is the whole manifest in key order (hooks, thirdParty, …).
	Raw *Obj `json:"-"`
}

// Paths under a home.
func DefaultManifest(home string) string {
	return filepath.Join(home, ".config", "piste-agents", "manifest.json")
}
func DefaultLedger(home string) string {
	return filepath.Join(home, ".local", "state", "piste-agents", "ledger.json")
}

// ErrExit carries a message printed to stderr and exit status 1.
type ErrExit struct{ Msg string }

func (e ErrExit) Error() string { return e.Msg }

// LoadManifest reads and version-checks the manifest.
func LoadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrExit{fmt.Sprintf("piste-agents: no manifest at %s (is piste.agents enabled?)", path)}
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	raw, err := ParseOrdered(b)
	if err != nil {
		return nil, err
	}
	if m.Raw, _ = raw.(*Obj); m.Raw == nil {
		m.Raw = NewObj()
	}
	if v, ok := m.Version.(float64); !ok || v != ManifestVersion {
		return nil, ErrExit{fmt.Sprintf("piste-agents: manifest version %s, expected %d", str(m.Version), ManifestVersion)}
	}
	return &m, nil
}

// refused is a managed block that piste declines to overwrite.
type refused struct{ msg string }

func (r refused) Error() string { return r.msg }

// guardTarget refuses symlinks (the target, or any parent up to and
// including home) and store paths.
func guardTarget(path, home string) error {
	if isSymlink(path) {
		return refused{path + " is a symlink; refusing to write through it"}
	}
	if strings.HasPrefix(resolve(path), fsx.StorePrefix) {
		return refused{path + " resolves into /nix/store"}
	}
	parent := filepath.Dir(path)
	for {
		if isSymlink(parent) {
			return refused{parent + " is a symlink; refusing to write through it"}
		}
		if parent == home || parent == filepath.Dir(parent) {
			break
		}
		parent = filepath.Dir(parent)
	}
	return nil
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&fs.ModeSymlink != 0
}

// resolve is a non-strict path resolve: symlinks resolved where
// the path exists, the rest appended.
func resolve(p string) string {
	p, _ = filepath.Abs(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	// Resolve the longest existing prefix, then follow a dangling link's
	// target text.
	if t, err := os.Readlink(p); err == nil {
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(p), t)
		}
		if t != p {
			return resolve(t)
		}
	}
	dir, base := filepath.Split(p)
	dir = filepath.Clean(dir)
	if dir == p {
		return p
	}
	return filepath.Join(resolve(dir), base)
}

func span(path, text string) (start, endStart int, ok bool, err error) {
	return spanOf(path, text, Begin, End)
}

func spanOf(path, text, Begin, End string) (start, endStart int, ok bool, err error) {
	b, e := strings.Count(text, Begin), strings.Count(text, End)
	if b != e || b > 1 {
		return 0, 0, false, refused{fmt.Sprintf("%s: found %d begin and %d end marker(s); expected 0 or 1 of each", path, b, e)}
	}
	if b == 0 {
		return 0, 0, false, nil
	}
	start, endStart = strings.Index(text, Begin), strings.Index(text, End)
	if endStart < start {
		return 0, 0, false, refused{path + ": end marker precedes begin marker"}
	}
	return start, endStart, true, nil
}

// managedBlock returns the block content of path (nil if none).
func managedBlock(path string) (block *string, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := string(b)
	start, endStart, ok, err := spanOf(path, text, Begin, End)
	if err != nil || !ok {
		return nil, err
	}
	s := strings.Trim(text[start+len(Begin):endStart], "\n")
	return &s, nil
}

func splice(path, block string) error {
	managed := Begin + "\n" + block + "\n" + End + "\n"
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return fsx.WriteAtomic(path, []byte(managed), 0)
	}
	if err != nil {
		return err
	}
	text := string(b)
	start, endStart, ok, err := span(path, text)
	if err != nil {
		return err
	}
	if !ok {
		sep, lead := "", "\n"
		if !strings.HasSuffix(text, "\n") && text != "" {
			sep = "\n"
		}
		if text == "" {
			lead = ""
		}
		return fsx.WriteAtomic(path, []byte(text+sep+lead+managed), 0)
	}
	out := text[:start] + strings.TrimRight(managed, "\n") + text[endStart+len(End):]
	return fsx.WriteAtomic(path, []byte(out), 0)
}

func sha(s string) string { return lock.SumString(s) }

// Apply splices every declared block; it returns the number of refusals.
// Other sections of the ledger (the hook records) are kept.
func Apply(m *Manifest, ledgerPath, home string, force bool, out, errw io.Writer) (int, error) {
	ledger, err := loadLedger(ledgerPath, newLedger)
	if err != nil {
		return 0, err
	}
	written := map[string]string{}
	if w, ok := ledger.Get("instructions").(*Obj); ok {
		for _, k := range w.Keys {
			if s, ok := w.Vals[k].(string); ok {
				written[k] = s
			}
		}
	}
	refusals := 0
	for _, e := range m.Instructions {
		target := filepath.Join(home, e.Target)
		raw, err := os.ReadFile(e.Block)
		if err != nil {
			return refusals, err
		}
		block := strings.Trim(string(raw), "\n")
		newHash := sha(block)
		err = func() error {
			if err := guardTarget(target, home); err != nil {
				return err
			}
			cur, err := managedBlock(target)
			if err != nil {
				return err
			}
			if cur != nil && sha(*cur) == newHash {
				written[e.Target] = newHash
				return nil
			}
			last, had := written[e.Target]
			if cur != nil && had && sha(*cur) != last && !force {
				return refused{target + ": the managed block was edited since piste-agents last wrote it; " +
					"not overwriting (edit the fragment instead, or `piste agents apply --force`)"}
			}
			if err := splice(target, block); err != nil {
				return err
			}
			written[e.Target] = newHash
			fmt.Fprintf(out, "piste-agents: updated managed block in %s\n", target)
			return nil
		}()
		var r refused
		if errors.As(err, &r) {
			refusals++
			fmt.Fprintf(errw, "piste-agents: REFUSED %s\n", r.msg)
		} else if err != nil {
			return refusals, err
		}
	}
	declared := map[string]bool{}
	for _, e := range m.Instructions {
		declared[e.Target] = true
	}
	var orphans []string
	for t := range written {
		if !declared[t] {
			orphans = append(orphans, t)
		}
	}
	sort.Strings(orphans)
	for _, o := range orphans {
		fmt.Fprintf(out, "piste-agents: %s still holds a managed block that is no longer declared (left in place)\n", filepath.Join(home, o))
	}
	w := NewObj()
	for k, v := range written {
		w.Set(k, v)
	}
	// The block hashes don't depend on the ledger version: an older ledger
	// keeps its version (and its hook records) for sync --user to migrate.
	ledger.Set("instructions", w)
	return refusals, writeLedger(ledgerPath, ledger)
}

// Row is one check result.
type Row struct {
	State  string `json:"state"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Detail string `json:"detail"`
}

// Healthy is the set of states check accepts as healthy. It includes
// "undeclared", which is only a warning for a generation without a zone (no
// third-party declarations).
var Healthy = map[string]bool{"ok": true, "deferred": true, "unmanaged": true, "foreign": true,
	"tolerated": true, "floating": true, "undeclared": true}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func classifyLink(e Link, home string) string {
	path := filepath.Join(home, e.Target)
	if !isSymlink(path) {
		if exists(path) {
			return "foreign"
		}
		return "missing"
	}
	if resolve(path) != resolve(e.Source) {
		return "foreign"
	}
	if exists(e.Source) {
		return "ok"
	}
	return "broken"
}

var (
	frontmatter = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n`)
	nameLine    = regexp.MustCompile(`(?m)^name:\s*['"]?([^'"\n]+?)['"]?\s*$`)
	descLine    = regexp.MustCompile(`(?m)^description:\s*\S`)
)

// SkillProblems: a skill without name/description is listed name-only or
// not at all.
// expected is the link name (#38): a third-party skill locked at path "."
// is the fetched repo root, whose store dir is not named after it.
func SkillProblems(dir, expected string) []string {
	b, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return []string{"SKILL.md not found"}
	}
	m := frontmatter.FindStringSubmatch(string(b))
	if m == nil {
		return []string{"no YAML frontmatter"}
	}
	var p []string
	if n := nameLine.FindStringSubmatch(m[1]); n == nil {
		p = append(p, "frontmatter missing 'name'")
	} else if expected == "" && n[1] != filepath.Base(dir) {
		p = append(p, fmt.Sprintf("name '%s' != directory '%s'", n[1], filepath.Base(dir)))
	} else if expected != "" && n[1] != expected {
		p = append(p, fmt.Sprintf("name '%s' != link '%s'", n[1], expected))
	}
	if !descLine.MatchString(m[1]) {
		p = append(p, "frontmatter missing 'description'")
	}
	return p
}

func classifyInstruction(e Instruction, home string) string {
	path := filepath.Join(home, e.Target)
	if !exists(path) {
		return "missing"
	}
	if guardTarget(path, home) != nil {
		return "refused"
	}
	cur, err := managedBlock(path)
	if err != nil {
		return "refused"
	}
	if cur == nil || sha(*cur) != e.SHA256 {
		return "stale"
	}
	return "ok"
}

// Check compares the manifest with the disk (read-only).
func Check(m *Manifest, home string) []Row {
	rows := []Row{}
	declared := map[string]bool{}
	for _, e := range m.Links {
		declared[e.Target] = true
		state, detail := classifyLink(e, home), e.Source
		if state == "ok" && e.Kind == "skills" {
			if p := SkillProblems(e.Source, e.Name); len(p) > 0 {
				state, detail = "invalid", strings.Join(p, "; ")
			}
		}
		rows = append(rows, Row{state, e.Kind, e.Target, detail})
	}
	for _, e := range m.Instructions {
		rows = append(rows, Row{classifyInstruction(e, home), "instructions", e.Target, strings.Join(e.Fragments, ", ")})
	}
	deferred := map[string]bool{}
	for _, n := range m.Deferred {
		rows = append(rows, Row{"deferred", "skills", n, "third-party; not locked yet (piste agents update)"})
		deferred[n] = true
	}
	dirSet := map[string]bool{}
	for t := range declared {
		if strings.Contains(t, "/skills/") {
			dirSet[filepath.Dir(t)] = true
		}
	}
	dirs := make([]string, 0, len(dirSet))
	for d := range dirSet {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		es, err := os.ReadDir(filepath.Join(home, d))
		if err != nil {
			continue
		}
		for _, c := range es {
			rel := d + "/" + c.Name()
			if strings.HasPrefix(c.Name(), ".") || declared[rel] || deferred[c.Name()] {
				continue
			}
			rows = append(rows, Row{"unmanaged", "skills", rel, "not declared by any preset"})
		}
	}
	return rows
}

// Bad reports rows outside the healthy states (no zone).
func Bad(rows []Row) bool { return BadWith(rows, Healthy) }

// BadWith reports rows outside the given healthy states.
func BadWith(rows []Row, healthy map[string]bool) bool {
	for _, r := range rows {
		if !healthy[r.State] {
			return true
		}
	}
	return false
}

// Text is the human table + summary, one row per line.
func Text(rows []Row) string { return TextWith(rows, Healthy) }

// TextWith renders rows as text, with the zone's healthy states.
func TextWith(rows []Row, healthy map[string]bool) string {
	var b strings.Builder
	counts := map[string]int{}
	for _, r := range rows {
		fmt.Fprintf(&b, "%-10s %-18s %s  %s\n", r.State, r.Kind, r.Target, r.Detail)
		counts[r.State]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	verdict := "OK"
	if BadWith(rows, healthy) {
		verdict = "FAIL"
	}
	fmt.Fprintf(&b, "\n%s: %s\n", verdict, strings.Join(parts, ", "))
	return b.String()
}

// JSON is the --json output (indented two spaces).
func JSON(rows []Row) (string, error) {
	return encode(rows, "  ") + "\n", nil
}
