package cli

// The read-only commands. The argv to nix / darwin-rebuild /
// home-manager / nh / git / ssh, the exit codes and the messages are pinned
// by cli/e2e/testdata/readonly.txtar; tables are plain aligned text.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/config"
	"github.com/elocke/piste/cli/internal/drift"
	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/flake"
	"github.com/elocke/piste/cli/internal/overlay"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/ui"
	"github.com/elocke/piste/cli/internal/workspace"
)

func init() {
	register("check", newCheck)
	register("dry", newDry)
	register("build", newBuild)
	register("diff", newDiff)
	register("log", newLog)
	register("gendiff", newGendiff)
	register("status", newStatus)
	register("drift", newDrift)
	register("remote", newRemote)
}

// plat is the machine and the flake piste acts on (in a piste checkout, the
// overlay workspace.Resolve picks). A command that needs the flake fails
// when none resolves; resolution notes are shown as warnings.
func (a *App) plat() (platform.Platform, error) {
	p, err := a.machine()
	if err != nil {
		return p, err
	}
	if p.FlakeErr != nil {
		a.errorf(p.FlakeErr.Error())
		return p, ExitError{1}
	}
	for _, w := range p.Warnings {
		a.warn(w)
	}
	return p, nil
}

// machine is plat for commands that don't need the flake (log, rollback).
func (a *App) machine() (platform.Platform, error) {
	env := platform.Host()
	env.Overlay = a.Overlay
	env.Hosts = a.overlayHosts
	return platform.Detect(env)
}

// stderrTail: the last n lines of a command's stderr that aren't nix
// warnings, joined, for an error message.
func stderrTail(b []byte, n int) string {
	var keep []string
	var indent []int
	for _, r := range strings.Split(string(b), "\n") {
		if l := strings.TrimSpace(r); l != "" && !strings.HasPrefix(l, "warning:") {
			keep = append(keep, l)
			indent = append(indent, len(r)-len(strings.TrimLeft(r, " \t")))
		}
	}
	// Nix's message is its last "error: <message>" line, then where: the
	// "at <file>:<line>:<col>:" after it, else the files of the option's
	// definitions it lists ("- In `<file>'", values dropped), else the
	// nearest trace frame before it, unless that is in a locked input («…»)
	// or the message runs on (an untracked file's git command): a frame
	// before is where nix threw, often in nixpkgs' lib. The code excerpt,
	// the trace and a download's response body are left to `piste build`.
	for i := len(keep) - 1; i >= 0; i-- {
		if msg, ok := strings.CutPrefix(keep[i], "error:"); !ok || strings.TrimSpace(msg) == "" {
			continue
		}
		at, rest, defs := "", []string{}, false
		inDef, defIndent := false, 0
		for k := i + 1; k < len(keep); k++ {
			l := keep[k]
			if l == "response body:" {
				break
			}
			if inDef && indent[k] > defIndent { // a definition's value
				continue
			}
			inDef = false
			if m := nixDef.FindStringSubmatch(l); m != nil {
				inDef, defIndent, defs = true, indent[k], true
				rest = append(rest, "in "+shortStorePath(m[1])+strings.TrimSuffix(m[2], ":"))
			} else if nixAt.MatchString(l) {
				if at == "" {
					at = l
				}
			} else if !nixExcerpt.MatchString(l) {
				rest = append(rest, l)
			}
		}
		if at == "" && !defs && len(rest) < n-1 {
			for j := i - 1; j >= 0; j-- {
				if nixAt.MatchString(keep[j]) {
					if !strings.HasPrefix(keep[j], "at «") {
						at = keep[j]
					}
					break
				}
			}
		}
		out := []string{keep[i]}
		if at != "" {
			out = append(out, "at "+shortStorePath(strings.TrimSuffix(strings.TrimPrefix(at, "at "), ":")))
		}
		for _, l := range rest {
			if len(out) >= n {
				break
			}
			out = append(out, l)
		}
		return strings.Join(out, " / ")
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, " / ")
}

// shortStorePath is a path in a flake source's store path as the source's
// own (hosts/box.nix); any other path as it is.
func shortStorePath(p string) string { return nixStorePrefix.ReplaceAllString(p, "") }

