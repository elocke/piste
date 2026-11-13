package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Sum is the hex sha256 of b.
func Sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// SumString is Sum of a string.
func SumString(s string) string { return Sum([]byte(s)) }

// FileSum hashes a regular file's bytes.
func FileSum(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return Sum(b), nil
}

// LinkSum identifies a symlink by its target text.
func LinkSum(target string) string { return SumString("link:" + target) }

// TreeSum hashes a directory tree: every regular file (path, executable
// bit, content hash) and symlink (path, target), sorted, so the result does
// not depend on walk order, timestamps or ownership. Dotfiles count; a
// .git directory does not.
func TreeSum(root string) (string, error) {
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			if d.Name() == ".git" && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			t, err := os.Readlink(p)
			if err != nil {
				return err
			}
			lines = append(lines, fmt.Sprintf("l %s %s", rel, t))
		case d.Type().IsRegular():
			fi, err := d.Info()
			if err != nil {
				return err
			}
			h, err := FileSum(p)
			if err != nil {
				return err
			}
			x := "-"
			if fi.Mode()&0o111 != 0 {
				x = "x"
			}
			lines = append(lines, fmt.Sprintf("%s %s %s", x, rel, h))
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	return SumString(strings.Join(lines, "\n")), nil
}

// MemFile is one file of an in-memory tree.
type MemFile struct {
	Data []byte
	Exec bool
}

// MemTreeSum is TreeSum of a tree not yet on disk (rel path → file), so a
// render's owned-dir hash is known before it is written.
func MemTreeSum(files map[string]MemFile) string {
	lines := make([]string, 0, len(files))
	for rel, f := range files {
		x := "-"
		if f.Exec {
			x = "x"
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", x, rel, Sum(f.Data)))
	}
	sort.Strings(lines)
	return SumString(strings.Join(lines, "\n"))
}
