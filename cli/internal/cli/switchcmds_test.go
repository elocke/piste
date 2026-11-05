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
	argv, err := a.buildCmd(platform.Platform{FlakeDir: "/f", User: "jane.doe"}, "box.lan", false, platform.EngineOverride{})
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
	a := &App{Stdout: io.Discard, Stderr: io.Discard}
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
		{"overlay's user", platform.Platform{FlakeDir: dir, User: "root"}, "box", false,
			[]string{"nix", "build", dir + `#homeConfigurations."jane@box".activationPackage`, "--override-input", "piste", "path:/ck"}},
		{"no user in the flake: $USER", platform.Platform{FlakeDir: "/nowhere", User: "joe"}, "box", true,
			[]string{"nix", "build", `/nowhere#homeConfigurations."joe@box".activationPackage`, "--dry-run", "--override-input", "piste", "path:/ck"}},
	} {
		got, err := a.buildCmd(tc.p, tc.host, tc.dry, o)
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
	if got := pisteCmd(true); got != "piste" {
		t.Errorf("on PATH: %q", got)
	}
	if got := pisteCmd(false); got != "nix run .#piste --" {
		t.Errorf("not on PATH: %q", got)
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
