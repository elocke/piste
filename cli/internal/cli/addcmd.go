package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/layer"
	"github.com/elocke/piste/cli/internal/overlay"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/workspace"
)

func init() { register("add", newAddOverlay) }

// newAddOverlay is `piste add <git url>`: an existing
// overlay cloned into overlays/<name>/ of this checkout, then built. (Adding
// a package is `piste pkg add`.)
func newAddOverlay(a *App) *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "add <git url>",
		Short: "Clone an overlay into this checkout and build it",
		Long: "Clone an existing overlay (a flake built with piste.lib.mkFlake) into\n" +
			"overlays/<name>/ of this piste checkout, then build it if its hosts include\n" +
			"this machine. To add a package, use piste pkg add.",
		// The old `add` (a package, now `piste pkg add`) took other forms:
		// they get the pointer, not cobra's arg or flag error.
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				a.errorf("piste add takes one git URL (see piste add --help)")
				return ExitError{1}
			}
			if len(args) != 1 {
				a.errorf(fmt.Sprintf("piste add takes one git URL; to add a package: piste pkg add %s", strings.Join(args, " ")))
				return ExitError{1}
			}
			_, _, err := a.addOverlay(args[0], name, false)
			var el errLayer
			if errors.As(err, &el) {
				os.RemoveAll(el.dir)
				a.errorf(fmt.Sprintf("%s is a team layer: start your overlay on it with piste setup %s (or piste init --layer %s)", el.url, el.url, el.url))
				return ExitError{1}
			}
			return err
		},
	}
	c.Flags().StringVar(&name, "name", "", "its directory in overlays/ (default: the repo's name)")
	c.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		a.errorf(fmt.Sprintf("piste add: %v; piste add takes a git URL, adding a package is piste pkg add (see piste pkg add --help)", err))
		return ExitError{1}
	})
	return c
}

var (
	scpURL = regexp.MustCompile(`^[^/\s]+@[^/\s]+:`)
	// <input>.lib.mkFlake: the engine's when the input is piste, else
	// another's (flake-parts.lib.mkFlake).
	libMkFlake = regexp.MustCompile(`[\w-]+\.lib\.mkFlake\b`)
	engineLib  = regexp.MustCompile(`\bpiste\.lib\b`)
	mkFlake    = regexp.MustCompile(`\bmkFlake\b`)
)

// cloneable: something git clone takes (a URL, user@host:path, a directory).
func cloneable(arg string) bool {
	if strings.Contains(arg, "://") || scpURL.MatchString(arg) {
		return true
	}
	fi, err := os.Stat(arg)
	return err == nil && fi.IsDir()
}

var agentsDir = regexp.MustCompile(`/overlays/([A-Za-z0-9][A-Za-z0-9._-]*)/agents`)

// expectedName is the overlay dir its home.nix or host files link skills
// from (overlays/<name>/agents, as piste init writes them), "" if none says.
func expectedName(dir string) string {
	files, _ := filepath.Glob(filepath.Join(dir, "hosts", "*.nix"))
	for _, f := range append([]string{filepath.Join(dir, "home.nix")}, files...) {
		if b, err := os.ReadFile(f); err == nil {
			if m := agentsDir.FindSubmatch(b); m != nil {
				return string(m[1])
			}
		}
	}
	return ""
}

// callsEngineMkFlake: the flake calls the engine's mkFlake, which its modules
// reach through the piste input: piste.lib.mkFlake (or
// inputs.piste.lib.mkFlake), or mkFlake from `inherit (piste.lib)` /
// `with piste.lib;`. Another library's <input>.lib.mkFlake doesn't count.
func callsEngineMkFlake(b []byte) bool {
	s := libMkFlake.ReplaceAllStringFunc(string(b), func(m string) string {
		if m == "piste.lib.mkFlake" {
			return m
		}
		return ""
	})
	return engineLib.MatchString(s) && mkFlake.MatchString(s)
}

// repoName is the last path element of a clone URL, without .git.
func repoName(url string) string {
	url = strings.TrimRight(url, "/")
	if i := strings.LastIndexAny(url, "/:"); i >= 0 {
		url = url[i+1:]
	}
	return strings.TrimSuffix(url, ".git")
}

