package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/workspace"
)

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// resolve is ResolveFlakeDir for a cwd, with no --overlay and no host
// lookup (a lookup fails the test).
func resolve(t *testing.T, cwd string, vars map[string]string, home string) (Flake, error) {
	t.Helper()
	return ResolveFlakeDir(cwd, env(vars), home, workspace.Query{Hostname: "box", User: "u"},
		func(string) (workspace.Hosts, error) {
			t.Error("hosts evaluated")
			return workspace.Hosts{}, nil
		})
}

func phys(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFindFlakeDirSkipsSubmodule(t *testing.T) {
	tmp := t.TempDir()
	submod := filepath.Join(tmp, "overlay", "piste")
	touch(t, filepath.Join(submod, "flake.nix"))
	if err := os.WriteFile(filepath.Join(submod, ".git"), []byte("gitdir: ../../.git/modules/piste"), 0o644); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(tmp, "overlay")
	touch(t, filepath.Join(overlay, "flake.nix"))
	os.Mkdir(filepath.Join(overlay, ".git"), 0o755)
	if f, err := resolve(t, submod, nil, tmp); err != nil || f.Dir != overlay || f.Source != FlakeFromCwd {
		t.Fatalf("got %+v %v, want %s", f, err, overlay)
	}
}

func TestFindFlakeDirRegular(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "myrepo")
	touch(t, filepath.Join(repo, "flake.nix"))
	os.Mkdir(filepath.Join(repo, ".git"), 0o755)
	if f, err := resolve(t, repo, nil, "/nonexistent"); err != nil || f.Dir != repo {
		t.Fatalf("got %+v %v", f, err)
	}
}

func TestFindFlakeDirFromSubdirectory(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "myrepo")
	touch(t, filepath.Join(repo, "flake.nix"))
	sub := filepath.Join(repo, "a", "b")
	os.MkdirAll(sub, 0o755)
	if f, err := resolve(t, sub, nil, "/nonexistent"); err != nil || f.Dir != repo {
		t.Fatalf("got %+v %v", f, err)
	}
}

func TestFindFlakeDirEnvFallback(t *testing.T) {
	empty := t.TempDir()
	if f, err := resolve(t, empty, map[string]string{"PISTE_FLAKE": "/custom/flake"}, empty); err != nil || f.Dir != "/custom/flake" || f.Source != FlakeFromEnv {
		t.Fatalf("got %+v %v", f, err)
	}
}

// With no PISTE_FLAKE, no manifest and no ~/piste there is no flake: flakes
// elsewhere under the home are not guessed.
func TestFindFlakeDirNoFallback(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	touch(t, filepath.Join(home, "src", "personal", "flake.nix"))
	touch(t, filepath.Join(home, "src", "piste", "flake.nix"))
	empty := filepath.Join(tmp, "empty")
	os.Mkdir(empty, 0o755)
	_, err := resolve(t, empty, nil, home)
	if err == nil || !strings.Contains(err.Error(), "no flake here: run piste inside a piste checkout or overlay, or set PISTE_FLAKE") {
		t.Fatalf("got %v", err)
	}
}

// checkout lays out <tmp>/piste: the engine (a git repo with .piste) and
// overlays/<name> repos, each a flake whose lock has a piste input.
func checkout(t *testing.T, tmp string, overlays ...string) string {
	t.Helper()
	root := filepath.Join(tmp, "piste")
	touch(t, filepath.Join(root, workspace.Marker))
	touch(t, filepath.Join(root, "flake.nix"))
	writeLock(t, root, "nixpkgs")
	os.Mkdir(filepath.Join(root, ".git"), 0o755)
	for _, o := range overlays {
		d := filepath.Join(root, "overlays", o)
		touch(t, filepath.Join(d, "flake.nix"))
		writeLock(t, d, "piste", "nixpkgs")
		os.Mkdir(filepath.Join(d, ".git"), 0o755)
	}
	return phys(t, root)
}

