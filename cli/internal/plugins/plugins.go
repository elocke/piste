// Package plugins finds git/kubectl-style piste plugins on PATH.
//
// `piste foo …` runs `piste-foo …` and `piste secrets foo …` runs
// `piste-secrets-foo …` when foo is not a built-in. A plugin can never shadow
// a built-in: the caller only asks after the built-in lookup failed. Optional
// plugins can be supplied by an overlay's package, so the shared binary
// carries no overlay code.
package plugins

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Prefix of every plugin executable.
const Prefix = "piste-"

// Find returns the executable for the command path (e.g. ["secrets",
// "rotate"] → piste-secrets-rotate), or "" if none is on PATH.
func Find(path []string, pathEnv string) string {
	if len(path) == 0 {
		return ""
	}
	for _, p := range path {
		if p == "" || strings.ContainsAny(p, "/\\") || strings.HasPrefix(p, "-") {
			return ""
		}
	}
	name := Prefix + strings.Join(path, "-")
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, name)
		if isExecutable(cand) {
			return cand
		}
	}
	return ""
}

// List returns the plugin command names on PATH (first match wins), for help.
func List(pathEnv string) []string {
	seen := map[string]bool{}
	for _, dir := range filepath.SplitList(pathEnv) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if !strings.HasPrefix(n, Prefix) || seen[n] {
				continue
			}
			if !isExecutable(filepath.Join(dir, n)) {
				continue
			}
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, strings.TrimPrefix(n, Prefix))
	}
	sort.Strings(out)
	return out
}

func isExecutable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}
