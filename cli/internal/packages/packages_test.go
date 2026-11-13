package packages

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(p string) string { b, _ := os.ReadFile(p); return string(b) }

func TestInsertsIntoMultilineList(t *testing.T) {
	f := filepath.Join(t.TempDir(), "apps.nix")
	write(t, f, "{\n  homebrew.casks = [\n    \"firefox\"\n  ];\n}\n")
	if ok, err := AddToNixList(`"slack"`, f, []string{"casks"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	c := read(f)
	if !strings.Contains(c, `"slack"`) || strings.Index(c, `"firefox"`) > strings.Index(c, `"slack"`) {
		t.Fatal(c)
	}
	if c != "{\n  homebrew.casks = [\n    \"firefox\"\n    \"slack\"\n  ];\n}\n" {
		t.Fatalf("%q", c)
	}
}

func TestFallsBackToAllPrefixed(t *testing.T) {
	f := filepath.Join(t.TempDir(), "packages-module.nix")
	write(t, f, "  allCasks = [\n    \"firefox\"\n  ];\n")
	if ok, _ := AddToNixList(`"slack"`, f, []string{"casks", "allCasks"}); !ok || !strings.Contains(read(f), `"slack"`) {
		t.Fatal(read(f))
	}
}

func TestExpandsInlineEmptyList(t *testing.T) {
	f := filepath.Join(t.TempDir(), "packages-module.nix")
	write(t, f, "  allBrews = [ ];\n")
	if ok, _ := AddToNixList(`"ffmpeg"`, f, []string{"brews", "allBrews"}); !ok {
		t.Fatal("not added")
	}
	c := read(f)
	if c != "  allBrews = [\n    \"ffmpeg\"\n  ];\n" || !strings.HasSuffix(strings.TrimRight(c, "\n"), "];") {
		t.Fatalf("%q", c)
	}
	f2 := filepath.Join(t.TempDir(), "x.nix")
	write(t, f2, "  allBrews = [ \"a\" ];\n")
	AddToNixList(`"b"`, f2, []string{"allBrews"})
	if read(f2) != "  allBrews = [\n    \"a\"\n    \"b\"\n  ];\n" {
		t.Fatalf("%q", read(f2))
	}
}

func TestNoCandidate(t *testing.T) {
	f := filepath.Join(t.TempDir(), "packages-module.nix")
	write(t, f, "  cliPkgs = with pkgs; [ ];\n")
	if ok, _ := AddToNixList(`"slack"`, f, []string{"casks", "allCasks"}); ok {
		t.Fatal("added")
	}
}

// A `with pkgs;` list, inline and empty: the entry goes in bare, as given.
func TestWithPkgsInline(t *testing.T) {
	f := filepath.Join(t.TempDir(), "mac.nix")
	write(t, f, "  piste.packages.extraPackages = with pkgs; [ ];\n")
	if ok, err := AddToNixList("ripgrep", f, []string{"extraPackages"}); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if c := read(f); c != "  piste.packages.extraPackages = with pkgs; [\n    ripgrep\n  ];\n" {
		t.Fatalf("%q", c)
	}
}

func TestWithPkgsInlineWithItems(t *testing.T) {
	f := filepath.Join(t.TempDir(), "x.nix")
	write(t, f, "x = with pkgs; [ a b ];\n")
	if ok, _ := AddToNixList("c", f, []string{"x"}); !ok {
		t.Fatal("not added")
	}
	if c := read(f); c != "x = with pkgs; [\n  a b\n  c\n];\n" {
		t.Fatalf("%q", c)
	}
}

// A plain list of packages: the caller writes pkgs.<name>; it goes in as given.
func TestPkgsEntryMultiline(t *testing.T) {
	f := filepath.Join(t.TempDir(), "box.nix")
	write(t, f, "{\n  home.packages = [\n    foo\n  ];\n}\n")
	if ok, _ := AddToNixList("pkgs.ripgrep", f, []string{"home.packages"}); !ok {
		t.Fatal("not added")
	}
	if c := read(f); c != "{\n  home.packages = [\n    foo\n    pkgs.ripgrep\n  ];\n}\n" {
		t.Fatalf("%q", c)
	}
}

// ListAt finds the first candidate that has a list, in either form: a full
// option path before its short opener, so another option's
// `…extraPackages = [` above it isn't taken for piste's own; and the short
// opener only where it starts a path, never inside another option's.
func TestListAt(t *testing.T) {
	lines := []string{
		"{",
		"  programs.neovim.extraPackages = [ ];",
		"  home.packages = with pkgs; [ ];",
		"  piste.packages.extraPackages = [ ];",
	}
	if i, w := ListAt(lines, []string{"piste.packages.extraPackages", "extraPackages"}); i != 3 || w {
		t.Errorf("piste's list: %d, %v; want 3, false", i, w)
	}
	if i, _ := ListAt(lines, []string{"extraPackages"}); i != -1 {
		t.Errorf("the short opener inside other paths: %d; want -1", i)
	}
	if i, w := ListAt(lines, []string{"home.packages"}); i != 2 || !w {
		t.Errorf("with pkgs: %d, %v; want 2, true", i, w)
	}
	if i, _ := ListAt(lines, []string{"casks.extra"}); i != -1 {
		t.Errorf("none: %d", i)
	}
}

// Writing follows ListAt: the full path wins over an earlier short match.
func TestAddPrefersFullPath(t *testing.T) {
	f := filepath.Join(t.TempDir(), "mac.nix")
	write(t, f, "  programs.neovim.extraPackages = [ ];\n  piste.packages.extraPackages = with pkgs; [ ];\n")
	if ok, _ := AddToNixList("ripgrep", f, []string{"piste.packages.extraPackages", "extraPackages"}); !ok {
		t.Fatal("not added")
	}
	if c := read(f); c != "  programs.neovim.extraPackages = [ ];\n  piste.packages.extraPackages = with pkgs; [\n    ripgrep\n  ];\n" {
		t.Fatalf("%q", c)
	}
}

// A nested `casks = { extra = [ … ]; }` isn't `casks.extra = [`: not found.
func TestNestedCasksNotMatched(t *testing.T) {
	f := filepath.Join(t.TempDir(), "work.nix")
	write(t, f, "  piste.packages.casks = {\n    extra = [ ];\n  };\n")
	if ok, _ := AddToNixList(`"slack"`, f, []string{"piste.packages.casks.extra", "casks.extra"}); ok {
		t.Fatal(read(f))
	}
}

func TestPnpmGlobals(t *testing.T) {
	d := t.TempDir()
	if ok, _ := AddToPnpmGlobals("zen-mcp", filepath.Join(d, "nope.nix")); ok {
		t.Fatal("missing file")
	}
	f := filepath.Join(d, "js-globals.nix")
	write(t, f, "let\n  pnpmPackages = [\n    \"typescript\"\n  ];\nin\n")
	if ok, _ := AddToPnpmGlobals("zen-mcp", f); !ok || strings.Index(read(f), `"typescript"`) > strings.Index(read(f), `"zen-mcp"`) {
		t.Fatal(read(f))
	}
	s := filepath.Join(d, "shell.nix")
	write(t, s, "{ programs.zsh.initContent = '''';\n}\n")
	if ok, _ := AddToPnpmGlobals("zen-mcp", s); ok {
		t.Fatal("no list")
	}
}

func TestDiscoverPnpmFiles(t *testing.T) {
	d := t.TempDir()
	write(t, filepath.Join(d, "modules", "devbox-packages.nix"), "  pnpmPackages = [\n    \"a\"\n  ];\n")
	write(t, filepath.Join(d, "modules", "js-globals.nix"), "  pnpmPackages = [\n    \"b\"\n  ];\n")
	write(t, filepath.Join(d, "modules", "git.nix"), "{ }\n")
	var names []string
	for _, p := range FindPnpmGlobalsFiles(d) {
		names = append(names, filepath.Base(p))
	}
	if !reflect.DeepEqual(names, []string{"js-globals.nix", "devbox-packages.nix"}) {
		t.Fatal(names)
	}
	d2 := t.TempDir()
	write(t, filepath.Join(d2, "modules", "shell.nix"), "    pnpmPackages = [\n      \"a\"\n    ];\n")
	if got := FindPnpmGlobalsFiles(d2); len(got) != 1 || filepath.Base(got[0]) != "shell.nix" {
		t.Fatal(got)
	}
	if got := FindPnpmGlobalsFiles(t.TempDir()); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestSearchNixpkgs(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"nix search": {Stdout: []byte(`{"legacyPackages.x86_64-linux.jq": {"pname": "jq", "version": "1.7"}, "legacyPackages.x86_64-linux.gojq": {"version": "0.12"}}`)}}}
	got := SearchNixpkgs(context.Background(), rec, "jq")
	want := []Match{{"legacyPackages.x86_64-linux.jq", "jq", "1.7"}, {"legacyPackages.x86_64-linux.gojq", "legacyPackages.x86_64-linux.gojq", "0.12"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
}

// A list adopt writes into is named by its option path; the
// short opener only matches where it starts an attribute path, so another
// option's `programs.neovim.extraPackages = [` above piste's nested
// `extraPackages = [` isn't taken for it. `pkg add`'s substring candidates
// (AddToNixList's `casks` in `homebrew.casks = [`) stay as they are.
func TestOptionListShortOpenerStartsAPath(t *testing.T) {
	const src = "{ pkgs, ... }:\n{\n" +
		"  home-manager.users.jane = { pkgs, ... }: { programs.neovim.extraPackages = with pkgs; [ nil ]; };\n" +
		"  piste.packages = {\n    extraPackages = with pkgs; [ jq ];\n  };\n}\n"
	cands := []string{"piste.packages.extraPackages", "extraPackages"}
	if i, w := ListAt(strings.Split(src, "\n"), cands); i != 4 || !w {
		t.Errorf("ListAt = %d, %v; want 4 (the nested list), true", i, w)
	}
	f := filepath.Join(t.TempDir(), "mac.nix")
	write(t, f, src)
	if ok, err := AddToOptionList("ripgrep", f, cands); !ok || err != nil {
		t.Fatal(ok, err)
	}
	want := "{ pkgs, ... }:\n{\n" +
		"  home-manager.users.jane = { pkgs, ... }: { programs.neovim.extraPackages = with pkgs; [ nil ]; };\n" +
		"  piste.packages = {\n    extraPackages = with pkgs; [\n      jq\n      ripgrep\n    ];\n  };\n}\n"
	if c := read(f); c != want {
		t.Fatalf("got\n%s\nwant\n%s", c, want)
	}
	// Only another option's list: nothing to write into.
	g := filepath.Join(t.TempDir(), "only.nix")
	write(t, g, "{\n  programs.helix.extraPackages = [ ];\n}\n")
	if ok, _ := AddToOptionList("ripgrep", g, cands); ok {
		t.Fatal(read(g))
	}
	// `config.` before the full path is the same option.
	h := filepath.Join(t.TempDir(), "cfg.nix")
	write(t, h, "{\n  config.piste.packages.extraPackages = [ ];\n}\n")
	if ok, _ := AddToOptionList("pkgs.ripgrep", h, cands); !ok {
		t.Fatal(read(h))
	}
}

// The writer writes only into a list that is the whole value
// (the matching `]`, found by depth, then `;`); a `] ++ …` list, or one
// whose opener has no matching `]`, is refused and the file left as it is.
func TestOptionListRefusesPartOfAValue(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"inline ++", "{\n  home.packages = [ pkgs.foo ] ++ lib.optionals pkgs.stdenv.isLinux [ pkgs.bar ];\n}\n"},
		{"multi-line ++ then another list", "{\n  home.packages = with pkgs; [\n    foo\n  ] ++ extra;\n  piste.packages.casks.extra = [\n  ];\n}\n"},
		{"multi-line ++ a conditional list", "{\n  home.packages = with pkgs; [\n    foo\n  ] ++ lib.optionals cond [\n    bar\n  ];\n}\n"},
		{"never closed", "{\n  home.packages = [\n    foo\n"},
	} {
		f := filepath.Join(t.TempDir(), "x.nix")
		write(t, f, c.src)
		if ok, err := AddToOptionList("ripgrep", f, []string{"home.packages"}); ok || err != nil {
			t.Errorf("%s: wrote (%v)", c.name, err)
		}
		if read(f) != c.src {
			t.Errorf("%s: file changed:\n%s", c.name, read(f))
		}
	}
}

// An opener in a comment isn't a list; the real one below it
// is written.
func TestOptionListSkipsComments(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"line comment",
			"{\n  # piste.packages.extraPackages = [ ];\n  piste.packages.extraPackages = with pkgs; [ ];\n}\n",
			"{\n  # piste.packages.extraPackages = [ ];\n  piste.packages.extraPackages = with pkgs; [\n    ripgrep\n  ];\n}\n"},
		{"after code",
			"{\n  a = 1; # piste.packages.extraPackages = [ ];\n  piste.packages.extraPackages = with pkgs; [ ];\n}\n",
			"{\n  a = 1; # piste.packages.extraPackages = [ ];\n  piste.packages.extraPackages = with pkgs; [\n    ripgrep\n  ];\n}\n"},
		{"block comment",
			"{\n  /*\n  piste.packages.extraPackages = [ ];\n  */\n  piste.packages.extraPackages = with pkgs; [ ];\n}\n",
			"{\n  /*\n  piste.packages.extraPackages = [ ];\n  */\n  piste.packages.extraPackages = with pkgs; [\n    ripgrep\n  ];\n}\n"},
	} {
		f := filepath.Join(t.TempDir(), "x.nix")
		write(t, f, c.src)
		if ok, err := AddToOptionList("ripgrep", f, []string{"piste.packages.extraPackages"}); !ok || err != nil {
			t.Errorf("%s: not written (%v)", c.name, err)
		}
		if got := read(f); got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
	// Only a commented opener: no list.
	if i, _ := ListAt([]string{"  # home.packages = [ ];"}, []string{"home.packages"}); i != -1 {
		t.Errorf("commented opener at %d", i)
	}
}

