package cli

// piste pkgs: what is out of date on this machine, grouped by who updates it.
// Read-only: it fetches the checkout's tags and asks the tools what is out of
// date, and changes nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/flake"
	"github.com/elocke/piste/cli/internal/layer"
	"github.com/elocke/piste/cli/internal/outdated"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/release"
	"github.com/elocke/piste/cli/internal/ui"
	"github.com/elocke/piste/cli/internal/workspace"
)

func init() {
	register("pkgs", newPkgs)
}

// brewManifest is the Homebrew manifest a switch writes on a Mac: a var so
// tests can point it elsewhere.
var brewManifest = "/etc/piste/homebrew.json"

// pnpmTool is the switched profile's piste-pnpm, looked up in
// $HOME/.nix-profile/bin, /etc/profiles/per-user/$USER/bin, then PATH; ""
// when absent.
func (a *App) pnpmTool() string {
	var dirs []string
	if h := os.Getenv("HOME"); h != "" {
		dirs = append(dirs, filepath.Join(h, ".nix-profile", "bin"))
	}
	if u := os.Getenv("USER"); u != "" {
		dirs = append(dirs, filepath.Join("/etc/profiles/per-user", u, "bin"))
	}
	for _, d := range dirs {
		f := filepath.Join(d, "piste-pnpm")
		if st, err := os.Stat(f); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return f
		}
	}
	if f, err := exec.LookPath("piste-pnpm"); err == nil {
		return f
	}
	return ""
}

// pnpmOutdated lists the declared pnpm globals that have a newer version;
// declared is false when the profile has no piste-pnpm.
func (a *App) pnpmOutdated() (items []outdated.PnpmItem, declared bool, err error) {
	tool := a.pnpmTool()
	if tool == "" {
		return nil, false, nil
	}
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{tool, "outdated"}})
	if err != nil {
		// Its stderr line says why (pnpm missing, or no JSON from it).
		if why := strings.TrimPrefix(tailLine(res.Stderr), "piste-pnpm: "); why != "" {
			return nil, true, fmt.Errorf("piste-pnpm outdated failed: %s", why)
		}
		return nil, true, fmt.Errorf("piste-pnpm outdated failed: %w", err)
	}
	return outdated.ParsePnpmOutdated(string(res.Stdout)), true, nil
}

// pnpmDeclared is piste.packages.pnpm as the switched profile declares it
// (piste-pnpm declared), entries as written; nil without a piste-pnpm, or
// when it fails.
func (a *App) pnpmDeclared() []string {
	tool := a.pnpmTool()
	if tool == "" {
		return nil
	}
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{tool, "declared"}})
	if err != nil {
		return nil
	}
	return strings.Fields(string(res.Stdout))
}

// tailLine is b's last non-blank line, trimmed.
func tailLine(b []byte) string {
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(ls[len(ls)-1])
}

// brewState is what brewManifest says of this Mac's Homebrew.
type brewState int

const (
	brewNoList brewState = iota // no manifest yet: a switch writes it
	brewOff                     // homebrew.enable = false: not this config's to upgrade
	brewOn
)

// brewOffLine is the report's line for a Mac whose Homebrew is off.
const brewOffLine = "Homebrew: not managed by this machine's config"

// readBrewManifest is brewManifest and what it says. A manifest without
// "enabled" is an older switch's, from before Homebrew could be off: on.
func readBrewManifest() ([]byte, brewState) {
	b, err := os.ReadFile(brewManifest)
	if err != nil {
		return nil, brewNoList
	}
	var m struct {
		Enabled *bool `json:"enabled"`
	}
	if json.Unmarshal(b, &m) == nil && m.Enabled != nil && !*m.Enabled {
		return b, brewOff
	}
	return b, brewOn
}

