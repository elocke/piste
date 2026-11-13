package machine

import (
	"bytes"
	"errors"
	"flag"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files instead of comparing with them")

// cases lists the fixture cases: every dir under testdata that holds an
// etc-nixos tree. A case has the tree (etc-nixos/), the carried files it
// should produce (want/) and the notes (want-notes.txt).
func cases(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no fixture cases under testdata")
	}
	return names
}

func carryCase(t *testing.T, name string) (map[string][]byte, []string) {
	t.Helper()
	files, notes, err := Carry(filepath.Join("testdata", name, "etc-nixos"))
	if err != nil {
		t.Fatalf("Carry(%s): %v", name, err)
	}
	return files, notes
}

func notesText(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, "\n") + "\n"
}

func TestCarry(t *testing.T) {
	for _, name := range cases(t) {
		t.Run(name, func(t *testing.T) {
			files, notes := carryCase(t, name)
			wantDir := filepath.Join("testdata", name, "want")
			notesFile := filepath.Join("testdata", name, "want-notes.txt")

			if *update {
				if err := os.RemoveAll(wantDir); err != nil {
					t.Fatal(err)
				}
				for rel, b := range files {
					p := filepath.Join(wantDir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(notesFile, []byte(notesText(notes)), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want := map[string][]byte{}
			err := filepath.WalkDir(wantDir, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(wantDir, p)
				want[filepath.ToSlash(rel)] = b
				return err
			})
			if err != nil {
				t.Fatalf("reading goldens (run with -update to write them): %v", err)
			}
			if got, w := keys(files), keys(want); !reflect.DeepEqual(got, w) {
				t.Errorf("carried files = %v, golden has %v", got, w)
			}
			for _, rel := range keys(files) {
				if w, ok := want[rel]; ok && !bytes.Equal(files[rel], w) {
					t.Errorf("%s differs from its golden at %s", rel, firstDiff(string(files[rel]), string(w)))
				}
			}
			wantNotes, err := os.ReadFile(notesFile)
			if err != nil {
				t.Fatalf("reading %s (run with -update to write it): %v", notesFile, err)
			}
			if got := notesText(notes); got != string(wantNotes) {
				t.Errorf("notes differ at %s\ngot:\n%s\nwant:\n%s", firstDiff(got, string(wantNotes)), got, wantNotes)
			}
		})
	}
}

func keys(m map[string][]byte) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// firstDiff describes where a and b first differ, by line.
func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y || i >= len(al) || i >= len(bl) {
			return "line " + strconv.Itoa(i+1) + ":\n  got:  " + x + "\n  want: " + y
		}
	}
	return "nowhere"
}

// unboundModulesPath lists the carried files that still use modulesPath
// without taking it as an argument: their top level is a function of one
// plain argument, so Carry marks them for the person to fix. Nix refuses these at
// parse time ("undefined variable"), which is the point of the marker.
var unboundModulesPath = map[string]bool{
	"shapes/lambda.nix": true,
}

// TestCarriedFilesParse runs nix-instantiate --parse over every carried
// file: a rewrite that breaks the syntax, or inserts modulesPath where it
// doesn't bind, fails here (nix parses with variables resolved).
func TestCarriedFilesParse(t *testing.T) {
	nixParse, err := exec.LookPath("nix-instantiate")
	if err != nil {
		t.Skip("nix-instantiate is not on PATH")
	}
	probe := filepath.Join(t.TempDir(), "probe.nix")
	if err := os.WriteFile(probe, []byte("{ }: { }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(nixParse, "--parse", probe).CombinedOutput(); err != nil {
		t.Skipf("nix-instantiate --parse does not work here: %v\n%s", err, out)
	}

	for _, name := range cases(t) {
		files, _ := carryCase(t, name)
		dir := t.TempDir()
		for _, rel := range keys(files) {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, files[rel], 0o644); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(nixParse, "--parse", p).CombinedOutput()
			id := name + "/" + rel
			switch {
			case unboundModulesPath[id]:
				if err == nil || !strings.Contains(string(out), "undefined variable 'modulesPath'") {
					t.Errorf("%s: expected nix to refuse the unbound modulesPath, got err=%v\n%s", id, err, out)
				}
				if !strings.Contains(string(files[rel]), "# piste:") {
					t.Errorf("%s: unbound modulesPath but no marker", id)
				}
			case err != nil:
				t.Errorf("%s does not parse: %v\n%s", id, err, out)
			}
		}
	}
}

// A file whose arguments are { pkgs } (no ...) and that uses a
// <nixpkgs/nixos/modules/...> lookup gains modulesPath as a required
// argument, which the module system supplies.
func TestNoEllipsisGainsModulesPath(t *testing.T) {
	files, notes := carryCase(t, "image-noellipsis")
	got := string(files["configuration.nix"])
	if !strings.HasPrefix(got, Header+"{ modulesPath, pkgs }:\n") {
		t.Errorf("arguments not extended:\n%s", got)
	}
	if !strings.Contains(got, `(modulesPath + "/virtualisation/lxc-container.nix")`) {
		t.Errorf("lookup not rewritten:\n%s", got)
	}
	// Only its networking.hostName (TestCarryLeavesTheHostNameToFlake).
	if want := []string{"configuration.nix:9: " + hostNameReason}; !reflect.DeepEqual(notes, want) {
		t.Errorf("notes = %v, want %v", notes, want)
	}
}

func TestHeaderOnceEvenWithLeadingComments(t *testing.T) {
	for _, name := range cases(t) {
		files, _ := carryCase(t, name)
		for rel, b := range files {
			if !strings.HasPrefix(string(b), Header) || strings.Count(string(b), Header) != 1 {
				t.Errorf("%s/%s: want Header exactly once, at the start", name, rel)
			}
		}
	}
}

func TestCarryDeterministic(t *testing.T) {
	for _, name := range cases(t) {
		f1, n1 := carryCase(t, name)
		f2, n2 := carryCase(t, name)
		if !reflect.DeepEqual(f1, f2) || !reflect.DeepEqual(n1, n2) {
			t.Errorf("%s: two runs differ", name)
		}
	}
}

func TestCarryNoConfiguration(t *testing.T) {
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "other.nix"), []byte("{ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"empty":     t.TempDir(),
		"only-else": other,
		"missing":   filepath.Join(t.TempDir(), "absent"),
	} {
		t.Run(name, func(t *testing.T) {
			files, notes, err := Carry(dir)
			if files != nil || notes != nil {
				t.Errorf("files = %v, notes = %v; want nil", files, notes)
			}
			if err == nil || err.Error() != "no configuration.nix in "+dir {
				t.Errorf("err = %v, want %q", err, "no configuration.nix in "+dir)
			}
			if !errors.Is(err, ErrNoConfiguration) {
				t.Errorf("err does not wrap ErrNoConfiguration: %v", err)
			}
		})
	}
}

