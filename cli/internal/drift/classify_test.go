package drift

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
)

// The brew listings print a tap's items by their full names; npm, pnpm and
// pip are read as before; ~/.cargo/bin without rustup's own binaries.
func TestScanBrewFlags(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".cargo", "bin"), 0o755)
	for _, b := range []string{"cargo", "rustc", "ripgrep-all", "my-tool"} {
		os.WriteFile(filepath.Join(home, ".cargo", "bin", b), nil, 0o755)
	}
	rec := &execx.Recorder{Replies: map[string]execx.Result{
		"brew leaves": {Stdout: []byte("ripgrep\nexample/tap/foo\n")},
		"brew list":   {Stdout: []byte("slack\n")},
		"npm list":    {Stdout: []byte(`{"dependencies": {"npm": {}, "prettier": {}}}`)},
		"pnpm list":   {Stdout: []byte(`[{"dependencies": {"tsx": {}}}]`)},
		"pip list":    {Stdout: []byte(`[{"name": "httpie"}]`)},
	}}
	got := Scan(context.Background(), rec, home, true)
	want := Installs{
		Brew: {"ripgrep", "example/tap/foo"}, Cask: {"slack"}, Npm: {"prettier"}, Pnpm: {"tsx"},
		Cargo: {"my-tool", "ripgrep-all"}, Pip: {"httpie"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	var argv []string
	for _, c := range rec.Calls[:2] {
		argv = append(argv, execx.Quote(c))
	}
	if !reflect.DeepEqual(argv, []string{"brew leaves --installed-on-request", "brew list --cask --full-name -1"}) {
		t.Errorf("brew argv %q", argv)
	}
	if got := Scan(context.Background(), &execx.Recorder{}, home, false); got[Brew] != nil || got[Cask] != nil {
		t.Errorf("Linux scanned brew: %v", got)
	}
}

// pnpmPackages / npmPackages are let bindings: read as text from the
// overlay's modules/ and its relative path inputs' (a team layer kept in a
// subfolder), never an absolute path input or one leading out of it.
func TestReadJS(t *testing.T) {
	flake := t.TempDir()
	write := func(rel, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(flake, rel)), 0o755)
		os.WriteFile(filepath.Join(flake, rel), []byte(s), 0o644)
	}
	write("layer/modules/shell.nix", "pnpmPackages = [ \"tsx\" ];\nNPM_PACKAGES=( \"bosun\" )\n")
	write("elsewhere/modules/x.nix", "pnpmPackages = [ \"not-an-input\" ];\n")
	write("flake.lock", `{"nodes": {
  "layer": {"locked": {"path": "./layer", "type": "path"}, "original": {"path": "./layer", "type": "path"}, "parent": []},
  "abs": {"locked": {"path": "/somewhere/else", "type": "path"}, "original": {"path": "/somewhere/else", "type": "path"}},
  "up": {"locked": {"path": "../elsewhere", "type": "path"}, "original": {"path": "../elsewhere", "type": "path"}, "parent": []},
  "root": {"inputs": {"layer": "layer", "abs": "abs", "up": "up"}}}, "root": "root", "version": 7}`)
	js := ReadJS(flake)
	if !js.Pnpm["tsx"] || !js.Npm["bosun"] || js.Pnpm["not-an-input"] {
		t.Errorf("%+v", js)
	}
	// Only the layer has a list: nothing of the overlay's own to add to.
	if js.PnpmList {
		t.Error("PnpmList from a path input's list")
	}
	write("modules/js.nix", "  pnpmPackages = [\n    \"githits@0.6.6\"\n  ];\n")
	js = ReadJS(flake)
	if !js.PnpmList || !js.Pnpm["githits"] {
		t.Errorf("own list: %+v", js)
	}
}