// brewOutdated lists the declared Homebrew apps that have a newer version,
// when the manifest's state is brewOn (else none, and brew isn't asked).
// greedy also counts casks that update themselves.
func (a *App) brewOutdated(greedy bool) (items []outdated.BrewItem, state brewState, err error) {
	manifest, state := readBrewManifest()
	if state != brewOn {
		return nil, state, nil
	}
	argv := []string{"brew", "outdated", "--json=v2"}
	if greedy {
		argv = append(argv, "--greedy")
	}
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: argv})
	if err != nil {
		return nil, state, fmt.Errorf("brew outdated failed: %w", err)
	}
	m, err := outdated.BrewOutdated(manifest, res.Stdout)
	if err != nil {
		return nil, state, err
	}
	// A name declared by an alias or an old name (python, brew's
	// python@3.13) isn't the one brew outdated gives: brew info pairs the
	// leftovers, when there are some on both sides. Without it, the direct
	// matches stand.
	if m.Left() {
		info, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"brew", "info", "--json=v2", "--installed"}})
		if err == nil {
			if am, err := outdated.BrewAliases(m, info.Stdout); err == nil {
				m = am
			}
		}
	}
	return m.Items(), state, nil
}

func newPkgs(a *App) *cobra.Command {
	c := &cobra.Command{
		Use:   "pkgs",
		Short: "Show what's out of date, and who updates it",
		Long: "Show what is out of date on this machine, grouped by who updates it. What your\n" +
			"overlay owns, piste pkgs update moves; the engine's moves with piste releases\n" +
			"(piste update), and a team layer's is a PR to that layer. This only reads.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			a.pkgsReport(p)
			return nil
		},
	}
	c.AddCommand(newPkgsUpdate(a))
	return c
}

func (a *App) heading(title string) {
	fmt.Fprintln(a.Stdout)
	fmt.Fprintln(a.Stdout, ui.For(a.Stdout).Bold.Render(title))
}

// layerSource is the directory of a layer input's source: a path input under
// the overlay, else the store path nix prefetches. "" when it can't be
// resolved.
func (a *App) layerSource(flakeDir string, lock []byte, in outdated.Input) string {
	if in.Path != "" {
		if filepath.IsAbs(in.Path) {
			return in.Path
		}
		return filepath.Join(flakeDir, in.Path)
	}
	// Only a flake built on the engine (it has a piste input of its own) can
	// be a layer: nothing else is downloaded just to look for the marker.
	if in.Ref == "" || !lockHasPiste(lock, in.Name) {
		return ""
	}
	res, err := a.runner().Run(context.Background(), execx.Cmd{
		Argv: []string{"nix", "flake", "prefetch", "--json", in.Ref}, Env: a.gitSSHEnv(flakeDir)})
	if err != nil {
		return ""
	}
	var r struct {
		StorePath string `json:"storePath"`
	}
	if json.Unmarshal(res.Stdout, &r) != nil {
		return ""
	}
	return r.StorePath
}

// lockHasPiste: the lock node of root input name has an input called piste.
func lockHasPiste(lock []byte, name string) bool {
	var l struct {
		Nodes map[string]struct {
			Inputs map[string]json.RawMessage `json:"inputs"`
		} `json:"nodes"`
	}
	if json.Unmarshal(lock, &l) != nil {
		return false
	}
	var node string
	if json.Unmarshal(l.Nodes["root"].Inputs[name], &node) != nil {
		return false
	}
	_, ok := l.Nodes[node].Inputs[release.Input]
	return ok
}

// repoURL is where a PR to an input's source goes, from its locked ref.
func repoURL(in outdated.Input) string {
	ref := in.Ref
	forge := map[string]string{"github:": "https://github.com/", "gitlab:": "https://gitlab.com/", "sourcehut:": "https://git.sr.ht/"}
	for pre, base := range forge {
		if strings.HasPrefix(ref, pre) {
			parts := strings.Split(strings.TrimPrefix(ref, pre), "/")
			if len(parts) >= 2 {
				return base + parts[0] + "/" + parts[1]
			}
		}
	}
	if strings.HasPrefix(ref, "git+") {
		u := strings.TrimPrefix(ref, "git+")
		if i := strings.Index(u, "?"); i >= 0 {
			u = u[:i]
		}
		return u
	}
	return ref
}

// layerHeading titles a layer's section: a layer inside the overlay is a
// commit here, any other is a PR to its repository.
func layerHeading(in outdated.Input) string {
	switch {
	case in.Path != "":
		return fmt.Sprintf("Layer %s (in this overlay: %s)", in.Name, in.Path)
	case repoURL(in) != "":
		return fmt.Sprintf("Layer %s (a change is a PR to %s)", in.Name, repoURL(in))
	}
	return "Layer " + in.Name
}

