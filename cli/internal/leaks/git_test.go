package leaks

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type gitRepo struct {
	t   *testing.T
	dir string
}

// newRepo is a fresh git repo in a temp dir, isolated from the machine's and
// the user's git config.
func newRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	r := &gitRepo{t, t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.name", "Ann")
	r.git("config", "user.email", "ann@example.com")
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *gitRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *gitRepo) write(path, content string) {
	r.t.Helper()
	p := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit stages everything and commits it, returning the sha.
func (r *gitRepo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "--allow-empty", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *gitRepo) commits(revs ...string) []Commit {
	r.t.Helper()
	cs, err := Commits(r.dir, revs)
	if err != nil {
		r.t.Fatal(err)
	}
	return cs
}

func TestCommitsAdded(t *testing.T) {
	r := newRepo(t)
	r.write("f.txt", "a\nb\nc\nd\n")
	r.write("h.txt", "1\n2\n3\n4\n5\n6\n")
	r.commit("one")
	r.write("f.txt", "a\nb\nnew\nd\n")
	r.write("g.txt", "++ x\n+++ y\n")
	r.write("h.txt", "1\n2\n3\n4\nfive\n6\n")
	r.commit("two")
	cs := r.commits("HEAD~1..HEAD")
	if len(cs) != 1 {
		t.Fatalf("got %d commits, want 1", len(cs))
	}
	want := []Added{{"f.txt", 3, "new"}, {"g.txt", 1, "++ x"}, {"g.txt", 2, "+++ y"}, {"h.txt", 5, "five"}}
	if !reflect.DeepEqual(cs[0].Added, want) {
		t.Errorf("Added = %v\nwant    %v", cs[0].Added, want)
	}
}

func TestCommitsPaths(t *testing.T) {
	r := newRepo(t)
	r.write("f.txt", "a fairly long line so the rename is found\nand another one\n")
	r.commit("one")
	r.write("overlays/a/empty", "")
	r.write("bin.dat", "plum\x00orchard\n")
	r.write(`a "b" c.txt`, "x\n")
	if err := os.Symlink("target/plum", filepath.Join(r.dir, "link")); err != nil {
		t.Fatal(err)
	}
	r.commit("two")
	r.git("mv", "f.txt", "renamed.txt")
	r.commit("three")
	cs := r.commits("HEAD~2..HEAD")
	if len(cs) != 2 {
		t.Fatalf("got %d commits, want 2", len(cs))
	}
	for _, p := range []string{"overlays/a/empty", "bin.dat", `a "b" c.txt`, "link"} {
		if !slices.Contains(cs[0].Paths, p) {
			t.Errorf("Paths %q lack %q", cs[0].Paths, p)
		}
	}
	for _, a := range cs[0].Added {
		if a.Path == "bin.dat" {
			t.Errorf("a binary file's content was read: %v", a)
		}
	}
	if !slices.Contains(cs[0].Added, Added{"link", 1, "target/plum"}) {
		t.Errorf("Added %v lack the symlink's target", cs[0].Added)
	}
	if !reflect.DeepEqual(cs[1].Paths, []string{"renamed.txt"}) || len(cs[1].Added) != 0 {
		t.Errorf("rename: Paths %q, Added %v", cs[1].Paths, cs[1].Added)
	}
}

func TestCommitsMeta(t *testing.T) {
	r := newRepo(t)
	first := r.commit("first")
	t.Setenv("GIT_COMMITTER_NAME", "Cy")
	t.Setenv("GIT_COMMITTER_EMAIL", "cy@example.com")
	second := r.commit("subject line\n\nbody line")
	cs := r.commits("HEAD")
	if len(cs) != 2 || cs[0].SHA != first || cs[1].SHA != second {
		t.Fatalf("commits %v, want %s then %s", cs, first, second)
	}
	c := cs[1]
	if c.Author != "Ann <ann@example.com>" || c.Committer != "Cy <cy@example.com>" {
		t.Errorf("Author %q, Committer %q", c.Author, c.Committer)
	}
	if c.Message != "subject line\n\nbody line" {
		t.Errorf("Message %q", c.Message)
	}
}

func TestCommitsMerge(t *testing.T) {
	r := newRepo(t)
	r.commit("base")
	r.git("checkout", "-q", "-b", "side")
	r.write("side.txt", "from side\n")
	r.commit("side")
	r.git("checkout", "-q", "main")
	merge := func() string {
		r.git("merge", "-q", "--no-ff", "-m", "merge side", "side")
		return r.git("rev-parse", "HEAD")
	}()
	cs := r.commits("main", "--not", "side")
	if len(cs) != 1 || cs[0].SHA != merge {
		t.Fatalf("commits %v, want only the merge", cs)
	}
	if !reflect.DeepEqual(cs[0].Added, []Added{{"side.txt", 1, "from side"}}) {
		t.Errorf("merge Added = %v (want the first-parent diff)", cs[0].Added)
	}
}

func TestCommitsBadRange(t *testing.T) {
	r := newRepo(t)
	r.commit("one")
	if _, err := Commits(r.dir, []string{"nosuchref"}); err == nil {
		t.Error("a bad range: no error")
	}
}

func TestMatcherCommits(t *testing.T) {
	r := newRepo(t)
	r.commit("base")
	m := NewMatcher("", []*List{list(t, "l", "plum orchard\n")})

	r.write("clean.txt", "ok\n")
	msg := r.commit("about Plum Orchard")
	r.write("plum-orchard.txt", "ok\n")
	name := r.commit("name")
	r.write("notes.txt", "Plum Orchard\n")
	middle := r.commit("middle")
	r.write("notes.txt", "fine\n")
	r.commit("later")
	r.write("overlays/a/empty", "")
	ov := r.commit("overlay")
	r.git("config", "user.email", "plum.orchard@example.com")
	who := r.commit("who")

	got := m.Commits(r.commits("HEAD~6..HEAD"))
	want := []Hit{
		{msg[:7], "commit message", `"Plum Orchard" (l)`},
		{name[:7], "plum-orchard.txt (name)", `"plum-orchard" (l)`},
		{middle[:7], "notes.txt:1", `"Plum Orchard" (l)`},
		{ov[:7], "overlays/a/empty", "under overlays/: an overlay's files never go in the engine"},
		{who[:7], "author", `"plum.orchard" (l)`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hits:\n%q\nwant\n%q", got, want)
	}
}

// git calls a file binary only for a NUL in its first 8000 bytes: one past
// that is text, its NULs land in git log's output and its lines are read.
func TestCommitsLateNUL(t *testing.T) {
	m := NewMatcher("", []*List{list(t, "l", "plum orchard\n")})
	pad := strings.Repeat("x\n", 4500) // 9000 bytes, lines 1-4500
	for _, tc := range []struct{ name, content, where string }{
		{"one NUL", pad + "a\x00b\nplum orchard\n", "late.log:4502"},
		{"five NULs", pad + "\x00plum orchard\x00\x00\x00\x00\n", "late.log:4501"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRepo(t)
			r.commit("base")
			r.write("late.log", tc.content)
			sha := r.commit("late")
			r.write("after.txt", "ok\n")
			r.commit("after")
			cs := r.commits("HEAD~2..HEAD")
			if len(cs) != 2 || cs[0].SHA != sha {
				t.Fatalf("got %d commits (first %.7s), want 2 (first %.7s)", len(cs), cs[0].SHA, sha)
			}
			want := []Hit{{sha[:7], tc.where, `"plum orchard" (l)`}}
			if got := m.Commits(cs); !reflect.DeepEqual(got, want) {
				t.Errorf("hits %q, want %q", got, want)
			}
		})
	}
}

// gitattributes can't hide a text file's lines (binary or -diff, committed
// or in .git/info/attributes); a NUL in the first 8000 bytes still makes a
// file binary, new or changed.
func TestCommitsAttributes(t *testing.T) {
	r := newRepo(t)
	r.write(".gitattributes", "*.txt binary\n")
	r.write("old.md", "ok\n")
	r.write("old.dat", "\x00 ok\n")
	r.commit("base")
	r.write(".git/info/attributes", "*.md -diff\n")
	r.write("new.md", "plum orchard\n")
	r.write("old.md", "ok\nplum orchard\n")
	r.write("new.txt", "plum orchard\n")
	r.write("new.dat", "plum\x00orchard\n")
	r.write("old.dat", "\x00 ok\nplum orchard\n")
	r.commit("hidden")
	cs := r.commits("HEAD~1..HEAD")
	want := []Added{{"new.md", 1, "plum orchard"}, {"new.txt", 1, "plum orchard"}, {"old.md", 2, "plum orchard"}}
	if len(cs) != 1 || !reflect.DeepEqual(cs[0].Added, want) {
		t.Errorf("Added = %v\nwant    %v", cs[0].Added, want)
	}
}
