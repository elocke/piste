// Package workspace holds the piste checkout layout rules:
// a checkout is a clone of the engine, marked by Marker at its root; each
// overlay is its own repo in <checkout>/overlays/<name>/. piste works out the
// checkout, the overlay and the engine to build against from the cwd; there
// is no workspace config file.
package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Marker is the file at a checkout's root.
const Marker = ".piste"

// physical resolves symlinks, falling back to the cleaned path.
func physical(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// FindCheckout walks up from dir to the directory holding Marker.
func FindCheckout(dir string) (root string, ok bool) {
	for d := physical(dir); ; d = filepath.Dir(d) {
		if exists(filepath.Join(d, Marker)) {
			return d, true
		}
		if filepath.Dir(d) == d {
			return "", false
		}
	}
}

// Overlays lists the overlays of a checkout: <root>/overlays/<name>/ with a
// flake.nix, sorted.
func Overlays(root string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "overlays"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && exists(filepath.Join(root, "overlays", e.Name(), "flake.nix")) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// OverlayOf names the overlay dir is in (<root>/overlays/<name> or below).
func OverlayOf(root, dir string) (name string, ok bool) {
	rel, err := filepath.Rel(filepath.Join(physical(root), "overlays"), physical(dir))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return strings.SplitN(rel, string(filepath.Separator), 2)[0], true
}

// Hosts are an overlay's host attribute names.
type Hosts struct{ Darwin, Home []string }

// HostsFunc reports an overlay's hosts (piste evaluates the overlay's flake).
type HostsFunc func(overlayDir string) (Hosts, error)

// Query is what resolution knows about the invocation and the machine.
type Query struct {
	Cwd, Overlay, Hostname, User string // Overlay: --overlay, "" if unset
	Darwin                       bool
}

// Resolution is the overlay piste acts on (Dir) and why. With no overlays, Dir
// is the checkout itself (the engine) and Overlay is "".
type Resolution struct {
	Root, Overlay, Dir, Reason string
	Warnings                   []string
}

// hostName is the configuration name this machine has in an overlay.
func (q Query) hostName() string {
	if q.Darwin {
		return q.Hostname
	}
	return q.User + "@" + q.Hostname
}

// Matches: the overlay has a configuration for this machine.
func Matches(h Hosts, q Query) bool {
	names := h.Home
	if q.Darwin {
		names = h.Darwin
	}
	for _, n := range names {
		if n == q.hostName() {
			return true
		}
	}
	return false
}

// Resolve picks the overlay of a checkout: --overlay, else the
// one the cwd is in, else the only one, else the one whose hosts include
// this machine. Two overlays claiming the machine is an error, never a
// silent pick.
func Resolve(root string, q Query, hosts HostsFunc) (Resolution, error) {
	root = physical(root)
	names, err := Overlays(root)
	if err != nil {
		return Resolution{}, err
	}
	r := Resolution{Root: root}
	pick := func(name, reason string) (Resolution, error) {
		r.Overlay, r.Dir, r.Reason = name, filepath.Join(root, "overlays", name), reason
		return r, nil
	}
	if q.Overlay != "" {
		for _, n := range names {
			if n == q.Overlay {
				return pick(n, "--overlay")
			}
		}
		return Resolution{}, fmt.Errorf("no overlay %s in %s/overlays (have: %s)", q.Overlay, root, strings.Join(names, ", "))
	}
	if n, ok := OverlayOf(root, q.Cwd); ok {
		return pick(n, "cwd")
	}
	switch len(names) {
	case 0:
		r.Dir, r.Reason = root, "no overlays: the engine"
		r.Warnings = []string{fmt.Sprintf("no overlay in %s/overlays: clone your overlay repo there (switch and build need one)", root)}
		return r, nil
	case 1:
		return pick(names[0], "the only overlay")
	}
	var matched []string
	for _, n := range names {
		h, err := hosts(filepath.Join(root, "overlays", n))
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("overlay %s: %v", n, err))
			continue
		}
		if Matches(h, q) {
			matched = append(matched, n)
		}
	}
	switch len(matched) {
	case 1:
		return pick(matched[0], "hosts include "+q.hostName())
	case 0:
		if len(r.Warnings) > 0 { // the failure is the likelier cause: lead with it
			return Resolution{}, fmt.Errorf("could not evaluate %s\n(no other overlay in %s/overlays has this host (%s); pass --overlay <name>)",
				strings.Join(r.Warnings, "\ncould not evaluate "), root, q.Hostname)
		}
		return Resolution{}, fmt.Errorf("no overlay in %s/overlays has this host (%s); pass --overlay <name>", root, q.Hostname)
	}
	return Resolution{}, fmt.Errorf("overlays %s both have this host (%s): pass --overlay <name>", strings.Join(matched, ", "), q.Hostname)
}

// OverrideRef is the flake ref piste overrides an overlay's engine with: a git
// work tree (a checkout, a worktree) as git+file, so only tracked files
// reach the store and never the overlays/ repos under a checkout; anything
// else as path:.
func OverrideRef(dir string) string {
	if exists(filepath.Join(dir, ".git")) {
		return "git+file://" + dir
	}
	return "path:" + dir
}

// CheckoutFromManifest is the checkout the agents manifest records: the
// piste-agents source's workdir is <checkout>/agents.
func CheckoutFromManifest(manifestPath string) (root string, ok bool) {
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", false
	}
	var m struct {
		Sources []struct{ Name, Workdir string } `json:"sources"`
	}
	if json.Unmarshal(b, &m) != nil {
		return "", false
	}
	for _, s := range m.Sources {
		if s.Name == "piste-agents" && s.Workdir != "" {
			root := filepath.Dir(s.Workdir)
			if exists(filepath.Join(root, Marker)) {
				return physical(root), true
			}
		}
	}
	return "", false
}
