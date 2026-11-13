package userscope

// User-scope apply and check cases.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type env struct {
	t            *testing.T
	home, store  string
	ledger       string
	out, errw    bytes.Buffer
	blockCounter int
}

func newEnv(t *testing.T) *env {
	d := t.TempDir()
	e := &env{t: t, home: filepath.Join(d, "home"), store: filepath.Join(d, "store")}
	os.MkdirAll(e.home, 0o755)
	os.MkdirAll(e.store, 0o755)
	e.ledger = filepath.Join(e.home, ".local/state/piste-agents/ledger.json")
	return e
}

func (e *env) manifest(block string, links []Link, deferred []string) *Manifest {
	e.blockCounter++
	bp := filepath.Join(e.store, "claude-block.md")
	os.WriteFile(bp, []byte(block), 0o644)
	return &Manifest{Version: float64(2), Links: links, Deferred: deferred, Instructions: []Instruction{{
		Target: ".claude/CLAUDE.md", Harness: "claude",
		Fragments: []string{"piste-agents/instructions/never-do.md"},
		Block:     bp, SHA256: sha(block),
	}}}
}

func (e *env) apply(m *Manifest, force bool) int {
	e.out.Reset()
	e.errw.Reset()
	n, err := Apply(m, e.ledger, e.home, force, &e.out, &e.errw)
	if err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) path(rel string) string { return filepath.Join(e.home, rel) }

func (e *env) write(rel, s string) {
	os.MkdirAll(filepath.Dir(e.path(rel)), 0o755)
	os.WriteFile(e.path(rel), []byte(s), 0o644)
}

func (e *env) read(rel string) string {
	b, _ := os.ReadFile(e.path(rel))
	return string(b)
}

const B, E = Begin, End

func TestCreatesMissingFileWithBlockOnly(t *testing.T) {
	e := newEnv(t)
	if n := e.apply(e.manifest("rule one", nil, nil), false); n != 0 {
		t.Fatal(n)
	}
	if got := e.read(".claude/CLAUDE.md"); got != B+"\nrule one\n"+E+"\n" {
		t.Fatalf("%q", got)
	}
}

func TestAppendsBlockAndKeepsHandContent(t *testing.T) {
	e := newEnv(t)
	e.write(".claude/CLAUDE.md", "# mine\nkeep me")
	e.apply(e.manifest("rule one", nil, nil), false)
	if got := e.read(".claude/CLAUDE.md"); got != "# mine\nkeep me\n\n"+B+"\nrule one\n"+E+"\n" {
		t.Fatalf("%q", got)
	}
}

func TestReplacesOnlyTheBlock(t *testing.T) {
	e := newEnv(t)
	e.write(".claude/CLAUDE.md", "top\n"+B+"\nold\n"+E+"\nbottom <!-- a comment -->\n")
	e.apply(e.manifest("new", nil, nil), false)
	if got := e.read(".claude/CLAUDE.md"); got != "top\n"+B+"\nnew\n"+E+"\nbottom <!-- a comment -->\n" {
		t.Fatalf("%q", got)
	}
}

func TestIdempotent(t *testing.T) {
	e := newEnv(t)
	m := e.manifest("rule one", nil, nil)
	e.apply(m, false)
	before := e.read(".claude/CLAUDE.md")
	fi1, _ := os.Stat(e.path(".claude/CLAUDE.md"))
	e.apply(m, false)
	fi2, _ := os.Stat(e.path(".claude/CLAUDE.md"))
	if e.read(".claude/CLAUDE.md") != before || !fi1.ModTime().Equal(fi2.ModTime()) {
		t.Fatal("rewritten")
	}
	if e.out.Len() != 0 {
		t.Fatalf("printed %q", e.out.String())
	}
}

func TestKeepsFileMode(t *testing.T) {
	e := newEnv(t)
	e.write(".claude/CLAUDE.md", "x\n")
	os.Chmod(e.path(".claude/CLAUDE.md"), 0o600)
	e.apply(e.manifest("rule one", nil, nil), false)
	fi, _ := os.Stat(e.path(".claude/CLAUDE.md"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatal(fi.Mode())
	}
}

func TestRefusesSymlinkedTarget(t *testing.T) {
	e := newEnv(t)
	real := e.path("elsewhere.md")
	os.WriteFile(real, []byte("mine\n"), 0o644)
	os.MkdirAll(e.path(".claude"), 0o755)
	os.Symlink(real, e.path(".claude/CLAUDE.md"))
	if n := e.apply(e.manifest("rule one", nil, nil), false); n != 1 {
		t.Fatal(n)
	}
	if !strings.Contains(e.errw.String(), "symlink") {
		t.Fatal(e.errw.String())
	}
	if b, _ := os.ReadFile(real); string(b) != "mine\n" {
		t.Fatal("wrote through the link")
	}
}

func TestRefusesSymlinkedParent(t *testing.T) {
	e := newEnv(t)
	repo := e.path("repo-checkout")
	os.MkdirAll(repo, 0o755)
	os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("repo file\n"), 0o644)
	os.Symlink(repo, e.path(".claude"))
	if n := e.apply(e.manifest("rule one", nil, nil), false); n != 1 {
		t.Fatal(n)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "CLAUDE.md")); string(b) != "repo file\n" {
		t.Fatal("wrote into the checkout")
	}
}

