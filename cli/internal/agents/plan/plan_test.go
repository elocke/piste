package plan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/fsx"
	"github.com/elocke/piste/cli/internal/agents/lock"
)

func write(t *testing.T, root, rel, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755)
	if err := os.WriteFile(filepath.Join(root, rel), []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, rel string) string {
	b, _ := os.ReadFile(filepath.Join(root, rel))
	return string(b)
}

func snapshot(t *testing.T, root string) map[string]string {
	m := map[string]string{}
	filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			b, _ := os.ReadFile(p)
			m[p] = string(b)
		}
		return nil
	})
	return m
}

// An error in the 3rd of 5 writes → zero files changed, no temp files.
func TestPlanThenApplyAtomic(t *testing.T) {
	root := t.TempDir()
	for i := 1; i <= 5; i++ {
		write(t, root, fmt.Sprintf("f%d", i), "old")
	}
	before := snapshot(t, root)
	p := New(root, &lock.Lock{})
	for i := 1; i <= 5; i++ {
		p.WriteFile(fmt.Sprintf("f%d", i), []byte("new"), 0, "")
	}
	fsx.Fault = "stage:3"
	defer func() { fsx.Fault = "" }()
	journaled := false
	err := p.Apply(func(*lock.Journal) error { journaled = true; return nil })
	if err == nil {
		t.Fatal("want fault")
	}
	if journaled {
		t.Fatal("journal written before staging finished")
	}
	after := snapshot(t, root)
	if len(after) != len(before) {
		t.Fatalf("files left behind: %v", after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed", k)
		}
	}
}

// A planning error (invalid edit) means nothing is applied at all.
func TestPlanErrorWritesNothing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.json", "{}")
	write(t, root, ".codex/config.toml", "[x]\n")
	p := New(root, &lock.Lock{})
	if err := p.EditJSON("a.json", "", func(d *edit.JSON) error { return d.Set("/k", []byte("1")) }); err != nil {
		t.Fatal(err)
	}
	err := p.EditText(".codex/config.toml", "", func(s string) (string, error) {
		return edit.SetTOML(s, "mcp", "[x]\ny = 1\n") // duplicate table
	})
	if err == nil {
		t.Fatal("duplicate accepted")
	}
	// The command stops here; a.json was never touched.
	if read(t, root, "a.json") != "{}" {
		t.Fatal("written during planning")
	}
}