var (
	// nixExcerpt: a line of the code Nix quotes with an error, or of its trace.
	nixExcerpt = regexp.MustCompile(`^(\d*\s*\||… )`)
	// nixAt: where, "at <file>:<line>:<col>:".
	nixAt = regexp.MustCompile(`^at \S+:\d+:\d+:$`)
	// nixDef: one of the definitions nix lists for an option, "- In
	// `<file>'", then ": <value>", or ":" and the value on lines indented
	// further.
	nixDef = regexp.MustCompile("^- In `(.+)'(:.*)?$")
	// nixStorePrefix: a flake source's store path, before its own path.
	nixStorePrefix = regexp.MustCompile(`^/nix/store/[0-9a-z]{32}-[^/]+/`)
)

// overlayHosts evaluates an overlay's host names against the engine of the
// checkout it is in, for workspace.Resolve at a checkout root.
func (a *App) overlayHosts(dir string) (workspace.Hosts, error) {
	if err := a.checkNix(); err != nil {
		return workspace.Hosts{}, err
	}
	root := filepath.Dir(filepath.Dir(dir))
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{
		"nix", "eval", "--json", "--override-input", "piste", workspace.OverrideRef(root), dir + "#.",
		"--apply", "o: { darwin = builtins.attrNames (o.darwinConfigurations or { }); home = builtins.attrNames (o.homeConfigurations or { }); " +
			"nixos = builtins.attrNames (o.nixosConfigurations or { }); }"}})
	if err != nil {
		if t := stderrTail(res.Stderr, 3); t != "" {
			return workspace.Hosts{}, fmt.Errorf("nix eval of its hosts failed: %s", t)
		}
		return workspace.Hosts{}, fmt.Errorf("nix eval of its hosts failed: %w", err)
	}
	var h struct {
		Darwin []string `json:"darwin"`
		Home   []string `json:"home"`
		NixOS  []string `json:"nixos"`
	}
	if err := json.Unmarshal(res.Stdout, &h); err != nil {
		return workspace.Hosts{}, fmt.Errorf("nix eval of its hosts: %w", err)
	}
	return workspace.Hosts{Darwin: h.Darwin, Home: h.Home, NixOS: h.NixOS}, nil
}

// hostRoles evaluates the roles each of an overlay's hosts of one kind
// has: the piste.roles.<role> options its home declares (on a Mac or NixOS,
// user's home in each system). A role a host's own modules import counts,
// and a Mac's platform roles too (overlay.SharedRoles writes them short).
func (a *App) hostRoles(dir, user string, kind Kind) (map[string][]string, error) {
	root := filepath.Dir(filepath.Dir(dir))
	expr := `o: { roles = builtins.mapAttrs (_: c: builtins.attrNames (c.config.piste.roles or { })) (o.homeConfigurations or { }); }`
	if kind != KindHome {
		systems := "darwinConfigurations"
		if kind == KindNixOS {
			systems = "nixosConfigurations"
		}
		expr = `o: { roles = builtins.mapAttrs (_: c: builtins.attrNames (c.config.home-manager.users."` + overlay.NixString(user) +
			`".piste.roles or { })) (o.` + systems + ` or { }); }`
	}
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{
		"nix", "eval", "--json", "--override-input", "piste", workspace.OverrideRef(root), dir + "#.", "--apply", expr}})
	if err != nil {
		if t := stderrTail(res.Stderr, 3); t != "" {
			return nil, fmt.Errorf("nix eval failed: %s", t)
		}
		return nil, fmt.Errorf("nix eval failed: %w", err)
	}
	var r struct {
		Roles map[string][]string `json:"roles"`
	}
	if err := json.Unmarshal(res.Stdout, &r); err != nil {
		return nil, fmt.Errorf("nix eval of the roles: %w", err)
	}
	return r.Roles, nil
}

func (a *App) info(msg string)    { a.say(a.Stdout, msg, "info") }
func (a *App) success(msg string) { a.say(a.Stdout, msg, "ok") }
func (a *App) warn(msg string)    { a.say(a.Stdout, msg, "warn") }
func (a *App) errorf(msg string)  { a.say(a.Stderr, msg, "err") }

func (a *App) say(w io.Writer, msg, kind string) {
	if rec := a.teeing(); rec != nil {
		fmt.Fprintln(rec, msg)
	}
	s := ui.For(w)
	switch kind {
	case "ok":
		msg = s.OK.Render(msg)
	case "warn":
		msg = s.Warn.Render(msg)
	case "err":
		msg = s.Err.Render(msg)
	}
	fmt.Fprintln(w, msg)
}

