package leaks

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// binaryPeek is how much of a file git reads to call it binary (a NUL in it).
const binaryPeek = 8000

// Tree is what the files under dir hold (an exported tree, as published):
// each entry's name, each text file's lines and each symlink's target (not
// followed). .git and binary files are skipped; an overlays/ directly in dir
// is one hit and is not read. files counts the text files and links read.
func (m *Matcher) Tree(dir string) (hits []Hit, files int, err error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, 0, err
	}
	if !fi.IsDir() {
		return nil, 0, fmt.Errorf("%s is not a directory", dir)
	}
	add := func(where, text string) {
		for _, what := range m.Line(text) {
			hits = append(hits, Hit{"", where, what})
		}
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "overlays" && filepath.Dir(p) == filepath.Clean(dir) {
			hits = append(hits, Hit{"", p, "an overlays/ directory: scan an exported tree, not a checkout"})
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		add(p+" (name)", d.Name())
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			files++
			add(p+" (link)", target)
		case d.Type().IsRegular():
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if bytes.IndexByte(b[:min(len(b), binaryPeek)], 0) >= 0 {
				return nil
			}
			files++
			for i, line := range strings.Split(string(b), "\n") {
				add(fmt.Sprintf("%s:%d", p, i+1), line)
			}
		}
		return nil
	})
	sortByWhere(hits)
	return hits, files, err
}

// sortByWhere orders a tree's hits by path, then line number (2 before 10),
// then what was found.
func sortByWhere(hits []Hit) {
	key := func(w string) (string, int) {
		if i := strings.LastIndexByte(w, ':'); i >= 0 {
			if n, err := strconv.Atoi(w[i+1:]); err == nil {
				return w[:i], n
			}
		}
		return w, 0
	}
	sort.SliceStable(hits, func(i, j int) bool {
		pi, ni := key(hits[i].Where)
		pj, nj := key(hits[j].Where)
		if pi != pj {
			return pi < pj
		}
		if ni != nj {
			return ni < nj
		}
		return hits[i].What < hits[j].What
	})
}