func (a *App) pkgsReport(p platform.Platform) {
	s := ui.For(a.Stdout)
	flakeDir := p.FlakeDir
	var inputs []outdated.Input
	lock, lerr := os.ReadFile(filepath.Join(flakeDir, "flake.lock"))
	if lerr == nil {
		var err error
		inputs, err = outdated.DirectInputs(lock, func(n string) bool { return engineOwned(flakeDir, n) })
		if err != nil {
			a.warn("couldn't read flake.lock: " + err.Error())
			inputs = nil
		}
	} else {
		a.warn("no flake.lock in " + flakeDir + ": run piste build to make one")
	}
	flakeNix, _ := os.ReadFile(filepath.Join(flakeDir, "flake.nix"))
	fresh := strings.Join(outdated.FreshNames(string(flakeNix)), ", ")

	var mine, engine, layers []outdated.Input
	layerDirs := map[string]bool{}
	for _, in := range inputs {
		switch {
		case in.Name == release.Input:
		case in.Owner == outdated.OwnerEngine:
			engine = append(engine, in)
		case in.Flake && a.isLayer(flakeDir, lock, in):
			layers = append(layers, in)
			layerDirs[in.Name] = true
			mine = append(mine, in)
		default:
			mine = append(mine, in)
		}
	}

	// Yours.
	fmt.Fprintln(a.Stdout, s.Bold.Render("Yours (piste pkgs update moves these)"))
	if len(mine) > 0 {
		var rows [][]string
		for _, in := range mine {
			note := "-"
			switch {
			case in.Name == "nixpkgs-fresh" && fresh != "":
				note = "fresh: " + fresh
			case layerDirs[in.Name]:
				note = "a team layer: its newest commit"
			}
			rows = append(rows, []string{in.Name, lockDate(in), note})
		}
		fmt.Fprint(a.Stdout, s.Table([]string{"Input", "Locked", "Note"}, rows))
	} else if lerr == nil {
		fmt.Fprintln(a.Stdout, "No flake inputs of your own.")
	}
	fmt.Fprintln(a.Stdout)
	items, declared, err := a.pnpmOutdated()
	switch {
	case err != nil:
		a.warn("pnpm globals: " + err.Error())
	case !declared:
		fmt.Fprintln(a.Stdout, "pnpm globals: none declared (piste.packages.pnpm in home.nix)")
	case len(items) == 0:
		fmt.Fprintln(a.Stdout, "pnpm globals: up to date")
	default:
		var rows [][]string
		for _, it := range items {
			rows = append(rows, []string{it.Name, it.Current + " → " + it.Latest})
		}
		fmt.Fprintln(a.Stdout, "pnpm globals:")
		fmt.Fprint(a.Stdout, s.Table([]string{"Package", "Version"}, rows))
	}
	if p.IsDarwin {
		bi, state, err := a.brewOutdated(false)
		switch {
		case state == brewNoList:
		case state == brewOff:
			fmt.Fprintln(a.Stdout, brewOffLine)
		case err != nil:
			a.warn("Homebrew: " + err.Error())
		case len(bi) == 0:
			fmt.Fprintln(a.Stdout, "Homebrew: up to date")
		default:
			var rows [][]string
			for _, it := range bi {
				kind := "formula"
				if it.Cask {
					kind = "cask"
				}
				rows = append(rows, []string{it.Name, kind, it.Current + " → " + it.Latest})
			}
			fmt.Fprintln(a.Stdout, "Homebrew:")
			fmt.Fprint(a.Stdout, s.Table([]string{"App", "Kind", "Version"}, rows))
		}
	}

	// The engine.
	a.heading("The engine (moves with piste releases)")
	a.engineLines(flakeDir, string(flakeNix))
	if len(engine) > 0 {
		var rows [][]string
		for _, in := range engine {
			rows = append(rows, []string{in.Name, lockDate(in)})
		}
		fmt.Fprint(a.Stdout, s.Table([]string{"Input", "Locked"}, rows))
	}

	// Each layer.
	for _, l := range layers {
		a.heading(layerHeading(l))
		own, err := outdated.Unfollowed(lock, l.Name)
		switch {
		case err != nil:
			a.warn("couldn't read the layer's inputs: " + err.Error())
		case len(own) == 0:
			fmt.Fprintln(a.Stdout, "Every input follows yours.")
		default:
			var rows [][]string
			for _, in := range own {
				rows = append(rows, []string{in.Name, lockDate(in)})
			}
			fmt.Fprint(a.Stdout, s.Table([]string{"Input", "Locked"}, rows))
		}
	}
}

