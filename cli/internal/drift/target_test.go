package drift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// overlay writes files (path -> text) into a fresh overlay dir.
func overlay(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, s := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// store is how the eval names an overlay file: under some store path.
func store(rel string) string { return "/nix/store/0000000000000000000000000000000a-source/" + rel }

var macNix = Lists(ToNixpkgs, true)

// A system-level host (a Mac or NixOS) takes nixpkgs into its system's
// lists, a home into home.packages; Homebrew's lists are the same either way.
func TestListsSystemLevel(t *testing.T) {
	options := func(ls []List) string {
		var o []string
		for _, l := range ls {
			o = append(o, l.Option)
		}
		return strings.Join(o, " ")
	}
	for _, c := range []struct {
		to          Target
		systemLevel bool
		want        string
	}{
		{ToNixpkgs, true, "piste.packages.extraPackages environment.systemPackages"},
		{ToNixpkgs, false, "piste.packages.extraPackages home.packages"},
		{ToCasks, true, "piste.packages.casks.extra homebrew.casks"},
		{ToBrews, false, "piste.packages.brews.extra homebrew.brews"},
	} {
		if got := options(Lists(c.to, c.systemLevel)); got != c.want {
			t.Errorf("Lists(%v, %v) = %s, want %s", c.to, c.systemLevel, got, c.want)
		}
	}
}

func TestFindListHostsFirst(t *testing.T) {
	dir := overlay(t, map[string]string{
		"modules/a.nix": "{ piste.packages.extraPackages = [ ]; }\n",
		"hosts/mac.nix": "{ piste.packages.extraPackages = [ ]; }\n",
	})
	w, ok := FindList(dir, macNix, map[string][]string{
		"piste.packages.extraPackages": {store("modules/a.nix"), store("hosts/mac.nix")},
	})
	if !ok || w.File != filepath.Join(dir, "hosts/mac.nix") || w.List.Option != "piste.packages.extraPackages" {
		t.Errorf("got %+v, %v", w, ok)
	}
}

// A module this host doesn't import (files doesn't name it) is never
// written to, even when it opens the list.
func TestFindListSkipsUnnamed(t *testing.T) {
	dir := overlay(t, map[string]string{
		"modules/devcontainer.nix": "{ piste.packages.extraPackages = [ ]; }\n",
		"modules/zz.nix":           "{ piste.packages.extraPackages = [ ]; }\n",
	})
	files := map[string][]string{"piste.packages.extraPackages": {store("modules/zz.nix")}}
	if w, ok := FindList(dir, macNix, files); !ok || w.File != filepath.Join(dir, "modules/zz.nix") {
		t.Errorf("got %+v, %v", w, ok)
	}
	if w, ok := FindList(dir, macNix, map[string][]string{"piste.packages.extraPackages": {store("modules/packages-module.nix")}}); ok {
		t.Errorf("none named: %+v", w)
	}
}

// A path input (a team layer in a subfolder) is not the overlay's to write.
func TestFindListSkipsPathInput(t *testing.T) {
	dir := overlay(t, map[string]string{
		"layer/hosts/x.nix": "{ piste.packages.extraPackages = [ ]; }\n",
		"flake.lock":        `{"nodes": {"layer": {"locked": {"path": "./layer", "type": "path"}}, "root": {"inputs": {"layer": "layer"}}}, "root": "root", "version": 7}`,
	})
	if w, ok := FindList(dir, macNix, map[string][]string{"piste.packages.extraPackages": {store("layer/hosts/x.nix"), store("hosts/x.nix")}}); ok {
		t.Errorf("wrote into the layer: %+v", w)
	}
}

// The lists in order; an option the host doesn't have isn't tried.
func TestFindListOrder(t *testing.T) {
	dir := overlay(t, map[string]string{
		"hosts/mac.nix": "{ pkgs, ... }: {\n  environment.systemPackages = [ pkgs.git ];\n  piste.packages.extraPackages = [ ];\n}\n",
	})
	w, ok := FindList(dir, macNix, map[string][]string{"environment.systemPackages": {store("hosts/mac.nix")}})
	if !ok || w.List.Option != "environment.systemPackages" || w.WithPkgs {
		t.Errorf("got %+v, %v", w, ok)
	}
}

func TestFindListWithPkgs(t *testing.T) {
	dir := overlay(t, map[string]string{"hosts/devbox.nix": "{ pkgs, ... }: {\n  home.packages = with pkgs; [ ];\n}\n"})
	w, ok := FindList(dir, Lists(ToNixpkgs, false), map[string][]string{"home.packages": {store("hosts/devbox.nix")}})
	if !ok || !w.WithPkgs || w.List.Option != "home.packages" {
		t.Errorf("got %+v, %v", w, ok)
	}
}

func TestHandFile(t *testing.T) {
	casks := Lists(ToCasks, true)
	// The nested form defines the option where adopt can't write: named.
	dir := overlay(t, map[string]string{
		"modules/work.nix": "{ piste.packages.casks = {\n    extra = [ ];\n  };\n}\n",
		"hosts/devbox.nix": "{ }\n",
	})
	files := map[string][]string{"piste.packages.casks.extra": {store("modules/work.nix")}, "homebrew.casks": {store("modules/platforms/darwin.nix")}}
	if _, ok := FindList(dir, casks, files); ok {
		t.Fatal("the nested form was written to")
	}
	if got := HandFile(dir, casks, files, "jane-mac"); got != "modules/work.nix" {
		t.Errorf("nested: %q", got)
	}
	// Defined nowhere in the overlay: the host's file.
	files = map[string][]string{"home.packages": {store("modules/roles/dev.nix")}}
	if got := HandFile(dir, Lists(ToNixpkgs, false), files, "jane@devbox"); got != "hosts/devbox.nix" {
		t.Errorf("host file: %q", got)
	}
	if got := HandFile(dir, Lists(ToNixpkgs, false), files, "jane@elsewhere"); got != "" {
		t.Errorf("nothing: %q", got)
	}
	if l := HandList(Lists(ToNixpkgs, false), files); l.Option != "home.packages" {
		t.Errorf("hand list %+v", l)
	}
	if l := HandList(casks, map[string][]string{}); l.Option != "homebrew.casks" {
		t.Errorf("hand list, none: %+v", l)
	}
}

func TestEntry(t *testing.T) {
	for _, c := range []struct {
		to       Target
		name     string
		withPkgs bool
		want     string
	}{
		{ToNixpkgs, "ripgrep", true, "ripgrep"},
		{ToNixpkgs, "ripgrep", false, "pkgs.ripgrep"},
		{ToNixpkgs, "g++", true, `pkgs."g++"`},
		{ToNixpkgs, "g++", false, `pkgs."g++"`},
		{ToCasks, "slack", false, `"slack"`},
		{ToBrews, "python@3.12", false, `"python@3.12"`},
		{ToPnpm, "@scope/pkg", false, `"@scope/pkg"`},
	} {
		if got := Entry(c.to, c.name, c.withPkgs); got != c.want {
			t.Errorf("Entry(%v, %q, %v) = %q, want %q", c.to, c.name, c.withPkgs, got, c.want)
		}
	}
}

func TestListCands(t *testing.T) {
	if got := (List{"homebrew.casks", "homebrew.casks"}).Cands(); len(got) != 1 {
		t.Errorf("%q", got)
	}
	if got := (List{"piste.packages.casks.extra", "casks.extra"}).Cands(); len(got) != 2 || got[0] != "piste.packages.casks.extra" {
		t.Errorf("%q", got)
	}
}

// Piste's list written in the nested form is found by its
// short opener, not another option's `programs.neovim.extraPackages = with
// pkgs; [` above it (whose form would otherwise set WithPkgs).
func TestFindListNestedShortOpener(t *testing.T) {
	dir := overlay(t, map[string]string{
		"hosts/mac.nix": "{ pkgs, ... }:\n{\n" +
			"  home-manager.users.jane = { pkgs, ... }: { programs.neovim.extraPackages = with pkgs; [ nil ]; };\n" +
			"  piste.packages = {\n    extraPackages = [ pkgs.jq ];\n  };\n}\n",
	})
	w, ok := FindList(dir, macNix, map[string][]string{"piste.packages.extraPackages": {store("hosts/mac.nix")}})
	if !ok || w.List.Option != "piste.packages.extraPackages" || w.WithPkgs {
		t.Errorf("got %+v, %v; want piste's plain list", w, ok)
	}
}