func TestApplyJournalsAndOwns(t *testing.T) {
	root := t.TempDir()
	l := &lock.Lock{}
	p := New(root, l)
	p.WriteFile("x.md", []byte("hi"), 0, "create")
	p.Own(lock.Owned{File: "x.md", Kind: lock.OwnedFile, SHA256: lock.SumString("hi")})
	var states []string
	if err := p.Apply(func(j *lock.Journal) error {
		if j == nil {
			states = append(states, "done")
		} else {
			states = append(states, j.State+":"+strings.Join(j.Changes, ","))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(states, "|") != "applying:create x.md|done" || len(l.Owned) != 1 {
		t.Fatal(states, l.Owned)
	}
}

// With 25 hand skills + 2 ours, removing ours leaves exactly 25; an
// edited owned skill is refused until --force.
func TestRemoveOnlyLocked(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 25; i++ {
		write(t, root, fmt.Sprintf(".claude/skills/hand%02d/SKILL.md", i), "hand")
	}
	l := &lock.Lock{}
	for _, n := range []string{"ours1", "ours2"} {
		write(t, root, ".claude/skills/"+n+"/SKILL.md", n)
		s, _ := lock.TreeSum(filepath.Join(root, ".claude/skills", n))
		l.Own(lock.Owned{File: ".claude/skills/" + n, Kind: lock.OwnedDir, SHA256: s, Item: "skill:" + n})
	}
	write(t, root, ".claude/skills/ours2/SKILL.md", "hand-edited")

	p := New(root, l)
	if err := p.Remove(l.OwnedBy("skill:ours1")[0]); err != nil {
		t.Fatal(err)
	}
	err := p.Remove(l.OwnedBy("skill:ours2")[0])
	if !errors.Is(err, ErrEdited) {
		t.Fatalf("edited skill: %v", err)
	}
	if err := p.Apply(nil); err != nil {
		t.Fatal(err)
	}
	es, _ := os.ReadDir(filepath.Join(root, ".claude/skills"))
	if len(es) != 26 { // 25 hand + the refused ours2
		t.Fatalf("%d entries", len(es))
	}

	p = New(root, l)
	p.Force = true
	if err := p.Remove(l.OwnedBy("skill:ours2")[0]); err != nil {
		t.Fatal(err)
	}
	p.Apply(nil)
	es, _ = os.ReadDir(filepath.Join(root, ".claude/skills"))
	if len(es) != 25 || len(l.Owned) != 0 {
		t.Fatalf("%d entries, owned %v", len(es), l.Owned)
	}
}

// Only our keys are added and removed; foreign siblings stay byte-identical.
func TestOwnOnlyJSON(t *testing.T) {
	root := t.TempDir()
	orig := "{\n  \"mcpServers\": {\n    \"a\": {\"command\": \"a\"},\n    \"b\": {\"url\": \"http://b\"}\n  }\n}\n"
	write(t, root, ".mcp.json", orig)
	l := &lock.Lock{}
	p := New(root, l)
	raw := []byte(`{"command":"piste-demo"}`)
	p.EditJSON(".mcp.json", "", func(d *edit.JSON) error { return d.Set("/mcpServers/piste-demo", raw) })
	h, _ := edit.Hash(raw)
	p.Own(lock.Owned{File: ".mcp.json", Kind: lock.OwnedJSON, Path: "/mcpServers/piste-demo", SHA256: h})
	if err := p.Apply(nil); err != nil {
		t.Fatal(err)
	}
	p = New(root, l)
	if err := p.Remove(l.Owned[0]); err != nil {
		t.Fatal(err)
	}
	p.Apply(nil)
	if got := read(t, root, ".mcp.json"); got != orig {
		t.Fatalf("not byte-identical:\n%s", got)
	}
}

// A foreign symlink in the way is refused; an owned one is replaced.
func TestLinkRefusesForeign(t *testing.T) {
	root := t.TempDir()
	checkout := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".claude/skills"), 0o755)
	os.Symlink(checkout, filepath.Join(root, ".claude/skills/x"))
	p := New(root, &lock.Lock{})
	if err := p.Link(".claude/skills/x", "../../.agents/skills/x"); !errors.Is(err, fsx.ErrRefused) {
		t.Fatalf("got %v", err)
	}
	if err := p.WriteFile(".claude/skills/x/SKILL.md", []byte("x"), 0, ""); !errors.Is(err, fsx.ErrRefused) {
		t.Fatalf("write through link: %v", err)
	}
	l := &lock.Lock{}
	l.Own(lock.Owned{File: ".claude/skills/x", Kind: lock.OwnedLink})
	p = New(root, l)
	if err := p.Link(".claude/skills/x", "../../.agents/skills/x"); err != nil {
		t.Fatal(err)
	}
	p.Apply(nil)
	if tgt, _ := os.Readlink(filepath.Join(root, ".claude/skills/x")); tgt != "../../.agents/skills/x" {
		t.Fatal(tgt)
	}
}

func TestChangesAndDiff(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a", "1\n")
	p := New(root, &lock.Lock{})
	p.WriteFile("a", []byte("2\n"), 0, "bump")
	p.WriteFile("b", []byte("new\n"), 0, "")
	p.WriteFile("a", []byte("2\n"), 0, "again")
	cs := p.Changes()
	if len(cs) != 2 || cs[0].Op != "update" || cs[1].Op != "create" {
		t.Fatal(cs)
	}
	if d := p.Diff(); !strings.Contains(d, "-1\n+2") {
		t.Fatal(d)
	}
	noop := New(root, &lock.Lock{})
	noop.WriteFile("a", []byte("1\n"), 0, "")
	if !noop.Empty() {
		t.Fatal("identical content is a change")
	}
}

func TestRemoveRefusedShowsDiff(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".claude/agents/r.md", "mine\n")
	l := &lock.Lock{}
	l.Own(lock.Owned{File: ".claude/agents/r.md", Kind: lock.OwnedFile, SHA256: lock.SumString("ours\n")})
	p := New(root, l)
	p.Expected = map[string][]byte{".claude/agents/r.md": []byte("ours\n")}
	err := p.Remove(l.Owned[0])
	if !errors.Is(err, ErrEdited) || !strings.Contains(err.Error(), "-ours\n+mine") {
		t.Fatalf("%v", err)
	}
}
