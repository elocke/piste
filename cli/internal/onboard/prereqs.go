// Package onboard is `piste setup`'s logic that needs no terminal:
// the prerequisite checklist and the release to clone.
package onboard

import "strings"

// Probe is how Prereqs looks at the machine (injected: tests fake it).
type Probe struct {
	LookPath func(name string) (string, error)
	Run      func(argv ...string) (string, error) // stdout, trimmed by the caller or not
	Exists   func(path string) bool
	Darwin   bool
}

// Check is one row of the checklist. A failed Fatal check stops setup; a
// failed Info check is a note, Fix what it says (nothing to fix).
type Check struct {
	Name, Fix       string
	OK, Fatal, Info bool
}

// Unsupported is the one line setup stops with on a machine piste can't
// build for, "" on one it can: an Intel Mac (the pinned nixpkgs has no
// x86_64-darwin).
func Unsupported(goos, goarch string) string {
	if goos == "darwin" && goarch == "amd64" {
		return "piste supports Macs with Apple silicon only: the nixpkgs it pins dropped Intel Macs (x86_64-darwin)"
	}
	return ""
}

// Prereqs is the checklist: Nix, flakes, git, and on macOS the
// Xcode command-line tools (fatal); on macOS, whether the Nix is
// Determinate's (a note). Every failed row says how to fix it.
func Prereqs(p Probe) []Check {
	run := func(argv ...string) (string, bool) {
		out, err := p.Run(argv...)
		return strings.TrimSpace(out), err == nil
	}
	_, nixErr := p.LookPath("nix")
	version, nixRuns := run("nix", "--version")
	haveNix := nixErr == nil && nixRuns
	features, ok := run("nix", "config", "show", "experimental-features")
	if !ok {
		// Nix < 2.20 and Lix < 2.90: no `nix config show`.
		all, _ := run("nix", "show-config")
		for _, l := range strings.Split(all, "\n") {
			if k, v, found := strings.Cut(l, "="); found && strings.TrimSpace(k) == "experimental-features" {
				features = strings.TrimSpace(v)
			}
		}
	}
	// Determinate Nix has flakes built in and lists no experimental feature
	// for them; other Nixes need "flakes" in experimental-features.
	flakes := strings.Contains(version, "Determinate") || strings.Contains(" "+features+" ", " flakes ")
	// Runs, not just found: macOS's /usr/bin/git is a stub that fails
	// without the command-line tools.
	_, gitRuns := run("git", "--version")

	gitFix := "install git with your package manager"
	if p.Darwin {
		gitFix = "xcode-select --install (git comes with the Xcode command-line tools)"
	}
	cs := []Check{
		{Name: "Nix", OK: haveNix, Fatal: true,
			Fix: "install Nix: curl -fsSL https://install.determinate.systems/nix | sh -s -- install"},
		{Name: "flakes", OK: haveNix && flakes, Fatal: true,
			Fix: "add `experimental-features = nix-command flakes` to ~/.config/nix/nix.conf (Determinate Nix has them on)"},
		{Name: "git", OK: gitRuns, Fatal: true, Fix: gitFix},
	}
	if p.Darwin {
		_, xcode := run("xcode-select", "-p")
		_, dErr := p.LookPath("determinate-nixd")
		cs = append(cs,
			Check{Name: "Xcode command-line tools", OK: xcode, Fatal: true, Fix: "xcode-select --install"},
			Check{Name: "Determinate Nix", OK: dErr == nil || p.Exists("/usr/local/bin/determinate-nixd"), Info: true,
				Fix: "not found; piste leaves Nix and its settings to whichever installer put it here, and is tested with Determinate Nix"})
	}
	return cs
}

// Blocked: a fatal check failed.
func Blocked(cs []Check) bool {
	for _, c := range cs {
		if c.Fatal && !c.OK {
			return true
		}
	}
	return false
}
