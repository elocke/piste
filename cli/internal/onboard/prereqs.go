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
	NixOS    bool // the machine runs NixOS (flakes may be off, git missing)
}

// Check is one row of the checklist. A failed Fatal check stops setup; a
// failed Info check is a note, Fix what it says (nothing to fix); an OK Info
// check passes with a note, which Fix holds too.
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
// Determinate's (a note). Every failed row says how to fix it. On NixOS
// flakes being off is a note, not a stop (setup turns them on for itself,
// and the first switch for good), and a missing git is fixed with git from
// nix shell.
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
	flakesCheck := Check{Name: "flakes", OK: haveNix && flakes, Fatal: true,
		Fix: "add `experimental-features = nix-command flakes` to ~/.config/nix/nix.conf (Determinate Nix has them on)"}
	if p.NixOS {
		// A fresh NixOS has no git, and Nix with flakes off. Nix itself is
		// there, so setup can run both with the flags on: the one command
		// that starts setup over with git is given, since only the person's
		// shell can run it.
		const flags = "--extra-experimental-features 'nix-command flakes'"
		gitFix = "nix " + flags + " shell nixpkgs#git --command nix " + flags + " run github:elocke/piste/stable -- setup <url>"
		if haveNix && !flakes {
			flakesCheck = Check{Name: "flakes", OK: true, Info: true,
				Fix: "Flakes are off in this NixOS's Nix; setup turns them on for itself, and your first switch turns them on for good"}
		}
	}
	cs := []Check{
		{Name: "Nix", OK: haveNix, Fatal: true,
			Fix: "install Nix: curl -fsSL https://install.determinate.systems/nix | sh -s -- install"},
		flakesCheck,
		{Name: "git", OK: gitRuns, Fatal: true, Fix: gitFix},
	}
	if p.Darwin {
		_, xcode := run("xcode-select", "-p")
		_, dErr := p.LookPath("determinate-nixd")
		// A note only when it is missing: a found row is a plain pass (an
		// OK row with Info set is printed as a note), with no Fix.
		found := dErr == nil || p.Exists("/usr/local/bin/determinate-nixd")
		determinate := Check{Name: "Determinate Nix", OK: found}
		if !found {
			determinate.Info = true
			determinate.Fix = "not found; piste leaves Nix and its settings to whichever installer put it here, and is tested with Determinate Nix"
		}
		cs = append(cs,
			Check{Name: "Xcode command-line tools", OK: xcode, Fatal: true, Fix: "xcode-select --install"},
			determinate)
	}
	return cs
}

// FlakesOff: the flakes row is NixOS's flakes-off note, a pass with a
// note: the Nix here has them off, and setup turns them on for its own nix
// calls.
func FlakesOff(cs []Check) bool {
	for _, c := range cs {
		if c.Name == "flakes" {
			return c.OK && c.Info
		}
	}
	return false
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
