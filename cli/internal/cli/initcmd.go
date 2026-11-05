package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/layer"
	"github.com/elocke/piste/cli/internal/overlay"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/workspace"
)

func init() { register("init", newInit) }

type initOpts struct {
	user, host, system, zone, gitName, gitEmail, engine string
	roles                                               []string
	sops, noBuild                                       bool
	// extras answers "Start minimal or with everything?" (--extras), the
	// one way to give it (extras isn't one of init's roles).
	extras bool
	// layer is a team layer's URL: the overlay starts from its starter, in
	// its zone. layerDir is a clone of it setup already made
	// (init uses it, and removes it).
	layer, layerDir string
}

// newInit is `piste init [name]`: a new overlay in
// overlays/<name>/ of this checkout, from the engine's templates/default.
func newInit(a *App) *cobra.Command {
	var o initOpts
	c := &cobra.Command{
		Use:   "init [name]",
		Short: "Create an overlay in this checkout from the engine's template",
		Long: "Create an overlay in overlays/<name>/ of this piste checkout from the engine's\n" +
			"templates/default, then build it. Each answer has a flag; a missing one is asked\n" +
			"for (Enter takes the default shown). The overlay is git-initialised with its\n" +
			"files staged; pushing it anywhere is up to you.\n\n" +
			"The last question, \"Start minimal or with everything?\", is --extras: everything\n" +
			"adds the extras role to the host (docs/customising.md, \"The extras pack\");\n" +
			"minimal, the default, is --extras=false.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, _, err := a.initOverlay(cmd.Flags().Changed, args, o)
			if errors.Is(err, errStopped) {
				a.errorf("Stopped: nothing written.")
				return ExitError{1}
			}
			return err
		},
	}
	f := c.Flags()
	f.StringVar(&o.user, "user", "", "your user on this machine (default $USER)")
	f.StringVar(&o.host, "host", "", "the host's name (default: this machine's)")
	f.StringVar(&o.system, "system", "", "one of aarch64-darwin, aarch64-linux or x86_64-linux (default: this machine's)")
	f.StringSliceVar(&o.roles, "role", nil, "a role: base, dev or desktop (repeatable; default dev on Linux, none on macOS; the extras role is --extras)")
	f.StringVar(&o.zone, "zone", "", "the overlay's agent zone (default personal)")
	f.StringVar(&o.gitName, "git-name", "", "your git name (default: git config --global user.name)")
	f.StringVar(&o.gitEmail, "git-email", "", "your git email (default: git config --global user.email)")
	f.StringVar(&o.engine, "engine", "", "the flake ref the overlay pins the engine at (default: the checkout's origin at its release)")
	f.BoolVar(&o.sops, "sops", false, "set up sops secrets: an age key (kept if there is one) and .sops.yaml")
	f.BoolVar(&o.extras, "extras", false, "start with everything: the extras role beside the others (default: minimal)")
	f.BoolVar(&o.noBuild, "no-build", false, "don't build it")
	f.StringVar(&o.layer, "layer", "", "a team layer's git URL: start the overlay from the layer's starter, in its zone")
	return c
}

// prompter asks one line per answer; Enter or EOF takes the default.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
	eof bool // the last read found the end of the input (Ctrl-D, a closed pipe)
}

// prompter is the App's one prompter: setup and init read the same stdin
// (two buffered readers of a pipe would take each other's lines).
func (a *App) prompter() *prompter {
	if a.pr == nil {
		a.pr = &prompter{out: a.Stdout}
		if a.Stdin != nil {
			a.pr.in = bufio.NewReader(a.Stdin)
		}
	}
	return a.pr
}

func (p *prompter) ask(label, def string) string {
	// Per read: on a terminal, Ctrl-D ends one read and the next waits again.
	p.eof = false
	if def == "" {
		fmt.Fprintf(p.out, "%s: ", label)
	} else {
		fmt.Fprintf(p.out, "%s [%s]: ", label, def)
	}
	if p.in == nil {
		p.eof = true
		fmt.Fprintln(p.out)
		return def
	}
	line, err := p.in.ReadString('\n')
	if err != nil && line == "" {
		p.eof = true
		fmt.Fprintln(p.out)
	}
	if line = strings.TrimSpace(line); line != "" {
		return line
	}
	return def
}