// checkNix is _check_nix: fail legibly when nix is absent.
func (a *App) checkNix() error {
	if err := platform.CheckNix(nil); err != nil {
		fmt.Fprintln(a.Stderr, err)
		return ExitError{1}
	}
	return nil
}

// darwinRebuildArgv is how to run darwin-rebuild: itself when it is on PATH;
// before nix-darwin's first switch installs it, the checkout's engine's
// (packages.<darwin>.darwin-rebuild) through nix, by nix's absolute path so
// sudo finds it. ok is false when there's neither.
func darwinRebuildArgv(onPath bool, nix, engineRef string) (argv []string, ok bool) {
	if onPath {
		return []string{"darwin-rebuild"}, true
	}
	if nix == "" || engineRef == "" {
		return nil, false
	}
	return []string{nix, "run", engineRef + "#darwin-rebuild", "--"}, true
}

// darwinRebuild is darwinRebuildArgv for this machine and checkout.
func (a *App) darwinRebuild(p platform.Platform) ([]string, error) {
	_, err := exec.LookPath("darwin-rebuild")
	nix, _ := exec.LookPath("nix")
	ref := ""
	if p.Checkout != "" {
		ref = workspace.OverrideRef(p.Checkout)
	}
	argv, ok := darwinRebuildArgv(err == nil, nix, ref)
	if !ok {
		return nil, a.checkDarwinRebuild()
	}
	if err != nil {
		a.info("darwin-rebuild isn't installed yet (nix-darwin's first switch installs it): running the checkout's")
	}
	return argv, nil
}

// checkDarwinRebuild is _check_darwin_rebuild.
func (a *App) checkDarwinRebuild() error {
	if _, err := exec.LookPath("darwin-rebuild"); err != nil {
		fmt.Fprintln(a.Stderr, "darwin-rebuild not found on PATH.\n"+
			"This is provided by nix-darwin system activation, not by nixpkgs.\n"+
			"Ensure nix-darwin is bootstrapped: https://github.com/nix-darwin/nix-darwin#install")
		return ExitError{1}
	}
	return nil
}

// pass runs argv with the terminal inherited and returns its exit code.
func (a *App) pass(argv []string, dir string) (int, error) {
	if argv[0] == "nix" {
		if err := a.checkNix(); err != nil {
			return 1, err
		}
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = dir
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, a.Stdout, a.Stderr
	// During a switch, its output goes into the switch's log too
	// (switchlog.go): through a pipe, so nix prints plain lines.
	if rec := a.teeing(); rec != nil {
		c.Stdout, c.Stderr = io.MultiWriter(a.Stdout, rec), io.MultiWriter(a.Stderr, rec)
	}
	err := c.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &ee):
		return ee.ExitCode(), nil
	}
	fmt.Fprintf(a.Stderr, "piste: %s: %v\n", argv[0], err)
	return 1, ExitError{1}
}

// runner captures output; --verbose only adds
// --show-trace to builds (no argv echo).
func (a *App) runner() execx.Runner {
	if a.execRunner != nil {
		return a.execRunner
	}
	return execx.Exec{}
}

func (a *App) buildArgs(argv []string) []string {
	if a.Verbose {
		return append(argv, "--show-trace")
	}
	return argv
}

// --- check -------------------------------------------------------------------

func newCheck(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Fast syntax/eval check (no build)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			o, err := a.engineOverride(p)
			if err != nil {
				return err
			}
			a.announceFlake(p, &o)
			a.info("Checking flake...")
			rc, err := a.pass(append([]string{"nix", "flake", "check", p.FlakeDir, "--no-build"}, o.Args()...), "")
			if err != nil {
				return err
			}
			if rc != 0 {
				a.errorf(fmt.Sprintf("Flake check failed (exit code %d)", rc))
				return ExitError{1}
			}
			a.success("Flake check passed.")
			return nil
		},
	}
}

// --- dry / build -------------------------------------------------------------------

// hostFlags is --host, the one way to pick a host other than this machine's:
// its name in the overlay's hosts (on Linux the user is the overlay's, else $USER).
func hostFlags(c *cobra.Command, host *string) {
	c.Flags().StringVar(host, "host", "", "Act on another host of the overlay, by hostname (default: this machine's)")
	c.PreRunE = func(*cobra.Command, []string) error {
		if strings.Contains(*host, "@") {
			return fmt.Errorf("--host takes a hostname, not user@hostname: %q", *host)
		}
		return nil
	}
}

