package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAtomicKeepsMode(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "f")
	os.WriteFile(p, []byte("old"), 0o600)
	if err := WriteAtomic(p, []byte("new"), 0); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	if string(b) != "new" || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%q %v", b, fi.Mode())
	}
	if n := leftovers(t, d); n != 0 {
		t.Fatalf("%d temp files left", n)
	}
}

func leftovers(t *testing.T, d string) int {
	n := 0
	es, _ := os.ReadDir(d)
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".tmp") {
			n++
		}
	}
	return n
}

// A fault after the temp file is written leaves the original intact
// and no temp file behind.
func TestAtomicWriteFault(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "f")
	os.WriteFile(p, []byte("original"), 0o644)
	Fault = "after-tmp"
	defer func() { Fault = "" }()
	if err := WriteAtomic(p, []byte("new"), 0); err == nil {
		t.Fatal("want fault")
	}
	if b, _ := os.ReadFile(p); string(b) != "original" {
		t.Fatalf("changed: %q", b)
	}
	if n := leftovers(t, d); n != 0 {
		t.Fatalf("%d temp files left", n)
	}
}

// Symlinked target / parent / store are refused.
func TestGuard(t *testing.T) {
	home := t.TempDir()
	checkout := filepath.Join(t.TempDir(), "checkout")
	os.MkdirAll(filepath.Join(checkout, "x"), 0o755)
	os.MkdirAll(filepath.Join(home, ".claude", "skills"), 0o755)
	link := filepath.Join(home, ".claude", "skills", "x")
	os.Symlink(filepath.Join(checkout, "x"), link)

	err := Guard(home, filepath.Join(link, "SKILL.md"), nil)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("write under foreign link: %v", err)
	}
	if err := Guard(home, link, nil); !errors.Is(err, ErrRefused) {
		t.Fatalf("foreign link itself: %v", err)
	}
	if err := Guard(home, link, func(p string) bool { return p == link }); err != nil {
		t.Fatalf("our link: %v", err)
	}
	if err := Guard(home, filepath.Join(home, ".claude", "CLAUDE.md"), nil); err != nil {
		t.Fatalf("plain file: %v", err)
	}
	if err := Guard(home, "/nix/store/abc-x/CLAUDE.md", nil); !errors.Is(err, ErrRefused) {
		t.Fatalf("store: %v", err)
	}
	if err := Guard(home, filepath.Join(t.TempDir(), "elsewhere"), nil); !errors.Is(err, ErrRefused) {
		t.Fatalf("outside root: %v", err)
	}
	// A store-style directory reached through a link.
	store := t.TempDir()
	old := StorePrefix
	StorePrefix = store + "/"
	defer func() { StorePrefix = old }()
	os.Symlink(store, filepath.Join(home, ".codex"))
	if err := Guard(home, filepath.Join(home, ".codex", "config.toml"), nil); !errors.Is(err, ErrRefused) {
		t.Fatalf("parent link into store: %v", err)
	}
}

func TestStageAbort(t *testing.T) {
	d := t.TempDir()
	s, err := Stage(filepath.Join(d, "new"), []byte("x"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	s.Abort()
	if _, err := os.Stat(filepath.Join(d, "new")); !os.IsNotExist(err) {
		t.Fatal("abort created the file")
	}
	if leftovers(t, d) != 0 {
		t.Fatal("temp left")
	}
}

func TestSymlink(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "a", "l")
	if err := Symlink("../t1", p); err != nil {
		t.Fatal(err)
	}
	if err := Symlink("../t2", p); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(p); got != "../t2" {
		t.Fatal(got)
	}
}
