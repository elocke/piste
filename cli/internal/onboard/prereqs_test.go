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
