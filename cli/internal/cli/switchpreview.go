package cli

// What setup's first switch will change, said before it asks (switch-preview):
// the files of yours it moves aside, read from the generation already built;
// on a Mac, the Homebrew apps and the sudo prompt; on a NixOS host, the sudo
// prompt; and an existing nix-darwin or NixOS system or home-manager
// generation it replaces.

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/elocke/piste/cli/internal/platform"
)

// previewShown caps the moved-aside list; the rest is a count.
const previewShown = 20

// storeDir is the Nix store the generation's scripts name (a variable for
// the tests).
var storeDir = "/nix/store"

// storeRefs are the store paths a script names: <storeDir>/<hash>-<name>.
func storeRefs(text string) []string {
	re := regexp.MustCompile(regexp.QuoteMeta(storeDir) + `/[0-9a-z]{32}-[A-Za-z0-9+._?=-]+`)
	return re.FindAllString(text, -1)
}

// previewSwitch prints what switching overlays/<name> will change on this
// machine: before setup's question, so the person agrees to the specific
// list. Nothing it can't read stops anything: the switch says it as it goes.
func (a *App) previewSwitch(name string) {
	a.Overlay = name
	p, err := a.machine()
	if err != nil || p.FlakeErr != nil {
		return
	}
	q, key, err := a.thisMachine()
	if err != nil {
		return
	}
	// The target setup's switch resolves (it names this machine as --host).
	// Elsewhere than a Mac or NixOS, a home, by name: no eval.
	t := target{Kind: KindHome, Name: key}
	if p.IsDarwin || p.IsNixOS {
		if t, err = a.resolveTarget(p, q.Hostname); err != nil {
			return
		}
	}
	gen := a.builtGeneration(p, t)
	if gen != "" {
		moved, links := movedAside(homeFilesOf(gen, p.User), p.HomeDir)
		if len(moved) > 0 {
			a.info(fmt.Sprintf("The switch moves %s of yours aside, to put its own in their place:", plural(len(moved), "file")))
			for i, m := range moved {
				if i == previewShown {
					fmt.Fprintf(a.Stdout, "  … and %d more\n", len(moved)-previewShown)
					break
				}
				t := tilde(p.HomeDir, filepath.Join(p.HomeDir, m))
				fmt.Fprintf(a.Stdout, "  %s → %s.backup-<time>\n", t, t)
			}
		}
		for i, l := range links {
			if i == previewShown {
				fmt.Fprintf(a.Stdout, "  … and %d more links of yours\n", len(links)-previewShown)
				break
			}
			a.warn(fmt.Sprintf("%s is a link of yours: the switch stops on it; move it away first", tilde(p.HomeDir, filepath.Join(p.HomeDir, l))))
		}
	}
	switch t.Kind {
	case KindDarwin:
		apps := "Homebrew apps"
		if n, ok := brewApps(gen); ok {
			apps = plural(n, "Homebrew app")
		}
		a.info(fmt.Sprintf("It installs %s and changes some macOS settings (the README lists them); sudo will ask for your password.", apps))
	case KindNixOS:
		a.info("sudo will ask for your password.")
	}
	if l := existingSetup(t.Kind, a.switchProfile(p, t.Kind), hmProfile(a.Env.Getenv, p.HomeDir, p.User, platform.UserProfiles), gen, a.piste()); l != "" {
		a.info(l)
	}
}

// existingSetup is the line about what this machine runs already: a
// generation of the profile a switch of kind moves (profile), which the
// switch replaces, or is on already when it is gen; on a Mac without a
// nix-darwin system, a standalone home-manager generation (hm), whose files
// the switch takes over. "" when there is neither.
func existingSetup(kind Kind, profile, hm, gen, piste string) string {
	if before, ok := activeGeneration(profile); ok {
		what := "home-manager generation"
		switch kind {
		case KindDarwin:
			what = "nix-darwin system"
		case KindNixOS:
			what = "NixOS system"
		}
		real, _ := filepath.EvalSymlinks(gen)
		if gen != "" && before.path == real {
			return fmt.Sprintf("This machine is on this %s already.", what)
		}
		return fmt.Sprintf("This machine has a %s already (generation %d): the switch replaces it, and %s rollback goes back to it.", what, before.num, piste)
	}
	if kind == KindDarwin {
		if before, ok := activeGeneration(hm); ok {
			return fmt.Sprintf("This machine has a standalone home-manager generation (generation %d): the nix-darwin switch takes over its files; home-manager's own generations stay, for home-manager's rollback.", before.num)
		}
	}
	return ""
}