// Only regular .nix files come along, and nothing outside etcDir is
// followed: a symlinked file or directory is not read, and a carried file
// that points at one (or at any non-.nix file) is noted.
func TestCarryNeverFollowsSymlinks(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.nix"), []byte("{ secret = true; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "inner.nix"), []byte("{ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	real := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(real, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("configuration.nix", "{ ... }:\n{\n  imports = [ ./link.nix ./linkdir ./notes.txt ];\n}\n")
	write("notes.txt", "not nix\n")
	if err := os.Symlink(filepath.Join(outside, "secret.nix"), filepath.Join(real, "link.nix")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(real, "linkdir")); err != nil {
		t.Fatal(err)
	}

	// etcDir itself may be a symlink (/etc/nixos often is).
	etc := filepath.Join(t.TempDir(), "etc-nixos")
	if err := os.Symlink(real, etc); err != nil {
		t.Fatal(err)
	}
	files, notes, err := Carry(etc)
	if err != nil {
		t.Fatal(err)
	}
	if got := keys(files); !reflect.DeepEqual(got, []string{"configuration.nix"}) {
		t.Errorf("carried %v, want only configuration.nix", got)
	}
	want := []string{
		"configuration.nix:8: ./link.nix isn't carried over (only .nix files are)",
		"configuration.nix:8: ./linkdir isn't carried over (only .nix files are)",
		"configuration.nix:8: ./notes.txt isn't carried over (only .nix files are)",
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}
}

// A name that runs on (nix.channels) is not a setting to mark, two settings
// on one line get one marker, and a read (config.nix.nixPath) is not a
// setting at all.
func TestChannelSettingBoundaries(t *testing.T) {
	dir := t.TempDir()
	body := "{ config, ... }:\n{\n  nix.nixPath = [ ]; nix.channel.enable = false;\n  nix.channels = [ ];\n" +
		"  x = config.nix.nixPath;\n  system.autoUpgrade = { enable = true; };\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "configuration.nix"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, notes, err := Carry(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"configuration.nix:6: the flake build doesn't use channels",
		"configuration.nix:10: " + autoUpgradeReason,
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}
}

