package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

// plaintextSecrets is every *.sops.yaml under dir (but .git) that has no
// top-level sops: block, so isn't encrypted, and holds a value: a non-empty
// scalar anywhere in it, or YAML that doesn't parse (it can't be shown to be
// empty). Paths are relative to dir, sorted. piste init without sops writes
// a team layer's secret names this way, empty, and stages the file; a value
// typed into it by hand would be committed in plaintext.
func plaintextSecrets(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			if e.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(e.Name(), ".sops.yaml") || e.Name() == ".sops.yaml" || isEncrypted(path) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var v any
		if err := yaml.Unmarshal(b, &v); err != nil || hasValue(v) {
			out = append(out, rel(dir, path))
		}
		return nil
	})
	slices.Sort(out)
	return out
}

// hasValue: v (parsed YAML) holds a scalar other than "" or null.
func hasValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(x) != ""
	case map[string]any:
		for _, w := range x {
			if hasValue(w) {
				return true
			}
		}
		return false
	case []any:
		for _, w := range x {
			if hasValue(w) {
				return true
			}
		}
		return false
	}
	return true // a number, a bool, a map with other keys: a value
}

// refusePlaintextSecrets stops `what` when dir holds a plaintext secrets file
// with values in it, saying how to get out: encrypt it in place before it's
// committed, or take the values out.
func (a *App) refusePlaintextSecrets(dir, what string) error {
	files := plaintextSecrets(dir)
	if len(files) == 0 {
		return nil
	}
	for _, f := range files {
		a.errorf(fmt.Sprintf("%s holds values but isn't encrypted (no sops: block); %s refuses it.", f, what))
	}
	a.errorf("A value in it would be committed in plaintext. Take the values out (check `git log -p` for any already committed, and rotate those),")
	a.errorf("then set them with " + a.piste() + " secrets init (once) and " + a.piste() + " secrets edit <file>, which encrypts the file first.")
	return ExitError{1}
}
