package cli

// piste pkgs update: this machine's packages up to date, within what its
// overlay owns. In order: the checkout to the newest engine release and this
// overlay's pin to it, the overlay's inputs, a build with the version diff,
// the switch (runSwitch, every check piste switch makes), then the declared
// pnpm globals and, on a Mac, the declared Homebrew apps.
//
// The pnpm and Homebrew upgrades come after the switch, never before: the
// Homebrew taps are store paths only a switch moves, and pnpm may be in the
// new generation only. So a run that stops before the switch (a failed
// build, a no, --no-switch) changes nothing on the machine: only the
// overlay's flake.nix and flake.lock, left for the person to commit.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/outdated"
	"github.com/elocke/piste/cli/internal/release"
	"github.com/elocke/piste/cli/internal/workspace"
)

// pkgsUpdateOpts are piste pkgs update's flags.
type pkgsUpdateOpts struct {
	yes, noSwitch, dryRun, greedy bool
	only                          []string
}

// updateSteps are the parts of the run --only picks: nix is the engine, the
// inputs, the build and the switch.
type updateSteps struct{ nix, pnpm, brew bool }

// stepsFor is what --only picks (every step, when none is named); brew is a
// Mac's only, and one whose config manages Homebrew (without --only, the
// brew step skips itself when it doesn't).
func stepsFor(only []string, darwin bool) (updateSteps, error) {
	if len(only) == 0 {
		return updateSteps{nix: true, pnpm: true, brew: darwin}, nil
	}
	var s updateSteps
	for _, o := range only {
		switch o {
		case "nix":
			s.nix = true
		case "pnpm":
			s.pnpm = true
		case "brew":
			if !darwin {
				return updateSteps{}, errors.New("brew: Homebrew is managed on a Mac only")
			}
			if _, st := readBrewManifest(); st == brewOff {
				return updateSteps{}, errors.New("brew: Homebrew isn't managed by this machine's config")
			}
			s.brew = true
		default:
			return updateSteps{}, fmt.Errorf("--only %q: it takes nix, pnpm or brew", o)
		}
	}
	return s, nil
}

func newPkgsUpdate(a *App) *cobra.Command {
	var o pkgsUpdateOpts
	c := &cobra.Command{
		Use:   "update",
		Short: "Bring this machine's packages up to date, then switch",
		Long: "Bring this machine's packages up to date, within what its overlay owns:\n\n" +
			"  1. the checkout to the newest piste release (a checkout on a branch pulls it),\n" +
			"     and this overlay's engine pin to that release;\n" +
			"  2. the overlay's inputs (nix flake update), nixpkgs-fresh included;\n" +
			"  3. a build, with the version changes against the current generation (nh's\n" +
			"     dry switch; without nh, a plain build);\n" +
			"  4. the switch, once you say yes, with every check piste switch makes;\n" +
			"  5. then the pnpm globals of piste.packages.pnpm and, on a Mac, the declared\n" +
			"     Homebrew formulae and casks that brew lists as outdated.\n\n" +
			"Nothing on this machine changes before the switch: after a failed build, a no or\n" +
			"--no-switch, only the checkout and the overlay's flake.nix and flake.lock have\n" +
			"moved, the overlay's for you to commit once they work. Other overlays keep their\n" +
			"engine pins (piste update moves them). A pnpm or Homebrew upgrade that fails is\n" +
			"named, the run goes on, and it exits 1. --only pnpm or brew upgrades against\n" +
			"what is switched now, with no build.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.pkgsUpdate(o)
		},
	}
	f := c.Flags()
	f.BoolVarP(&o.yes, "yes", "y", false, "switch without asking")
	f.BoolVar(&o.noSwitch, "no-switch", false, "stop after the build: nothing on this machine changes")
	f.StringSliceVar(&o.only, "only", nil, "only these steps: nix (the engine, inputs, build and switch), pnpm or brew (repeatable)")
	f.BoolVar(&o.dryRun, "dry-run", false, "print the moves and the upgrades, and change nothing")
	f.BoolVar(&o.greedy, "greedy", false, "also upgrade the casks that update themselves (brew upgrade --greedy)")
	return c
}

// pkgsUpdate is piste pkgs update. The commit hint, when the overlay's
// files changed, is its last line, whatever ends the run.
func (a *App) pkgsUpdate(o pkgsUpdateOpts) error {
	if o.yes && o.noSwitch {
		a.errorf("--yes switches without asking and --no-switch never switches: give one of them")
		return ExitError{1}
	}
	steps, err := stepsFor(o.only, runtime.GOOS == "darwin")
	if err != nil {
		a.errorf(err.Error())
		return ExitError{1}
	}
	// Without the nix step there is no build to stop after: the upgrades
	// would run, against --no-switch's promise.
	if o.noSwitch && !steps.nix {
		a.errorf("--no-switch changes nothing on this machine and --only pnpm or brew upgrades it: give one of them")
		return ExitError{1}
	}
	if steps.nix {
		commit, upgrade, err := a.pkgsUpdateNix(o)
		if commit != "" {
			defer a.info(fmt.Sprintf("Commit %s's flake.nix and flake.lock once it works.", commit))
		}
		if err != nil || !upgrade {
			return err
		}
	}
	ok := true
	if steps.pnpm {
		if o.dryRun {
			ok = a.pnpmWould() && ok
		} else {
			ok = a.pnpmUpgrade() && ok
		}
	}
	if steps.brew {
		if o.dryRun {
			ok = a.brewWould(o.greedy) && ok
		} else {
			ok = a.brewUpgrade(o.greedy) && ok
		}
	}
	if o.dryRun {
		a.info("Dry run: nothing changed.")
	}
	if !ok {
		return ExitError{1}
	}
	return nil
}