// profileUser is the user of the overlay's home configurations: the user in
// its flake.nix, else $USER.
func profileUser(p platform.Platform) string {
	if u := flakeUser(p.FlakeDir); u != "" {
		return u
	}
	return p.User
}

// flakeUser is the user in the flake.nix in dir, "" when there is none.
func flakeUser(dir string) string {
	if b, err := os.ReadFile(filepath.Join(dir, "flake.nix")); err == nil {
		if u, ok := overlay.FlakeUser(b); ok {
			return u
		}
	}
	return ""
}

// buildCmd is the build piste build, dry and fix run: t's configuration,
// against the checkout's engine as switch builds it. A Mac's is
// darwin-rebuild's (its own pick of this machine's, without --host); a
// NixOS host's, its system's toplevel; a home's, its activation package.
func (a *App) buildCmd(p platform.Platform, t target, dry bool, o platform.EngineOverride) ([]string, error) {
	var argv []string
	switch t.Kind {
	case KindDarwin:
		rebuild, err := a.darwinRebuild(p)
		if err != nil {
			return nil, err
		}
		ref := p.FlakeDir
		if t.Host != "" {
			ref += "#" + t.Name
		}
		argv = append(rebuild, "build", "--flake", ref)
	case KindNixOS:
		argv = []string{"nix", "build", p.FlakeDir + `#nixosConfigurations."` + t.Name + `".config.system.build.toplevel`}
	default:
		// Quoted: a profile with a dot (jane.doe@box.lan) is one attribute name.
		argv = []string{"nix", "build", p.FlakeDir + `#homeConfigurations."` + t.Name + `".activationPackage`}
	}
	if dry {
		argv = append(argv, "--dry-run")
	}
	return append(a.buildArgs(argv), o.Args()...), nil
}

func newDry(a *App) *cobra.Command {
	var host string
	c := &cobra.Command{
		Use:   "dry",
		Short: "Dry run: show what would change",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			o, err := a.engineOverride(p)
			if err != nil {
				return err
			}
			a.announceFlake(p, &o)
			a.info("Dry run (showing what would change)...")
			t, err := a.targetFor(p, host)
			if err != nil {
				return err
			}
			argv, err := a.buildCmd(p, t, true, o)
			if err != nil {
				return err
			}
			rc, err := a.pass(argv, "")
			if err != nil {
				return err
			}
			if rc != 0 {
				a.errorf(fmt.Sprintf("Dry run failed (exit code %d)", rc))
				if !a.Verbose {
					a.warn("Rerun with --verbose for --show-trace output")
				}
				return ExitError{1}
			}
			return nil
		},
	}
	hostFlags(c, &host)
	return c
}

// stalePanel is print_staleness_warning.
func (a *App) stalePanel(w string) {
	if w == "" {
		return
	}
	s := ui.For(a.Stdout)
	fmt.Fprintln(a.Stdout)
	fmt.Fprint(a.Stdout, s.Panel("Stale nixpkgs", s.Warn.Render(w)))
	fmt.Fprintln(a.Stdout)
}

func newBuild(a *App) *cobra.Command {
	var host string
	c := &cobra.Command{
		Use:   "build",
		Short: "Build configuration without applying",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.runBuild(host)
		},
	}
	hostFlags(c, &host)
	return c
}

// runBuild is `piste build` (also run by `piste init` and `add`): host is the
// hostname to build, "" for this machine's.
func (a *App) runBuild(host string) error {
	p, err := a.plat()
	if err != nil {
		return err
	}
	if err := a.refusePlaintextSecrets(p.FlakeDir, a.piste()+" build"); err != nil {
		return err
	}
	a.stalePanel(flake.StaleWarning(p.FlakeDir))
	o, err := a.engineOverride(p)
	if err != nil {
		return err
	}
	a.announceFlake(p, &o)
	a.info("Building...")
	t, err := a.targetFor(p, host)
	if err != nil {
		return err
	}
	argv, err := a.buildCmd(p, t, false, o)
	if err != nil {
		return err
	}
	rc, err := a.pass(argv, "")
	if err != nil {
		return err
	}
	if rc != 0 {
		a.errorf(fmt.Sprintf("Build failed (exit code %d)", rc))
		if !a.Verbose {
			a.warn("Rerun with: " + a.piste() + " --verbose build")
		}
		a.fixHint(host)
		return ExitError{1}
	}
	// No quotes around the command: on a fresh NixOS it has its own.
	a.success("Build complete. To apply: " + a.piste() + " switch")
	return nil
}