// askCheck asks until check passes, saying why an answer didn't; at the end
// of the input the last error stands (a bad answer then Ctrl-D is not the
// default).
func (p *prompter) askCheck(label, def string, check func(string) error) (string, error) {
	var prev error
	for {
		v := p.ask(label, def)
		if p.eof && prev != nil {
			return v, prev
		}
		err := check(v)
		if err == nil || p.eof {
			return v, err
		}
		fmt.Fprintf(p.out, "  %v\n", err)
		prev = err
	}
}

func gitOut(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// splitRoles: "dev, desktop" or "dev desktop"; "none" is no roles.
func splitRoles(s string) []string {
	var out []string
	for _, r := range strings.FieldsFunc(s, func(c rune) bool { return c == ',' || c == ' ' }) {
		if r != "none" {
			out = append(out, r)
		}
	}
	return out
}

// initOverlay is `piste init`: the overlay's name, and whether it was built
// (it isn't with --no-build, or for another machine's system).
func (a *App) initOverlay(changed func(string) bool, args []string, o initOpts) (name string, built bool, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, err
	}
	root, ok := workspace.FindCheckout(cwd)
	if !ok {
		a.errorf("piste init: run inside a piste checkout (clone the engine, then run piste init there)")
		return "", false, ExitError{1}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	here, err := platform.Detect(platform.Host())
	if err != nil {
		return "", false, err
	}
	overlays := filepath.Join(root, "overlays")
	checkName := func(n string) error {
		if err := overlay.ValidName(n); err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(overlays, n)); err == nil {
			return fmt.Errorf("overlays/%s exists", n)
		}
		return nil
	}
	// What can be refused without an answer is, before the first question.
	if len(args) == 1 {
		if err := checkName(args[0]); err != nil {
			a.errorf(err.Error())
			return "", false, ExitError{1}
		}
	}
	var ans overlay.Answers
	if changed("engine") {
		ans.Engine = o.engine
	} else {
		tag := gitOut(root, "describe", "--exact-match", "--tags", "HEAD")
		if ans.Engine, err = overlay.EngineRef(gitOut(root, "remote", "get-url", "origin"), tag, gitOut(root, "rev-parse", "HEAD")); err != nil {
			a.errorf(err.Error())
			return "", false, ExitError{1}
		}
	}

	// A team layer: its starter is the template, its zone is the overlay's,
	// and its secret names make the sops skeleton. Read before any question.
	tmpl := filepath.Join(root, "templates", "default")
	var meta layer.Meta
	if o.layer != "" {
		dir, m, ref, cleanup, err := a.initFromLayer(o.layer, o.layerDir)
		if err != nil {
			a.errorf(err.Error())
			return "", false, ExitError{1}
		}
		defer cleanup()
		if changed("zone") && o.zone != m.Zone {
			a.errorf(fmt.Sprintf("the team layer is in zone %s: an overlay on it can't be in zone %s (--zone)", m.Zone, o.zone))
			return "", false, ExitError{1}
		}
		tmpl, meta, ans.Layer = dir, m, ref
	}

	// Flag values that don't hang on another answer, before the first
	// question (the hostname's macOS rule needs the system: with --system
	// here, else when it is answered).
	hostSys := ""
	if changed("system") {
		hostSys = o.system
	}
	for _, f := range []struct {
		flag string
		err  func() error
	}{
		{"user", func() error { return overlay.CheckUser(o.user) }},
		{"system", func() error { return overlay.CheckSystem(o.system) }},
		{"host", func() error { return overlay.CheckHostname(o.host, hostSys) }},
		{"role", func() error { return overlay.CheckInitRoles(splitRoles(strings.Join(o.roles, ","))) }},
		{"zone", func() error { return overlay.CheckZone(o.zone) }},
		{"git-name", func() error { return overlay.CheckGit("git name", o.gitName) }},
		{"git-email", func() error { return overlay.CheckGit("git email", o.gitEmail) }},
	} {
		if changed(f.flag) {
			if err := f.err(); err != nil {
				a.errorf(fmt.Sprintf("%v (--%s)", err, f.flag))
				return "", false, ExitError{1}
			}
		}
	}

	// "Start minimal or with everything?": answered by --extras alone
	// (Render puts the extras role after the others).
	extrasGiven := changed("extras")
	ans.Extras = o.extras
	flagRoles := splitRoles(strings.Join(o.roles, ","))

	// At a terminal, the questions are a form (initform.go); else one line
	// each, as below.
	var bad error
	if a.useForms() {
		ans.User = flagOr(changed("user"), o.user, a.Env.Getenv("USER"))
		ans.System = flagOr(changed("system"), o.system, here.System)
		ans.Hostname = flagOr(changed("host"), o.host, platform.ShortHostname())
		ans.Roles = flagRoles
		if !changed("role") && !overlay.Darwin(ans.System) {
			ans.Roles = []string{"dev"}
		}
		ans.Zone = flagOr(changed("zone"), o.zone, "personal")
		if o.layer != "" {
			ans.Zone = meta.Zone
		}
		if len(args) == 1 {
			ans.Name = args[0]
		}
		ans.GitName = flagOr(changed("git-name"), o.gitName, gitOut(cwd, "config", "--global", "user.name"))
		ans.GitEmail = flagOr(changed("git-email"), o.gitEmail, gitOut(cwd, "config", "--global", "user.email"))
		ans.Sops = o.sops
		if o.layer != "" && !changed("sops") {
			ans.Sops = len(meta.Secrets) > 0
		}
		q := initQs{user: !changed("user"), system: !changed("system"), host: !changed("host"), roles: !changed("role"),
			zone: !changed("zone") && o.layer == "", name: len(args) == 0, gitName: !changed("git-name"), gitEmail: !changed("git-email"), sops: !changed("sops"),
			extras: !extrasGiven}
		if err := a.initForm(&ans, q, checkName); err != nil {
			return "", false, err
		}
	} else {
		// A flag's value is checked (again: the hostname with the system); a
		// typed answer is asked again until it passes. After a refusal nothing
		// more is asked.
		p := a.prompter()
		pick := func(flag, val, label, def string, check func(string) error) string {
			if bad != nil {
				return ""
			}
			if changed(flag) {
				if err := check(val); err != nil {
					bad = fmt.Errorf("%w (--%s)", err, flag)
				}
				return val
			}
			v, err := p.askCheck(label, def, check)
			bad = err
			return v
		}
		ans.User = pick("user", o.user, "User", a.Env.Getenv("USER"), overlay.CheckUser)
		ans.System = pick("system", o.system, "System", here.System, overlay.CheckSystem)
		ans.Hostname = pick("host", o.host, "Hostname", platform.ShortHostname(),
			func(h string) error { return overlay.CheckHostname(h, ans.System) })
		roleDef := ""
		if !overlay.Darwin(ans.System) {
			roleDef = "dev"
		}
		ans.Roles = splitRoles(pick("role", strings.Join(flagRoles, ","), "Roles (base, dev, desktop, or none; the macOS platform brings desktop)", roleDef,
			func(s string) error { return overlay.CheckInitRoles(splitRoles(s)) }))
		if o.layer != "" {
			ans.Zone = meta.Zone
		} else {
			ans.Zone = pick("zone", o.zone, "Agent zone", "personal", overlay.CheckZone)
		}
		if len(args) == 1 {
			ans.Name = args[0]
		} else if bad == nil {
			ans.Name, bad = p.askCheck("Overlay name (its directory in overlays/)", ans.Zone, checkName)
		}
		ans.GitName = pick("git-name", o.gitName, "Git name", gitOut(cwd, "config", "--global", "user.name"),
			func(v string) error { return overlay.CheckGit("git name", v) })
		ans.GitEmail = pick("git-email", o.gitEmail, "Git email", gitOut(cwd, "config", "--global", "user.email"),
			func(v string) error { return overlay.CheckGit("git email", v) })
		if bad == nil {
			if changed("sops") {
				ans.Sops = o.sops
			} else {
				def := "n"
				if len(meta.Secrets) > 0 {
					def = "y" // the team layer names secrets
				}
				ans.Sops = strings.HasPrefix(strings.ToLower(p.ask("Set up sops secrets (an age key and .sops.yaml)? y/n", def)), "y")
			}
		}
		if bad == nil && !extrasGiven {
			var v string
			v, bad = p.askCheck("Start minimal or with everything (the extras role: more tools, aliases and settings)? minimal/everything", "minimal", checkStart)
			ans.Extras = startEverything(v)
		}
	}
	if bad == nil {
		bad = overlay.Validate(ans)
	}
	if bad != nil {
		a.errorf(bad.Error())
		return "", false, ExitError{1}
	}
	ans.Checkout = overlay.CheckoutExpr(root, home)
	ans.Home = linuxHome(ans.User, a.Env.Getenv("USER"), home, here.System)

	dst := filepath.Join(overlays, ans.Name)
	if err := overlay.Render(tmpl, dst, ans); err != nil {
		a.errorf(err.Error())
		return "", false, ExitError{1}
	}
	// From here a failure takes the overlay away again: it is what this run
	// wrote, and a re-run can then make it (an age key made stays).
	unmake := func(err error) (string, bool, error) {
		if os.RemoveAll(dst) == nil {
			a.warn(fmt.Sprintf("overlays/%s removed: init didn't finish", ans.Name))
		}
		return "", false, err
	}
	if err := a.gitIn(dst, "init", "-q", "-b", "main"); err != nil {
		return unmake(err)
	}
	if ans.Sops {
		if err := a.ensureSops(home, dst, ans.Hostname); err != nil {
			if _, ok := err.(ExitError); !ok {
				a.errorf(err.Error())
			}
			return unmake(ExitError{1})
		}
		if len(meta.Secrets) > 0 {
			if err := secretsSkeleton(home, dst, meta.Secrets); err != nil {
				a.errorf(err.Error())
				return unmake(ExitError{1})
			}
		}
	} else if len(meta.Secrets) > 0 {
		// No sops yet: the names still land, empty and unencrypted, with how
		// to fill them in later (secrets edit encrypts the file first).
		if err := writeSkeleton(dst, append([]byte(skeletonLater), layer.Skeleton(meta.Secrets)...)); err != nil {
			return unmake(err)
		}
	}
	// Builds see tracked files only: stage everything written here.
	if err := a.gitIn(dst, "add", "-A"); err != nil {
		return unmake(err)
	}
	a.success(fmt.Sprintf("Created overlays/%s (host %s, zone %s), its files staged", ans.Name, overlay.HostKey(ans), ans.Zone))
	if ans.Layer != "" {
		a.info("On the team layer " + ans.Layer)
	}
	a.info("Engine pinned at " + ans.Engine)
	if o.noBuild {
		return ans.Name, false, nil
	}
	if ans.System != here.System {
		a.info(fmt.Sprintf("Not building: %s isn't this machine (%s); build it there with piste build --overlay %s", ans.System, here.System, ans.Name))
		return ans.Name, false, nil
	}
	a.Overlay = ans.Name
	if err := a.runBuild(ans.Hostname); err != nil {
		a.warn(fmt.Sprintf("overlays/%s was created; fix the build and run: %s build --overlay %s", ans.Name, a.piste(), ans.Name))
		return ans.Name, false, err
	}
	return ans.Name, true, nil
}

