package cli

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/platform"
)

func TestAgeKeygenRoundTrip(t *testing.T) {
	key := filepath.Join(t.TempDir(), "keys.txt")
	pub, err := ageKeygen(key)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub, "age1") || agePubkey(key) != pub {
		t.Fatalf("pubkey %q, read back %q", pub, agePubkey(key))
	}
	fi, _ := os.Stat(key)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	b, _ := os.ReadFile(key)
	if !strings.Contains(string(b), "# public key: "+pub+"\nAGE-SECRET-KEY-1") {
		t.Fatalf("not age-keygen's format:\n%s", b)
	}
	if _, err := ageKeygen(key); err == nil {
		t.Fatal("overwrote an existing key")
	}
	if agePubkey(filepath.Join(t.TempDir(), "none")) != "" {
		t.Fatal("pubkey of a missing file")
	}
}

func TestSopsFilesOrder(t *testing.T) {
	d := t.TempDir()
	for _, f := range []string{"a-c/x.sops.yaml", "a/b.sops.yaml", ".sops.yaml", "a/plain.yaml", "z.sops.yaml"} {
		os.MkdirAll(filepath.Join(d, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(d, f), nil, 0o644)
	}
	var got []string
	for _, f := range sopsFiles(d) {
		got = append(got, rel(d, f))
	}
	// pathlib orders by component: "a" < "a-c", though "a-c/" < "a/" as strings
	want := []string{"a/b.sops.yaml", "a-c/x.sops.yaml", "z.sops.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
}

func TestHMGenLine(t *testing.T) {
	m := hmGenLine.FindStringSubmatch("2024-01-15 10:30 : id 42 -> /nix/store/abc-hm")
	if m == nil || m[1] != "2024-01-15 10:30" || m[2] != "42" || m[3] != "/nix/store/abc-hm" {
		t.Fatalf("%q", m)
	}
	if hmGenLine.MatchString("x 2024-01-15 10:30 : id 42 -> /p") {
		t.Fatal("re.match is anchored")
	}
}

// The home-manager profile is quoted in the flake ref: a user or host with a
// dot (jane.doe, box.lan) is one attribute name, not a path.
func TestBuildCmdQuotesProfile(t *testing.T) {
	a := &App{Stdout: io.Discard, Stderr: io.Discard}
	argv, err := a.buildCmd(platform.Platform{FlakeDir: "/f"}, target{Kind: KindHome, Name: "jane.doe@box.lan", Host: "box.lan"}, false, platform.EngineOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if want := `/f#homeConfigurations."jane.doe@box.lan".activationPackage`; argv[2] != want {
		t.Fatalf("argv %v; want %s", argv, want)
	}
}

// --host is a hostname on both platforms. On Linux the user is the overlay's
// (its flake.nix's user), else $USER; and build, dry and fix build the
// checkout's engine the way switch does (--override-input).
func TestBuildCmdHostAndEngine(t *testing.T) {
	fakeNixConfigs(t, `[]`, `[]`)
	a := &App{Stdout: io.Discard, Stderr: io.Discard, Env: Env{Getenv: envOf(nil)}}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{ outputs = _: let\n  user = \"jane\";\nin { }; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := platform.EngineOverride{Path: "/ck", Reason: "test"}
	for _, tc := range []struct {
		name string
		p    platform.Platform
		host string
		dry  bool
		want []string
	}{
		{"overlay's user", platform.Platform{IsLinux: true, FlakeDir: dir, User: "root"}, "box", false,
			[]string{"nix", "build", dir + `#homeConfigurations."jane@box".activationPackage`, "--override-input", "piste", "path:/ck"}},
		{"no user in the flake: $USER", platform.Platform{IsLinux: true, FlakeDir: "/nowhere", User: "joe"}, "box", true,
			[]string{"nix", "build", `/nowhere#homeConfigurations."joe@box".activationPackage`, "--dry-run", "--override-input", "piste", "path:/ck"}},
	} {
		tg, err := a.resolveTarget(tc.p, tc.host)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got, err := a.buildCmd(tc.p, tg, tc.dry, o)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n  %q\nwant\n  %q", tc.name, got, tc.want)
		}
	}
}

// A NixOS host builds its system's toplevel, the way switch does: dry and
// --verbose as for a home, the checkout's engine last.
func TestBuildCmdNixOS(t *testing.T) {
	o := platform.EngineOverride{Path: "/ck", Reason: "test"}
	tg := target{Kind: KindNixOS, Name: "box"}
	const attr = `/f#nixosConfigurations."box".config.system.build.toplevel`
	for _, tc := range []struct {
		name         string
		dry, verbose bool
		want         []string
	}{
		{"build", false, false, []string{"nix", "build", attr, "--override-input", "piste", "path:/ck"}},
		{"dry", true, false, []string{"nix", "build", attr, "--dry-run", "--override-input", "piste", "path:/ck"}},
		{"dry, verbose", true, true, []string{"nix", "build", attr, "--dry-run", "--show-trace", "--override-input", "piste", "path:/ck"}},
	} {
		a := &App{Stdout: io.Discard, Stderr: io.Discard, Verbose: tc.verbose}
		got, err := a.buildCmd(platform.Platform{IsLinux: true, IsNixOS: true, FlakeDir: "/f"}, tg, tc.dry, o)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n  %q\nwant\n  %q", tc.name, got, tc.want)
		}
	}
}

// Before nix-darwin's first switch there is no darwin-rebuild: the checkout's
// engine runs it (packages.<darwin>.darwin-rebuild), by nix's absolute path so
// sudo finds it.
func TestDarwinRebuildArgv(t *testing.T) {
	if argv, ok := darwinRebuildArgv(true, "/nix/bin/nix", "git+file:///ck"); !ok || !reflect.DeepEqual(argv, []string{"darwin-rebuild"}) {
		t.Errorf("on PATH: %v, %v", argv, ok)
	}
	want := []string{"/nix/bin/nix", "run", "git+file:///ck#darwin-rebuild", "--"}
	if argv, ok := darwinRebuildArgv(false, "/nix/bin/nix", "git+file:///ck"); !ok || !reflect.DeepEqual(argv, want) {
		t.Errorf("fresh Mac in a checkout: %v, %v; want %v", argv, ok, want)
	}
	if _, ok := darwinRebuildArgv(false, "/nix/bin/nix", ""); ok {
		t.Error("fresh Mac outside a checkout: there is no engine to run it from")
	}
}

// Until the first switch installs piste, the way to run it is through the
// checkout's flake (setup is run with `nix run`).
func TestPisteCmd(t *testing.T) {
	if got := pisteCmd(true, false); got != "piste" {
		t.Errorf("on PATH: %q", got)
	}
	if got := pisteCmd(false, false); got != "nix run .#piste --" {
		t.Errorf("not on PATH: %q", got)
	}
}

// On a NixOS whose Nix has flakes off, setup turned them on for its own run
// only. A nix run line it prints turns them on too, for nix run and for the
// nix calls piste makes (a nix flag would reach the first only). Once piste
// is on PATH, plain piste.
func TestPisteCmdFlakesOff(t *testing.T) {
	const on = "NIX_CONFIG='extra-experimental-features = nix-command flakes' nix run .#piste --"
	if got := pisteCmd(false, true); got != on {
		t.Errorf("flakes off, not on PATH: %q, want %q", got, on)
	}
	if got := pisteCmd(true, true); got != "piste" {
		t.Errorf("flakes off, on PATH: %q", got)
	}
	// After setup moved into the checkout: the cd first, then the same form.
	t.Setenv("PATH", binDir(t, nil))
	a := &App{Env: Env{Getenv: envOf(map[string]string{"HOME": "/home/user"})}, movedTo: "/home/user/piste", flakesOff: true}
	if got, want := a.piste(), "cd ~/piste && "+on; got != want {
		t.Errorf("setup moved, flakes off: %q, want %q", got, want)
	}
	if got := a.pisteHere(); got != on {
		t.Errorf("a line with its own cd, flakes off: %q, want %q", got, on)
	}
	a.flakesOff = false
	if got := a.pisteHere(); got != "nix run .#piste --" {
		t.Errorf("a line with its own cd, flakes on: %q", got)
	}
	// A run started from such a line (setup's run over): its NIX_CONFIG
	// holds the flakes, and the lines it prints pass them on. Among other
	// settings too; not when NIX_CONFIG holds other settings only.
	for cfg, want := range map[string]string{
		"extra-experimental-features = nix-command flakes":                                           on,
		"substituters = https://cache.example.com\nextra-experimental-features = nix-command flakes": on,
		"substituters = https://cache.example.com":                                                   "nix run .#piste --",
	} {
		b := &App{Env: Env{Getenv: envOf(map[string]string{"HOME": "/home/user", "NIX_CONFIG": cfg})}}
		if got := b.pisteHere(); got != want {
			t.Errorf("NIX_CONFIG %q: %q, want %q", cfg, got, want)
		}
	}
}

// gitHere is how a printed line runs git: plain where git is on PATH and
// the flakes come from Nix itself; through nix shell where the person's
// shell may have none: a fresh NixOS's (its flakes in NIX_CONFIG: setup's
// git was its own nix shell's), or no git on PATH at all.
func TestGitHere(t *testing.T) {
	const flagged = "nix --extra-experimental-features 'nix-command flakes' shell nixpkgs#git --command git"
	withGit := binDir(t, map[string]string{"git": ""})
	for _, c := range []struct {
		name, path, nixConfig string
		flakesOff             bool
		want                  string
	}{
		{"git, flakes on", withGit, "", false, "git"},
		{"git, setup turned flakes on", withGit, "", true, flagged},
		{"git, flakes in NIX_CONFIG", withGit, "extra-experimental-features = nix-command flakes", false, flagged},
		{"no git", binDir(t, nil), "", false, "nix shell nixpkgs#git --command git"},
		{"no git, flakes in NIX_CONFIG", binDir(t, nil), "extra-experimental-features = nix-command flakes", false, flagged},
	} {
		t.Setenv("PATH", c.path)
		a := &App{Env: Env{Getenv: envOf(map[string]string{"NIX_CONFIG": c.nixConfig})}, flakesOff: c.flakesOff}
		if got := a.gitHere(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The gh login line after a failed GitHub clone runs gh, which makes no nix
// calls: nix's own flag turns flakes on for it, as in setup's git line.
func TestGithubCloneHintFlakesOff(t *testing.T) {
	for _, off := range []bool{false, true} {
		want := "\n  nix run nixpkgs#gh -- auth login --web --git-protocol https\n"
		if off {
			want = "\n  nix --extra-experimental-features 'nix-command flakes' run nixpkgs#gh -- auth login --web --git-protocol https\n"
		}
		var out strings.Builder
		a := &App{Stdout: &out, Stderr: &out, flakesOff: off}
		a.githubCloneHint("git@github.com:jane/overlay.git")
		if !strings.Contains(out.String(), want) {
			t.Errorf("flakes off %v: %q, want a line %q", off, out.String(), want)
		}
	}
}

// ensureAgeKey makes this machine's key once and never replaces it; a key
// file it can't read gives no public key.
func TestEnsureAgeKey(t *testing.T) {
	a := &App{Stdout: io.Discard, Stderr: io.Discard}
	home := t.TempDir()
	pub, err := a.ensureAgeKey(home)
	if err != nil || !strings.HasPrefix(pub, "age1") {
		t.Fatalf("first: %q, %v", pub, err)
	}
	before, _ := os.ReadFile(ageKeyPath(home))
	again, err := a.ensureAgeKey(home)
	after, _ := os.ReadFile(ageKeyPath(home))
	if err != nil || again != pub || string(after) != string(before) {
		t.Fatalf("again: %q, %v; the key file kept: %v", again, err, string(after) == string(before))
	}
	if err := os.WriteFile(ageKeyPath(home), []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pub, err := a.ensureAgeKey(home); pub != "" || err != nil {
		t.Fatalf("garbage: %q, %v; want \"\", nil", pub, err)
	}
}
