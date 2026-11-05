package notices

// The CI header check: every Go file with a
// "Portions ported from" header is listed in THIRD_PARTY_NOTICES.md, and
// every listed file exists and carries the header. Runs in `go test` and so
// in the Nix build and the go CI job; cli/tools/check-notices is the same
// check for a shell.

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const header = "// Portions ported from " + "rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,"

var row = regexp.MustCompile("(?m)^\\| `([^`]+\\.go)` \\|")

func TestNoticesMatchHeaders(t *testing.T) {
	block := Text
	if i := strings.Index(block, "<!-- ported-files:begin -->"); i >= 0 {
		block = block[i:]
	}
	if i := strings.Index(block, "<!-- ported-files:end -->"); i >= 0 {
		block = block[:i]
	}
	listed := map[string]bool{}
	for _, m := range row.FindAllStringSubmatch(block, -1) {
		listed[m[1]] = true
	}
	headed := map[string]bool{}
	filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		b, _ := os.ReadFile(p)
		if strings.HasPrefix(string(b), header) {
			headed[filepath.ToSlash(p)] = true
		} else if strings.Contains(string(b), "Portions ported from "+"rulesync") && p != "notices_test.go" {
			t.Errorf("%s: the rulesync header must be the first line", p)
		}
		return nil
	})
	var missing, stale []string
	for f := range headed {
		if !listed[f] {
			missing = append(missing, f)
		}
	}
	for f := range listed {
		if !headed[f] {
			stale = append(stale, f)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("files with a rulesync header missing from THIRD_PARTY_NOTICES.md: %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("THIRD_PARTY_NOTICES.md lists files without the header (or missing): %v", stale)
	}
	if len(listed) == 0 {
		t.Fatal("no ported files listed")
	}
	if !strings.Contains(Text, "Copyright (c) 2024 dyoshikawa") || !strings.Contains(Text, "Permission is hereby granted") {
		t.Fatal("the MIT notice text is missing")
	}
}
