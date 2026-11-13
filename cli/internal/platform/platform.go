// Package platform detects the system, hostname and flake directory.
package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elocke/piste/cli/internal/envvar"
	"github.com/elocke/piste/cli/internal/workspace"
)

// Platform describes the machine piste runs on.
type Platform struct {
	OS       string `json:"os"`     // "Darwin" or "Linux"
	Arch     string `json:"arch"`   // uname-style: "arm64", "x86_64", "aarch64"
	System   string `json:"system"` // Nix system: "aarch64-darwin", "x86_64-linux", ...
	IsDarwin bool   `json:"is_darwin"`
	IsLinux  bool   `json:"is_linux"`
	// IsNixOS: this machine runs NixOS, and nothing more. Whether a command
	// acts on a NixOS host also takes the overlay's hosts (the cli
	// package's resolveTarget).
	IsNixOS  bool   `json:"is_nixos"`
	Hostname string `json:"hostname"`
	FlakeDir string `json:"flake_dir"`
	// FlakeSource is one of the FlakeFrom* constants.
	FlakeSource string `json:"flake_source"`
	// Checkout and Overlay: the piste checkout and overlay the flake is in,
	// when it is in one; Warnings are resolution notes to show the user.
	Checkout string   `json:"checkout,omitempty"`
	Overlay  string   `json:"overlay,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	// FlakeErr: no flake could be resolved (FlakeDir is ""). Commands that
	// need one fail with it; the rest still get the machine.
	FlakeErr error  `json:"-"`
	User     string `json:"user"`
	HomeDir  string `json:"home_dir"`
}

// Env is everything detection reads, so tests can supply their own.
type Env struct {
	GOOS, GOARCH string
	Hostname     func() (string, error)
	Getwd        func() (string, error)
	Getenv       func(string) string
	Home         string
	NixOS        bool                // the machine runs NixOS (NixOSMarker)
	Overlay      string              // --overlay ("" if unset)
	Hosts        workspace.HostsFunc // an overlay's hosts; nil: no lookup
}

// The machine piste reads, as variables so the test binaries can pin them:
// the end-to-end and agents goldens must not depend on the machine they run on.
var (
	// Hostname is this machine's name: on a Mac its LocalHostName.
	Hostname      = func() (string, error) { return machineHostname(runtime.GOOS, localHostName, os.Hostname) }
	Arch          = runtime.GOARCH
	SystemProfile = "/nix/var/nix/profiles/system" // nix-darwin's
	// UserProfiles holds <user>/home-manager when home-manager keeps its
	// profile there (no ~/.local/state/nix/profiles).
	UserProfiles = "/nix/var/nix/profiles/per-user"
	// CgroupFile is the cgroup piste runs in (Linux): a switch inside a
	// systemd user service runs as its own unit.
	CgroupFile = "/proc/self/cgroup"
	// NixOSMarker is the file NixOS writes on every NixOS machine.
	NixOSMarker = "/etc/NIXOS"
	// MachineEtc is a NixOS machine's own configuration (configuration.nix
	// and what it imports), which piste host add and init carry into the
	// overlay.
	MachineEtc = "/etc/nixos"
	// ContainerMarker is the file systemd writes when it runs in a
	// container (an LXC guest): its root is the host's to give.
	ContainerMarker = "/run/systemd/container"
	// DiskByUUID is udev's directory of links to filesystems by uuid: the
	// name a NixOS host's root must have for piste to know it as this
	// machine's.
	DiskByUUID = "/dev/disk/by-uuid"
)

// localHostName is scutil's LocalHostName, the name darwin-rebuild picks a
// configuration by.
func localHostName() (string, error) {
	out, err := exec.Command("/usr/sbin/scutil", "--get", "LocalHostName").Output()
	return string(out), err
}

// machineHostname is the name piste knows a machine by: on darwin scutil's
// LocalHostName, the one darwin-rebuild uses (os.Hostname follows the
// network: janes-mbp.lan on one, another name on the next), else, and when
// scutil has none, os.Hostname's.
func machineHostname(goos string, scutil, osHostname func() (string, error)) (string, error) {
	if goos == "darwin" {
		if h, err := scutil(); err == nil && strings.TrimSpace(h) != "" {
			return strings.TrimSpace(h), nil
		}
	}
	return osHostname()
}

// ShortHostname is Hostname without macOS's ".local" ("" on error).
func ShortHostname() string {
	h, _ := Hostname()
	return NormalizeHostname(h)
}

// Host returns the Env of the running process.
func Host() Env {
	home, _ := os.UserHomeDir()
	return Env{
		GOOS: runtime.GOOS, GOARCH: Arch,
		Hostname: Hostname, Getwd: os.Getwd, Getenv: os.Getenv, Home: home,
		NixOS: isFile(NixOSMarker),
	}
}

var systems = map[[2]string]struct{ os, arch, system string }{
	{"darwin", "arm64"}: {"Darwin", "arm64", "aarch64-darwin"},
	{"darwin", "amd64"}: {"Darwin", "x86_64", "x86_64-darwin"},
	{"linux", "arm64"}:  {"Linux", "aarch64", "aarch64-linux"},
	{"linux", "amd64"}:  {"Linux", "x86_64", "x86_64-linux"},
}

// Detect fills a Platform from env.
func Detect(env Env) (Platform, error) {
	s, ok := systems[[2]string{env.GOOS, env.GOARCH}]
	if !ok {
		return Platform{}, fmt.Errorf("unsupported platform: %s %s", env.GOOS, env.GOARCH)
	}
	host, err := env.Hostname()
	if err != nil {
		return Platform{}, fmt.Errorf("hostname: %w", err)
	}
	cwd, err := env.Getwd()
	if err != nil {
		return Platform{}, fmt.Errorf("cwd: %w", err)
	}
	// Physical cwd ($PWD may hold a symlinked path).
	if r, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = r
	}
	user := env.Getenv("USER")
	if user == "" {
		user = "unknown"
	}
	nixos := s.os == "Linux" && env.NixOS
	q := workspace.Query{Overlay: env.Overlay, Hostname: NormalizeHostname(host), User: user, Darwin: s.os == "Darwin", NixOS: nixos}
	f, ferr := ResolveFlakeDir(cwd, env.Getenv, env.Home, q, env.Hosts)
	return Platform{
		OS: s.os, Arch: s.arch, System: s.system,
		IsDarwin: s.os == "Darwin", IsLinux: s.os == "Linux", IsNixOS: nixos,
		Hostname:    NormalizeHostname(host),
		FlakeDir:    f.Dir,
		FlakeSource: f.Source,
		Checkout:    f.Checkout,
		Overlay:     f.Overlay,
		Warnings:    f.Warnings,
		FlakeErr:    ferr,
		User:        user,
		HomeDir:     env.Home,
	}, nil
}

// NormalizeHostname strips macOS's ".local" suffix.
func NormalizeHostname(h string) string {
	return strings.TrimSuffix(h, ".local")
}

// Where the flake dir came from, as `piste where` reports it. A flake found in a
// piste checkout says so ("checkout <root>: overlay <name> (<why>)");
// anything starting FALLBACK is not the tree piste was run in.
const (
	FlakeFromCwd = "cwd"
	FlakeFromEnv = "$PISTE_FLAKE"
)

// Flake is the flake piste acts on and how it was found.
type Flake struct {
	Dir, Source       string
	Checkout, Overlay string // the piste checkout and overlay, when in one
	Warnings          []string
}

// ResolveFlakeDir finds the flake piste acts on:
//  1. walking up from cwd, the first flake.nix whose directory is not a git
//     submodule. A checkout root (it holds workspace.Marker) means one of
//     its overlays (workspace.Resolve); a flake inside a checkout is itself,
//     unless --overlay names another overlay of that checkout;
//  2. $PISTE_FLAKE;
//  3. the checkout the agents manifest records, then ~/piste, each
//     resolved to an overlay.
//
// Submodules and linked worktrees both have a .git file; a worktree is the
// tree you are in, so it resolves to itself.
func ResolveFlakeDir(cwd string, getenv func(string) string, home string, q workspace.Query, hosts workspace.HostsFunc) (Flake, error) {
	if hosts == nil {
		hosts = func(string) (workspace.Hosts, error) { return workspace.Hosts{}, errors.New("no host lookup") }
	}
	q.Cwd = cwd
	inCheckout := func(root, label string) (Flake, error) {
		r, err := workspace.Resolve(root, q, hosts)
		if err != nil {
			return Flake{}, err
		}
		f := Flake{Dir: r.Dir, Checkout: r.Root, Overlay: r.Overlay, Warnings: r.Warnings}
		if r.Overlay == "" {
			f.Source = label + ": the engine (no overlays)"
		} else {
			f.Source = label + ": overlay " + r.Overlay + " (" + r.Reason + ")"
		}
		return f, nil
	}
	for p := filepath.Clean(cwd); ; p = filepath.Dir(p) {
		if isFile(filepath.Join(p, "flake.nix")) && !isSubmodule(p) {
			if isFile(filepath.Join(p, workspace.Marker)) {
				return inCheckout(p, "checkout "+p)
			}
			if root, ok := workspace.FindCheckout(p); ok {
				if q.Overlay != "" {
					return inCheckout(root, "checkout "+root)
				}
				name, _ := workspace.OverlayOf(root, p)
				return Flake{Dir: p, Source: FlakeFromCwd, Checkout: root, Overlay: name}, nil
			}
			if q.Overlay != "" { // never silently ignored: a worktree's checkout, or an error
				if main := WorktreeMainCheckout(p); main != "" {
					if root, ok := workspace.FindCheckout(main); ok {
						return inCheckout(root, "checkout "+root)
					}
				}
				return Flake{}, fmt.Errorf("--overlay %s: %s is not in a piste checkout", q.Overlay, p)
			}
			return Flake{Dir: p, Source: FlakeFromCwd}, nil
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if d := getenv("PISTE_FLAKE"); d != "" && q.Overlay == "" { // --overlay beats it
		return Flake{Dir: d, Source: FlakeFromEnv}, nil
	}
	if root, ok := workspace.CheckoutFromManifest(filepath.Join(home, ".config", "piste-agents", "manifest.json")); ok {
		return inCheckout(root, "FALLBACK: checkout "+root+" from the agents manifest")
	}
	if pd := filepath.Join(home, "piste"); isFile(filepath.Join(pd, workspace.Marker)) {
		return inCheckout(pd, "FALLBACK: checkout ~/piste")
	}
	return Flake{}, errors.New("no flake here: run piste inside a piste checkout or overlay, or set PISTE_FLAKE")
}

// gitdirOf is the git dir a .git FILE at p points to ("gitdir: <path>").
func gitdirOf(p string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(p, ".git"))
	if err != nil {
		return "", false
	}
	first, _, _ := strings.Cut(string(b), "\n")
	rest, ok := strings.CutPrefix(first, "gitdir:")
	if !ok {
		return "", false
	}
	return joinAbs(p, strings.TrimSpace(rest)), true
}

// worktreeCommonDir is the common git dir if p is a linked worktree. Its
// .git file points at <common>/worktrees/<name>, which holds a `commondir`
// file; a submodule's points at .../modules/<name>, which does not.
func worktreeCommonDir(p string) (string, bool) {
	gitdir, ok := gitdirOf(p)
	if !ok {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return "", false
	}
	return joinAbs(gitdir, strings.TrimSpace(string(b))), true
}

// WorktreeMainCheckout is the primary checkout of the repo p is a linked
// worktree of ("" if p is not one, or is a worktree of a bare repo).
func WorktreeMainCheckout(p string) string {
	common, ok := worktreeCommonDir(p)
	if !ok || filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}

func isSubmodule(p string) bool {
	fi, err := os.Stat(filepath.Join(p, ".git"))
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	_, wt := worktreeCommonDir(p)
	return !wt
}

func joinAbs(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}

// EngineOverride is the local piste passed as --override-input.
type EngineOverride struct {
	Path   string // "": use the locked input
	Reason string
}

// Args is the nix argv for the override (none for the locked input).
func (o EngineOverride) Args() []string {
	if o.Path == "" {
		return nil
	}
	return []string{"--override-input", "piste", workspace.OverrideRef(o.Path)}
}

// Describe says where the override came from.
func (o EngineOverride) Describe() string {
	p := o.Path
	if p == "" {
		p = "none"
	}
	return p + " (" + o.Reason + ")"
}

// ResolveEngineOverride picks the engine an overlay builds against:
// $PISTE_ENGINE (a path, or "none"), else the piste checkout the overlay
// is in, else, for a linked worktree of an overlay (they live outside the
// checkout), the checkout of its main repo. A flake without a piste
// input (the engine itself) gets no override; a flake with no checkout
// around it builds its locked engine.
func ResolveEngineOverride(flakeDir string, getenv func(string) string, home string) (EngineOverride, error) {
	o, err := resolveEngineOverride(flakeDir, getenv, home)
	if err == nil && o.Path != "" && isFile(filepath.Join(o.Path, workspace.Marker)) {
		if _, gerr := os.Stat(filepath.Join(o.Path, ".git")); gerr != nil {
			return EngineOverride{}, fmt.Errorf("checkout %s is not a git repository: piste builds against a checkout as git+file "+
				"(path: would copy overlays/ into the store); clone it with git, or set PISTE_ENGINE", o.Path)
		}
	}
	return o, err
}

func resolveEngineOverride(flakeDir string, getenv func(string) string, home string) (EngineOverride, error) {
	here := filepath.Clean(flakeDir)
	if !hasEngineInput(here) {
		return EngineOverride{Reason: "flake has no piste input"}, nil
	}
	env := strings.TrimSpace(envvar.Get(getenv, "ENGINE"))
	switch env {
	case "none":
		return EngineOverride{Reason: "PISTE_ENGINE=" + env + ": locked input"}, nil
	case "":
	default:
		p := env
		if p == "~" || strings.HasPrefix(p, "~/") {
			p = home + p[1:]
		}
		p = filepath.Clean(p)
		if !isFile(filepath.Join(p, "flake.nix")) {
			return EngineOverride{}, fmt.Errorf("PISTE_ENGINE=%s: no flake.nix in %s", env, p)
		}
		return EngineOverride{Path: p, Reason: "$PISTE_ENGINE"}, nil
	}
	if root, ok := workspace.FindCheckout(here); ok {
		if _, in := workspace.OverlayOf(root, here); in {
			return EngineOverride{Path: root, Reason: "the checkout around the overlay"}, nil
		}
	}
	if main := WorktreeMainCheckout(here); main != "" {
		if root, ok := workspace.FindCheckout(main); ok {
			if _, in := workspace.OverlayOf(root, main); in {
				return EngineOverride{Path: root, Reason: "FALLBACK: worktree of " + main +
					", in checkout " + root + "; set PISTE_ENGINE to change"}, nil
			}
		}
	}
	return EngineOverride{Reason: "no checkout around the flake: locked input"}, nil
}

// hasEngineInput is false only when flake.lock shows no piste input.
func hasEngineInput(dir string) bool {
	in, ok := rootInputs(dir)
	if !ok {
		return true
	}
	_, ok = in["piste"]
	return ok
}

// rootInputs is the root node's inputs in dir's flake.lock; !ok when there
// is no lock, it doesn't parse, or its root has no inputs.
func rootInputs(dir string) (map[string]json.RawMessage, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "flake.lock"))
	if err != nil {
		return nil, false
	}
	var lock struct {
		Root  string `json:"root"`
		Nodes map[string]struct {
			Inputs map[string]json.RawMessage `json:"inputs"`
		} `json:"nodes"`
	}
	if json.Unmarshal(b, &lock) != nil {
		return nil, false
	}
	if lock.Root == "" {
		lock.Root = "root"
	}
	node, ok := lock.Nodes[lock.Root]
	if !ok || node.Inputs == nil {
		return nil, false
	}
	return node.Inputs, true
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// ErrNixMissing: piste deliberately does not bundle nix (see cli/default.nix).
var ErrNixMissing = errors.New("nix not found on PATH.\n" +
	"piste uses whichever nix the system provides (Determinate Nix, the " +
	"upstream installer, or NixOS) rather than bundling its own.\n" +
	"Ensure /nix/var/nix/profiles/default/bin is on PATH.")

// CheckNix fails legibly when nix is absent.
func CheckNix(lookPath func(string) (string, error)) error {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("nix"); err != nil {
		return ErrNixMissing
	}
	return nil
}
