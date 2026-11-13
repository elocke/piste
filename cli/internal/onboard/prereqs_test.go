package onboard

import (
	"errors"
	"strings"
	"testing"
)

// fake is a machine for Prereqs: tools on PATH, command outputs (a missing
// key fails), files.
type fake struct {
	path  map[string]bool
	out   map[string]string
	files map[string]bool
}

func good() fake {
	return fake{
		path: map[string]bool{"nix": true, "git": true, "determinate-nixd": true},
		out: map[string]string{
			"nix --version":                         "nix (Determinate Nix 3.6.0) 2.29.0",
			"nix config show experimental-features": "flakes nix-command",
			"git --version":                         "git version 2.51.0",
			"xcode-select -p":                       "/Library/Developer/CommandLineTools",
			"git config --global user.name":         "Jane Doe",
			"git config --global user.email":        "jane@example.com",
		},
		files: map[string]bool{},
	}
}

func (f fake) probe(darwin bool) Probe {
	return Probe{
		LookPath: func(n string) (string, error) {
			if f.path[n] {
				return "/bin/" + n, nil
			}
			return "", errors.New("not found")
		},
		Run: func(argv ...string) (string, error) {
			if o, ok := f.out[strings.Join(argv, " ")]; ok {
				return o, nil
			}
			return "", errors.New("exit status 1")
		},
		Exists: func(p string) bool { return f.files[p] },
		Darwin: darwin,
	}
}

// nixos is the probe of a NixOS machine.
func (f fake) nixos() Probe {
	p := f.probe(false)
	p.NixOS = true
	return p
}

func row(t *testing.T, cs []Check, name string) Check {
	t.Helper()
	for _, c := range cs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q row in %+v", name, cs)
	return Check{}
}

func TestPrereqsAllGood(t *testing.T) {
	for _, darwin := range []bool{false, true} {
		cs := Prereqs(good().probe(darwin))
		for _, c := range cs {
			if !c.OK {
				t.Errorf("darwin=%v: %s not OK (%s)", darwin, c.Name, c.Fix)
			}
		}
		if Blocked(cs) {
			t.Errorf("darwin=%v: blocked", darwin)
		}
		mac := 0
		for _, c := range cs {
			if c.Name == "Xcode command-line tools" || c.Name == "Determinate Nix" {
				mac++
			}
		}
		if want := map[bool]int{false: 0, true: 2}[darwin]; mac != want {
			t.Errorf("darwin=%v: %d macOS rows, want %d", darwin, mac, want)
		}
	}
}

func TestPrereqsNoNix(t *testing.T) {
	f := good()
	delete(f.path, "nix")
	cs := Prereqs(f.probe(false))
	c := row(t, cs, "Nix")
	if c.OK || !c.Fatal || !strings.Contains(c.Fix, "install.determinate.systems") {
		t.Fatalf("Nix row %+v", c)
	}
	if !Blocked(cs) {
		t.Fatal("not blocked without Nix")
	}
}

// Determinate Nix has flakes built in: experimental-features lists nothing
// (seen on Determinate Nix 3.15).
func TestPrereqsDeterminateFlakes(t *testing.T) {
	f := good()
	f.out["nix config show experimental-features"] = ""
	if c := row(t, Prereqs(f.probe(false)), "flakes"); !c.OK {
		t.Fatalf("flakes on Determinate Nix: %+v", c)
	}
}

func TestPrereqsFlakesOff(t *testing.T) {
	f := good()
	f.out["nix --version"] = "nix (Nix) 2.31.0"
	f.out["nix config show experimental-features"] = "nix-command"
	cs := Prereqs(f.probe(false))
	c := row(t, cs, "flakes")
	if c.OK || !c.Fatal || !strings.Contains(c.Fix, "experimental-features = nix-command flakes") {
		t.Fatalf("flakes row %+v", c)
	}
	if !Blocked(cs) {
		t.Fatal("not blocked with flakes off")
	}
}

