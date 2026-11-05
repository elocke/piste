package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/drift"
	"github.com/elocke/piste/cli/internal/flake"
	"github.com/elocke/piste/cli/internal/packages"
	"github.com/elocke/piste/cli/internal/platform"
)

func init() { register("adopt", newAdopt) }

// newAdopt is `piste adopt`: what is installed outside Nix,
// offered where the overlay keeps that kind of thing; the ticked ones
// written, the switch offered, then the copies outside Nix that Nix now
// provides removed.
func newAdopt(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "adopt",
		Short: "Adopt what you installed outside Nix: write it into your overlay, switch, remove the outside copies",
		Long: "Finds what is installed outside Nix (Homebrew formulae and casks, npm and pnpm\n" +
			"globals, cargo install, pip --user) and shows where each would go in this host's\n" +
			"overlay: nixpkgs, Homebrew's lists or pnpmPackages. The ticked ones are written\n" +
			"into the lists the overlay already keeps; then it offers the switch, and after a\n" +
			"switch that worked, offers to remove the copies outside Nix that Nix now\n" +
			"provides. Nothing is removed without that switch. Taps' items, excluded names and\n" +
			"names nixpkgs doesn't have are listed, not offered.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error { return a.runAdopt() },
	}
}

func (a *App) runAdopt() error {
	p, err := a.plat()
	if err != nil {
		return err
	}
	f, err := a.findDrift(p)
	if err != nil {
		return err
	}
	var listed, offered []drift.Find
	dups := false
	for _, x := range f.finds {
		switch {
		case x.Class == drift.Duplicate:
			dups = true
		case x.Class == drift.Untracked && x.To == drift.NotOffered:
			listed = append(listed, x)
		case x.Class == drift.Untracked:
			offered = append(offered, x)
		}
	}
	if len(listed) > 0 {
		fmt.Fprintln(a.Stdout, "Left as they are:")
		tw, nw := 0, 0
		for _, x := range listed {
			tw, nw = max(tw, len(x.Tool)), max(nw, len(x.Name))
		}
		for _, x := range listed {
			fmt.Fprintf(a.Stdout, "  %-*s  %-*s  %s\n", tw, x.Tool, nw, x.Name, x.Why)
		}
	}
	if len(offered) == 0 && !dups {
		a.success("Nothing to adopt.")
		return nil
	}
	var written []drift.Find
	byHand := false
	if len(offered) > 0 {
		picked, err := a.pickAdopt(fmt.Sprintf("Adopt into %s (%s)", overlayLabel(p), f.host), offered)
		if errors.Is(err, errStopped) {
			a.errorf("No answer: stopped, nothing written.")
			return ExitError{1}
		}
		if err != nil {
			return err
		}
		written, byHand = a.writeAdopted(f, picked)
	}
	exit := func() error {
		if byHand {
			return ExitError{1}
		}
		return nil
	}
	if len(written) == 0 && !dups {
		if !byHand {
			a.info("Nothing adopted.")
		}
		return exit()
	}
	removals := drift.Removals(f.finds, written)
	if !a.askSwitch() {
		if len(removals) > 0 {
			a.info("Nothing removed: run " + a.piste() + " adopt again to switch and remove the copies outside Nix.")
		} else {
			a.info("Run " + a.piste() + " switch when you're ready.")
		}
		return exit()
	}
	// Any error, the undo of a failed switch included, is "not switched".
	if err := a.runSwitch("", false); err != nil {
		a.warn("Nothing removed: the switch didn't finish. Run " + a.piste() + " adopt again once it does.")
		return err
	}
	removals = a.pnpmChecked(f.finds, written, removals)
	if len(removals) == 0 {
		return exit()
	}
	if !a.askRemove(removals) {
		a.info("Kept them: " + a.piste() + " drift lists them.")
		return exit()
	}
	for _, argv := range removals {
		if rc, err := a.pass(argv, ""); err != nil || rc != 0 {
			a.warn("`" + strings.Join(argv, " ") + "` failed: run it yourself")
		}
	}
	return exit()
}

