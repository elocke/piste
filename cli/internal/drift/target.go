package drift

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/packages"
)

// List is a list adopt writes into: the option that holds it and the text
// that opens it (`<Opener> = [`, or `= with pkgs; [`).
type List struct{ Option, Opener string }

// Cands are the openers to look for, the option's full path first: an
// earlier `programs.neovim.extraPackages = [` mustn't pass for piste's.
func (l List) Cands() []string {
	if l.Option == l.Opener {
		return []string{l.Option}
	}
	return []string{l.Option, l.Opener}
}

var (
	extraPackages = List{"piste.packages.extraPackages", "extraPackages"}
	lists         = map[Target][]List{
		ToCasks: {{"piste.packages.casks.extra", "casks.extra"}, {"homebrew.casks", "homebrew.casks"}},
		ToBrews: {{"piste.packages.brews.extra", "brews.extra"}, {"homebrew.brews", "homebrew.brews"}},
	}
)

// Lists are the lists a target's entries go into, in the order tried.
func Lists(to Target, darwin bool) []List {
	if to != ToNixpkgs {
		return lists[to]
	}
	if darwin {
		return []List{extraPackages, {"environment.systemPackages", "systemPackages"}}
	}
	return []List{extraPackages, {"home.packages", "home.packages"}}
}

// Where is the file a target's entries go into.
type Where struct {
	File     string // absolute
	List     List
	WithPkgs bool // the list is `with pkgs; [`: entries go in bare
}

// FindList is where entries for lists go: for each list whose option this
// host has (a key of files, the eval's option -> defining files), the first
// of the overlay's own .nix files (hosts/ first, then by path; none in a
// path input) that the option's files name and whose text opens the list.
// files names a file by its path in the overlay after a "/": the store
// prefix differs per source.
func FindList(flakeDir string, lists []List, files map[string][]string) (Where, bool) {
	own := ownFiles(flakeDir)
	for _, l := range lists {
		defs, ok := files[l.Option]
		if !ok {
			continue
		}
		for _, rel := range own {
			if !named(defs, rel) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(flakeDir, rel))
			if err != nil {
				continue
			}
			if i, with := packages.ListAt(strings.Split(string(b), "\n"), l.Cands()); i >= 0 {
				return Where{File: filepath.Join(flakeDir, rel), List: l, WithPkgs: with}, true
			}
		}
	}
	return Where{}, false
}

// HandList is the list to name when there's nowhere to write: the first the
// host has, else the last tried.
func HandList(lists []List, files map[string][]string) List {
	for _, l := range lists {
		if _, ok := files[l.Option]; ok {
			return l
		}
	}
	return lists[len(lists)-1]
}

// HandFile is the overlay file (relative) to name in add-by-hand advice:
// one that defines HandList's option in a form adopt can't write (a nested
// `casks = { extra = [ … ]; }`), else hosts/<host>.nix ("user@" dropped)
// if there is one, else "".
func HandFile(flakeDir string, lists []List, files map[string][]string, host string) string {
	defs := files[HandList(lists, files).Option]
	for _, rel := range ownFiles(flakeDir) {
		if named(defs, rel) {
			return rel
		}
	}
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	rel := filepath.Join("hosts", host+".nix")
	if fi, err := os.Stat(filepath.Join(flakeDir, rel)); err == nil && fi.Mode().IsRegular() {
		return rel
	}
	return ""
}

// named: one of defs is the overlay file rel.
func named(defs []string, rel string) bool {
	for _, d := range defs {
		if strings.HasSuffix(d, "/"+filepath.ToSlash(rel)) {
			return true
		}
	}
	return false
}

// ownFiles are the overlay's .nix files, relative: hosts/ first, then by
// path; nothing under .git or a path input (a layer kept in a subfolder).
func ownFiles(flakeDir string) []string {
	skip := map[string]bool{}
	for _, d := range pathInputs(flakeDir) {
		skip[d] = true
	}
	var out []string
	filepath.WalkDir(flakeDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != flakeDir && (d.Name() == ".git" || skip[p]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".nix") {
			rel, _ := filepath.Rel(flakeDir, p)
			out = append(out, rel)
		}
		return nil
	})
	host := func(rel string) bool { return strings.HasPrefix(filepath.ToSlash(rel), "hosts/") }
	sort.SliceStable(out, func(i, j int) bool {
		if host(out[i]) != host(out[j]) {
			return host(out[i])
		}
		return out[i] < out[j]
	})
	return out
}

var nixIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_'-]*$`)

// Entry is name as an element of a target's list: a package bare inside
// `with pkgs;`, else pkgs.<name> (quoted when it isn't an identifier); any
// other target's entry is a string.
func Entry(to Target, name string, withPkgs bool) string {
	switch {
	case to != ToNixpkgs:
		return `"` + name + `"`
	case !nixIdent.MatchString(name):
		return `pkgs."` + name + `"`
	case withPkgs:
		return name
	}
	return "pkgs." + name
}
