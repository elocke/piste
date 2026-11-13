package drift

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/packages"
)

// Tool is where an install outside Nix was found.
type Tool string

const (
	Brew  Tool = "brew" // a Homebrew formula installed on request
	Cask  Tool = "cask"
	Npm   Tool = "npm"
	Pnpm  Tool = "pnpm"
	Cargo Tool = "cargo" // a binary in ~/.cargo/bin
	Pip   Tool = "pip"   // pip --user
)

// Service isn't a tool of Tools: Installs[Service] are the formulae
// registered as brew services (a Mac's), which stay in Homebrew.
const Service Tool = "brew services"

// Tools in the order drift and adopt list them.
var Tools = []Tool{Brew, Cask, Npm, Pnpm, Cargo, Pip}

// Installs is what each tool has installed, in the tool's own order (and
// the brew services, under Service).
type Installs map[Tool][]string

// Scan lists what each tool has installed outside Nix (Homebrew on a Mac
// only). Both Homebrew listings print a tap's items by their full names.
func Scan(ctx context.Context, r execx.Runner, home string, darwin bool) Installs {
	in := Installs{}
	if darwin {
		in[Brew], _ = lines(ctx, r, "brew", "leaves", "--installed-on-request")
		in[Cask], _ = lines(ctx, r, "brew", "list", "--cask", "--full-name", "-1")
		if s := brewServices(ctx, r); len(s) > 0 {
			in[Service] = s
		}
	}
	in[Npm] = npmGlobals(ctx, r)
	in[Pnpm] = PnpmGlobals(ctx, r)
	in[Cargo] = cargoInstalls(home)
	in[Pip] = pipInstalls(ctx, r)
	return in
}

// brewServices are the formulae `brew services` has registered: started
// (or in any state but "none"), or with a service file in place (a user is
// set; a root daemon reads as "none" to the person's user). A launchd job
// points into the formula's keg, and Nix's package of that name wouldn't
// run it.
func brewServices(ctx context.Context, r execx.Runner) []string {
	res, err := r.Run(ctx, execx.Cmd{Argv: []string{"brew", "services", "list", "--json"}})
	if err != nil {
		return nil
	}
	var svcs []struct {
		Name   string  `json:"name"`
		Status string  `json:"status"`
		User   *string `json:"user"`
	}
	if json.Unmarshal(res.Stdout, &svcs) != nil {
		return nil
	}
	var out []string
	for _, s := range svcs {
		if s.Name != "" && (s.Status != "none" || s.User != nil && *s.User != "") {
			out = append(out, s.Name)
		}
	}
	return out
}

// JS is the overlay's JS globals: pnpmPackages and npmPackages are let
// bindings, not options, so they are read as text; piste.packages.pnpm is
// given (ReadJS's pnpmOption).
type JS struct {
	Pnpm, Npm map[string]bool // declared pnpm globals (unpinned) and npm globals
	PnpmList  bool            // the overlay's own modules/ have a pnpmPackages list to add to
}

// ReadJS reads the pnpmPackages / PNPM_PACKAGES and npmPackages /
// NPM_PACKAGES lists of the overlay's modules/ and its relative path
// inputs' modules/. pnpmOption is piste.packages.pnpm, as written ("name",
// "name@<version>"): declared pnpm globals too, by name.
func ReadJS(flakeDir string, pnpmOption []string) JS {
	all := ScanNixFiles(filepath.Join(flakeDir, "modules")) + "\n"
	for _, dir := range pathInputs(flakeDir) {
		all += ScanNixFiles(filepath.Join(dir, "modules")) + "\n"
	}
	js := JS{Pnpm: map[string]bool{}, PnpmList: len(packages.FindPnpmGlobalsFiles(flakeDir)) > 0}
	for p := range merge(ExtractBashArrayStrings(all, []string{"PNPM_PACKAGES"}), ExtractNixListStrings(all, []string{"pnpmPackages"})) {
		js.Pnpm[Unpin(p)] = true
	}
	// Any @ past the first character starts the version, as piste-pnpm
	// reads its entries: "x@latest" is x.
	for _, p := range pnpmOption {
		if i := strings.LastIndex(p, "@"); i > 0 {
			p = p[:i]
		}
		js.Pnpm[p] = true
	}
	js.Npm = merge(ExtractBashArrayStrings(all, []string{"NPM_PACKAGES"}), ExtractNixListStrings(all, []string{"npmPackages"}))
	return js
}

// Class is what an install found outside Nix is.
type Class int

const (
	InHomebrew Class = iota // in homebrew.casks / brews: the same install, declared
	DeclaredJS              // a JS global its pnpmPackages / npmPackages declare
	Duplicate               // Nix declares it too: removed after a switch
	Untracked               // offered, or listed with why not
)

// Target is where adopt would write an untracked find.
type Target int

const (
	NotOffered Target = iota // listed only; Why says why
	ToNixpkgs
	ToCasks
	ToBrews
	ToPnpm
)

// Find is one install found outside Nix.
type Find struct {
	Tool  Tool
	Name  string
	Class Class
	To    Target   // Untracked: where adopt would put it
	Why   string   // To == NotOffered: why it isn't offered; ToBrews: why not nixpkgs
	Pkg   *Package // the nixpkgs package of that name here, if any
	// ViaPnpm: a Duplicate npm global that pnpmPackages names and Nix
	// doesn't; pnpm, not Nix, provides it once switched.
	ViaPnpm bool
}