// pnpmChecked is removals less the npm globals that only pnpm provides
// (drift.PnpmOnly) and pnpm doesn't have after the switch: its hook skipped
// or failed them, so npm's copy stays, and the person is told.
func (a *App) pnpmChecked(finds, written []drift.Find, removals [][]string) [][]string {
	want := drift.PnpmOnly(finds, written)
	if len(want) == 0 {
		return removals
	}
	have := map[string]bool{}
	for _, n := range drift.PnpmGlobals(context.Background(), a.runner()) {
		have[n] = true
	}
	held := map[string]bool{}
	var names []string
	for _, n := range want {
		if !have[n] {
			held[n] = true
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return removals
	}
	it := "it"
	if len(names) > 1 {
		it = "them"
	}
	a.warn(fmt.Sprintf("Kept npm's %s: pnpm doesn't have %s yet (the switch's output says why); run %s adopt again once it does.",
		strings.Join(names, " "), it, a.piste()))
	var keep []drift.Find
	for _, x := range finds {
		if !(x.Tool == drift.Npm && held[x.Name]) {
			keep = append(keep, x)
		}
	}
	return drift.Removals(keep, written)
}

// overlayLabel is the overlay as the person knows it: overlays/<name> in a
// checkout, else its directory.
func overlayLabel(p platform.Platform) string {
	if p.Checkout != "" {
		if r, err := filepath.Rel(p.Checkout, p.FlakeDir); err == nil {
			return r
		}
	}
	return p.FlakeDir
}

// showPath is an overlay file relative to the checkout, else to the flake.
func showPath(p platform.Platform, file string) string {
	base := p.FlakeDir
	if p.Checkout != "" {
		base = p.Checkout
	}
	if r, err := filepath.Rel(base, file); err == nil {
		return r
	}
	return file
}

// pickAdopt asks which of offered to adopt, every one ticked to start:
// one form, or a yes-by-default question each. errStopped: Ctrl+C, or the
// end of the input.
func (a *App) pickAdopt(title string, offered []drift.Find) ([]drift.Find, error) {
	w := 0
	for _, f := range offered {
		w = max(w, len(f.Name))
	}
	label := func(f drift.Find) string {
		return fmt.Sprintf("%-*s  %s → %s", w, f.Name, f.Tool, targetLabel(f))
	}
	if !a.useForms() {
		fmt.Fprintln(a.Stdout, title+":")
		var picked []drift.Find
		for _, f := range offered {
			ok, err := a.yesNo("  " + label(f) + "?")
			if err != nil {
				return nil, err
			}
			if ok {
				picked = append(picked, f)
			}
		}
		return picked, nil
	}
	opts := make([]huh.Option[int], len(offered))
	sel := make([]int, len(offered))
	for i, f := range offered {
		opts[i], sel[i] = huh.NewOption(label(f), i), i
	}
	// huh sizes the list to the options less the title and description
	// lines: give it room for both and up to 15 options, scrolling past that.
	if err := a.form(huh.NewForm(huh.NewGroup(huh.NewMultiSelect[int]().
		Title(title).Description("Space toggles, Enter adopts").Options(opts...).Value(&sel).
		Height(min(len(offered), 15) + 2)))); err != nil {
		return nil, err
	}
	sort.Ints(sel)
	picked := make([]drift.Find, 0, len(sel))
	for _, i := range sel {
		picked = append(picked, offered[i])
	}
	return picked, nil
}

// yesNo is a plain yes-by-default question; errStopped at the end of the
// input.
func (a *App) yesNo(label string) (bool, error) {
	p := a.prompter()
	for {
		v := strings.ToLower(strings.TrimSpace(p.ask(label+" [Y/n]", "")))
		if p.eof {
			return false, errStopped
		}
		switch v {
		case "", "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(a.Stdout, "  Answer y or n.")
	}
}

// askSwitch: Switch now unless Later is picked; Ctrl+C and the end of the
// input are Later.
func (a *App) askSwitch() bool {
	const q = "Switch now? Nix then provides them."
	if !a.useForms() {
		ok, err := a.yesNo(q)
		return err == nil && ok
	}
	ok, err := a.askYesNo(q, "Switch now", "Later", true)
	return err == nil && ok
}

// askRemove: Remove the copies outside Nix (the commands shown) unless Keep
// is picked; Ctrl+C and the end of the input are Keep.
func (a *App) askRemove(cmds [][]string) bool {
	n := drift.Copies(cmds)
	q := fmt.Sprintf("Remove %d copies outside Nix? Nix provides them now.", n)
	if n == 1 {
		q = "Remove 1 copy outside Nix? Nix provides it now."
	}
	lines := make([]string, len(cmds))
	for i, c := range cmds {
		lines[i] = strings.Join(c, " ")
	}
	if !a.useForms() {
		for _, l := range lines {
			fmt.Fprintln(a.Stdout, "  "+l)
		}
		ok, err := a.yesNo(q)
		return err == nil && ok
	}
	v := true
	err := a.form(huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(q).
		Description(strings.Join(lines, "\n")).Affirmative("Remove").Negative("Keep").Value(&v))))
	return err == nil && v
}