// pkgsUpdateNix is the engine, the inputs, the build and the switch, for
// this machine's overlay. commit is the overlay ("overlays/<name>") when
// its flake.nix or flake.lock changed; upgrade, whether the pnpm and
// Homebrew upgrades follow: the machine switched, or a dry run.
func (a *App) pkgsUpdateNix(o pkgsUpdateOpts) (commit string, upgrade bool, err error) {
	p, err := a.plat()
	if err != nil {
		return "", false, err
	}
	root, err := checkoutRoot()
	if err != nil {
		a.errorf(err.Error())
		return "", false, ExitError{1}
	}
	name, in := workspace.OverlayOf(root, p.FlakeDir)
	if !in {
		a.errorf(fmt.Sprintf("piste pkgs update moves an overlay of the piste checkout, and %s isn't one: run it in %s/overlays/<name>, or give --overlay <name>", p.FlakeDir, root))
		return "", false, ExitError{1}
	}
	label := "overlays/" + name

	// 1 and 2: the checkout, then this overlay's pin and inputs. A git
	// refusal stops before any overlay is touched.
	mv, err := a.moveCheckout(o.dryRun, []string{name})
	if err != nil {
		return "", false, err
	}
	changed, ok := a.updateOverlayPin(mv.Root, name, mv.Newest, o.dryRun)
	if !ok {
		a.errorf(fmt.Sprintf("Fix %s above, then run piste pkgs update again.", label))
		return "", false, ExitError{1}
	}
	a.otherPins(mv.Root, name, mv.Newest)
	if changed {
		commit = label
	}
	if o.dryRun {
		return "", true, nil
	}

	// 3: the build, with the version diff when nh is here.
	eo, err := a.engineOverride(p)
	if err != nil {
		return commit, false, err
	}
	var argv []string
	if _, nerr := exec.LookPath("nh"); nerr == nil {
		t, err := a.gendiffTarget(p, "")
		if err != nil {
			return commit, false, err
		}
		argv = a.gendiffCmd(p, t, eo)
		a.announceFlake(p, &eo)
		a.info("Building, with the version changes against the current generation...")
	} else {
		t, err := a.targetFor(p, "")
		if err != nil {
			return commit, false, err
		}
		if argv, err = a.buildCmd(p, t, false, eo); err != nil {
			return commit, false, err
		}
		a.announceFlake(p, &eo)
		a.info("Building (nh isn't installed, so no version changes are shown)...")
	}
	rc, err := a.pass(argv, "")
	if err != nil {
		return commit, false, err
	}
	if rc != 0 {
		a.errorf(fmt.Sprintf("Build failed (exit code %d): nothing on this machine changed.", rc))
		a.fixHint("")
		return commit, false, ExitError{1}
	}

	// 4: the switch, asked for. A release's Upgrading notes, printed with
	// the move above, are named again just before it (--yes doesn't wait).
	notSwitched := "nothing on this machine changed. To apply: " + a.piste() + " switch"
	if mv.Upgrading && !o.noSwitch {
		a.warn("Read the Upgrading notes above: your overlay may need a change for this release.")
	}
	switch {
	case o.noSwitch:
		a.info("Not switched (--no-switch): " + notSwitched)
		return commit, false, nil
	case !o.yes:
		yes, err := a.askSwitchNow()
		if err != nil {
			return commit, false, err
		}
		if !yes {
			a.info("Not switched: " + notSwitched)
			return commit, false, nil
		}
	}
	if err := a.runSwitch("", false, false); err != nil {
		return commit, false, err
	}
	return commit, true, nil
}

// askSwitchNow asks "Switch now?" before pkgs update's switch. Ctrl+C at
// the question stops the run ("Aborted!", exit 1); input that ends there
// (no terminal, no --yes) is a no.
func (a *App) askSwitchNow() (bool, error) {
	yes, err := a.confirm("Switch now?")
	switch {
	case errors.Is(err, errAborted) && a.useForms():
		return false, a.aborted(err)
	case errors.Is(err, errAborted):
		// The input ended at the prompt: its line ends here.
		fmt.Fprintln(a.Stdout)
		return false, nil
	}
	return yes, err
}

