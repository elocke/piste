package userscope

// The user-scope ledger (~/.local/state/piste-agents/ledger.json): the
// managed blocks apply wrote (by text hash) and the hook records sync --user
// keeps (the Claude carrier, the owned Codex groups by groupID).

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/elocke/piste/cli/internal/agents/fsx"
)

// LedgerVersion is the ledger format. Version 2 keys the owned Codex groups
// by canonical(); version 1 used the layout of legacyCanonical.
const LedgerVersion = 2

func newLedger() *Obj {
	o := NewObj()
	o.Set("version", LedgerVersion)
	return o
}

func ledgerVersion(l *Obj) int {
	switch v := l.Get("version").(type) {
	case json.Number:
		n, _ := strconv.Atoi(string(v))
		return n
	case int:
		return v
	}
	return 0
}

// loadLedger reads the ledger in order (def when missing).
func loadLedger(path string, def func() *Obj) (*Obj, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return def(), nil
	}
	if err != nil {
		return nil, err
	}
	v, err := ParseOrdered(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	o, ok := v.(*Obj)
	if !ok {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	return o, nil
}

func writeLedger(path string, l *Obj) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, encode(sorted(l), "  ")+"\n")
}

func writeAtomic(path, text string) error { return fsx.WriteAtomic(path, []byte(text), 0) }

// migrateLedger brings a version-1 ledger to the current version. Its owned
// Codex groups were keyed in the old layout: each one still in the hooks
// file as piste wrote it is re-keyed by groupID; a group edited by hand since
// matches neither key, so it stays unowned (left alone, as under version 1).
// A key already in the current layout is kept the same way, so a version-2
// ledger an older piste-agents relabelled as version 1 keeps its groups.
// The ledger is only changed when the migration succeeds.
func migrateLedger(l *Obj, home string) error {
	if ledgerVersion(l) != 1 {
		return nil
	}
	old := map[string]bool{}
	if xs, ok := l.Get("codex_groups").([]any); ok {
		for _, x := range xs {
			old[str(x)] = true
		}
	}
	var ids []any
	if target := getS(l, "codex_target"); target != "" && len(old) > 0 {
		data, err := readCodexHooks(filepath.Join(home, target))
		if err != nil {
			return fmt.Errorf("migrating the ledger: %s: %w", target, err)
		}
		seen := map[string]bool{}
		hooks, _ := data.Get("hooks").(*Obj)
		for _, event := range hooks.keys() {
			groups, _ := hooks.Vals[event].([]any)
			for _, g := range groups {
				// The current key too: an older piste-agents apply
				// (rollback) relabels a version-2 ledger as version 1.
				if id := groupID(event, g); (old[legacyGroupID(event, g)] || old[id]) && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i].(string) < ids[j].(string) })
	}
	if l.Has("codex_groups") {
		if ids == nil {
			ids = []any{}
		}
		l.Set("codex_groups", ids)
	}
	l.Set("version", LedgerVersion)
	return nil
}

func (o *Obj) keys() []string {
	if o == nil {
		return nil
	}
	return o.Keys
}

// legacyGroupID is a group's key in a version-1 ledger: sha256(event NUL
// legacyCanonical(group)). Only migrateLedger uses it.
func legacyGroupID(event string, group any) string {
	return sha(event + "\x00" + legacyCanonical(group))
}

// legacyCanonical is the version-1 layout: canonical() with every non-ASCII
// character escaped (\uXXXX, astral ones as surrogate pairs), integers
// without a sign on zero, and other numbers re-rendered as shortest floats
// (100.0, 1.5, 1e-05, 1e+16).
func legacyCanonical(v any) string {
	var b strings.Builder
	for _, r := range canonical(legacyNumbers(v)) {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xffff:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

func legacyNumbers(v any) any {
	switch x := v.(type) {
	case *Obj:
		o := NewObj()
		for _, k := range x.keys() {
			o.Set(k, legacyNumbers(x.Vals[k]))
		}
		return o
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = legacyNumbers(e)
		}
		return out
	case json.Number:
		s := string(x)
		if !strings.ContainsAny(s, ".eE") {
			if strings.TrimLeft(strings.TrimPrefix(s, "-"), "0") == "" {
				return json.Number("0")
			}
			return x
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(f, 0) {
			return x
		}
		if a := math.Abs(f); a != 0 && (a < 1e-4 || a >= 1e16) {
			return json.Number(strconv.FormatFloat(f, 'e', -1, 64))
		}
		s = strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return json.Number(s)
	}
	return v
}