// BrewServiceWhy is why a formula that runs as a brew service is offered
// into Homebrew's brews though nixpkgs has it.
const BrewServiceWhy = "a brew service; Nix wouldn't run it"

// Classify sorts every install into its class, in Tools order and then each
// tool's own: in Homebrew's lists, a declared JS global, a duplicate of a
// declared Nix package, or untracked (with where adopt would put it). A cask
// (an app) and a formula that runs as a brew service are never duplicates:
// Nix's package of the name isn't the app, and wouldn't run the service.
func Classify(in Installs, d Declarations, js JS) []Find {
	nix, svc := set(d.Nix), set(in[Service])
	var out []Find
	for _, t := range Tools {
		for _, n := range in[t] {
			f := Find{Tool: t, Name: n}
			if p, ok := d.Pkgs[n]; ok {
				f.Pkg = &p
			}
			homebrew := t == Cask || t == Brew && svc[n]
			switch {
			case t == Brew && slices.Contains(d.Brews, n), t == Cask && slices.Contains(d.Casks, n):
				f.Class = InHomebrew
			case t == Pnpm && js.Pnpm[n], t == Npm && js.Npm[n]:
				f.Class = DeclaredJS
			case !homebrew && (nix[n] || f.Pkg != nil && nix[f.Pkg.Pname]):
				f.Class = Duplicate
			case t == Npm && js.Pnpm[n]:
				f.Class, f.ViaPnpm = Duplicate, true
			default:
				f.Class = Untracked
				f.To, f.Why = offer(f, d, js, svc[n])
			}
			out = append(out, f)
		}
	}
	return out
}

// offer is where an untracked find would go, or why it isn't offered;
// service: a formula that runs as a brew service.
func offer(f Find, d Declarations, js JS, service bool) (Target, string) {
	switch {
	case (f.Tool == Brew || f.Tool == Cask) && strings.Contains(f.Name, "/"):
		return NotOffered, "from a tap: add the tap to your flake first"
	case slices.Contains(d.Exclude, f.Name):
		return NotOffered, "excluded by this host"
	case f.Tool == Brew && service:
		return ToBrews, BrewServiceWhy
	case f.Tool == Brew && f.Pkg != nil:
		return ToNixpkgs, ""
	case f.Tool == Brew:
		return ToBrews, ""
	case f.Tool == Cask:
		return ToCasks, ""
	case (f.Tool == Npm || f.Tool == Pnpm) && js.PnpmList:
		return ToPnpm, ""
	case f.Pkg != nil:
		return ToNixpkgs, ""
	case f.Tool == Npm || f.Tool == Pnpm:
		return NotOffered, "no pnpmPackages list and no nixpkgs package by that name"
	}
	return NotOffered, "no nixpkgs package by that name"
}

// removeArgv is each tool's removal command, before the names; a cask goes
// with brew's.
var removeArgv = map[Tool][]string{
	Brew: {"brew", "uninstall"}, Cask: {"brew", "uninstall"},
	Npm: {"npm", "rm", "-g"}, Pnpm: {"pnpm", "rm", "-g"},
	Cargo: {"cargo", "uninstall"}, Pip: {"pip", "uninstall", "--yes"},
}

// Removals are the commands that remove the copies outside Nix once a
// switch has made Nix provide them: every duplicate in finds, plus each
// adopted find that went to nixpkgs, or an npm global that went to
// pnpmPackages. One command per tool, names in find order.
func Removals(finds, adopted []Find) [][]string {
	picked := map[[2]string]bool{}
	for _, f := range adopted {
		picked[[2]string{string(f.Tool), f.Name}] = true
	}
	names := map[string][]string{} // by command
	var order []string
	for _, f := range finds {
		gone := f.Class == Duplicate ||
			picked[[2]string{string(f.Tool), f.Name}] && (f.To == ToNixpkgs || f.Tool == Npm && f.To == ToPnpm)
		if !gone {
			continue
		}
		cmd := strings.Join(removeArgv[f.Tool], " ")
		if names[cmd] == nil {
			order = append(order, cmd)
		}
		names[cmd] = append(names[cmd], f.Name)
	}
	var out [][]string
	for _, cmd := range order {
		out = append(out, append(strings.Fields(cmd), names[cmd]...))
	}
	return out
}

// PnpmOnly are the npm globals Removals removes that pnpm, not Nix,
// provides once switched: one pnpmPackages names (ViaPnpm), or one just
// adopted into it. The switch's pnpm hook installs them, and carries on
// without them when pnpm is missing or fails: check pnpm has them first.
func PnpmOnly(finds, adopted []Find) []string {
	picked := map[string]bool{}
	for _, f := range adopted {
		if f.Tool == Npm && f.To == ToPnpm {
			picked[f.Name] = true
		}
	}
	var out []string
	for _, f := range finds {
		if f.Tool == Npm && (f.Class == Duplicate && f.ViaPnpm || f.Class == Untracked && picked[f.Name]) {
			out = append(out, f.Name)
		}
	}
	return out
}

// Copies counts the names in Removals' commands.
func Copies(cmds [][]string) int {
	n := 0
	for _, c := range cmds {
		for _, t := range Tools {
			if p := removeArgv[t]; len(c) >= len(p) && strings.Join(c[:len(p)], " ") == strings.Join(p, " ") {
				n += len(c) - len(p)
				break
			}
		}
	}
	return n
}

func set(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