// A fresh NixOS install has flakes off, and that doesn't stop setup: the
// row passes with a note, since setup turns them on for itself and the first
// switch for good. Elsewhere the same state still blocks.
func TestPrereqsNixOSFlakesOff(t *testing.T) {
	f := good()
	f.out["nix --version"] = "nix (Nix) 2.31.0"
	f.out["nix config show experimental-features"] = "nix-command"
	cs := Prereqs(f.nixos())
	c := row(t, cs, "flakes")
	if !c.OK || !c.Info || c.Fatal {
		t.Fatalf("flakes row on NixOS = %+v, want OK and a note", c)
	}
	const want = "Flakes are off in this NixOS's Nix; setup turns them on for itself, and your first switch turns them on for good"
	if c.Fix != want {
		t.Errorf("flakes note = %q, want %q", c.Fix, want)
	}
	if Blocked(cs) {
		t.Error("blocked on NixOS with flakes off")
	}
	// Not NixOS: still fatal.
	if c := row(t, Prereqs(f.probe(false)), "flakes"); c.OK || !c.Fatal {
		t.Errorf("flakes row off NixOS = %+v, want failed and fatal", c)
	}
	// A NixOS whose fresh Nix can't even `nix config show` (nix-command is
	// off too): no output at all, the same note.
	delete(f.out, "nix config show experimental-features")
	if c := row(t, Prereqs(f.nixos()), "flakes"); !c.OK || !c.Info {
		t.Errorf("flakes row on NixOS without nix-command = %+v", c)
	}
	// With flakes on, a plain pass: no note.
	g := good()
	if c := row(t, Prereqs(g.nixos()), "flakes"); !c.OK || c.Info {
		t.Errorf("flakes on NixOS with them on = %+v, want a plain pass", c)
	}
	// No Nix at all on NixOS: still fatal, since nothing could turn them on.
	delete(g.path, "nix")
	if c := row(t, Prereqs(g.nixos()), "flakes"); c.OK || !c.Fatal {
		t.Errorf("flakes row on NixOS without nix = %+v", c)
	}
}

// A fresh NixOS install has no git. It stays fatal, and the fix is the one
// command that gets git (and flakes) for the run that starts setup over.
func TestPrereqsNixOSNoGit(t *testing.T) {
	f := good()
	delete(f.out, "git --version")
	cs := Prereqs(f.nixos())
	c := row(t, cs, "git")
	if c.OK || !c.Fatal {
		t.Fatalf("git row on NixOS = %+v, want failed and fatal", c)
	}
	const want = "nix --extra-experimental-features 'nix-command flakes' shell nixpkgs#git --command nix --extra-experimental-features 'nix-command flakes' run github:elocke/piste/stable -- setup <url>"
	if c.Fix != want {
		t.Errorf("git fix = %q, want %q", c.Fix, want)
	}
	if !Blocked(cs) {
		t.Error("not blocked without git on NixOS")
	}
	// Elsewhere the fix is the package manager's.
	if c := row(t, Prereqs(f.probe(false)), "git"); strings.Contains(c.Fix, "nix shell") {
		t.Errorf("git fix off NixOS = %q", c.Fix)
	}
	// And no Mac rows on NixOS.
	for _, c := range cs {
		if c.Name == "Xcode command-line tools" || c.Name == "Determinate Nix" {
			t.Errorf("Mac row on NixOS: %+v", c)
		}
	}
}

func TestPrereqsMac(t *testing.T) {
	f := good()
	delete(f.out, "xcode-select -p")
	delete(f.path, "determinate-nixd")
	cs := Prereqs(f.probe(true))
	if c := row(t, cs, "Xcode command-line tools"); c.OK || !c.Fatal || !strings.Contains(c.Fix, "xcode-select --install") {
		t.Errorf("Xcode row %+v", c)
	}
	// Without Determinate Nix: a note, never a fix, and never nix.enable
	// (with it on, nix-darwin's nixbld GIDs clash with the installer's).
	if c := row(t, cs, "Determinate Nix"); c.OK || c.Fatal || !c.Info || strings.Contains(c.Fix, "nix.enable") {
		t.Errorf("Determinate row %+v", c)
	}
	f.files["/usr/local/bin/determinate-nixd"] = true
	if c := row(t, Prereqs(f.probe(true)), "Determinate Nix"); !c.OK {
		t.Errorf("determinate-nixd at /usr/local/bin not found: %+v", c)
	}
	// Found, on PATH or at /usr/local/bin: a plain pass. An OK row with Info
	// set is printed as a note, so it must not carry it, nor a fix: there is
	// nothing to fix.
	for _, onPath := range []bool{true, false} {
		g := good()
		if !onPath {
			delete(g.path, "determinate-nixd")
			g.files["/usr/local/bin/determinate-nixd"] = true
		}
		if c := row(t, Prereqs(g.probe(true)), "Determinate Nix"); !c.OK || c.Info || c.Fix != "" {
			t.Errorf("found Determinate Nix (on PATH %v) = %+v, want a plain pass", onPath, c)
		}
	}
}