// --- diff / log / gendiff -----------------------------------------------------------

func newDiff(a *App) *cobra.Command {
	return &cobra.Command{
		Use:    "diff",
		Hidden: true, // maintainer tool
		Short:  "Show uncommitted git changes",
		Args:   cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			rc, err := a.pass([]string{"git", "diff"}, p.FlakeDir)
			if err != nil {
				return err
			}
			if rc != 0 {
				return ExitError{1}
			}
			return nil
		},
	}
}

func newLog(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "log",
		Short: "Show recent configuration generations",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.machine()
			if err != nil {
				return err
			}
			kind, err := a.generationsKind(p)
			if err != nil {
				return err
			}
			a.info("Recent generations:")
			if kind == KindHome {
				_, err := a.pass([]string{"home-manager", "generations"}, "")
				return err
			}
			if kind == KindDarwin {
				res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"darwin-rebuild", "--list-generations"}})
				if err == nil && strings.TrimSpace(string(res.Stdout)) != "" {
					lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
					if len(lines) > 10 {
						lines = lines[len(lines)-10:]
					}
					for _, l := range lines {
						fmt.Fprintln(a.Stdout, l)
					}
					return nil
				}
			}
			// NixOS, and a Mac without darwin-rebuild's list: the system
			// profile's links.
			links, _ := filepath.Glob(platform.SystemProfile + "-*-link")
			sort.Strings(links)
			if len(links) > 10 {
				links = links[len(links)-10:]
			}
			for _, l := range links {
				fmt.Fprintln(a.Stdout, l)
			}
			return nil
		},
	}
}

// engineOverride is the local engine for
// --override-input. A bad $PISTE_ENGINE ends the command, as SystemExit does.
func (a *App) engineOverride(p platform.Platform) (platform.EngineOverride, error) {
	o, err := platform.ResolveEngineOverride(p.FlakeDir, a.Env.Getenv, p.HomeDir)
	if err != nil {
		a.errorf(err.Error())
		return o, ExitError{1}
	}
	return o, nil
}

// announceFlake is announce_flake: which flake (and override) a command
// targets, as a warning when either is a fallback. The same flake, source
// and override again in one run (piste pkgs update's build, then its
// switch) print nothing.
func (a *App) announceFlake(p platform.Platform, o *platform.EngineOverride) {
	said := p.FlakeDir + "\x00" + p.FlakeSource + "\x00"
	if o != nil {
		said += "override " + o.Describe()
	}
	if said == a.announced {
		return
	}
	a.announced = said
	switch {
	case strings.HasPrefix(p.FlakeSource, "FALLBACK"), p.FlakeSource == platform.FlakeFromEnv:
		a.warn("Flake: " + p.FlakeDir + " (" + p.FlakeSource + ")")
	case p.FlakeSource == platform.FlakeFromCwd:
		a.info("Flake: " + p.FlakeDir)
	default: // found in a piste checkout
		a.info("Flake: " + p.FlakeDir + " (" + p.FlakeSource + ")")
	}
	if o == nil {
		return
	}
	if strings.HasPrefix(o.Reason, "FALLBACK") {
		a.warn("engine override: " + o.Describe())
	} else {
		a.info("engine override: " + o.Describe())
	}
}

func newGendiff(a *App) *cobra.Command {
	var host string
	c := &cobra.Command{
		Use:   "gendiff",
		Short: "Preview diff vs current generation (wraps `nh darwin|os|home diff`)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if _, err := exec.LookPath("nh"); err != nil {
				a.errorf("nh not found on PATH. Install via `piste switch` (nh is in devPkgs).")
				return ExitError{1}
			}
			p, err := a.plat()
			if err != nil {
				return err
			}
			t, err := a.gendiffTarget(p, host)
			if err != nil {
				return err
			}
			o, err := a.engineOverride(p)
			if err != nil {
				return err
			}
			argv := a.gendiffCmd(p, t, o)
			a.announceFlake(p, &o)
			a.info(fmt.Sprintf("Previewing generation diff (%s)...", argv[1]))
			rc, err := a.pass(argv, "")
			if err != nil {
				return err
			}
			if rc != 0 {
				a.errorf(fmt.Sprintf("gendiff failed (exit code %d)", rc))
				if !a.Verbose {
					a.warn("Rerun with: piste --verbose gendiff")
				}
				return ExitError{1}
			}
			return nil
		},
	}
	hostFlags(c, &host)
	return c
}