func TestClassify(t *testing.T) {
	pkg := func(pname string) *Package { return &Package{Pname: pname, Version: "1.0"} }
	d := Declarations{
		Nix: []string{"jq", "foo", "git"}, Casks: []string{"firefox"}, Brews: []string{"mas"},
		Exclude: []string{"zoom"},
		Pkgs: map[string]Package{
			"jq": *pkg("jq"), "pyfoo": *pkg("foo"), "ripgrep": *pkg("ripgrep"),
			"prettier": *pkg("prettier"), "httpie": *pkg("httpie"),
		},
	}
	in := Installs{
		Brew:  {"mas", "jq", "example/tap/foo", "ripgrep", "python@3.12"},
		Cask:  {"firefox", "zoom", "slack"},
		Npm:   {"typescript", "bosun", "prettier"},
		Pnpm:  {"tsx", "left"},
		Cargo: {"my-tool"},
		Pip:   {"pyfoo", "httpie"},
	}
	js := JS{Pnpm: map[string]bool{"tsx": true, "typescript": true}, Npm: map[string]bool{"bosun": true}, PnpmList: true}
	got := Classify(in, d, js)
	want := []Find{
		{Tool: Brew, Name: "mas", Class: InHomebrew},
		{Tool: Brew, Name: "jq", Class: Duplicate, Pkg: pkg("jq")},
		{Tool: Brew, Name: "example/tap/foo", Class: Untracked, To: NotOffered, Why: "from a tap: add the tap to your flake first"},
		{Tool: Brew, Name: "ripgrep", Class: Untracked, To: ToNixpkgs, Pkg: pkg("ripgrep")},
		{Tool: Brew, Name: "python@3.12", Class: Untracked, To: ToBrews},
		{Tool: Cask, Name: "firefox", Class: InHomebrew},
		{Tool: Cask, Name: "zoom", Class: Untracked, To: NotOffered, Why: "excluded by this host"},
		{Tool: Cask, Name: "slack", Class: Untracked, To: ToCasks},
		{Tool: Npm, Name: "typescript", Class: Duplicate, ViaPnpm: true},
		{Tool: Npm, Name: "bosun", Class: DeclaredJS},
		{Tool: Npm, Name: "prettier", Class: Untracked, To: ToPnpm, Pkg: pkg("prettier")},
		{Tool: Pnpm, Name: "tsx", Class: DeclaredJS},
		{Tool: Pnpm, Name: "left", Class: Untracked, To: ToPnpm},
		{Tool: Cargo, Name: "my-tool", Class: Untracked, To: NotOffered, Why: "no nixpkgs package by that name"},
		{Tool: Pip, Name: "pyfoo", Class: Duplicate, Pkg: pkg("foo")},
		{Tool: Pip, Name: "httpie", Class: Untracked, To: ToNixpkgs, Pkg: pkg("httpie")},
	}
	if !reflect.DeepEqual(got, want) {
		for i := range max(len(got), len(want)) {
			var g, w Find
			if i < len(got) {
				g = got[i]
			}
			if i < len(want) {
				w = want[i]
			}
			if !reflect.DeepEqual(g, w) {
				t.Errorf("find %d: got %+v, want %+v", i, g, w)
			}
		}
	}
	// Without the overlay's own pnpmPackages list: nixpkgs if a package
	// there, else listed.
	js.PnpmList = false
	got = Classify(Installs{Npm: {"prettier"}, Pnpm: {"left"}}, d, js)
	if got[0].To != ToNixpkgs || got[1].To != NotOffered || got[1].Why != "no pnpmPackages list and no nixpkgs package by that name" {
		t.Errorf("no pnpm list: %+v", got)
	}
}

// The duplicates, plus what was just adopted into nixpkgs and npm globals
// adopted into pnpmPackages; one command per tool, names in find order. A
// cask or brew kept in Homebrew, a pnpm global into pnpmPackages, and an
// offered find left unticked stay.
func TestRemovals(t *testing.T) {
	ripgrep := Find{Tool: Brew, Name: "ripgrep", Class: Untracked, To: ToNixpkgs}
	py := Find{Tool: Brew, Name: "python@3.12", Class: Untracked, To: ToBrews}
	slack := Find{Tool: Cask, Name: "slack", Class: Untracked, To: ToCasks}
	prettier := Find{Tool: Npm, Name: "prettier", Class: Untracked, To: ToPnpm}
	tsx := Find{Tool: Pnpm, Name: "tsx", Class: Untracked, To: ToPnpm}
	rga := Find{Tool: Cargo, Name: "ripgrep-all", Class: Untracked, To: ToNixpkgs}
	httpie := Find{Tool: Pip, Name: "httpie", Class: Untracked, To: ToNixpkgs}
	finds := []Find{
		{Tool: Brew, Name: "jq", Class: Duplicate},
		{Tool: Brew, Name: "mas", Class: InHomebrew},
		ripgrep, py,
		{Tool: Brew, Name: "lazygit", Class: Untracked, To: ToNixpkgs}, // unticked
		{Tool: Cask, Name: "firefox", Class: Duplicate},
		slack, prettier,
		{Tool: Npm, Name: "typescript", Class: Duplicate},
		tsx, rga,
		{Tool: Pip, Name: "black", Class: Duplicate},
		httpie,
	}
	adopted := []Find{ripgrep, py, slack, prettier, tsx, rga, httpie}
	want := [][]string{
		{"brew", "uninstall", "jq", "ripgrep", "firefox"},
		{"npm", "rm", "-g", "prettier", "typescript"},
		{"cargo", "uninstall", "ripgrep-all"},
		{"pip", "uninstall", "--yes", "black", "httpie"},
	}
	if got := Removals(finds, adopted); !reflect.DeepEqual(got, want) {
		t.Errorf("got %q", got)
	}
	if n := Copies(want); n != 8 {
		t.Errorf("Copies = %d, want 8", n)
	}
	if got := Removals([]Find{tsx, slack}, []Find{tsx, slack}); got != nil {
		t.Errorf("nothing to remove: %q", got)
	}
}

