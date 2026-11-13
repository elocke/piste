package leaks

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTree(t *testing.T) {
	d := t.TempDir()
	out := t.TempDir()
	writeFile(t, filepath.Join(d, "a/b.txt"), "ok\nPlum Orchard\n")
	writeFile(t, filepath.Join(d, "plum-orchard.md"), "ok\n")
	writeFile(t, filepath.Join(d, "plum_orchard/x.txt"), "ok\n")
	writeFile(t, filepath.Join(d, "plum_orchard/y.txt"), "ok\n")
	writeFile(t, filepath.Join(d, "bin.dat"), "\x00 plum orchard\n")
	writeFile(t, filepath.Join(d, ".git/config"), "plum orchard\n")
	writeFile(t, filepath.Join(d, "sub/.git"), "gitdir: plum orchard\n")
	writeFile(t, filepath.Join(out, "real.txt"), "plum orchard\n")
	if err := os.Symlink("elsewhere/plum orchard", filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(out, "real.txt"), filepath.Join(d, "followed")); err != nil {
		t.Fatal(err)
	}

	m := NewMatcher("", []*List{list(t, "l", "plum orchard\n")})
	hits, files, err := m.Tree(d)
	if err != nil {
		t.Fatal(err)
	}
	want := []Hit{
		{"", filepath.Join(d, "a/b.txt") + ":2", `"Plum Orchard" (l)`},
		{"", filepath.Join(d, "link") + " (link)", `"plum orchard" (l)`},
		{"", filepath.Join(d, "plum-orchard.md") + " (name)", `"plum-orchard" (l)`},
		{"", filepath.Join(d, "plum_orchard") + " (name)", `"plum_orchard" (l)`},
	}
	if !reflect.DeepEqual(hits, want) {
		t.Errorf("hits:\n%q\nwant\n%q", hits, want)
	}
	// a/b.txt, plum-orchard.md, plum_orchard/{x,y}.txt, link, followed.
	if files != 6 {
		t.Errorf("files = %d, want 6", files)
	}
}

func TestTreeOverlays(t *testing.T) {
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "overlays/x/f"), "plum orchard\n")
	writeFile(t, filepath.Join(d, "sub/overlays/g.txt"), "ok\n")
	m := NewMatcher("", []*List{list(t, "l", "plum orchard\n")})
	hits, files, err := m.Tree(d)
	if err != nil {
		t.Fatal(err)
	}
	want := []Hit{{"", filepath.Join(d, "overlays"), "an overlays/ directory: scan an exported tree, not a checkout"}}
	if !reflect.DeepEqual(hits, want) {
		t.Errorf("hits %q, want %q", hits, want)
	}
	if files != 1 {
		t.Errorf("files = %d, want 1 (sub/overlays/g.txt)", files)
	}
}

func TestTreeMissing(t *testing.T) {
	if _, _, err := NewMatcher("", nil).Tree(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("a missing dir: no error")
	}
}

func TestTreeLineOrder(t *testing.T) {
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "f.txt"), "x\nplum orchard\nx\nx\nx\nx\nx\nx\nx\nplum orchard\n")
	hits, _, err := NewMatcher("", []*List{list(t, "l", "plum orchard\n")}).Tree(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Where != filepath.Join(d, "f.txt")+":2" || hits[1].Where != filepath.Join(d, "f.txt")+":10" {
		t.Errorf("hits %q, want line 2 before line 10", hits)
	}
}