// gendiffTarget is what gendiff previews for host ("" for this machine's):
// this machine's home, with no --host, is nh's own pick, so no eval.
func (a *App) gendiffTarget(p platform.Platform, host string) (target, error) {
	if p.IsDarwin || host != "" || p.IsNixOS {
		return a.targetFor(p, host)
	}
	return target{Kind: KindHome}, nil
}

// gendiffCmd is nh's dry switch of t, against the engine o: it builds the
// configuration and shows its version changes against the current
// generation. piste gendiff and piste pkgs update run it.
func (a *App) gendiffCmd(p platform.Platform, t target, o platform.EngineOverride) []string {
	sub, ref := "home", p.FlakeDir
	var sel []string
	switch {
	case t.Kind == KindDarwin:
		sub = "darwin"
		if t.Host != "" {
			ref += "#" + t.Name
		}
	case t.Kind == KindNixOS:
		// nh os picks a nixosConfigurations name with -H.
		sub, sel = "os", []string{"-H", t.Name}
	case t.Host != "":
		// nh builds "<user>@<host>" as a homeConfigurations name only via -c.
		sel = []string{"-c", t.Name}
	}
	return append(append([]string{"nh", sub, "switch", "--dry", ref}, sel...), o.Args()...)
}

// --- status ----------------------------------------------------------------------------

type gitStatus struct {
	branch                             string
	dirty                              bool
	untracked, modified, ahead, behind int
	lastDate, lastMsg                  string
}

func (a *App) gitOut(dir string, args ...string) (string, bool) {
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: append([]string{"git"}, args...), Dir: dir})
	return string(res.Stdout), err == nil
}

// getGitStatus reads the branch, dirty state and ahead/behind of dir.
func (a *App) getGitStatus(dir string) gitStatus {
	g := gitStatus{branch: "unknown"}
	if out, ok := a.gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD"); ok {
		g.branch = strings.TrimSpace(out)
	}
	out, _ := a.gitOut(dir, "status", "--porcelain")
	var lines []string
	if strings.TrimSpace(out) != "" {
		lines = strings.Split(strings.TrimSpace(out), "\n")
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "??") {
			g.untracked++
		}
	}
	g.modified = len(lines) - g.untracked
	g.dirty = len(lines) > 0
	if out, ok := a.gitOut(dir, "rev-list", "--left-right", "--count", g.branch+"...origin/"+g.branch); ok {
		if f := strings.Fields(out); len(f) == 2 {
			fmt.Sscan(f[0], &g.ahead)
			fmt.Sscan(f[1], &g.behind)
		}
	}
	if out, ok := a.gitOut(dir, "log", "-1", "--format=%ci|||%s"); ok && strings.Contains(out, "|||") {
		parts := strings.SplitN(strings.TrimSpace(out), "|||", 2)
		g.lastDate, g.lastMsg = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return g
}

// kvPanel is the key/value table (key column width 18) in a panel that
// `piste status` and `piste secrets status` print. Values wrap to the console
// width less the panel (4) and the padded key (18+4) and value (4) columns.
func (a *App) kvPanel(title string, rows [][2]string) {
	s := ui.For(a.Stdout)
	var b strings.Builder
	for _, r := range rows {
		for i, l := range ui.Fit(r[1], ui.Width()-30) {
			key := r[0]
			if i > 0 {
				key = ""
			}
			fmt.Fprintf(&b, "%s  %s\n", s.Dim.Render(fmt.Sprintf("%-18s", key)), l)
		}
	}
	fmt.Fprint(a.Stdout, s.Panel(title, strings.TrimRight(b.String(), "\n")))
}

// repoType: the piste engine (a checkout root), an overlay (its lock has a
// piste input), or another flake.
func repoType(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "flake.nix")); err != nil {
		return "unknown"
	}
	if _, err := os.Stat(filepath.Join(dir, workspace.Marker)); err == nil {
		return "engine"
	}
	if l, err := flake.ReadLock(dir); err == nil && l.ResolveInput("piste") != nil {
		return "overlay"
	}
	return "flake"
}