// checkStart: an answer to "Start minimal or with everything?" (a prefix of
// either will do).
func checkStart(s string) error {
	if s = strings.ToLower(strings.TrimSpace(s)); s != "" && (strings.HasPrefix("minimal", s) || strings.HasPrefix("everything", s)) {
		return nil
	}
	return fmt.Errorf("%q: minimal or everything", s)
}

// startEverything: the answer, checked, is everything.
func startEverything(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s != "" && strings.HasPrefix("everything", s)
}

// flagOr is a flag's value when it was given, else def.
func flagOr(given bool, v, def string) string {
	if given {
		return v
	}
	return def
}

// linuxHome is a Linux host's home.homeDirectory: this user's home as it is
// here, when here is Linux too; else the usual place.
func linuxHome(user, me, home, here string) string {
	if user == me && !overlay.Darwin(here) {
		return home
	}
	return "/home/" + user
}

// gitIn runs git in dir, its output passed through.
func (a *App) gitIn(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, a.Stdout, a.Stderr
	if err := cmd.Run(); err != nil {
		a.errorf(fmt.Sprintf("git %s in %s: %v", strings.Join(args, " "), dir, err))
		return ExitError{1}
	}
	return nil
}

// skeletonPath is where an overlay on a team layer keeps its secrets' values,
// relative to the overlay (the starter's sopsFile).
const skeletonPath = "secrets/env/api-keys.sops.yaml"