// No git identity row: init asks for the overlay's, and a fresh Mac has
// none, so the row only ever warned.
func TestPrereqsNoGitIdentityRow(t *testing.T) {
	f := good()
	f.out["git config --global user.email"] = ""
	for _, darwin := range []bool{false, true} {
		for _, c := range Prereqs(f.probe(darwin)) {
			if strings.Contains(c.Name, "identity") {
				t.Errorf("darwin=%v: %+v", darwin, c)
			}
		}
	}
}

// An Intel Mac: the pinned nixpkgs has no x86_64-darwin, so setup stops
// before anything else, with one line.
func TestUnsupported(t *testing.T) {
	if l := Unsupported("darwin", "amd64"); !strings.Contains(l, "Apple silicon") {
		t.Errorf("Intel Mac: %q", l)
	}
	for _, s := range [][2]string{{"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		if l := Unsupported(s[0], s[1]); l != "" {
			t.Errorf("%v: %q, want supported", s, l)
		}
	}
}

// Nix < 2.20 and Lix < 2.90 have no `nix config show`: their
// `nix show-config` says (Ubuntu 24.04's apt Nix is 2.18).
func TestPrereqsOldNixShowConfig(t *testing.T) {
	f := good()
	f.out["nix --version"] = "nix (Nix) 2.18.1"
	delete(f.out, "nix config show experimental-features")
	f.out["nix show-config"] = "cores = 0\nexperimental-features = flakes nix-command\nsandbox = true"
	if c := row(t, Prereqs(f.probe(false)), "flakes"); !c.OK {
		t.Fatalf("flakes on Nix 2.18 with them enabled: %+v", c)
	}
	f.out["nix show-config"] = "experimental-features = nix-command"
	if c := row(t, Prereqs(f.probe(false)), "flakes"); c.OK {
		t.Fatalf("flakes off on Nix 2.18: %+v", c)
	}
}

// macOS's /usr/bin/git is on PATH without the command-line tools and fails
// when run: git counts only when it runs.
func TestPrereqsGitStub(t *testing.T) {
	f := good()
	delete(f.out, "git --version")
	for _, c := range Prereqs(f.probe(true)) {
		if c.Name == "git" && (c.OK || !c.Fatal) {
			t.Errorf("git row = %+v, want failed and fatal", c)
		}
	}
}

// FlakesOff: the checklist's flakes row is NixOS's flakes-off note, the one
// case where setup turns flakes on for its own nix calls (NIX_CONFIG). Flakes
// on, on NixOS or elsewhere, and flakes off elsewhere (a stop) are not.
func TestFlakesOff(t *testing.T) {
	off := good()
	off.out["nix --version"] = "nix (Nix) 2.31.0"
	off.out["nix config show experimental-features"] = "nix-command"
	for _, c := range []struct {
		name string
		p    Probe
		want bool
	}{
		{"NixOS, flakes off", off.nixos(), true},
		{"NixOS, flakes on", good().nixos(), false},
		{"not NixOS, flakes off", off.probe(false), false},
		{"not NixOS, flakes on", good().probe(false), false},
		{"a Mac", good().probe(true), false},
	} {
		if got := FlakesOff(Prereqs(c.p)); got != c.want {
			t.Errorf("%s: FlakesOff = %v, want %v", c.name, got, c.want)
		}
	}
}