func TestRefusesCorruptMarkers(t *testing.T) {
	e := newEnv(t)
	text := B + "\na\n" + E + "\n" + B + "\nb\n"
	e.write(".claude/CLAUDE.md", text)
	if n := e.apply(e.manifest("x", nil, nil), false); n != 1 || e.read(".claude/CLAUDE.md") != text {
		t.Fatal(n)
	}
}

func TestRefusesHandEditedBlockUnlessForced(t *testing.T) {
	e := newEnv(t)
	e.apply(e.manifest("v1", nil, nil), false)
	e.write(".claude/CLAUDE.md", strings.Replace(e.read(".claude/CLAUDE.md"), "v1", "hand edit", 1))
	if n := e.apply(e.manifest("v2", nil, nil), false); n != 1 || !strings.Contains(e.errw.String(), "edited") {
		t.Fatal(n, e.errw.String())
	}
	if !strings.Contains(e.read(".claude/CLAUDE.md"), "hand edit") {
		t.Fatal("overwritten")
	}
	if n := e.apply(e.manifest("v2", nil, nil), true); n != 0 || !strings.Contains(e.read(".claude/CLAUDE.md"), "v2") {
		t.Fatal(n)
	}
}

func TestAdoptsExistingBlockWithoutLedger(t *testing.T) {
	e := newEnv(t)
	e.write(".claude/CLAUDE.md", B+"\nfrom august\n"+E+"\n")
	if n := e.apply(e.manifest("now", nil, nil), false); n != 0 || e.read(".claude/CLAUDE.md") != B+"\nnow\n"+E+"\n" {
		t.Fatal(n)
	}
}

func TestNoTempFilesLeft(t *testing.T) {
	e := newEnv(t)
	e.apply(e.manifest("x", nil, nil), false)
	es, _ := os.ReadDir(e.path(".claude"))
	for _, x := range es {
		if strings.HasSuffix(x.Name(), ".tmp") {
			t.Fatal(x.Name())
		}
	}
}

// The ledger keeps sections another writer added (the hook records).
func TestLedgerKeepsOtherSections(t *testing.T) {
	e := newEnv(t)
	os.MkdirAll(filepath.Dir(e.ledger), 0o755)
	os.WriteFile(e.ledger, []byte(`{"version": 1, "instructions": {}, "hooks": {"codex": ["x"]}}`), 0o644)
	e.apply(e.manifest("x", nil, nil), false)
	var l map[string]any
	b, _ := os.ReadFile(e.ledger)
	json.Unmarshal(b, &l)
	if l["hooks"] == nil || l["instructions"].(map[string]any)[".claude/CLAUDE.md"] != sha("x") {
		t.Fatalf("%s", b)
	}
	if !strings.HasSuffix(string(b), "}\n") || !strings.HasPrefix(string(b), "{\n  \"hooks\"") {
		t.Fatalf("format %q", b)
	}
}

func TestOrphanReported(t *testing.T) {
	e := newEnv(t)
	os.MkdirAll(filepath.Dir(e.ledger), 0o755)
	os.WriteFile(e.ledger, []byte(`{"version": 1, "instructions": {".codex/AGENTS.md": "abc"}}`), 0o644)
	e.apply(e.manifest("x", nil, nil), false)
	if !strings.Contains(e.out.String(), ".codex/AGENTS.md still holds a managed block that is no longer declared (left in place)") {
		t.Fatal(e.out.String())
	}
}

// --- check -------------------------------------------------------------------------

func (e *env) skill(name, fm string) string {
	d := filepath.Join(e.store, "skills", name)
	os.MkdirAll(d, 0o755)
	if fm == "" {
		fm = "---\nname: " + name + "\ndescription: A skill.\n---\nbody\n"
	}
	os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(fm), 0o644)
	return d
}

func (e *env) link(name string) Link {
	return Link{Target: ".claude/skills/" + name, Kind: "skills", Name: name, Harness: "claude",
		Repo: "piste-agents", Source: filepath.Join(e.store, "skills", name)}
}

func states(rows []Row) map[[2]string]bool {
	m := map[[2]string]bool{}
	for _, r := range rows {
		m[[2]string{r.State, r.Target}] = true
	}
	return m
}