// On a Mac, `brew services list --json` names the formulae
// registered as services: one started, or one whose service file is in
// place (a user set; a root daemon reads as "none" to the user). One never
// started isn't.
func TestScanBrewServices(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{
		"brew services": {Stdout: []byte(`[
  {"name": "tailscale", "status": "none", "user": "root", "file": "/Library/LaunchDaemons/homebrew.mxcl.tailscale.plist", "exit_code": null},
  {"name": "syncthing", "status": "started", "user": "jane", "file": "/Users/jane/Library/LaunchAgents/homebrew.mxcl.syncthing.plist", "exit_code": 0},
  {"name": "unbound", "status": "error", "user": "jane", "file": "/Users/jane/Library/LaunchAgents/homebrew.mxcl.unbound.plist", "exit_code": 1},
  {"name": "redis", "status": "none", "user": null, "file": "/opt/homebrew/opt/redis/homebrew.mxcl.redis.plist", "exit_code": null}
]`)},
	}}
	got := Scan(context.Background(), rec, t.TempDir(), true)
	if want := []string{"tailscale", "syncthing", "unbound"}; !reflect.DeepEqual(got[Service], want) {
		t.Errorf("services %q, want %q", got[Service], want)
	}
	var seen bool
	for _, c := range rec.Calls {
		seen = seen || execx.Quote(c) == "brew services list --json"
	}
	if !seen {
		t.Error("no brew services list --json")
	}
	if got := Scan(context.Background(), &execx.Recorder{}, t.TempDir(), false); got[Service] != nil {
		t.Errorf("Linux read brew services: %v", got[Service])
	}
}

// A formula that runs as a brew service stays in Homebrew:
// offered into Homebrew's brews though nixpkgs has it (Nix wouldn't run the
// service), never a duplicate even when Nix declares it, so never removed.
func TestClassifyBrewService(t *testing.T) {
	d := Declarations{Nix: []string{"redis"}, Pkgs: map[string]Package{
		"tailscale": {Pname: "tailscale", Version: "1.90.0"}, "redis": {Pname: "redis", Version: "8.2.1"},
	}}
	got := Classify(Installs{Brew: {"tailscale", "redis"}, Service: {"tailscale", "redis"}}, d, JS{})
	for _, f := range got {
		if f.Class != Untracked || f.To != ToBrews || f.Why != BrewServiceWhy {
			t.Errorf("%s: %+v; want untracked, to Homebrew brews, with the service reason", f.Name, f)
		}
	}
	if r := Removals(got, got); r != nil {
		t.Errorf("removes a brew service: %q", r)
	}
}

// A cask is an app, not a duplicate of the Nix package of
// its name (the ollama app beside nixpkgs' ollama): offered into Homebrew's
// casks, never removed.
func TestClassifyCaskNotDuplicate(t *testing.T) {
	p := Package{Pname: "ollama", Version: "0.12.6"}
	d := Declarations{Nix: []string{"ollama"}, Pkgs: map[string]Package{"ollama": p}}
	got := Classify(Installs{Cask: {"ollama"}}, d, JS{})
	if want := []Find{{Tool: Cask, Name: "ollama", Class: Untracked, To: ToCasks, Pkg: &p}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// The npm globals whose removal rests on pnpm, not Nix: one
// pnpmPackages names (and Nix doesn't), or one just adopted into it.
func TestPnpmOnly(t *testing.T) {
	d := Declarations{Nix: []string{"eslint"}}
	js := JS{Pnpm: map[string]bool{"typescript": true, "eslint": true}, PnpmList: true}
	finds := Classify(Installs{Npm: {"typescript", "eslint", "prettier", "left"}, Pnpm: {"tsx"}}, d, js)
	adopted := []Find{finds[2], finds[4]} // prettier, and pnpm's tsx
	if got := PnpmOnly(finds, adopted); !reflect.DeepEqual(got, []string{"typescript", "prettier"}) {
		t.Errorf("got %q", got)
	}
}
