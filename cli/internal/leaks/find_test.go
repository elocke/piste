package leaks

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFindLists(t *testing.T) {
	co := newRepo(t)
	writeFile(t, filepath.Join(co.dir, ".piste"), "")
	writeFile(t, filepath.Join(co.dir, ".gitignore"), "/overlays/\n")
	co.commit("init")
	writeFile(t, filepath.Join(co.dir, "overlays/b/leak-markers.extra.txt"), "x\n")
	writeFile(t, filepath.Join(co.dir, "overlays/a/leak-markers.txt"), "x\n")
	writeFile(t, filepath.Join(co.dir, "overlays/a/other.txt"), "x\n")
	if err := os.MkdirAll(filepath.Join(co.dir, "overlays/c/leak-markers.dir.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(co.dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(co.dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(root, "overlays/a/leak-markers.txt"),
		filepath.Join(root, "overlays/b/leak-markers.extra.txt"),
	}

	// An overlay repo inside the checkout, with a worktree of its own.
	ov := &gitRepo{t, filepath.Join(co.dir, "overlays/a")}
	ov.git("init", "-q", "-b", "main")
	ov.git("config", "user.name", "Ann")
	ov.git("config", "user.email", "ann@example.com")
	ov.git("config", "commit.gpgsign", "false")
	ov.commit("overlay")
	base := t.TempDir()
	ov.git("worktree", "add", "-q", filepath.Join(base, "owt"), "-b", "x")
	co.git("worktree", "add", "-q", filepath.Join(base, "wt"), "-b", "side")

	for _, cwd := range []string{co.dir, filepath.Join(co.dir, "sub"), filepath.Join(base, "wt"), filepath.Join(base, "owt")} {
		gotRoot, got := FindLists(cwd)
		if gotRoot != root || !reflect.DeepEqual(got, want) {
			t.Errorf("FindLists(%s) = %s, %q; want %s, %q", cwd, gotRoot, got, root, want)
		}
	}
	if gotRoot, got := FindLists(t.TempDir()); gotRoot != "" || len(got) != 0 {
		t.Errorf("outside a checkout: %s, %q", gotRoot, got)
	}
}