func TestResolveFlakeDirCheckout(t *testing.T) {
	t.Run("root with one overlay", func(t *testing.T) {
		root := checkout(t, t.TempDir(), "mine")
		f, err := resolve(t, root, nil, "/nonexistent")
		want := filepath.Join(root, "overlays", "mine")
		if err != nil || f.Dir != want || f.Checkout != root || f.Overlay != "mine" ||
			f.Source != "checkout "+root+": overlay mine (the only overlay)" {
			t.Fatalf("got %+v %v", f, err)
		}
	})
	t.Run("root with no overlays is the engine", func(t *testing.T) {
		root := checkout(t, t.TempDir())
		f, err := resolve(t, root, nil, "/nonexistent")
		if err != nil || f.Dir != root || f.Overlay != "" || len(f.Warnings) != 1 ||
			f.Source != "checkout "+root+": the engine (no overlays)" {
			t.Fatalf("got %+v %v", f, err)
		}
	})
	t.Run("inside an overlay", func(t *testing.T) {
		root := checkout(t, t.TempDir(), "a", "b")
		sub := filepath.Join(root, "overlays", "b", "hosts")
		os.MkdirAll(sub, 0o755)
		f, err := resolve(t, sub, nil, "/nonexistent")
		if err != nil || f.Dir != filepath.Join(root, "overlays", "b") || f.Source != FlakeFromCwd || f.Checkout != root || f.Overlay != "b" {
			t.Fatalf("got %+v %v", f, err)
		}
	})
	t.Run("--overlay beats the cwd", func(t *testing.T) {
		root := checkout(t, t.TempDir(), "a", "b")
		f, err := ResolveFlakeDir(filepath.Join(root, "overlays", "a"), env(nil), "/nonexistent",
			workspace.Query{Overlay: "b"}, nil)
		if err != nil || f.Overlay != "b" || f.Source != "checkout "+root+": overlay b (--overlay)" {
			t.Fatalf("got %+v %v", f, err)
		}
	})
	t.Run("by host at the root", func(t *testing.T) {
		root := checkout(t, t.TempDir(), "a", "b")
		f, err := ResolveFlakeDir(root, env(nil), "/nonexistent", workspace.Query{Hostname: "box", User: "u"},
			func(dir string) (workspace.Hosts, error) {
				if filepath.Base(dir) == "b" {
					return workspace.Hosts{Home: []string{"u@box"}}, nil
				}
				return workspace.Hosts{}, nil
			})
		if err != nil || f.Overlay != "b" || f.Source != "checkout "+root+": overlay b (hosts include u@box)" {
			t.Fatalf("got %+v %v", f, err)
		}
	})
	t.Run("two overlays claim the host", func(t *testing.T) {
		root := checkout(t, t.TempDir(), "a", "b")
		_, err := ResolveFlakeDir(root, env(nil), "/nonexistent", workspace.Query{Hostname: "box", User: "u"},
			func(string) (workspace.Hosts, error) { return workspace.Hosts{Home: []string{"u@box"}}, nil })
		if err == nil || !strings.Contains(err.Error(), "overlays a, b both have this host (box)") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("the checkout the agents manifest records", func(t *testing.T) {
		tmp := t.TempDir()
		root := checkout(t, tmp, "mine")
		home := filepath.Join(tmp, "home")
		touch(t, filepath.Join(home, ".config", "piste-agents", "manifest.json"))
		os.WriteFile(filepath.Join(home, ".config", "piste-agents", "manifest.json"),
			[]byte(`{"sources":[{"name":"piste-agents","workdir":"`+root+`/agents"}]}`), 0o644)
		elsewhere := filepath.Join(tmp, "elsewhere")
		os.Mkdir(elsewhere, 0o755)
		f, err := resolve(t, elsewhere, nil, home)
		if err != nil || f.Dir != filepath.Join(root, "overlays", "mine") ||
			f.Source != "FALLBACK: checkout "+root+" from the agents manifest: overlay mine (the only overlay)" {
			t.Fatalf("got %+v %v", f, err)
		}
	})
	t.Run("~/piste", func(t *testing.T) {
		home := phys(t, t.TempDir())
		root := checkout(t, home, "mine")
		elsewhere := filepath.Join(home, "elsewhere")
		os.Mkdir(elsewhere, 0o755)
		f, err := resolve(t, elsewhere, nil, home)
		if err != nil || f.Dir != filepath.Join(root, "overlays", "mine") ||
			f.Source != "FALLBACK: checkout ~/piste: overlay mine (the only overlay)" {
			t.Fatalf("got %+v %v", f, err)
		}
	})
}

func detectWith(goos, goarch, host string) (Platform, error) {
	return Detect(Env{
		GOOS: goos, GOARCH: goarch,
		Hostname: func() (string, error) { return host, nil },
		Getwd:    func() (string, error) { return "/", nil },
		Getenv:   env(map[string]string{"USER": "u"}),
		Home:     "/home/u",
	})
}

func TestHostnameStripsLocal(t *testing.T) {
	p, err := detectWith("darwin", "arm64", "Macbook.local")
	if err != nil || p.Hostname != "Macbook" || p.System != "aarch64-darwin" || !p.IsDarwin {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestHostnameUnchanged(t *testing.T) {
	p, err := detectWith("linux", "amd64", "devbox")
	if err != nil || p.Hostname != "devbox" || p.System != "x86_64-linux" || p.Arch != "x86_64" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestUnsupported(t *testing.T) {
	if _, err := detectWith("windows", "amd64", "x"); err == nil {
		t.Fatal("want error")
	}
}

func TestCheckNix(t *testing.T) {
	if err := CheckNix(func(string) (string, error) { return "", errors.New("no") }); !errors.Is(err, ErrNixMissing) {
		t.Fatalf("got %v", err)
	}
	if err := CheckNix(func(string) (string, error) { return "/nix/var/nix/profiles/default/bin/nix", nil }); err != nil {
		t.Fatal(err)
	}
}

// makeWorktree lays out a linked worktree the way `git worktree add` does.
func makeWorktree(t *testing.T, main, wt string) {
	t.Helper()
	admin := filepath.Join(main, ".git", "worktrees", "wt")
	touch(t, filepath.Join(admin, "commondir"))
	os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0o644)
	os.WriteFile(filepath.Join(admin, "gitdir"), []byte(wt+"/.git\n"), 0o644)
	os.MkdirAll(wt, 0o755)
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeLock(t *testing.T, dir string, inputs ...string) {
	t.Helper()
	in := ""
	for i, n := range inputs {
		if i > 0 {
			in += ","
		}
		in += `"` + n + `":"` + n + `"`
	}
	if err := os.WriteFile(filepath.Join(dir, "flake.lock"),
		[]byte(`{"nodes":{"root":{"inputs":{`+in+`}}},"root":"root","version":7}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindFlakeDirWorktreeResolvesToItself(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	main := filepath.Join(home, "src", "piste")
	touch(t, filepath.Join(main, "flake.nix"))
	wt := filepath.Join(tmp, "wt", "abc", "fix")
	makeWorktree(t, main, wt)
	touch(t, filepath.Join(wt, "flake.nix"))
	sub := filepath.Join(wt, "cli", "piste")
	os.MkdirAll(sub, 0o755)
	if f, err := resolve(t, sub, nil, home); err != nil || f.Dir != wt || f.Source != FlakeFromCwd {
		t.Fatalf("got %+v %v, want %s", f, err, wt)
	}
}

func TestFindFlakeDirRelativeGitdirWorktree(t *testing.T) {
	tmp := t.TempDir()
	main, wt := filepath.Join(tmp, "main"), filepath.Join(tmp, "wt")
	makeWorktree(t, main, wt)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ../main/.git/worktrees/wt\n"), 0o644)
	touch(t, filepath.Join(wt, "flake.nix"))
	if f, err := resolve(t, wt, nil, "/nonexistent"); err != nil || f.Dir != wt {
		t.Fatalf("got %+v %v", f, err)
	}
}

func TestFindFlakeDirSubmoduleInsideWorktree(t *testing.T) {
	tmp := t.TempDir()
	main, wt := filepath.Join(tmp, "main"), filepath.Join(tmp, "wt")
	makeWorktree(t, main, wt)
	touch(t, filepath.Join(wt, "flake.nix"))
	modules := filepath.Join(main, ".git", "worktrees", "wt", "modules", "piste")
	os.MkdirAll(modules, 0o755)
	submod := filepath.Join(wt, "piste")
	touch(t, filepath.Join(submod, "flake.nix"))
	os.WriteFile(filepath.Join(submod, ".git"), []byte("gitdir: "+modules+"\n"), 0o644)
	if f, err := resolve(t, submod, nil, "/nonexistent"); err != nil || f.Dir != wt {
		t.Fatalf("got %+v %v, want %s", f, err, wt)
	}
}

// --overlay is never silently ignored: from a worktree of an overlay it goes
// through the worktree's checkout, it beats $PISTE_FLAKE, and from a flake in
// no checkout it is an error.
func TestResolveFlakeDirOverlayOutsideCheckout(t *testing.T) {
	tmp := t.TempDir()
	root := checkout(t, tmp, "a", "b")
	wt := filepath.Join(tmp, "wt", "abc")
	makeWorktree(t, filepath.Join(root, "overlays", "a"), wt)
	touch(t, filepath.Join(wt, "flake.nix"))
	f, err := ResolveFlakeDir(wt, env(nil), "/nonexistent", workspace.Query{Overlay: "b"}, nil)
	if err != nil || f.Dir != filepath.Join(root, "overlays", "b") || f.Overlay != "b" {
		t.Fatalf("worktree: %+v %v", f, err)
	}

	home := phys(t, t.TempDir())
	ck := checkout(t, home, "mine")
	elsewhere := filepath.Join(home, "elsewhere")
	os.Mkdir(elsewhere, 0o755)
	f, err = ResolveFlakeDir(elsewhere, env(map[string]string{"PISTE_FLAKE": "/custom/flake"}), home, workspace.Query{Overlay: "mine"}, nil)
	if err != nil || f.Dir != filepath.Join(ck, "overlays", "mine") {
		t.Fatalf("--overlay vs PISTE_FLAKE: %+v %v", f, err)
	}

	lone := filepath.Join(t.TempDir(), "lone")
	touch(t, filepath.Join(lone, "flake.nix"))
	_, err = ResolveFlakeDir(lone, env(nil), "/nonexistent", workspace.Query{Overlay: "x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "--overlay x: "+lone+" is not in a piste checkout") {
		t.Fatalf("lone flake: %v", err)
	}
}

// A checkout that is not a git repository (a tarball) would only build as
// path:, which copies overlays/ (their .git, their secrets) into the store:
// refused, from the overlay and through PISTE_ENGINE alike.
func TestEngineOverrideRefusesPathCheckout(t *testing.T) {
	tmp := t.TempDir()
	root := checkout(t, tmp, "mine")
	if err := os.Remove(filepath.Join(root, ".git")); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(root, "overlays", "mine")
	if _, err := ResolveEngineOverride(mine, env(nil), tmp); err == nil || !strings.Contains(err.Error(), root+" is not a git repository") {
		t.Fatalf("checkout: %v", err)
	}
	if _, err := ResolveEngineOverride(mine, env(map[string]string{"PISTE_ENGINE": root}), tmp); err == nil || !strings.Contains(err.Error(), "is not a git repository") {
		t.Fatalf("PISTE_ENGINE: %v", err)
	}
}

func TestEngineOverride(t *testing.T) {
	tmp := t.TempDir()
	root := checkout(t, tmp, "mine")
	mine := filepath.Join(root, "overlays", "mine")

	o, err := ResolveEngineOverride(mine, env(nil), tmp)
	if err != nil || o.Path != root || o.Reason != "the checkout around the overlay" ||
		len(o.Args()) != 3 || o.Args()[2] != "git+file://"+root {
		t.Fatalf("checkout: %+v %v %v", o, err, o.Args())
	}

	// A worktree of the overlay outside the checkout builds
	// against the checkout that holds its main repo.
	wt := filepath.Join(tmp, "wt", "abc", "fix")
	makeWorktree(t, mine, wt)
	touch(t, filepath.Join(wt, "flake.nix"))
	writeLock(t, wt, "piste")
	o, err = ResolveEngineOverride(wt, env(nil), tmp)
	if err != nil || o.Path != root || !strings.HasPrefix(o.Reason, "FALLBACK: worktree of "+mine) || o.Args()[2] != "git+file://"+root {
		t.Fatalf("worktree: %+v %v", o, err)
	}

	gitDir := filepath.Join(tmp, "engine-wt")
	touch(t, filepath.Join(gitDir, "flake.nix"))
	os.Mkdir(filepath.Join(gitDir, ".git"), 0o755)
	if o, _ := ResolveEngineOverride(mine, env(map[string]string{"PISTE_ENGINE": gitDir}), tmp); o.Path != gitDir || o.Args()[2] != "git+file://"+gitDir {
		t.Fatalf("env git: %+v %v", o, o.Args())
	}
	plain := filepath.Join(tmp, "plain")
	touch(t, filepath.Join(plain, "flake.nix"))
	if o, _ := ResolveEngineOverride(mine, env(map[string]string{"PISTE_ENGINE": plain}), tmp); o.Args()[2] != "path:"+plain {
		t.Fatalf("env plain: %+v", o.Args())
	}
	if o, _ := ResolveEngineOverride(mine, env(map[string]string{"PISTE_ENGINE": "none"}), tmp); o.Args() != nil {
		t.Fatalf("none: %+v", o)
	}
	if _, err := ResolveEngineOverride(mine, env(map[string]string{"PISTE_ENGINE": filepath.Join(tmp, "nope")}), tmp); err == nil {
		t.Fatal("want error for a missing PISTE_ENGINE flake")
	}
	if o, _ := ResolveEngineOverride(root, env(nil), tmp); o.Path != "" {
		t.Fatalf("the engine itself: %+v", o)
	}
	lone := filepath.Join(tmp, "lone")
	os.MkdirAll(lone, 0o755)
	writeLock(t, lone, "piste")
	if o, _ := ResolveEngineOverride(lone, env(nil), tmp); o.Path != "" || o.Reason != "no checkout around the flake: locked input" {
		t.Fatalf("nothing: %+v", o)
	}
}

// A Mac's name is its LocalHostName, as darwin-rebuild reads it, not
// os.Hostname's, which follows the network (janes-mbp.lan here,
// something else on the next one); without scutil's, os.Hostname's.
func TestMachineHostname(t *testing.T) {
	scutil := func(name string, err error) func() (string, error) {
		return func() (string, error) { return name, err }
	}
	osName := func() (string, error) { return "janes-mbp.lan", nil }
	for _, c := range []struct {
		goos   string
		scutil func() (string, error)
		want   string
	}{
		{"darwin", scutil("jane-mac\n", nil), "jane-mac"},
		{"darwin", scutil("", errors.New("exit status 1")), "janes-mbp.lan"},
		{"darwin", scutil("  \n", nil), "janes-mbp.lan"},
		{"linux", scutil("jane-mac", nil), "janes-mbp.lan"},
	} {
		got, err := machineHostname(c.goos, c.scutil, osName)
		if err != nil || got != c.want {
			t.Errorf("%s: %q, %v; want %q", c.goos, got, err, c.want)
		}
	}
}