func TestLinkStates(t *testing.T) {
	e := newEnv(t)
	e.skill("ok-skill", "")
	os.MkdirAll(e.path(".claude/skills"), 0o755)
	os.Symlink(filepath.Join(e.store, "skills/ok-skill"), e.path(".claude/skills/ok-skill"))
	m := e.manifest("rule one", []Link{e.link("ok-skill"), e.link("missing-skill")}, nil)
	e.apply(m, false)
	s := states(Check(m, e.home))
	for _, want := range [][2]string{{"ok", ".claude/skills/ok-skill"}, {"missing", ".claude/skills/missing-skill"}, {"ok", ".claude/CLAUDE.md"}} {
		if !s[want] {
			t.Errorf("missing %v in %v", want, s)
		}
	}
}

func TestForeignCopyAndUnmanaged(t *testing.T) {
	e := newEnv(t)
	e.skill("mine", "")
	os.MkdirAll(e.path(".claude/skills/mine"), 0o755)
	os.MkdirAll(e.path(".claude/skills/hand-made"), 0o755)
	os.MkdirAll(e.path(".claude/skills/paseo"), 0o755)
	m := e.manifest("x", []Link{e.link("mine")}, []string{"paseo"})
	s := states(Check(m, e.home))
	for _, want := range [][2]string{{"foreign", ".claude/skills/mine"}, {"unmanaged", ".claude/skills/hand-made"}, {"deferred", "paseo"}} {
		if !s[want] {
			t.Errorf("missing %v", want)
		}
	}
	if s[[2]string{"unmanaged", ".claude/skills/paseo"}] {
		t.Error("deferred listed as unmanaged")
	}
}

func TestNameOnlySkillIsInvalid(t *testing.T) {
	e := newEnv(t)
	e.skill("bad", "---\nname: bad\n---\nno description\n")
	os.MkdirAll(e.path(".claude/skills"), 0o755)
	os.Symlink(filepath.Join(e.store, "skills/bad"), e.path(".claude/skills/bad"))
	for _, r := range Check(e.manifest("x", []Link{e.link("bad")}, nil), e.home) {
		if r.Target == ".claude/skills/bad" {
			if r.State != "invalid" || !strings.Contains(r.Detail, "description") {
				t.Fatal(r)
			}
			return
		}
	}
	t.Fatal("row missing")
}

func TestBrokenLink(t *testing.T) {
	e := newEnv(t)
	os.MkdirAll(e.path(".claude/skills"), 0o755)
	os.Symlink(filepath.Join(e.store, "skills/gone"), e.path(".claude/skills/gone"))
	if !states(Check(e.manifest("x", []Link{e.link("gone")}, nil), e.home))[[2]string{"broken", ".claude/skills/gone"}] {
		t.Fatal("broken link not reported")
	}
}

func TestStaleBlockAfterFragmentChange(t *testing.T) {
	e := newEnv(t)
	e.apply(e.manifest("v1", nil, nil), false)
	if !states(Check(e.manifest("v2", nil, nil), e.home))[[2]string{"stale", ".claude/CLAUDE.md"}] {
		t.Fatal("not stale")
	}
}

func TestTextAndJSON(t *testing.T) {
	rows := []Row{{"ok", "skills", ".claude/skills/a", "/s/a"}, {"missing", "instructions", ".claude/CLAUDE.md", "f1, f2"}}
	want := "ok         skills             .claude/skills/a  /s/a\nmissing    instructions       .claude/CLAUDE.md  f1, f2\n\nFAIL: 1 missing, 1 ok\n"
	if got := Text(rows); got != want {
		t.Fatalf("%q", got)
	}
	j, _ := JSON([]Row{{"ok", "skills", "é<x>", "d"}})
	if j != "[\n  {\n    \"state\": \"ok\",\n    \"kind\": \"skills\",\n    \"target\": \"é<x>\",\n    \"detail\": \"d\"\n  }\n]\n" {
		t.Fatalf("%q", j)
	}
	if j, _ := JSON([]Row{}); j != "[]\n" {
		t.Fatalf("%q", j)
	}
}

func TestLoadManifestErrors(t *testing.T) {
	d := t.TempDir()
	if _, err := LoadManifest(filepath.Join(d, "none.json")); err == nil || !strings.Contains(err.Error(), "is piste.agents enabled?") {
		t.Fatal(err)
	}
	p := filepath.Join(d, "m.json")
	os.WriteFile(p, []byte(`{"version": 3}`), 0o644)
	if _, err := LoadManifest(p); err == nil || err.Error() != "piste-agents: manifest version 3, expected 2" {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte(`{}`), 0o644)
	if _, err := LoadManifest(p); err == nil || err.Error() != "piste-agents: manifest version null, expected 2" {
		t.Fatal(err)
	}
}
