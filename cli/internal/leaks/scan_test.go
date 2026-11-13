package leaks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKey is AWS-shaped, not a real key, and built at run time so this file
// carries no secret-shaped literal.
var fakeKey = "AKIA" + "Z7QXJ3KD2LMN4PQ5"

func requireGitleaks(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not on PATH")
	}
}

func gitleaksHits(hits []Hit) []Hit {
	var out []Hit
	for _, h := range hits {
		if strings.HasPrefix(h.What, "gitleaks: ") {
			out = append(out, h)
		}
	}
	return out
}

// The range has the hook's shape, several revision arguments: <sha> --not
// --remotes=<remote>. The pushed base's key is not this push's.
func TestScanRangeGitleaks(t *testing.T) {
	requireGitleaks(t)
	r := newRepo(t)
	r.write("old.py", `aws_access_key_id = "`+fakeKey+`"`+"\n")
	r.commit("base")
	bare := t.TempDir()
	r.git("init", "-q", "--bare", bare)
	r.git("remote", "add", "origin", bare)
	r.git("push", "-q", "origin", "main")
	r.write("config.py", `aws_access_key_id = "`+fakeKey+`"`+"\n")
	r.write("bin.dat", "\x00\x01\xff plum orchard\n") // binary: gitleaks reads it as text, the matcher skips it
	key := r.commit("key")
	r.write("notes.txt", "plum orchard\n")
	notes := r.commit("notes")

	m := NewMatcher("", []*List{list(t, "l", "plum orchard\n")})
	n, hits, err := ScanRange(r.dir, []string{notes, "--not", "--remotes=origin"}, m)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(hits) != 2 {
		t.Fatalf("commits %d, hits %q; want 2 commits, 2 hits", n, hits)
	}
	if h := hits[0]; h.Commit != key[:7] || h.Where != "config.py:1" || !strings.HasPrefix(h.What, "gitleaks: aws-access-token") {
		t.Errorf("first hit %q, want the key's commit's gitleaks finding", h)
	}
	if h := hits[1]; h.Commit != notes[:7] || h.Where != "notes.txt:1" {
		t.Errorf("second hit %q, want the notes commit's term", h)
	}
}

func TestScanTreeGitleaks(t *testing.T) {
	requireGitleaks(t)
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "config.py"), `aws_access_key_id = "`+fakeKey+`"`+"\n")
	n, hits, err := ScanTree(d, NewMatcher("", nil))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(hits) != 1 || hits[0].Where != filepath.Join(d, "config.py")+":1" ||
		!strings.HasPrefix(hits[0].What, "gitleaks: aws-access-token") {
		t.Errorf("files %d, hits %q", n, hits)
	}
}

func TestGitleaksAllow(t *testing.T) {
	requireGitleaks(t)
	line := `aws_access_key_id = "` + fakeKey + `" # gitleaks:allow` + "\n"
	r := newRepo(t)
	r.commit("base")
	r.write("config.py", line)
	r.commit("allowed")
	if _, hits, err := ScanRange(r.dir, []string{"HEAD~1..HEAD"}, NewMatcher("", nil)); err != nil || len(gitleaksHits(hits)) != 0 {
		t.Errorf("range: hits %q, err %v", hits, err)
	}
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "config.py"), line)
	if _, hits, err := ScanTree(d, NewMatcher("", nil)); err != nil || len(gitleaksHits(hits)) != 0 {
		t.Errorf("tree: hits %q, err %v", hits, err)
	}
}

func TestScanRangeBadRange(t *testing.T) {
	r := newRepo(t)
	r.commit("base")
	if _, _, err := ScanRange(r.dir, []string{"nosuchref"}, NewMatcher("", nil)); err == nil {
		t.Error("a bad range: no error (gitleaks alone exits 0 on it)")
	}
}

func TestGitleaksFailure(t *testing.T) {
	r := newRepo(t)
	r.commit("base")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gitleaks"), []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := GitleaksRange(r.dir, []string{"HEAD"}); err == nil || !strings.Contains(err.Error(), "gitleaks failed (exit 1): boom") {
		t.Errorf("range: err = %v", err)
	}
	if _, err := GitleaksDir(r.dir); err == nil || !strings.Contains(err.Error(), "gitleaks failed (exit 1): boom") {
		t.Errorf("dir: err = %v", err)
	}
}

func TestGitleaksMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := GitleaksDir(t.TempDir()); err == nil || !strings.Contains(err.Error(), "gitleaks not found") {
		t.Errorf("err = %v", err)
	}
}

// wantKeyHit wants hits to be gitleaks' finding of fakeKey at where, in
// commit ("" in a tree).
func wantKeyHit(t *testing.T, hits []Hit, err error, commit, where string) {
	t.Helper()
	if err != nil || len(hits) != 1 || hits[0].Commit != commit || hits[0].Where != where ||
		!strings.HasPrefix(hits[0].What, "gitleaks: aws-access-token") {
		t.Errorf("hits %q, err %v; want gitleaks' aws-access-token at %s %s", hits, err, commit, where)
	}
}