// addOverlay is `piste add`: the overlay's name, and whether its hosts include
// this machine (then it was built). ask: the caller offers to add this machine
// when they don't (setup), so the no-host line leaves out the command.
func (a *App) addOverlay(url, name string, ask bool) (placed string, here bool, err error) {
	if !cloneable(url) {
		a.errorf(fmt.Sprintf("piste add: %q is not a git URL; to add a package: piste pkg add %s", url, url))
		return "", false, ExitError{1}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", false, err
	}
	root, ok := workspace.FindCheckout(cwd)
	if !ok {
		a.errorf("piste add: run inside a piste checkout (clone the engine, then run piste add there)")
		return "", false, ExitError{1}
	}
	if !strings.Contains(url, "://") && !scpURL.MatchString(url) {
		// A local directory: git clone runs in overlays/, the path is the
		// user's, from where they are.
		if abs, err := filepath.Abs(url); err == nil {
			url = abs
		}
	}
	given := name != ""
	if given {
		if err := overlay.ValidName(name); err != nil {
			a.errorf(err.Error() + " (--name)")
			return "", false, ExitError{1}
		}
		if _, err := os.Lstat(filepath.Join(root, "overlays", name)); err == nil {
			a.errorf(fmt.Sprintf("overlays/%s exists", name))
			return "", false, ExitError{1}
		}
	}
	// Cloned under a temporary name first: the name it belongs under can be
	// in its host files (piste init's link the skills from
	// overlays/<name>/agents).
	overlays := filepath.Join(root, "overlays")
	if err := os.MkdirAll(overlays, 0o755); err != nil {
		return "", false, err
	}
	tmp := fmt.Sprintf(".piste-add-%d", os.Getpid())
	// A Nix git+ ref (piste prints the layer as one) is a URL to git.
	if err := a.gitIn(overlays, "clone", "-q", strings.TrimPrefix(url, "git+"), tmp); err != nil {
		a.githubCloneHint(url)
		return "", false, err
	}
	clone := filepath.Join(overlays, tmp)
	// A team layer isn't an overlay: setup starts one on it (init --layer),
	// from this clone; whoever gets errLayer removes it.
	if layer.IsLayer(clone) {
		return "", false, errLayer{url, clone}
	}
	if b, err := os.ReadFile(filepath.Join(clone, "flake.nix")); err != nil || !callsEngineMkFlake(b) {
		// Our own fresh clone: nothing of the user's is in it.
		if err := os.RemoveAll(clone); err != nil {
			return "", false, err
		}
		a.errorf(fmt.Sprintf("%s is not a piste overlay: it has no flake.nix calling piste.lib.mkFlake (the clone was removed)", url))
		return "", false, ExitError{1}
	}
	expected := expectedName(clone)
	switch {
	case !given && expected != "":
		name = expected
	case !given:
		name = repoName(url)
	case expected != "" && expected != name:
		a.warn(fmt.Sprintf("overlays/%s: its hosts link its skills from overlays/%s; clone it there, or edit its hosts", name, expected))
	}
	dst := filepath.Join(overlays, name)
	if err := overlay.ValidName(name); err != nil {
		os.RemoveAll(clone)
		a.errorf(err.Error() + " (--name)")
		return "", false, ExitError{1}
	}
	if _, err := os.Lstat(dst); err == nil {
		os.RemoveAll(clone)
		a.errorf(fmt.Sprintf("overlays/%s exists", name))
		return "", false, ExitError{1}
	}
	if err := os.Rename(clone, dst); err != nil {
		return "", false, err
	}
	a.success("Added overlays/" + name)

	hosts, err := a.overlayHosts(dst)
	if err != nil {
		a.warn(fmt.Sprintf("overlays/%s was added, but %v; fix it and run: %s build --overlay %s", name, err, a.piste(), name))
		return name, false, ExitError{1}
	}
	q, key, err := a.thisMachine()
	if err != nil {
		return name, false, err
	}
	if !workspace.Matches(hosts, q) {
		a.noHost(name, key, !ask)
		return name, false, nil
	}
	a.Overlay = name
	if err := a.runBuild(q.Hostname); err != nil {
		a.warn(fmt.Sprintf("overlays/%s was added; fix the build and run: %s build --overlay %s", name, a.piste(), name))
		return name, true, err
	}
	return name, true, nil
}

// noHost says overlays/<n> has no host for this machine (key); with hint,
// the command that adds it.
func (a *App) noHost(n, key string, hint bool) {
	line := fmt.Sprintf("overlays/%s has no host for this machine (%s)", n, key)
	if hint {
		a.info(fmt.Sprintf("%s: %s host add --overlay %s adds it", line, a.piste(), n))
		return
	}
	a.info(line + ".")
}

// thisMachine is this machine as the overlays' hosts name it: the query
// workspace.Matches takes, and its host key (hostname on macOS, else
// user@hostname).
func (a *App) thisMachine() (workspace.Query, string, error) {
	host, err := platform.Hostname()
	if err != nil {
		return workspace.Query{}, "", err
	}
	q := workspace.Query{Hostname: platform.NormalizeHostname(host), User: a.Env.Getenv("USER"), Darwin: runtime.GOOS == "darwin"}
	if q.Darwin {
		return q, q.Hostname, nil
	}
	return q, q.User + "@" + q.Hostname, nil
}

// githubHTTPS is a github.com clone URL's https form ("" for another host):
// git@github.com:<owner>/<repo>, ssh://git@github.com/<owner>/<repo> and
// https://github.com/<owner>/<repo>, with or without git+ and .git.
func githubHTTPS(url string) string {
	u := strings.TrimPrefix(url, "git+")
	for _, p := range []string{"git@github.com:", "ssh://git@github.com/", "ssh://git@github.com:22/", "https://github.com/"} {
		if rest, ok := strings.CutPrefix(u, p); ok && strings.Count(strings.Trim(rest, "/"), "/") == 1 {
			return "https://github.com/" + strings.TrimSuffix(strings.Trim(rest, "/"), ".git") + ".git"
		}
	}
	return ""
}

// githubCloneHint follows a failed clone of a github.com URL: a fresh
// machine has no SSH key for GitHub yet (piste github makes one after the
// first switch), so a private repo clones over https, with gh's login.
func (a *App) githubCloneHint(url string) {
	https := githubHTTPS(url)
	if https == "" {
		return
	}
	a.info("A private repo on a machine with no GitHub key yet: log gh in, then run this again with the https URL:")
	fmt.Fprintln(a.Stdout, "  nix run nixpkgs#gh -- auth login --web --git-protocol https")
	fmt.Fprintln(a.Stdout, "  "+https)
}

// errLayer is addOverlay's answer for a team layer's URL, with its clone:
// setup starts an overlay from that clone (init --layer), add removes it and
// points at setup.
type errLayer struct{ url, dir string }

func (e errLayer) Error() string { return e.url + " is a team layer, not an overlay" }