// Brackets are matched by depth, outside comments and
// strings; a trailing comment and anything after the `;` stay.
func TestOptionListBrackets(t *testing.T) {
	for _, c := range []struct{ name, src, want string }{
		{"nested inline",
			"  home.packages = [ (python3.withPackages (ps: [ ps.requests ])) ];\n",
			"  home.packages = [\n    (python3.withPackages (ps: [ ps.requests ]))\n    pkgs.ripgrep\n  ];\n"},
		{"nested multi-line",
			"  home.packages = [\n    (python3.withPackages (ps: [\n      ps.requests\n    ]))\n  ];\n  other = [\n  ];\n",
			"  home.packages = [\n    (python3.withPackages (ps: [\n      ps.requests\n    ]))\n    pkgs.ripgrep\n  ];\n  other = [\n  ];\n"},
		{"trailing comment inline",
			"  home.packages = [ ]; # mine\n",
			"  home.packages = [\n    pkgs.ripgrep\n  ]; # mine\n"},
		{"brackets in a comment and a string",
			"  home.packages = [\n    foo # see [1\n    (bar \"x]\")\n  ];\n",
			"  home.packages = [\n    foo # see [1\n    (bar \"x]\")\n    pkgs.ripgrep\n  ];\n"},
		{"closing after the last item",
			"  home.packages = [\n    foo\n    bar ];\n",
			"  home.packages = [\n    foo\n    bar\n    pkgs.ripgrep\n  ];\n"},
		{"empty multi-line",
			"  home.packages = [\n  ];\n",
			"  home.packages = [\n    pkgs.ripgrep\n  ];\n"},
	} {
		f := filepath.Join(t.TempDir(), "x.nix")
		write(t, f, c.src)
		if ok, err := AddToOptionList("pkgs.ripgrep", f, []string{"home.packages"}); !ok || err != nil {
			t.Errorf("%s: not written (%v)", c.name, err)
		}
		if got := read(f); got != c.want {
			t.Errorf("%s: got\n%q\nwant\n%q", c.name, got, c.want)
		}
	}
}
