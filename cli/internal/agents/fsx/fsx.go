// Package fsx is the agents engine's only way to touch the filesystem.
//
//   - Guard refuses to write through a symlink the lock does not record as
//     ours (checking the target and every parent up to the harness root) and
//     refuses anything that resolves into /nix/store.
//   - Writes are atomic: a temp file in the same directory, fsync, rename(2).
//     The file is either fully old or fully new, and a harness that rewrites
//     the file concurrently never sees a half-written one.
//   - Stage/Commit split a write so a plan can prepare every file before
//     renaming any (package plan).
package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrRefused marks a write that would be unsafe. Nothing was written.
var ErrRefused = errors.New("refused")

// Refusedf builds an ErrRefused error.
func Refusedf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, a...))
}

// StorePrefix is where Nix-managed files live; never written.
var StorePrefix = "/nix/store/"

// Fault is the fault-injection point, read from PISTE_FAULT by the commands
// (tests only): "after-tmp" fails every write after its temp file is
// written; "stage:N" fails the Nth staged write of a plan.
var Fault = ""

// Guard checks that path may be written. root bounds the parent walk (the
// harness root, e.g. $HOME or the project root). ours reports symlinks the
// lock records as written by us; nil means none are.
func Guard(root, path string, ours func(string) bool) error {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if strings.HasPrefix(path+"/", StorePrefix) {
		return Refusedf("%s is in the Nix store", path)
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		if ours == nil || !ours(path) {
			return Refusedf("%s is a symlink not written by piste; refusing to write through it", path)
		}
	}
	if !within(root, path) {
		return Refusedf("%s is outside %s", path, root)
	}
	for p := filepath.Dir(path); within(root, p) && p != root; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return Refusedf("%s is a symlink; refusing to write through it", p)
		}
	}
	// The resolved location must not be the store (e.g. a root that is
	// itself a store path, or a bind mount of one).
	if real, err := filepath.EvalSymlinks(existingPrefix(path)); err == nil && strings.HasPrefix(real+"/", StorePrefix) {
		return Refusedf("%s resolves into the Nix store (%s)", path, real)
	}
	return nil
}

func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

func existingPrefix(p string) string {
	for {
		if _, err := os.Lstat(p); err == nil || p == filepath.Dir(p) {
			return p
		}
		p = filepath.Dir(p)
	}
}

// DefaultMode is 0666 &^ umask, what a plain create would get.
func DefaultMode() os.FileMode {
	old := syscall.Umask(0)
	syscall.Umask(old)
	return 0o666 &^ os.FileMode(old)
}

// Staged is a fully written temp file waiting to be renamed into place.
type Staged struct {
	Path string // final location
	tmp  string
}

// Stage writes data to a temp file beside path (fsync'd, with path's
// current mode, or mode for a new file). Nothing at path changes yet.
func Stage(path string, data []byte, mode os.FileMode) (*Staged, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	} else if mode == 0 {
		mode = DefaultMode()
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".piste-*.tmp")
	if err != nil {
		return nil, err
	}
	s := &Staged{Path: path, tmp: f.Name()}
	fail := func(err error) (*Staged, error) {
		f.Close()
		os.Remove(s.tmp)
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(s.tmp)
		return nil, err
	}
	if Fault == "after-tmp" {
		os.Remove(s.tmp)
		return nil, fmt.Errorf("injected fault after writing %s", s.tmp)
	}
	return s, nil
}

// Commit renames the temp file into place.
func (s *Staged) Commit() error {
	if err := os.Rename(s.tmp, s.Path); err != nil {
		os.Remove(s.tmp)
		return err
	}
	syncDir(filepath.Dir(s.Path))
	return nil
}

// Abort removes the temp file.
func (s *Staged) Abort() { os.Remove(s.tmp) }

// WriteAtomic stages and commits in one step.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	s, err := Stage(path, data, mode)
	if err != nil {
		return err
	}
	return s.Commit()
}

// Symlink points path at target atomically (a temp link, then rename).
func Symlink(target, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".piste-link.tmp")
	os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