// otherPins names each other overlay of the checkout whose engine pin is a
// release behind newest: piste update moves those, this command doesn't.
func (a *App) otherPins(root, name, newest string) {
	if newest == "" {
		return
	}
	overlays, _ := workspace.Overlays(root)
	for _, o := range overlays {
		if o == name {
			continue
		}
		if tag := pinOf(root, o); tag != "" && release.Newer(newest, tag) {
			a.info(fmt.Sprintf("overlays/%s's engine is on %s: piste update moves it", o, tag))
		}
	}
}

// pinOf is the release overlays/<name>'s engine pin names; "" when it
// names none.
func pinOf(root, name string) string {
	b, err := os.ReadFile(filepath.Join(root, "overlays", name, "flake.nix"))
	if err != nil {
		return ""
	}
	url, _ := release.ReadPin(string(b))
	return release.PinTag(url)
}

// pnpmNone is the line for a profile with no piste-pnpm.
const pnpmNone = "pnpm globals: none declared (piste.packages.pnpm in home.nix)"

// pnpmUpgrade upgrades the declared pnpm globals of the switched profile
// (piste-pnpm upgrade, the terminal inherited); false when it failed.
func (a *App) pnpmUpgrade() bool {
	tool := a.pnpmTool()
	if tool == "" {
		a.info(pnpmNone)
		return true
	}
	a.info("Upgrading the pnpm globals...")
	if res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{tool, "upgrade"}, Passthrough: true}); err != nil {
		a.warn(fmt.Sprintf("pnpm globals: some didn't upgrade (piste-pnpm upgrade %s). To try again: %s pkgs update --only pnpm", failure(res, err), a.piste()))
		return false
	}
	a.success("✓ pnpm globals upgraded")
	return true
}

// pnpmWould says which declared pnpm globals an upgrade would move.
func (a *App) pnpmWould() bool {
	items, declared, err := a.pnpmOutdated()
	switch {
	case err != nil:
		a.warn("pnpm globals: " + err.Error())
		return false
	case !declared:
		a.info(pnpmNone)
	case len(items) == 0:
		a.info("pnpm globals: up to date")
	}
	for _, it := range items {
		a.info(fmt.Sprintf("pnpm globals: would upgrade %s %s → %s", it.Name, it.Current, it.Latest))
	}
	return true
}

// brewOutdatedSaid is brewOutdated with the cases that end the step said:
// no manifest, an error (ok false) and nothing outdated. Homebrew off
// (homebrew.enable = false) isn't this config's: the step ends quietly.
// items is what to upgrade.
func (a *App) brewOutdatedSaid(greedy bool) (items []outdated.BrewItem, ok bool) {
	items, state, err := a.brewOutdated(greedy)
	switch {
	case state == brewNoList:
		a.info("Homebrew: no list of declared apps yet (" + brewManifest + "): a switch writes it")
	case state == brewOff:
	case err != nil:
		a.warn(fmt.Sprintf("Homebrew: %v. To try again: %s pkgs update --only brew", err, a.piste()))
		return nil, false
	case len(items) == 0:
		a.info("Homebrew: up to date")
	}
	return items, true
}

// brewUpgrade upgrades the declared Homebrew formulae and casks that brew
// lists as outdated: one brew upgrade for each kind that has any, the
// terminal inherited. false when a step failed; the other still runs.
func (a *App) brewUpgrade(greedy bool) bool {
	items, ok := a.brewOutdatedSaid(greedy)
	if !ok {
		return false
	}
	var formulae, casks []string
	for _, it := range items {
		if it.Cask {
			casks = append(casks, it.Name)
		} else {
			formulae = append(formulae, it.Name)
		}
	}
	caskArgs := []string{"--cask"}
	if greedy {
		caskArgs = append(caskArgs, "--greedy")
	}
	for _, k := range []struct {
		what  string
		args  []string
		names []string
	}{{"formulae", []string{"--formula"}, formulae}, {"casks", caskArgs, casks}} {
		if len(k.names) == 0 {
			continue
		}
		argv := append(append([]string{"brew", "upgrade"}, k.args...), k.names...)
		a.info(fmt.Sprintf("Upgrading the Homebrew %s: %s...", k.what, strings.Join(k.names, ", ")))
		if res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: argv, Passthrough: true}); err != nil {
			a.warn(fmt.Sprintf("Homebrew: some %s didn't upgrade (%s %s). To try again: %s pkgs update --only brew", k.what, strings.Join(argv[:3], " "), failure(res, err), a.piste()))
			ok = false
			continue
		}
		a.success("✓ Homebrew " + k.what + " upgraded")
	}
	return ok
}

// failure says how a run that returned err ended: its exit code, else
// why it didn't run.
func failure(res execx.Result, err error) string {
	if res.ExitCode != 0 {
		return fmt.Sprintf("exited %d", res.ExitCode)
	}
	return "failed: " + err.Error()
}

// brewWould says which declared Homebrew apps an upgrade would move.
func (a *App) brewWould(greedy bool) bool {
	items, ok := a.brewOutdatedSaid(greedy)
	for _, it := range items {
		kind := ""
		if it.Cask {
			kind = " (cask)"
		}
		a.info(fmt.Sprintf("Homebrew: would upgrade %s%s %s → %s", it.Name, kind, it.Current, it.Latest))
	}
	return ok
}
