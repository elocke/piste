package userscope

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Groups a version-1 piste wrote to ~/.codex/hooks.json. A's non-ASCII
// command keys it differently in the two layouts.
const (
	groupA  = `{"matcher": "Bash", "hooks": [{"type": "command", "command": "guard — café", "timeout": 45}]}`
	groupB  = `{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk-rewrite", "timeout": 10}]}`
	groupB2 = `{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk-rewrite --mine", "timeout": 10}]}` // B, edited by hand
	groupC  = `{"matcher": "Bash", "hooks": [{"type": "command", "command": "new-guard", "timeout": 5}]}`
	groupF  = `{"hooks": [{"type": "command", "command": "foreign"}]}`
)

func parse(t *testing.T, s string) any {
	t.Helper()
	v, err := ParseOrdered([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func hooksManifest(t *testing.T, groups ...string) *Manifest {
	raw := `{"version": 2, "hooks": {"codex": {"target": ".codex/hooks.json", "hooks": {"PreToolUse": [` + strings.Join(groups, ", ") + `]}}}}`
	return &Manifest{Version: float64(2), Raw: parse(t, raw).(*Obj)}
}

// A version-1 ledger owning A and B (by their old keys) beside a managed
// block, and the hooks file as the user left it: A untouched, B edited.
func oldLedgerHome(t *testing.T) *env {
	e := newEnv(t)
	e.write(".codex/hooks.json", `{"hooks": {"PreToolUse": [`+groupA+`, `+groupB2+`], "Stop": [`+groupF+`]}}`)
	e.write(".claude/CLAUDE.md", B+"\nrule one\n"+E+"\n")
	old, _ := json.Marshal(map[string]any{
		"version":      1,
		"instructions": map[string]any{".claude/CLAUDE.md": sha("rule one")},
		"codex_groups": []string{legacyGroupID("PreToolUse", parse(t, groupA)), legacyGroupID("PreToolUse", parse(t, groupB))},
		"codex_target": ".codex/hooks.json",
	})
	if legacyGroupID("PreToolUse", parse(t, groupA)) == groupID("PreToolUse", parse(t, groupA)) {
		t.Fatal("group A must key differently in the two layouts")
	}
	e.write(".local/state/piste-agents/ledger.json", string(old))
	return e
}

func (e *env) ledgerObj() *Obj {
	l, err := loadLedger(e.ledger, newLedger)
	if err != nil {
		e.t.Fatal(err)
	}
	return l
}

// syncHooks runs sync --user's hook steps with no harness CLIs: the codex
// trust step always fails (no codex app-server), so it is not counted.
func (e *env) syncHooks(m *Manifest) int {
	cb, xb := ClaudeBin, CodexBin
	ClaudeBin, CodexBin = filepath.Join(e.home, "no-claude"), filepath.Join(e.home, "no-codex")
	defer func() { ClaudeBin, CodexBin = cb, xb }()
	e.out.Reset()
	e.errw.Reset()
	n, err := SyncHooks(m, e.ledger, e.home, false, &e.out, &e.errw)
	if err != nil {
		e.t.Fatal(err)
	}
	if !strings.Contains(e.errw.String(), "piste-agents: codex trust: cannot start `codex app-server`") {
		e.t.Fatalf("codex trust ran: %s", e.errw.String())
	}
	return n - 1
}

func groups(t *testing.T, e *env, event string) []string {
	var out []string
	data := parse(t, e.read(".codex/hooks.json")).(*Obj)
	gs, _ := data.Get("hooks").(*Obj).Get(event).([]any)
	for _, g := range gs {
		out = append(out, canonical(g))
	}
	return out
}

func canon(t *testing.T, s string) string { return canonical(parse(t, s)) }

// sync --user on a version-1 ledger: the untouched group is still piste's
// (retracted, it goes), the hand-edited one is not (it stays), and the
// ledger is version 2 with the new keys. Nothing is refused.
func TestOldLedgerMigratesOnSync(t *testing.T) {
	e := oldLedgerHome(t)
	if n := e.syncHooks(hooksManifest(t, groupC)); n != 0 {
		t.Fatalf("%d failures: %s", n, e.errw.String())
	}
	got := groups(t, e, "PreToolUse")
	want := []string{canon(t, groupB2), canon(t, groupC)}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("PreToolUse groups:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if f := groups(t, e, "Stop"); len(f) != 1 || f[0] != canon(t, groupF) {
		t.Fatalf("foreign group: %v", f)
	}
	l := e.ledgerObj()
	if ledgerVersion(l) != LedgerVersion {
		t.Fatalf("version %v", l.Get("version"))
	}
	if ids := str(l.Get("codex_groups")); ids != `["`+groupID("PreToolUse", parse(t, groupC))+`"]` {
		t.Fatalf("codex_groups %s", ids)
	}
	if s := str(l.Get("instructions")); s != `{".claude/CLAUDE.md":"`+sha("rule one")+`"}` {
		t.Fatalf("instructions %s", s)
	}
	// The managed block is still piste's: apply rewrites it without --force.
	if n := e.apply(e.manifest("rule two", nil, nil), false); n != 0 {
		t.Fatalf("refused: %s", e.errw.String())
	}
}

// The migration itself: owned groups still as written are re-keyed, the
// edited one is dropped; a version-2 ledger is left alone.
func TestMigrateLedgerRekeys(t *testing.T) {
	e := oldLedgerHome(t)
	l := e.ledgerObj()
	if err := migrateLedger(l, e.home); err != nil {
		t.Fatal(err)
	}
	if ids := str(l.Get("codex_groups")); ids != `["`+groupID("PreToolUse", parse(t, groupA))+`"]` {
		t.Fatalf("codex_groups %s", ids)
	}
	if ledgerVersion(l) != LedgerVersion {
		t.Fatal(l.Get("version"))
	}
	before := encode(l, "")
	if err := migrateLedger(l, e.home); err != nil || encode(l, "") != before {
		t.Fatalf("a current ledger changed: %v %s", err, encode(l, ""))
	}
	// An older piste-agents apply (rollback) relabels a version-2 ledger as
	// version 1 and keeps its keys: the migration takes the current key too,
	// so A stays owned.
	l.Set("version", 1)
	if err := migrateLedger(l, e.home); err != nil {
		t.Fatal(err)
	}
	if ids := str(l.Get("codex_groups")); ids != `["`+groupID("PreToolUse", parse(t, groupA))+`"]` || ledgerVersion(l) != LedgerVersion {
		t.Fatalf("relabelled ledger: version %v, codex_groups %s", l.Get("version"), ids)
	}
}

// After that relabel, sync --user still retracts the group piste owns.
func TestRelabelledLedgerRetractsOnSync(t *testing.T) {
	e := oldLedgerHome(t)
	l := e.ledgerObj()
	l.Set("codex_groups", []any{groupID("PreToolUse", parse(t, groupA))})
	if err := writeLedger(e.ledger, l); err != nil {
		t.Fatal(err)
	}
	if n := e.syncHooks(hooksManifest(t, groupC)); n != 0 {
		t.Fatalf("%d failures: %s", n, e.errw.String())
	}
	got := groups(t, e, "PreToolUse")
	want := []string{canon(t, groupB2), canon(t, groupC)}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("PreToolUse groups:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An empty hooks file is an empty one to the migration too, as to sync:
// nothing is owned any more, and the codex step doesn't fail.
func TestOldLedgerEmptyHooksFile(t *testing.T) {
	e := oldLedgerHome(t)
	e.write(".codex/hooks.json", "")
	l := e.ledgerObj()
	if err := migrateLedger(l, e.home); err != nil {
		t.Fatal(err)
	}
	if ids := str(l.Get("codex_groups")); ids != "[]" || ledgerVersion(l) != LedgerVersion {
		t.Fatalf("version %v, codex_groups %s", l.Get("version"), ids)
	}
	e = oldLedgerHome(t)
	e.write(".codex/hooks.json", "")
	if n := e.syncHooks(hooksManifest(t, groupC)); n != 0 {
		t.Fatalf("%d failures: %s", n, e.errw.String())
	}
	if got := groups(t, e, "PreToolUse"); len(got) != 1 || got[0] != canon(t, groupC) {
		t.Fatalf("PreToolUse groups: %v", got)
	}
}

// A hand edit is still caught after the format change: apply on a
// version-1 ledger refuses an edited block (and keeps the version for sync
// to migrate), and accepts an untouched one.
func TestOldLedgerApply(t *testing.T) {
	e := oldLedgerHome(t)
	if n := e.apply(e.manifest("rule one", nil, nil), false); n != 0 {
		t.Fatalf("untouched block refused: %s", e.errw.String())
	}
	if v := ledgerVersion(e.ledgerObj()); v != 1 {
		t.Fatalf("apply changed the version to %d", v)
	}
	e.write(".claude/CLAUDE.md", B+"\nrule one, edited\n"+E+"\n")
	if n := e.apply(e.manifest("rule two", nil, nil), false); n != 1 || !strings.Contains(e.errw.String(), "edited since piste-agents last wrote it") {
		t.Fatalf("edited block not refused: %d %s", n, e.errw.String())
	}
	if e.read(".claude/CLAUDE.md") != B+"\nrule one, edited\n"+E+"\n" {
		t.Fatal("edited block overwritten")
	}
}

// An unreadable hooks file stops the migration: the codex step fails and
// the ledger keeps its version-1 keys.
func TestOldLedgerUnreadableHooksFile(t *testing.T) {
	e := oldLedgerHome(t)
	e.write(".codex/hooks.json", `{"hooks": `)
	before, _ := os.ReadFile(e.ledger)
	if n := e.syncHooks(hooksManifest(t, groupC)); n != 1 || !strings.Contains(e.errw.String(), "piste-agents: codex: migrating the ledger") {
		t.Fatalf("%d: %s", n, e.errw.String())
	}
	l := e.ledgerObj()
	if ledgerVersion(l) != 1 || !bytes.Contains(before, []byte(str(l.Get("codex_groups").([]any)[0]))) {
		t.Fatalf("ledger changed: %s", encode(l, ""))
	}
	if _, err := os.Stat(filepath.Join(e.home, ".codex/hooks.json")); err != nil {
		t.Fatal(err)
	}
}
