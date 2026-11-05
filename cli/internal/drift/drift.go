// Package drift finds installs outside Nix and sorts them against what
// the host declares (piste drift and piste adopt): Scan lists them, Declared
// evaluates the host once, Classify sorts them, FindList says where adopt
// writes each.
package drift

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/execx"
)

func lines(ctx context.Context, r execx.Runner, argv ...string) ([]string, bool) {
	res, err := r.Run(ctx, execx.Cmd{Argv: argv})
	if err != nil {
		return nil, false
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out, true
}

func jsonOut(ctx context.Context, r execx.Runner, argv ...string) (any, bool) {
	res, err := r.Run(ctx, execx.Cmd{Argv: argv})
	if err != nil {
		return nil, false
	}
	var v any
	if json.Unmarshal(res.Stdout, &v) != nil {
		return nil, false
	}
	return v, true
}

// orderedKeys decodes a JSON object's keys in document order.
func orderedKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return keys
		}
		keys = append(keys, t.(string))
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return keys
		}
	}
	return keys
}

func npmGlobals(ctx context.Context, r execx.Runner) []string {
	res, err := r.Run(ctx, execx.Cmd{Argv: []string{"npm", "list", "-g", "--depth=0", "--json"}})
	if err != nil {
		return nil
	}
	var d struct {
		Dependencies json.RawMessage `json:"dependencies"`
	}
	if json.Unmarshal(res.Stdout, &d) != nil {
		return nil
	}
	var out []string
	for _, k := range orderedKeys(d.Dependencies) {
		if k != "npm" {
			out = append(out, k)
		}
	}
	return out
}

// PnpmGlobals are pnpm's global packages, by name.
func PnpmGlobals(ctx context.Context, r execx.Runner) []string {
	res, err := r.Run(ctx, execx.Cmd{Argv: []string{"pnpm", "list", "-g", "--depth=0", "--json"}})
	if err != nil {
		return nil
	}
	var d []struct {
		Dependencies json.RawMessage `json:"dependencies"`
	}
	if json.Unmarshal(res.Stdout, &d) != nil || len(d) == 0 {
		return nil
	}
	return orderedKeys(d[0].Dependencies)
}

// Unpin: "name@1.2.3" / "@scope/name@1.2.3" -> the package name.
func Unpin(spec string) string {
	i := strings.LastIndex(spec, "@")
	if i <= 0 {
		return spec
	}
	v := spec[i+1:]
	if v != "" && v[0] >= '0' && v[0] <= '9' {
		return spec[:i]
	}
	return spec
}

var rustup = map[string]bool{"cargo": true, "cargo-clippy": true, "cargo-fmt": true, "cargo-miri": true,
	"clippy-driver": true, "rls": true, "rust-analyzer": true, "rust-gdb": true, "rust-gdbgui": true,
	"rust-lldb": true, "rustc": true, "rustdoc": true, "rustfmt": true, "rustup": true}

func cargoInstalls(home string) []string {
	es, err := os.ReadDir(filepath.Join(home, ".cargo", "bin"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() && !rustup[e.Name()] {
			out = append(out, e.Name())
		}
	}
	return out
}

func pipInstalls(ctx context.Context, r execx.Runner) []string {
	v, ok := jsonOut(ctx, r, "pip", "list", "--user", "--format=json")
	if !ok {
		return nil
	}
	var out []string
	for _, p := range v.([]any) {
		if m, ok := p.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

var quoted = regexp.MustCompile(`"([^"]+)"`)

// listPattern matches `<name> = <open> … <close>` for whole names only:
// npmPackages isn't the end of pnpmPackages.
func listPattern(names []string, open, close string) *regexp.Regexp {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = regexp.QuoteMeta(n)
	}
	return regexp.MustCompile(`(?s)\b(?:` + strings.Join(q, "|") + `)\s*=\s*` + open + `(.*?)` + close)
}

// ExtractNixListStrings: quoted strings of `name = [ … ];` lists.
func ExtractNixListStrings(content string, names []string) map[string]bool {
	out := map[string]bool{}
	for _, m := range listPattern(names, `\[`, `\];`).FindAllStringSubmatch(content, -1) {
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			out[q[1]] = true
		}
	}
	return out
}

// ExtractBashArrayStrings: quoted strings of `NAME=( … )` arrays.
func ExtractBashArrayStrings(content string, names []string) map[string]bool {
	out := map[string]bool{}
	for _, m := range listPattern(names, `\(`, `\)`).FindAllStringSubmatch(content, -1) {
		for _, q := range quoted.FindAllStringSubmatch(m[1], -1) {
			out[q[1]] = true
		}
	}
	return out
}

// ScanNixFiles concatenates every .nix file under dir.
func ScanNixFiles(dir string) string {
	var parts []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".nix") {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil {
			parts = append(parts, string(b))
		}
		return nil
	})
	return strings.Join(parts, "\n")
}

// pathInputs is the overlay's relative path inputs, as directories: flakes
// in a subfolder of its repo (a team layer kept there, `path:./layer`). An
// absolute path, or one leading out of the overlay, is skipped.
func pathInputs(flakeDir string) []string {
	b, err := os.ReadFile(filepath.Join(flakeDir, "flake.lock"))
	if err != nil {
		return nil
	}
	var lock struct {
		Nodes map[string]struct {
			Locked struct{ Type, Path string } `json:"locked"`
		} `json:"nodes"`
	}
	if json.Unmarshal(b, &lock) != nil {
		return nil
	}
	var dirs []string
	for _, n := range lock.Nodes {
		p := filepath.Clean(n.Locked.Path)
		if n.Locked.Type != "path" || filepath.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || p == "." {
			continue
		}
		dirs = append(dirs, filepath.Join(flakeDir, p))
	}
	sort.Strings(dirs)
	return dirs
}

func merge(a, b map[string]bool) map[string]bool {
	for k := range b {
		a[k] = true
	}
	return a
}