// skeletonLater heads the skeleton written without sops.
const skeletonLater = `# Your secrets' values, named by the team layer. Not encrypted yet: never put
# a value here by hand. Fill them in with piste secrets init (once), then
# piste secrets edit secrets/env/api-keys.sops.yaml, which encrypts this file
# before opening it; then set piste.secrets.enable = true in your host file.
`

// writeSkeleton writes the overlay's sops file (mode 0600).
func writeSkeleton(dst string, b []byte) error {
	path := filepath.Join(dst, skeletonPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// initFromLayer reads a team layer's marker and starter from a clone: the
// one in have (setup's), else a shallow clone into a temporary directory.
// It returns the starter's directory, the layer's meta and flake ref, and a
// cleanup that removes the clone.
func (a *App) initFromLayer(url, have string) (tmpl string, meta layer.Meta, ref string, cleanup func(), err error) {
	cleanup = func() {}
	if have != "" {
		cleanup = func() { os.RemoveAll(have) }
	}
	if ref, err = layer.Ref(url); err != nil {
		cleanup()
		return "", meta, "", func() {}, err
	}
	dir := have
	if dir == "" {
		tmp, err := os.MkdirTemp("", "piste-layer-")
		if err != nil {
			return "", meta, "", cleanup, err
		}
		cleanup = func() { os.RemoveAll(tmp) }
		dir = filepath.Join(tmp, "layer")
		clone := exec.Command("git", "clone", "-q", "--depth", "1", strings.TrimPrefix(url, "git+"), dir)
		var stderr strings.Builder
		clone.Stderr = &stderr
		if clone.Run() != nil {
			a.githubCloneHint(url)
			cleanup()
			return "", meta, "", func() {}, fmt.Errorf("git clone %s: %s", url, gitFailure(stderr.String()))
		}
	}
	fail := func(err error) (string, layer.Meta, string, func(), error) {
		cleanup()
		return "", layer.Meta{}, "", func() {}, err
	}
	if !layer.IsLayer(dir) {
		return fail(fmt.Errorf("%s is not a team layer (no %s at its root)", url, layer.Marker))
	}
	if meta, err = layer.Read(dir); err != nil {
		return fail(fmt.Errorf("%s: %w", url, err))
	}
	tmpl = filepath.Join(dir, layer.Template)
	if _, err := os.Stat(filepath.Join(tmpl, "flake.nix")); err != nil {
		return fail(fmt.Errorf("%s: the team layer has no starter overlay (%s/flake.nix)", url, layer.Template))
	}
	return tmpl, meta, ref, cleanup, nil
}

// secretsSkeleton writes the team layer's secret names, empty, to the
// overlay's sops file and encrypts it in place with the overlay's .sops.yaml.
func secretsSkeleton(home, dst string, secrets map[string]string) error {
	if err := writeSkeleton(dst, layer.Skeleton(secrets)); err != nil {
		return err
	}
	c := exec.Command("sops", "-e", "-i", skeletonPath)
	c.Dir, c.Env = dst, sopsEnv(home)
	var stderr strings.Builder
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("sops couldn't encrypt %s: %s", skeletonPath, strings.TrimSpace(stderr.String()))
	}
	return nil
}