// lastSwitch is when this machine last switched: a home's newest
// home-manager generation, else the system profile's time.
func (a *App) lastSwitch(kind Kind) string {
	if kind != KindHome {
		if fi, err := os.Stat(platform.SystemProfile); err == nil {
			return fi.ModTime().Format("2006-01-02 15:04")
		}
		return ""
	}
	res, err := a.runner().Run(context.Background(), execx.Cmd{Argv: []string{"home-manager", "generations"}})
	if err == nil && strings.TrimSpace(string(res.Stdout)) != "" {
		return strings.TrimSpace(strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")[0])
	}
	return ""
}

func newStatus(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Health dashboard: host, nixpkgs age, git state, drift summary",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			g := a.getGitStatus(p.FlakeDir)
			s := ui.For(a.Stdout)
			age := s.Dim.Render("unknown")
			if d, ok := flake.AgeDays(p.FlakeDir, "nixpkgs"); ok {
				switch {
				case d < 14:
					age = s.OK.Render(fmt.Sprintf("%d days", d))
				case d < 30:
					age = s.Warn.Render(fmt.Sprintf("%d days", d))
				default:
					age = s.Err.Render(fmt.Sprintf("%d days (stale!)", d))
				}
			}
			kind, err := a.generationsKind(p)
			if err != nil {
				return err
			}
			last := a.lastSwitch(kind)
			if last == "" {
				last = s.Dim.Render("unknown")
			}
			dirty := s.OK.Render("clean")
			if g.dirty {
				dirty = s.Err.Render("dirty")
			}
			rows := [][2]string{
				{"Host", s.Bold.Render(p.Hostname)},
				{"Platform", fmt.Sprintf("%s (%s)", p.OS, p.System)},
				{"User", p.User},
				{"Flake dir", p.FlakeDir},
				{"Repo type", s.Bold.Render(repoType(p.FlakeDir))},
				{"", ""},
				{"nixpkgs age", age},
				{"Flake inputs", fmt.Sprint(flake.InputCount(p.FlakeDir))},
				{"Last switch", last},
				{"", ""},
				{"Git branch", fmt.Sprintf("%s (%s)", g.branch, dirty)},
			}
			if g.modified > 0 {
				rows = append(rows, [2]string{"  Modified", s.Warn.Render(fmt.Sprintf("%d files", g.modified))})
			}
			if g.untracked > 0 {
				rows = append(rows, [2]string{"  Untracked", s.Warn.Render(fmt.Sprintf("%d files", g.untracked))})
			}
			if g.ahead > 0 {
				rows = append(rows, [2]string{"  Ahead", fmt.Sprintf("%d commits", g.ahead)})
			}
			if g.behind > 0 {
				rows = append(rows, [2]string{"  Behind", s.Err.Render(fmt.Sprintf("%d commits", g.behind))})
			}
			if g.lastMsg != "" {
				m := []rune(g.lastMsg)
				if len(m) > 60 {
					m = m[:60]
				}
				rows = append(rows, [2]string{"  Last commit", s.Dim.Render(string(m))})
			}
			a.kvPanel("piste status", rows)
			return nil
		},
	}
}

// --- drift -------------------------------------------------------------------------------

func newDrift(a *App) *cobra.Command {
	return &cobra.Command{
		Use:   "drift",
		Short: "Detect package drift: installs outside nix that should be tracked",
		Long: "Lists what is installed outside Nix (Homebrew, npm and pnpm globals, cargo,\n" +
			"pip --user) against what this host declares, read from one eval: what Nix\n" +
			"provides too, and what isn't declared. Read-only; piste adopt acts on it.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			p, err := a.plat()
			if err != nil {
				return err
			}
			f, err := a.findDrift(p, nil)
			if err != nil {
				return err
			}
			var dups, untracked [][]string
			toNixpkgs := false // a find extraPackages could take
			for _, x := range f.finds {
				switch x.Class {
				case drift.Duplicate:
					dups = append(dups, []string{string(x.Tool), x.Name})
				case drift.Untracked:
					untracked = append(untracked, []string{string(x.Tool), x.Name, targetLabel(x)})
					toNixpkgs = toNixpkgs || x.To == drift.ToNixpkgs
				}
			}
			if len(dups)+len(untracked) == 0 {
				a.success("No drift: everything installed outside Nix is declared.")
				return nil
			}
			s := ui.For(a.Stdout)
			fmt.Fprintln(a.Stdout)
			a.warn(fmt.Sprintf("Found %d installs outside Nix:", len(dups)+len(untracked)))
			fmt.Fprintln(a.Stdout)
			// adopt doesn't write a NixOS host: no hints of it there.
			dupsHead, col := "Also provided by Nix (piste adopt removes these after a switch)", "piste adopt"
			if f.kind == KindNixOS {
				dupsHead, col = "Also provided by Nix", "Where it would go"
			}
			if len(dups) > 0 {
				fmt.Fprintln(a.Stdout, s.Bold.Render(dupsHead))
				fmt.Fprint(a.Stdout, s.Table([]string{"Tool", "Package"}, dups))
				fmt.Fprintln(a.Stdout)
			}
			if len(untracked) > 0 {
				fmt.Fprintln(a.Stdout, s.Bold.Render("Not declared"))
				fmt.Fprint(a.Stdout, s.Table([]string{"Tool", "Package", col}, untracked))
				fmt.Fprintln(a.Stdout)
			}
			if f.kind == KindNixOS && toNixpkgs {
				a.info("piste adopt doesn't support NixOS hosts yet: add the packages to your host's piste.packages.extraPackages by hand.")
			}
			return nil
		},
	}
}