// builtGeneration is the store path the switch would activate for t: a
// home's home-manager generation, a nix-darwin or NixOS system. Built
// already by setup's build, so this is nix finding it; "" when nix says
// nothing usable.
func (a *App) builtGeneration(p platform.Platform, t target) string {
	attr := `#homeConfigurations."` + t.Name + `".activationPackage`
	switch t.Kind {
	case KindDarwin:
		attr = `#darwinConfigurations."` + t.Name + `".system`
	case KindNixOS:
		attr = `#nixosConfigurations."` + t.Name + `".config.system.build.toplevel`
	}
	o, err := a.engineOverride(p)
	if err != nil {
		return ""
	}
	argv := append([]string{"build", "--no-link", "--print-out-paths", p.FlakeDir + attr}, o.Args()...)
	c := exec.Command("nix", argv...)
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, a.Stderr
	if c.Run() != nil {
		a.info("Couldn't build this machine to say what the switch changes (above); the switch will try the same build.")
		return ""
	}
	gen := strings.TrimSpace(out.String())
	if !filepath.IsAbs(gen) || strings.Contains(gen, "\n") || !isDir(gen) {
		return ""
	}
	return gen
}

// homeFilesOf is the home-files directory of the home-manager generation in
// gen: gen's own (a standalone home-manager generation), else, in a
// nix-darwin system, user's, found through the system's activate script and
// the activation-<user> script it runs. "" when there is none.
func homeFilesOf(gen, user string) string {
	if d := filepath.Join(gen, "home-files"); isDir(d) {
		return d
	}
	refs := func(path string) []string {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		return storeRefs(string(b))
	}
	hm := func(paths []string) string {
		for _, r := range paths {
			if strings.HasSuffix(r, "-home-manager-generation") && isDir(filepath.Join(r, "home-files")) {
				return filepath.Join(r, "home-files")
			}
		}
		return ""
	}
	system := refs(filepath.Join(gen, "activate"))
	if d := hm(system); d != "" {
		return d
	}
	for _, r := range system {
		if strings.HasSuffix(r, "-activation-"+user) {
			if d := hm(refs(r)); d != "" {
				return d
			}
		}
	}
	return ""
}

// movedAside is what home-manager's link check (its check-link-targets.sh)
// finds in home's way, for each file or link of homeFiles. moved: a real
// file or directory there, which the switch moves aside, unless it is a file
// with the same bytes. links: a link of the person's there (one that
// resolves, isn't into a *-home-manager-files of the store, and doesn't
// hold the same bytes), which home-manager never moves: the switch stops on
// it. A dangling link and what's absent are neither. Relative to home, in
// walk order.
func movedAside(homeFiles, home string) (moved, links []string) {
	if homeFiles == "" || home == "" {
		return nil, nil
	}
	root, err := filepath.EvalSymlinks(homeFiles)
	if err != nil {
		return nil, nil
	}
	store, err := filepath.EvalSymlinks(storeDir)
	if err != nil {
		store = storeDir
	}
	ours := func(dest string) bool {
		rest, ok := strings.CutPrefix(dest, store+"/")
		return ok && strings.Contains(rest, "-home-manager-files/")
	}
	_ = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		target := filepath.Join(home, rel)
		fi, err := os.Lstat(target)
		if err != nil {
			return nil
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			dest, err := os.Readlink(target)
			if _, serr := os.Stat(target); err != nil || serr != nil || ours(dest) || sameBytes(path, target) {
				return nil
			}
			links = append(links, rel)
			return nil
		}
		if fi.Mode().IsRegular() && sameBytes(path, target) {
			return nil
		}
		moved = append(moved, rel)
		return nil
	})
	return moved, links
}

// sameBytes: a and b (followed through links) hold the same bytes.
func sameBytes(a, b string) bool {
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

// brewApps counts the casks and Mac App Store apps in the Brewfile a
// nix-darwin system's activation runs brew bundle on; !ok without one.
func brewApps(gen string) (int, bool) {
	if gen == "" {
		return 0, false
	}
	b, err := os.ReadFile(filepath.Join(gen, "activate"))
	if err != nil {
		return 0, false
	}
	for _, r := range storeRefs(string(b)) {
		if !strings.HasSuffix(r, "-Brewfile") {
			continue
		}
		f, err := os.ReadFile(r)
		if err != nil {
			return 0, false
		}
		n := 0
		for _, l := range strings.Split(string(f), "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "cask ") || strings.HasPrefix(l, "mas ") {
				n++
			}
		}
		return n, true
	}
	return 0, false
}

// plural is "1 file", "2 files".
func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %ss", n, one)
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