// gitleaks runs with piste's config and git log flags: nothing in the repo,
// the tree, the environment or git's config turns its rules off or hides
// the added lines from it.
func TestGitleaksPinned(t *testing.T) {
	requireGitleaks(t)
	noRules := `title = "no rules"` + "\n"
	keyLine := `aws_access_key_id = "` + fakeKey + `"` + "\n"
	rangeCase := func(t *testing.T, setup func(r *gitRepo) (dir string)) {
		r := newRepo(t)
		r.commit("base")
		r.write("config.py", keyLine)
		key := r.commit("key")
		hits, err := GitleaksRange(setup(r), []string{"HEAD~1..HEAD"})
		wantKeyHit(t, hits, err, key[:7], "config.py:1")
	}
	treeCase := func(t *testing.T, setup func(d string)) {
		d := t.TempDir()
		writeFile(t, filepath.Join(d, "config.py"), keyLine)
		setup(d)
		hits, err := GitleaksDir(d)
		wantKeyHit(t, hits, err, "", filepath.Join(d, "config.py")+":1")
	}
	envConfig := func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "rules.toml")
		writeFile(t, p, noRules)
		t.Setenv("GITLEAKS_CONFIG", p)
	}

	t.Run("range: an untracked gitleaks config", func(t *testing.T) {
		rangeCase(t, func(r *gitRepo) string { r.write(".gitleaks.toml", noRules); return r.dir })
	})
	t.Run("range: GITLEAKS_CONFIG", func(t *testing.T) {
		rangeCase(t, func(r *gitRepo) string { envConfig(t); return r.dir })
	})
	t.Run("range: GITLEAKS_CONFIG_TOML", func(t *testing.T) {
		rangeCase(t, func(r *gitRepo) string { t.Setenv("GITLEAKS_CONFIG_TOML", noRules); return r.dir })
	})
	t.Run("range: color.ui always", func(t *testing.T) {
		rangeCase(t, func(r *gitRepo) string { r.git("config", "color.ui", "always"); return r.dir })
	})
	t.Run("range: git -c color.diff=always", func(t *testing.T) {
		rangeCase(t, func(r *gitRepo) string {
			t.Setenv("GIT_CONFIG_PARAMETERS", `'color.diff'='always'`)
			return r.dir
		})
	})
	t.Run("range: diff.relative from a subdirectory", func(t *testing.T) {
		rangeCase(t, func(r *gitRepo) string {
			r.git("config", "diff.relative", "true")
			sub := filepath.Join(r.dir, "sub")
			writeFile(t, filepath.Join(sub, "x"), "")
			return sub
		})
	})
	t.Run("range: format.pretty and log.date", func(t *testing.T) {
		rangeCase(t, func(r *gitRepo) string {
			r.git("config", "format.pretty", "oneline")
			r.git("config", "log.date", "relative")
			return r.dir
		})
	})
	t.Run("range: -diff in .git/info/attributes", func(t *testing.T) {
		rangeCase(t, func(r *gitRepo) string { r.write(".git/info/attributes", "*.py -diff\n"); return r.dir })
	})
	t.Run("tree: a gitleaks config", func(t *testing.T) {
		treeCase(t, func(d string) { writeFile(t, filepath.Join(d, ".gitleaks.toml"), noRules) })
	})
	t.Run("tree: GITLEAKS_CONFIG", func(t *testing.T) {
		treeCase(t, func(string) { envConfig(t) })
	})
	t.Run("tree: an ignore file where piste runs", func(t *testing.T) {
		treeCase(t, func(d string) {
			cwd := t.TempDir()
			writeFile(t, filepath.Join(cwd, ".gitleaksignore"), filepath.Join(d, "config.py")+":aws-access-token:1\n")
			t.Chdir(cwd)
		})
	})
}

// A .gitleaksignore in what gitleaks scans would make it skip what it lists
// (piste's fixtures use an inline gitleaks:allow): refused, never clean.
func TestGitleaksIgnoreFile(t *testing.T) {
	r := newRepo(t)
	r.commit("base")
	r.write(".gitleaksignore", "x:config.py:aws-access-token:1\n")
	if _, err := GitleaksRange(r.dir, []string{"HEAD"}); err == nil || !strings.Contains(err.Error(), ".gitleaksignore") {
		t.Errorf("range: err = %v", err)
	}
	d := t.TempDir()
	writeFile(t, filepath.Join(d, ".gitleaksignore"), "x\n")
	if _, err := GitleaksDir(d); err == nil || !strings.Contains(err.Error(), ".gitleaksignore") {
		t.Errorf("tree: err = %v", err)
	}
}