// --- remote ----------------------------------------------------------------------------------

func (a *App) remoteHosts() map[string]config.Host {
	home, _ := os.UserHomeDir()
	c, err := config.Load(config.Path(os.Getenv, home))
	if err != nil {
		a.warn(err.Error())
		return nil
	}
	return c.Remote.Hosts
}

func (a *App) resolveHost(name string) string {
	if h, ok := a.remoteHosts()[name]; ok && h.SSH != "" {
		return h.SSH
	}
	a.warn(fmt.Sprintf("'%s' not in known hosts -- using as SSH host directly", name))
	return name
}

func (a *App) sshExec(host string, cmd []string) (int, error) {
	argv := append([]string{"ssh", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new", host, "--"}, cmd...)
	return a.pass(argv, "")
}

func newRemote(a *App) *cobra.Command {
	root := &cobra.Command{Use: "remote", Short: "Remote host management via SSH", Hidden: true}
	root.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List known remote hosts",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			hosts := a.remoteHosts()
			names := make([]string, 0, len(hosts))
			for n := range hosts {
				names = append(names, n)
			}
			sort.Strings(names)
			var rows [][]string
			for _, n := range names {
				ssh := hosts[n].SSH
				if ssh == "" {
					ssh = n // what resolveHost connects to
				}
				rows = append(rows, []string{n, ssh, hosts[n].Description})
			}
			fmt.Fprint(a.Stdout, ui.For(a.Stdout).Table([]string{"Name", "SSH Host", "Description"}, rows))
			return nil
		},
	})
	for _, v := range []struct{ sub, title, fail string }{
		{"status", "Status", "Failed to get status from %s (exit code %d)"},
		{"drift", "Drift", "Failed to run drift on %s (exit code %d)"},
	} {
		v := v
		root.AddCommand(&cobra.Command{
			Use:   v.sub + " <host>",
			Short: map[string]string{"status": "Check status of a remote host", "drift": "Run drift detection on a remote host"}[v.sub],
			Args:  cobra.ExactArgs(1),
			RunE: func(_ *cobra.Command, args []string) error {
				h := a.resolveHost(args[0])
				fmt.Fprint(a.Stdout, ui.For(a.Stdout).Panel("", fmt.Sprintf("%s: %s (%s)", v.title, args[0], h)))
				a.info(fmt.Sprintf("Connecting to %s...", h))
				rc, err := a.sshExec(h, []string{"piste", v.sub})
				if err != nil {
					return err
				}
				if rc != 0 {
					a.errorf(fmt.Sprintf(v.fail, args[0], rc))
					return ExitError{rc}
				}
				return nil
			},
		})
	}
	root.AddCommand(&cobra.Command{
		Use:                "exec <host> <cmd>...",
		Short:              "Execute a piste command on a remote host",
		Args:               cobra.MinimumNArgs(2),
		DisableFlagParsing: true,
		RunE: func(_ *cobra.Command, args []string) error {
			h := a.resolveHost(args[0])
			a.info(fmt.Sprintf("Running 'piste %s' on %s...", strings.Join(args[1:], " "), h))
			rc, err := a.sshExec(h, append([]string{"piste"}, args[1:]...))
			if err != nil {
				return err
			}
			if rc != 0 {
				a.errorf(fmt.Sprintf("Command failed on %s (exit code %d)", args[0], rc))
				return ExitError{rc}
			}
			return nil
		},
	})
	root.AddCommand(newRemoteMove(a))
	return root
}

var _ = time.Now