// handList is what a kind's entries need when adopt can't write them: the
// option and the overlay file to add them to.
type handList struct {
	option, file string
	entries      []string
}

// writeAdopted writes picked into the overlay's lists (see drift.FindList;
// pnpm globals into the first pnpmPackages list) and says where. What has
// no list to go into is named for adding by hand (byHand). A name two tools
// found is written once, and both finds count as written.
func (a *App) writeAdopted(f found, picked []drift.Find) (written []drift.Find, byHand bool) {
	p := f.p
	type segment struct {
		opener string
		names  []string
	}
	added := map[string][]segment{} // file -> what went into it
	var files []string
	note := func(file, opener, name string) {
		if added[file] == nil {
			files = append(files, file)
		}
		segs := added[file]
		if len(segs) > 0 && segs[len(segs)-1].opener == opener {
			segs[len(segs)-1].names = append(segs[len(segs)-1].names, name)
		} else {
			segs = append(segs, segment{opener, []string{name}})
		}
		added[file] = segs
	}
	var hand []handList
	var targets []drift.Target
	byTarget := map[drift.Target][]drift.Find{}
	type key struct {
		to   drift.Target
		name string
	}
	seen := map[key]bool{}
	var again []drift.Find // a name already picked for its target
	for _, x := range picked {
		k := key{x.To, x.Name}
		if seen[k] {
			again = append(again, x)
			continue
		}
		seen[k] = true
		if byTarget[x.To] == nil {
			targets = append(targets, x.To)
		}
		byTarget[x.To] = append(byTarget[x.To], x)
	}
	for _, to := range targets {
		finds := byTarget[to]
		if to == drift.ToPnpm {
			file := packages.FindPnpmGlobalsFiles(p.FlakeDir)[0]
			for _, x := range finds {
				if ok, err := packages.AddToPnpmGlobals(x.Name, file); ok && err == nil {
					written = append(written, x)
					note(file, packages.PnpmListName, x.Name)
				} else {
					hand = appendHand(hand, packages.PnpmListName, showPath(p, file), `"`+x.Name+`"`)
				}
			}
			continue
		}
		lists := drift.Lists(to, p.IsDarwin)
		w, ok := drift.FindList(p.FlakeDir, lists, f.decl.Files)
		if !ok {
			l, file := drift.HandList(lists, f.decl.Files), drift.HandFile(p.FlakeDir, lists, f.decl.Files, f.host)
			for _, x := range finds {
				hand = appendHand(hand, l.Option, file, drift.Entry(to, x.Name, false))
			}
			continue
		}
		for _, x := range finds {
			entry := drift.Entry(to, x.Name, w.WithPkgs)
			if ok, err := packages.AddToOptionList(entry, w.File, w.List.Cands()); ok && err == nil {
				written = append(written, x)
				note(w.File, w.List.Opener, x.Name)
			} else {
				rel, _ := filepath.Rel(p.FlakeDir, w.File)
				hand = appendHand(hand, w.List.Option, rel, entry)
			}
		}
	}
	for _, x := range again {
		if slices.ContainsFunc(written, func(w drift.Find) bool { return w.To == x.To && w.Name == x.Name }) {
			written = append(written, x)
		}
	}
	for _, file := range files {
		var parts []string
		for _, s := range added[file] {
			parts = append(parts, s.opener+" "+strings.Join(s.names, " "))
		}
		a.success("Added to " + showPath(p, file) + ":")
		fmt.Fprintln(a.Stdout, "  "+strings.Join(parts, " · "))
	}
	for _, h := range hand {
		where := "this host's configuration"
		if h.file != "" {
			where = h.file
		}
		a.warn(fmt.Sprintf("Add by hand: %s into %s in %s", strings.Join(h.entries, " "), h.option, where))
	}
	return written, len(hand) > 0
}