// "$${" is text in a Nix string, not an interpolation: a lookup written in
// the string is the string's value and stays as it is, and the real import
// after it is still found and rewritten.
func TestCarryDollarDollarBraceIsText(t *testing.T) {
	files, notes := carryCase(t, "shapes")
	got := string(files["dollars.nix"])
	for _, kept := range []string{
		"a $${b\n    see <nixpkgs/nixos/modules/profiles/minimal.nix>\n",
		`"a $${b <nixpkgs/nixos/modules/profiles/minimal.nix>"`,
	} {
		if !strings.Contains(got, kept) {
			t.Errorf("string text changed, lost %q:\n%s", kept, got)
		}
	}
	if !strings.Contains(got, `imports = [ (modulesPath + "/profiles/base.nix") ];`) {
		t.Errorf("the real import was not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "{ modulesPath, ... }:\n{") {
		t.Errorf("modulesPath was not added:\n%s", got)
	}
	for _, n := range notes {
		if strings.HasPrefix(n, "dollars.nix:") {
			t.Errorf("unexpected note %q", n)
		}
	}
}

// A let...in or with...; in front of an attribute set takes the same
// function in front as a bare attribute set does, so the file keeps working.
func TestCarryPrologueBeforeAttrsGetsAFunction(t *testing.T) {
	files, _ := carryCase(t, "shapes")
	for _, name := range []string{"letin.nix", "with-attrs.nix", "letrec.nix"} {
		got := string(files[name])
		if !strings.Contains(got, "{ modulesPath, ... }:\n") || strings.Contains(got, "# piste: modulesPath") {
			t.Errorf("%s:\n%s", name, got)
		}
	}
}

// A directory the user can't list (a root-only secrets/) is left out with a
// note and the rest is carried; a .nix file the user can't read is an error.
func TestCarryUnreadableEntries(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permissions don't apply")
	}
	dir := t.TempDir()
	write := func(rel, body string) string {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("configuration.nix", "{ ... }:\n{\n  imports = [ <home-manager/nixos> ];\n}\n")
	write("modules/net.nix", "{ ... }: { }\n")
	write("secrets/key.nix", "{ ... }: { }\n")
	write("zzz.nix", "{ ... }: { }\n")
	secrets := filepath.Join(dir, "secrets")
	if err := os.Chmod(secrets, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(secrets, 0o755) })
	if _, err := os.ReadDir(secrets); err == nil {
		t.Skip("a mode 000 directory is still readable here")
	}

	files, notes, err := Carry(dir)
	if err != nil {
		t.Fatalf("an unreadable directory must not abort the carry: %v", err)
	}
	if got, want := keys(files), []string{"configuration.nix", "modules/net.nix", "zzz.nix"}; !reflect.DeepEqual(got, want) {
		t.Errorf("carried %v, want %v", got, want)
	}
	if len(notes) != 2 ||
		!strings.HasPrefix(notes[0], "configuration.nix:6: <home-manager/nixos>") ||
		!strings.HasPrefix(notes[1], "secrets: can't read it as ") ||
		!strings.HasSuffix(notes[1], "; not carried") {
		t.Errorf("notes:\n%s", strings.Join(notes, "\n"))
	}

	// Now a .nix file nobody can read.
	if err := os.Chmod(secrets, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "zzz.nix")
	if err := os.Chmod(bad, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(bad); err == nil {
		t.Skip("a mode 000 file is still readable here")
	}
	files, notes, err = Carry(dir)
	if err == nil || files != nil || notes != nil {
		t.Errorf("an unreadable .nix file must fail: files=%v notes=%v err=%v", keys(files), notes, err)
	}
}

// A file that ends right after a let...in or with...; is not Nix, but Carry
// reads every .nix file, so it must come through with a marker, not a panic.
func TestCarryFileEndingAfterPrologue(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"configuration.nix": "{ ... }: { }\n",
		"b.nix":             "let a = <nixpkgs/nixos/modules/x.nix>; in\n",
		"c.nix":             "with <nixpkgs/nixos/modules/x.nix>;",
		"d.nix":             "# only a comment\n",
		"e.nix":             "",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, notes, err := Carry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 5 {
		t.Errorf("carried %v", keys(files))
	}
	want := []string{
		"b.nix:4: modulesPath is used here but is not an argument of this file; add it to the file's arguments",
		"c.nix:4: modulesPath is used here but is not an argument of this file; add it to the file's arguments",
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}
	for _, name := range []string{"b.nix", "c.nix"} {
		if got := string(files[name]); !strings.Contains(got, "# piste: modulesPath is used here") {
			t.Errorf("%s has no marker:\n%s", name, got)
		}
	}
}

// No prefix of any fixture file makes rewrite panic: truncated, half-typed
// Nix is still read.
func TestRewriteSurvivesTruncatedFiles(t *testing.T) {
	c := &carrier{root: t.TempDir(), carried: map[string]bool{"configuration.nix": true}}
	for _, name := range cases(t) {
		root := filepath.Join("testdata", name, "etc-nixos")
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || filepath.Ext(p) != ".nix" {
				return err
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for n := 0; n <= len(src); n++ {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%s cut at %d bytes: %v", p, n, r)
						}
					}()
					c.rewrite("configuration.nix", src[:n])
				}()
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// The host's name is its key in flake.nix (mkFlake sets networking.hostName
// as a default, which a carried plain setting would beat): a statement on
// lines of its own is commented out under a marker, one sharing a line with
// other code is only marked; both are noted. Reads of the option, and a
// hostName that isn't networking's, are left alone.
func TestCarryLeavesTheHostNameToFlake(t *testing.T) {
	files, notes := carryCase(t, "hostname")
	marker := "# piste: " + hostNameReason
	for _, c := range []struct {
		file string
		want []string // in order, each a whole line
	}{
		{"configuration.nix", []string{
			"  " + marker,
			`  # networking.hostName = "example"; # from the installer`,
			"  networking.useDHCP = true;",
			`  environment.etc."hostname-copy".text = config.networking.hostName;`,
		}},
		{"nested.nix", []string{
			"  networking = {",
			"    " + marker,
			`    # hostName = "example";`,
			"    firewall.enable = true;",
			`    hostName = "example";`, // avahi's
		}},
		{"multiline.nix", []string{
			"  " + marker,
			"  # networking.hostName =",
			"    # let",
			`      # n = "example";`,
			"    # in",
			"    # lib.mkDefault n;",
			`  networking.domain = "example.com";`,
		}},
		{"shared.nix", []string{
			"  " + marker,
			`  networking.hostName = "example"; networking.useDHCP = true;`,
		}},
		{"shared-nested.nix", []string{
			"  " + marker,
			`  networking = { hostName = "example"; };`,
		}},
	} {
		lines := strings.Split(string(files[c.file]), "\n")
		at := 0
		for _, w := range c.want {
			for at < len(lines) && lines[at] != w {
				at++
			}
			if at == len(lines) {
				t.Errorf("%s: no line %q (in order) in:\n%s", c.file, w, files[c.file])
				break
			}
		}
	}
	if n := strings.Count(string(files["nested.nix"]), marker); n != 1 {
		t.Errorf("nested.nix: %d markers, want 1 (avahi's hostName isn't networking's)", n)
	}
	if strings.Contains(string(files["configuration.nix"]), "piste: "+hostNameReason+"\n  environment") {
		t.Error("configuration.nix: the read of config.networking.hostName is marked")
	}
	want := []string{
		"configuration.nix:15: " + hostNameReason,
		"multiline.nix:6: " + hostNameReason,
		"nested.nix:6: " + hostNameReason,
		"shared-nested.nix:5: " + hostNameReason,
		"shared.nix:5: " + hostNameReason,
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}
}

// A carried system.autoUpgrade would rebuild the machine from /etc/nixos
// (with channels) every night, putting back the system piste switched away
// from: each binding on lines of its own (dotted, a whole
// system.autoUpgrade = { ... }; block, or inside a system = { ... } block)
// is commented out under its marker; one sharing a line is only marked;
// each is noted. A read of the option is left alone.
func TestCarryTurnsAutoUpgradeOff(t *testing.T) {
	dir := t.TempDir()
	body := `{ config, lib, ... }:
{
  system.autoUpgrade.enable = true;
  system.autoUpgrade.allowReboot = true; # nightly
  system.autoUpgrade = {
    channel = "https://nixos.org/channels/nixos-24.05";
    dates = "04:00";
  };
  system = {
    autoUpgrade.enable = true;
    stateVersion = "24.05";
  };
  system.autoUpgrade.flags = [ "--update-input" "nixpkgs" ]; networking.useDHCP = true;
  x = config.system.autoUpgrade.enable;
}
`
	if err := os.WriteFile(filepath.Join(dir, "configuration.nix"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	files, notes, err := Carry(dir)
	if err != nil {
		t.Fatal(err)
	}
	marker := "# piste: " + autoUpgradeReason
	got := string(files["configuration.nix"])
	want := Header + `{ config, lib, ... }:
{
  ` + marker + `
  # system.autoUpgrade.enable = true;
  ` + marker + `
  # system.autoUpgrade.allowReboot = true; # nightly
  ` + marker + `
  # system.autoUpgrade = {
    # channel = "https://nixos.org/channels/nixos-24.05";
    # dates = "04:00";
  # };
  system = {
    ` + marker + `
    # autoUpgrade.enable = true;
    stateVersion = "24.05";
  };
  ` + marker + `
  system.autoUpgrade.flags = [ "--update-input" "nixpkgs" ]; networking.useDHCP = true;
  x = config.system.autoUpgrade.enable;
}
`
	if got != want {
		t.Errorf("carried file differs at %s\ngot:\n%s", firstDiff(got, want), got)
	}
	wantNotes := []string{
		"configuration.nix:6: " + autoUpgradeReason,
		"configuration.nix:8: " + autoUpgradeReason,
		"configuration.nix:10: " + autoUpgradeReason,
		"configuration.nix:16: " + autoUpgradeReason,
		"configuration.nix:20: " + autoUpgradeReason,
	}
	if !reflect.DeepEqual(notes, wantNotes) {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(notes, "\n"), strings.Join(wantNotes, "\n"))
	}
	if autoUpgradeReason != "system.autoUpgrade would rebuild from /etc/nixos, not this overlay" {
		t.Errorf("reason %q", autoUpgradeReason)
	}
}

// networking.hostName is taken out only at a module's top level: directly in
// its attribute set, its config = { … } or config.…, or its
// networking = { … }. A container's, a specialisation's or any other nested
// set's is its own, and stays live and unmarked. At the top level, a form
// carry can't comment out without breaking what is around it (an inherit,
// a lib.mkMerge or lib.mkIf, a rec or with block) stays live under a marker
// that says it may override the host's name.
func TestCarryHostNameAtTopLevelOnly(t *testing.T) {
	files, notes := carryCase(t, "toplevel")
	marker := "# piste: " + hostNameReason
	live := "# piste: " + hostNameLiveReason
	for _, c := range []struct {
		file string
		want []string // in order, each a whole line
	}{
		{"configuration.nix", []string{
			"  " + marker,
			`  # networking.hostName = "example";`,
			`  containers.web.config.networking.hostName = "web";`,
			`      networking.hostName = "db";`,
			`      hostName = "cache";`,
			`    networking.hostName = "other";`,
		}},
		{"config-block.nix", []string{"    " + marker, `    # networking.hostName = "example";`, "    networking.useDHCP = true;"}},
		{"config-dotted.nix", []string{"  " + marker, `  # config.networking.hostName = "example";`}},
		{"config-mkif.nix", []string{"    " + live, `    networking.hostName = "example";`}},
		{"inherit.nix", []string{"    " + live, "    inherit hostName;"}},
		{"merge.nix", []string{"    " + live, `    { hostName = "example"; }`}},
		{"rec.nix", []string{"    " + live, `    hostName = "example";`, `    extraHosts = "127.0.0.1 ${hostName}";`}},
		{"with.nix", []string{"    " + live, `    hostName = mkDefault "example";`}},
	} {
		lines := strings.Split(string(files[c.file]), "\n")
		at := 0
		for _, w := range c.want {
			for at < len(lines) && lines[at] != w {
				at++
			}
			if at == len(lines) {
				t.Errorf("%s: no line %q (in order) in:\n%s", c.file, w, files[c.file])
				break
			}
		}
	}
	if n := strings.Count(string(files["configuration.nix"]), "# piste:"); n != 1 {
		t.Errorf("configuration.nix: %d markers, want 1 (the containers' and the specialisation's names are their own):\n%s", n, files["configuration.nix"])
	}
	want := []string{
		"config-block.nix:9: " + hostNameReason,
		"config-dotted.nix:5: " + hostNameReason,
		"config-mkif.nix:8: " + hostNameLiveReason,
		"configuration.nix:19: " + hostNameReason,
		"inherit.nix:9: " + hostNameLiveReason,
		"merge.nix:7: " + hostNameLiveReason,
		"nix-blocks.nix:6: " + channelReason,
		"nix-blocks.nix:8: " + channelReason,
		"rec.nix:6: " + hostNameLiveReason,
		"with.nix:7: " + hostNameLiveReason,
	}
	if !reflect.DeepEqual(notes, want) {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}
	if hostNameLiveReason != "networking.hostName here may override the host's name; remove it if this machine's name should be its key in flake.nix" {
		t.Errorf("reason %q", hostNameLiveReason)
	}
}

// nixPath and channel.enable are channel settings only in the module's
// top-level nix = { … } (or dotted from nix.): the same names in another
// set, or in a container's nix, are left alone.
func TestCarryChannelSettingsInTheTopLevelNixBlock(t *testing.T) {
	files, _ := carryCase(t, "toplevel")
	got := string(files["nix-blocks.nix"])
	if n := strings.Count(got, "# piste: "+channelReason); n != 2 {
		t.Errorf("nix-blocks.nix: %d markers, want 2 (the top-level nix block's):\n%s", n, got)
	}
	for _, kept := range []string{
		"  services.example = {\n    nixPath = [ \"unrelated\" ];\n    channel.enable = true;\n  };\n",
		"    nix = {\n      nixPath = [",
	} {
		if !strings.Contains(got, kept) {
			t.Errorf("nix-blocks.nix: marked %q:\n%s", kept, got)
		}
	}
}
