package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

// The files home-manager's link check moves aside, read from the built
// generation: a real file or directory where it links one, unless the same
// bytes. A live link of the person's (stow, dotbot) that differs is a
// collision the switch stops on: listed apart. A generation's own links
// (into *-home-manager-files), a dangling link and what's absent aren't
// listed.
func TestMovedAside(t *testing.T) {
	store, home := t.TempDir(), t.TempDir()
	old := storeDir
	storeDir = store
	t.Cleanup(func() { storeDir = old })
	src := filepath.Join(store, "src")
	write(t, filepath.Join(src, "zshrc"), "piste's\n")
	write(t, filepath.Join(src, "gitconfig"), "same\n")
	write(t, filepath.Join(src, "ssh-config"), "Host *\n")
	write(t, filepath.Join(src, "nvim", "init.lua"), "--\n")
	write(t, filepath.Join(src, "vimrc"), "piste's\n")
	files := filepath.Join(store, strings.Repeat("a", 32)+"-home-manager-files")
	link(t, filepath.Join(src, "zshrc"), filepath.Join(files, ".zshrc"))
	link(t, filepath.Join(src, "gitconfig"), filepath.Join(files, ".gitconfig"))
	link(t, filepath.Join(src, "ssh-config"), filepath.Join(files, ".ssh", "config"))
	link(t, filepath.Join(src, "nvim"), filepath.Join(files, ".config", "nvim"))
	link(t, filepath.Join(src, "zshrc"), filepath.Join(files, ".profile"))
	link(t, filepath.Join(src, "zshrc"), filepath.Join(files, ".bashrc"))
	link(t, filepath.Join(src, "zshrc"), filepath.Join(files, ".absent"))
	link(t, filepath.Join(src, "vimrc"), filepath.Join(files, ".vimrc"))
	link(t, filepath.Join(src, "gitconfig"), filepath.Join(files, ".inputrc"))
	// The generation's home-files is itself a link (to *-home-manager-files).
	gen := filepath.Join(store, "gen")
	link(t, files, filepath.Join(gen, "home-files"))
	// An older generation's files, with other bytes.
	oldFiles := filepath.Join(store, strings.Repeat("b", 32)+"-home-manager-files")
	write(t, filepath.Join(oldFiles, ".profile"), "older\n")

	write(t, filepath.Join(home, ".zshrc"), "mine\n")
	write(t, filepath.Join(home, ".gitconfig"), "same\n")
	write(t, filepath.Join(home, ".ssh", "config"), "Host box\n")
	write(t, filepath.Join(home, ".config", "nvim", "init.vim"), "mine\n")
	link(t, filepath.Join(oldFiles, ".profile"), filepath.Join(home, ".profile")) // an old generation's
	link(t, "/elsewhere/bashrc", filepath.Join(home, ".bashrc"))                  // dangling
	write(t, filepath.Join(home, "dotfiles", "vimrc"), "mine\n")
	link(t, filepath.Join(home, "dotfiles", "vimrc"), filepath.Join(home, ".vimrc")) // stow's
	write(t, filepath.Join(home, "dotfiles", "inputrc"), "same\n")
	link(t, filepath.Join(home, "dotfiles", "inputrc"), filepath.Join(home, ".inputrc")) // stow's, same bytes

	got, links := movedAside(homeFilesOf(gen, "jane"), home)
	want := []string{".config/nvim", ".ssh/config", ".zshrc"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("moved %q, want %q", got, want)
	}
	if want := []string{".vimrc"}; !reflect.DeepEqual(links, want) {
		t.Errorf("links of yours %q, want %q", links, want)
	}
	for _, d := range []string{"", filepath.Join(store, "none")} {
		if m, l := movedAside(d, home); m != nil || l != nil {
			t.Errorf("no generation (%q): %q, %q listed", d, m, l)
		}
	}
}