// appendHand adds entry to the hand list for option and file.
func appendHand(hs []handList, option, file, entry string) []handList {
	for i := range hs {
		if hs[i].option == option && hs[i].file == file {
			hs[i].entries = append(hs[i].entries, entry)
			return hs
		}
	}
	return append(hs, handList{option, file, []string{entry}})
}

// found is one scan, one eval and their classification: what `piste drift`
// reports and `piste adopt` acts on.
type found struct {
	p     platform.Platform
	host  string // the host's name: the Mac's hostname, or the home's profile
	decl  drift.Declarations
	js    drift.JS
	finds []drift.Find
}

// findDrift scans for installs outside Nix, evaluates what this host
// declares (with the switch's engine override), and classifies them. A
// failed eval stops the command: there is no fallback.
func (a *App) findDrift(p platform.Platform) (found, error) {
	ctx, r := context.Background(), a.runner()
	a.info("Scanning for installs outside Nix...")
	in := drift.Scan(ctx, r, p.HomeDir, p.IsDarwin)
	if err := a.checkNix(); err != nil {
		return found{}, err
	}
	host, installable := p.Hostname, p.FlakeDir+`#darwinConfigurations."`+p.Hostname+`"`
	if !p.IsDarwin {
		host = flake.DetectProfile(ctx, r, p.FlakeDir, profileUser(p), p.Hostname)
		installable = p.FlakeDir + `#homeConfigurations."` + host + `"`
	}
	o, err := a.engineOverride(p)
	if err != nil {
		return found{}, err
	}
	a.info("Reading what " + host + " declares...")
	var names []string
	for _, t := range drift.Tools {
		names = append(names, in[t]...)
	}
	d, err := drift.Declared(ctx, r, installable, o.Args(), names)
	if err != nil {
		why := err.Error()
		var ee *drift.EvalError
		if errors.As(err, &ee) {
			if t := stderrTail(ee.Stderr, 3); t != "" {
				why = t
			}
		}
		a.errorf("Couldn't read what " + host + " declares: " + why)
		a.errorf("Run `" + a.piste() + " build` to see why.")
		return found{}, ExitError{1}
	}
	js := drift.ReadJS(p.FlakeDir)
	return found{p: p, host: host, decl: d, js: js, finds: drift.Classify(in, d, js)}, nil
}

// targetLabel is where an untracked find would go, or why it isn't offered.
func targetLabel(f drift.Find) string {
	switch f.To {
	case drift.ToNixpkgs:
		return "nixpkgs " + f.Pkg.Pname + " " + f.Pkg.Version
	case drift.ToBrews:
		if f.Why != "" {
			return "Homebrew brews (" + f.Why + ")"
		}
		return "Homebrew brews"
	case drift.ToCasks:
		return "Homebrew casks"
	case drift.ToPnpm:
		return "pnpm globals"
	}
	return f.Why
}