func (a *App) isLayer(flakeDir string, lock []byte, in outdated.Input) bool {
	src := a.layerSource(flakeDir, lock, in)
	return src != "" && layer.IsLayer(src)
}

func lockDate(in outdated.Input) string {
	if in.Modified == 0 {
		return "unknown"
	}
	return tsDate(in.Modified)
}

// engineLines says what the engine is at: the checkout's nixpkgs and its
// age, the checkout against origin (a release against the newest, a branch
// against its upstream), and this overlay's engine pin. Offline it says so
// and uses what the checkout has.
func (a *App) engineLines(flakeDir, flakeNix string) {
	root, err := checkoutRoot()
	if err != nil {
		a.info(err.Error())
		return
	}
	offline := false
	if _, _, err := gitCapture(root, a.gitSSHEnv(root), "fetch", "-q", "--tags", "origin"); err != nil {
		offline = true
		a.warn("couldn't reach origin: showing what this checkout has")
	}
	if lock, err := os.ReadFile(filepath.Join(root, "flake.lock")); err == nil {
		if ins, _ := outdated.DirectInputs(lock, nil); len(ins) > 0 {
			for _, in := range ins {
				if in.Name == "nixpkgs" && in.Modified != 0 {
					days := daysFloor(flake.Now().Sub(time.Unix(in.Modified, 0)))
					a.info(fmt.Sprintf("The checkout's nixpkgs is from %s (%d days old)", tsDate(in.Modified), days))
				}
			}
		}
	}
	tags, _, _ := gitCapture(root, nil, "tag", "-l")
	newest := release.Latest(strings.Fields(tags))
	b, _, _ := gitCapture(root, nil, "symbolic-ref", "-q", "--short", "HEAD")
	branch := strings.TrimSpace(b)
	yours := ""
	switch {
	case branch != "":
		line := fmt.Sprintf("piste: the checkout is on the branch %s", branch)
		if !offline {
			if n, _, err := gitCapture(root, nil, "rev-list", "--count", "HEAD..origin/"+branch); err == nil {
				switch c := strings.TrimSpace(n); c {
				case "0":
					line += ", up to date with origin"
				case "1":
					line += ", 1 commit behind origin (piste update pulls it)"
				default:
					line += fmt.Sprintf(", %s commits behind origin (piste update pulls them)", c)
				}
			}
		}
		a.info(line)
	default:
		yours = release.HeadRelease(root)
		switch {
		case yours == "":
			a.info("piste: the checkout is on neither a release nor a branch.")
		case newest != "" && release.Newer(newest, yours):
			a.info(fmt.Sprintf("piste: the checkout is on %s, newest %s (piste update moves it)", yours, newest))
		default:
			a.info(fmt.Sprintf("piste: the checkout is on %s, the newest release", yours))
		}
	}
	if _, in := workspace.OverlayOf(root, flakeDir); !in {
		return
	}
	url, ok := release.ReadPin(flakeNix)
	if !ok {
		return
	}
	pin := release.PinTag(url)
	switch {
	case pin == "":
		a.info("This overlay's engine pin is a branch or another ref, not a release: it moves with the checkout.")
	case newest != "" && release.Newer(newest, pin):
		a.info(fmt.Sprintf("This overlay's engine pin is %s, behind %s (piste pkgs update moves it).", pin, newest))
	case pin == yours:
		a.info(fmt.Sprintf("This overlay's engine pin is %s, the same as the checkout.", pin))
	default:
		a.info(fmt.Sprintf("This overlay's engine pin is %s.", pin))
	}
}