// The line about the setup already on this machine: the profile the switch
// moves; on a Mac without a nix-darwin system, a standalone home-manager
// generation, whose files the switch takes over.
func TestExistingSetup(t *testing.T) {
	dir := t.TempDir()
	profile := func(name, target string) string {
		p := filepath.Join(dir, name)
		link(t, target, p+"-4-link")
		link(t, name+"-4-link", p)
		return p
	}
	gen, other := filepath.Join(dir, "gen"), filepath.Join(dir, "other")
	for _, d := range []string{gen, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sys, hm, none := profile("system", other), profile("home-manager", other), filepath.Join(dir, "none")
	for _, c := range []struct {
		name        string
		darwin      bool
		profile, hm string
		gen         string
		want        string
	}{
		{"linux, none", false, none, none, gen, ""},
		{"linux, another", false, hm, hm, gen, "This machine has a home-manager generation already (generation 4): the switch replaces it, and piste rollback goes back to it."},
		{"darwin, another system", true, sys, hm, gen, "This machine has a nix-darwin system already (generation 4): the switch replaces it, and piste rollback goes back to it."},
		{"darwin, this system", true, sys, none, other, "This machine is on this nix-darwin system already."},
		{"darwin, standalone home-manager", true, none, hm, gen, "This machine has a standalone home-manager generation (generation 4): the nix-darwin switch takes over its files; home-manager's own generations stay, for home-manager's rollback."},
		{"darwin, none", true, none, none, gen, ""},
	} {
		if got := existingSetup(c.darwin, c.profile, c.hm, c.gen, "piste"); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// A nix-darwin system: its activate script runs activation-<user>, which
// runs the user's home-manager generation; its Brewfile's casks and mas
// apps are the Homebrew apps.
func TestHomeFilesOfDarwinSystem(t *testing.T) {
	store := t.TempDir()
	old := storeDir
	storeDir = store
	t.Cleanup(func() { storeDir = old })
	h := func(c byte, name string) string { return filepath.Join(store, strings.Repeat(string(c), 32)+"-"+name) }
	hm, jane, other, brewfile, sys := h('a', "home-manager-generation"), h('b', "activation-jane"), h('c', "activation-sam"), h('d', "Brewfile"), h('e', "darwin-system-26.11")
	if err := os.MkdirAll(filepath.Join(hm, "home-files"), 0o755); err != nil {
		t.Fatal(err)
	}
	otherHM := h('f', "home-manager-generation")
	if err := os.MkdirAll(filepath.Join(otherHM, "home-files"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, jane, "#!/bin/sh\nexec "+hm+"/activate --driver-version 1 >&2\n")
	write(t, other, "#!/bin/sh\nexec "+otherHM+"/activate --driver-version 1 >&2\n")
	write(t, brewfile, "tap \"homebrew/cask\"\nbrew \"mas\"\ncask \"ghostty\"\ncask \"firefox\"\nmas \"Xcode\", id: 497799835\n")
	write(t, filepath.Join(sys, "activate"), "#!/bin/sh\nbrew bundle --file='"+brewfile+"' --no-upgrade\n"+
		"sudo -u sam --set-home "+other+"\nsudo -u jane --set-home "+jane+"\n")
	if got := homeFilesOf(sys, "jane"); got != filepath.Join(hm, "home-files") {
		t.Errorf("home-files %q, want jane's %q", got, filepath.Join(hm, "home-files"))
	}
	if got := homeFilesOf(sys, "nobody"); got != "" {
		t.Errorf("no such user: %q", got)
	}
	if n, ok := brewApps(sys); !ok || n != 3 {
		t.Errorf("brewApps = %d, %v; want 3 (two casks, one mas)", n, ok)
	}
	if _, ok := brewApps(hm); ok {
		t.Error("a home-manager generation has no Brewfile")
	}
}

// A github.com clone that fails gets the https form to try with gh's login.
func TestGithubHTTPS(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:jane/overlay.git":         "https://github.com/jane/overlay.git",
		"git@github.com:jane/overlay":             "https://github.com/jane/overlay.git",
		"ssh://git@github.com/acme/layer.git":     "https://github.com/acme/layer.git",
		"git+ssh://git@github.com/acme/layer.git": "https://github.com/acme/layer.git",
		"https://github.com/jane/overlay":         "https://github.com/jane/overlay.git",
		"git@gitlab.com:jane/overlay.git":         "",
		"/home/jane/overlay":                      "",
		"git@github.com:jane":                     "",
		"https://github.com/jane/overlay/tree/x":  "",
	} {
		if got := githubHTTPS(in); got != want {
			t.Errorf("githubHTTPS(%q) = %q, want %q", in, got, want)
		}
	}
}

// After a Linux switch: a login shell that isn't zsh gets the two commands.
func TestZshHint(t *testing.T) {
	if l := zshHint("/bin/zsh", "~/.nix-profile/bin/zsh"); l != nil {
		t.Errorf("zsh already: %q", l)
	}
	l := zshHint("/bin/bash", "~/.nix-profile/bin/zsh")
	if len(l) != 2 || !strings.Contains(l[0], "still bash") || l[1] != "  echo ~/.nix-profile/bin/zsh | sudo tee -a /etc/shells && chsh -s ~/.nix-profile/bin/zsh" {
		t.Errorf("bash: %q", l)
	}
}
