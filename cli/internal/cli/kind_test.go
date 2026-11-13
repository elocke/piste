package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/platform"
)

func TestKindString(t *testing.T) {
	for k, want := range map[Kind]string{KindHome: "home", KindDarwin: "darwin", KindNixOS: "nixos"} {
		if got := k.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(k), got, want)
		}
	}
}

// loneHome is pickTarget's warning on a NixOS machine box (user u) with no
// NixOS host, when it takes the overlay's only home of u, u@laptop.
const loneHome = "this overlay has no NixOS host for box; using the home u@laptop (piste host add makes this machine a NixOS host)"

// pickTarget: a Mac acts on its nix-darwin system; --host naming a NixOS
// host acts on it from any Linux; a NixOS machine acts on its NixOS host when
// the overlay has one; everything else on its home-manager configuration,
// user@host for --host, else flake.PickProfile's.
func TestPickTarget(t *testing.T) {
	for _, c := range []struct {
		name                  string
		darwin, nixos         bool
		host                  string
		nixosNames, homeNames []string
		want                  target
	}{
		{name: "Mac", darwin: true, want: target{Kind: KindDarwin, Name: "box"}},
		{name: "Mac, --host", darwin: true, host: "other", want: target{Kind: KindDarwin, Name: "other", Host: "other"}},
		{name: "Ubuntu", homeNames: []string{"u@box", "u@other"}, want: target{Kind: KindHome, Name: "u@box"}},
		{name: "Ubuntu, the only u@*", homeNames: []string{"u@other", "v@box"}, want: target{Kind: KindHome, Name: "u@other"}},
		{name: "Ubuntu, no home of its own", homeNames: []string{"v@box"}, want: target{Kind: KindHome, Name: "u@linux"}},
		{name: "Ubuntu, a NixOS host of its name", nixosNames: []string{"box"}, homeNames: []string{"u@box"},
			want: target{Kind: KindHome, Name: "u@box"}},
		{name: "NixOS with a NixOS host", nixos: true, nixosNames: []string{"box"},
			want: target{Kind: KindNixOS, Name: "box"}},
		{name: "NixOS with only a home", nixos: true, nixosNames: []string{"other"}, homeNames: []string{"u@box"},
			want: target{Kind: KindHome, Name: "u@box"}},
		{name: "--host naming a NixOS host, from Ubuntu", host: "server", nixosNames: []string{"server"},
			want: target{Kind: KindNixOS, Name: "server", Host: "server"}},
		{name: "--host naming a home, from Ubuntu", host: "laptop", nixosNames: []string{"server"},
			want: target{Kind: KindHome, Name: "u@laptop", Host: "laptop"}},
		{name: "--host naming a home, from NixOS", nixos: true, host: "laptop", nixosNames: []string{"box"},
			want: target{Kind: KindHome, Name: "u@laptop", Host: "laptop"}},
		{name: "--host naming a Linux host, from a Mac", darwin: true, host: "server", nixosNames: []string{"server"},
			want: target{Kind: KindDarwin, Name: "server", Host: "server"}},
		// switch's hostname guard: a NixOS machine none of the overlay's
		// NixOS hosts is, with no home there either.
		{name: "NixOS, another NixOS host and no home", nixos: true, nixosNames: []string{"other", "server"}, homeNames: []string{"v@box"},
			want: target{Kind: KindHome, Name: "u@linux", Unmatched: []string{"other", "server"}}},
		// A lone home of another name, with no NixOS host for this one: said.
		{name: "NixOS, another NixOS host and the only u@*", nixos: true, nixosNames: []string{"other"}, homeNames: []string{"u@laptop"},
			want: target{Kind: KindHome, Name: "u@laptop", Warning: loneHome}},
		{name: "NixOS, no NixOS hosts and the only u@*", nixos: true, homeNames: []string{"u@laptop", "v@box"},
			want: target{Kind: KindHome, Name: "u@laptop", Warning: loneHome}},
		{name: "NixOS, its own home among others: not said", nixos: true, homeNames: []string{"u@box", "u@laptop"},
			want: target{Kind: KindHome, Name: "u@box"}},
		{name: "Ubuntu, the only u@*: not said", homeNames: []string{"u@laptop"},
			want: target{Kind: KindHome, Name: "u@laptop"}},
		{name: "NixOS, --host naming the only u@*: not said", nixos: true, host: "laptop", homeNames: []string{"u@laptop"},
			want: target{Kind: KindHome, Name: "u@laptop", Host: "laptop"}},
		{name: "NixOS, no NixOS hosts and no home", nixos: true, homeNames: []string{"v@box"},
			want: target{Kind: KindHome, Name: "u@linux"}},
		{name: "NixOS, --host matching nothing", nixos: true, host: "laptop", nixosNames: []string{"other"},
			want: target{Kind: KindHome, Name: "u@laptop", Host: "laptop"}},
		{name: "Ubuntu, another NixOS host and no home", nixosNames: []string{"other"},
			want: target{Kind: KindHome, Name: "u@linux"}},
	} {
		got := pickTarget(c.darwin, c.nixos, c.host, "box", "u", c.nixosNames, c.homeNames)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// A NixOS machine the overlay has both as a NixOS host and as a home-manager
// one acts on the NixOS one, and says so in one line. resolveTarget runs the
// evals pickTarget needs.
func TestKindForBothPrefersNixOS(t *testing.T) {
	const want = `this machine has both nixosConfigurations.box and homeConfigurations."u@box"; using the NixOS one`
	got := pickTarget(false, true, "", "box", "u", []string{"box"}, []string{"u@box"})
	if !reflect.DeepEqual(got, target{Kind: KindNixOS, Name: "box", Warning: want}) {
		t.Errorf("pickTarget: got %+v", got)
	}

	fakeNixConfigs(t, `["box"]`, `["u@box"]`)
	a, _ := kindApp()
	tg, err := a.resolveTarget(platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "box", User: "u", FlakeDir: t.TempDir()}, "")
	if err != nil || tg.Kind != KindNixOS || tg.Name != "box" || tg.Warning != want {
		t.Errorf("resolveTarget: got %+v %v", tg, err)
	}
}

// resolveTarget evaluates only the names pickTarget needs: none on a Mac,
// homeConfigurations alone where it did before (no --host, not NixOS),
// nixosConfigurations for --host.
func TestResolveTargetEvals(t *testing.T) {
	for _, c := range []struct {
		name      string
		p         platform.Platform
		host      string
		want      target
		wantEvals []string
	}{
		{"Mac", platform.Platform{IsDarwin: true, Hostname: "box", User: "u"}, "",
			target{Kind: KindDarwin, Name: "box"}, nil},
		{"Ubuntu", platform.Platform{IsLinux: true, Hostname: "box", User: "u"}, "",
			target{Kind: KindHome, Name: "u@box"}, []string{"homeConfigurations"}},
		{"Ubuntu, --host", platform.Platform{IsLinux: true, Hostname: "box", User: "u"}, "server",
			target{Kind: KindNixOS, Name: "server", Host: "server"}, []string{"nixosConfigurations"}},
		{"NixOS", platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "server", User: "u"}, "",
			target{Kind: KindNixOS, Name: "server"}, []string{"nixosConfigurations", "homeConfigurations"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := fakeNixConfigs(t, `["server"]`, `["u@box"]`)
			a, errs := kindApp()
			c.p.FlakeDir = t.TempDir()
			got, err := a.resolveTarget(c.p, c.host)
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v %v (stderr %q)", got, err, errs)
			}
			var evals []string
			for _, l := range nonEmptyLines(log) {
				evals = append(evals, evalOf(l))
			}
			if strings.Join(evals, " ") != strings.Join(c.wantEvals, " ") {
				t.Errorf("evals %q, want %q", evals, c.wantEvals)
			}
		})
	}
}

// On a NixOS machine with no --host, an overlay without nixosConfigurations
// (the eval's `or { }`: no names) is a home, as before NixOS hosts; an eval
// that fails is an error, not a silent home (Ruling R13). With --host, a
// failed eval leaves a home, as on any Linux.
func TestResolveTargetNixOSEvalFails(t *testing.T) {
	nixos := platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "box", User: "u"}
	for _, c := range []struct {
		name, reply, host string
		want              target
		wantErr           string
	}{
		{name: "absent", reply: `echo '[]'`, want: target{Kind: KindHome, Name: "u@box"}},
		{name: "failed", reply: `echo 'error: boom' >&2; exit 1`, wantErr: "can't read nixosConfigurations from %s: error: boom"},
		{name: "failed, --host", reply: `echo 'error: boom' >&2; exit 1`, host: "other", want: target{Kind: KindHome, Name: "u@other", Host: "other"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeNix(t, `case "$*" in
*'(o.nixosConfigurations or { })'*) `+c.reply+` ;;
*'(o.homeConfigurations or { })'*|*'#homeConfigurations '*) echo '["u@box"]' ;;
esac`)
			a, errs := kindApp()
			p := nixos
			p.FlakeDir = t.TempDir()
			got, err := a.resolveTarget(p, c.host)
			if c.wantErr != "" {
				var ee ExitError
				if want := fmt.Sprintf(c.wantErr, p.FlakeDir) + "\n"; !errors.As(err, &ee) || ee.Code != 1 || errs.String() != want {
					t.Fatalf("got %+v %v and %q, want exit 1 and %q", got, err, errs.String(), want)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v %v, want %+v", got, err, c.want)
			}
		})
	}
}

// Every configuration-names eval reads the overlay against the engine
// builds use (here $PISTE_ENGINE's): an overlay pinned to a release before
// NixOS hosts would throw on a NixOS host's key, so a home on Ubuntu would
// fall back to user@linux and a Mac's hostname guard would be skipped.
func TestResolveTargetNamesOverride(t *testing.T) {
	engine := t.TempDir()
	if err := os.WriteFile(filepath.Join(engine, "flake.nix"), []byte("{ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	names := func(dir, attr string) string {
		return "eval --json " + dir + "#. --override-input piste path:" + engine +
			" --apply o: builtins.attrNames (o." + attr + " or { }) --no-write-lock-file"
	}
	for _, c := range []struct {
		name string
		p    platform.Platform
		want target
		// the evals, by output read
		evals []string
	}{
		{"NixOS", platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "box", User: "u"},
			target{Kind: KindNixOS, Name: "box"}, []string{"nixosConfigurations", "homeConfigurations"}},
		{"Ubuntu", platform.Platform{IsLinux: true, Hostname: "box", User: "u"},
			target{Kind: KindHome, Name: "u@pad"}, []string{"homeConfigurations"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := fakeNixConfigs(t, `["box"]`, `["u@pad"]`)
			a, errs := kindApp()
			a.Env.Getenv = envOf(map[string]string{"USER": "u", "PISTE_ENGINE": engine})
			c.p.FlakeDir = t.TempDir()
			got, err := a.resolveTarget(c.p, "")
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v %v (stderr %q)", got, err, errs)
			}
			var want []string
			for _, e := range c.evals {
				want = append(want, names(c.p.FlakeDir, e))
			}
			if lines := nonEmptyLines(log); !reflect.DeepEqual(lines, want) {
				t.Errorf("evals:\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// An engine override that can't be resolved ($PISTE_ENGINE naming no flake,
// a checkout that isn't a git repository) stops nothing that only reads the
// names off NixOS: they are read without it, as before NixOS hosts (status,
// log and rollback don't need it; a build says why it can't). On a NixOS
// machine it stays an error (Rulings R13, R14): the names decide whether
// this machine is a whole system, and are read only as builds read them.
func TestResolveTargetBadOverride(t *testing.T) {
	noFlake := func(t *testing.T) (dir string, env map[string]string) {
		return t.TempDir(), map[string]string{"USER": "u", "PISTE_ENGINE": "/nonexistent"}
	}
	notGit := func(t *testing.T) (dir string, env map[string]string) {
		root := t.TempDir()
		dir = filepath.Join(root, "overlays", "o")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".piste"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		return dir, map[string]string{"USER": "u"}
	}
	for _, c := range []struct {
		name    string
		setup   func(*testing.T) (string, map[string]string)
		p       platform.Platform
		host    string
		want    target
		evals   []string
		wantErr string // stderr's start, for an error
	}{
		{name: "Ubuntu, PISTE_ENGINE with no flake", setup: noFlake, p: platform.Platform{IsLinux: true, Hostname: "box", User: "u"},
			want: target{Kind: KindHome, Name: "u@box"}, evals: []string{"homeConfigurations"}},
		{name: "Ubuntu, a checkout that isn't a git repository", setup: notGit, p: platform.Platform{IsLinux: true, Hostname: "box", User: "u"},
			want: target{Kind: KindHome, Name: "u@box"}, evals: []string{"homeConfigurations"}},
		{name: "Ubuntu, --host", setup: noFlake, p: platform.Platform{IsLinux: true, Hostname: "box", User: "u"}, host: "server",
			want: target{Kind: KindNixOS, Name: "server", Host: "server"}, evals: []string{"nixosConfigurations"}},
		{name: "NixOS, PISTE_ENGINE with no flake", setup: noFlake, p: platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "server", User: "u"},
			wantErr: "PISTE_ENGINE=/nonexistent: no flake.nix in /nonexistent"},
		{name: "NixOS, a checkout that isn't a git repository", setup: notGit, p: platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "server", User: "u"},
			wantErr: "checkout "},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := fakeNixConfigs(t, `["server"]`, `["u@box"]`)
			a, errs := kindApp()
			dir, env := c.setup(t)
			a.Env.Getenv = envOf(env)
			c.p.FlakeDir = dir
			got, err := a.resolveTarget(c.p, c.host)
			if c.wantErr != "" {
				var ee ExitError
				if !errors.As(err, &ee) || ee.Code != 1 || !strings.HasPrefix(errs.String(), c.wantErr) {
					t.Fatalf("got %+v %v and %q, want exit 1 and %q…", got, err, errs.String(), c.wantErr)
				}
				if n := len(nonEmptyLines(log)); n != 0 {
					t.Errorf("%d evals, want none", n)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) || errs.Len() != 0 {
				t.Fatalf("got %+v %v (stderr %q), want %+v", got, err, errs, c.want)
			}
			var evals []string
			for _, l := range nonEmptyLines(log) {
				if strings.Contains(l, "--override-input") {
					t.Errorf("an eval with an override: %s", l)
				}
				evals = append(evals, evalOf(l))
			}
			if !reflect.DeepEqual(evals, c.evals) {
				t.Errorf("evals %q, want %q", evals, c.evals)
			}
		})
	}
}

// switch's hostname guard on a Mac reads darwinConfigurations' names as
// builds read the overlay, with the engine override.
func TestDarwinConfigNamesOverride(t *testing.T) {
	engine := t.TempDir()
	if err := os.WriteFile(filepath.Join(engine, "flake.nix"), []byte("{ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	log := fakeNix(t, `case "$*" in *'(o.darwinConfigurations or { })'*|*'#darwinConfigurations '*) echo '["mac"]' ;; esac`)
	a, errs := kindApp()
	a.Env.Getenv = envOf(map[string]string{"USER": "u", "PISTE_ENGINE": engine})
	dir := t.TempDir()
	o, err := platform.ResolveEngineOverride(dir, a.Env.Getenv, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.darwinConfigNames(platform.Platform{IsDarwin: true, FlakeDir: dir}, o)
	if err != nil || !reflect.DeepEqual(got, []string{"mac"}) {
		t.Fatalf("got %q %v (stderr %q)", got, err, errs)
	}
	want := "eval --json " + dir + "#. --override-input piste path:" + engine +
		" --apply o: builtins.attrNames (o.darwinConfigurations or { }) --no-write-lock-file"
	if lines := nonEmptyLines(log); len(lines) != 1 || lines[0] != want {
		t.Errorf("evals:\n%s\nwant\n%s", strings.Join(lines, "\n"), want)
	}
	// A failed eval is no names, as before: the build says why.
	fakeNix(t, `echo 'error: boom' >&2; exit 1`)
	if got, err := a.darwinConfigNames(platform.Platform{IsDarwin: true, FlakeDir: dir}, o); got != nil || err != nil {
		t.Errorf("a failed eval: got %q %v, want none", got, err)
	}
}

// machineKind, for the commands about this machine (status, log, clean):
// a Mac and any Linux but NixOS without an eval; a NixOS machine as
// resolveTarget has it, and a home when there is no overlay to read.
func TestMachineKind(t *testing.T) {
	for _, c := range []struct {
		name      string
		p         platform.Platform
		want      Kind
		wantEvals int
	}{
		{"Mac", platform.Platform{IsDarwin: true, Hostname: "box"}, KindDarwin, 0},
		{"Ubuntu", platform.Platform{IsLinux: true, Hostname: "box"}, KindHome, 0},
		{"NixOS, its NixOS host", platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "server"}, KindNixOS, 2},
		{"NixOS, a home", platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "box"}, KindHome, 2},
		{"NixOS, no overlay", platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "server", FlakeErr: errors.New("no flake")}, KindHome, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			log := fakeNixConfigs(t, `["server"]`, `["u@box"]`)
			a, _ := kindApp()
			c.p.User, c.p.FlakeDir = "u", t.TempDir()
			got, err := a.machineKind(c.p)
			if err != nil || got != c.want {
				t.Errorf("got %v %v, want %v", got, err, c.want)
			}
			if n := len(nonEmptyLines(log)); n != c.wantEvals {
				t.Errorf("%d evals, want %d", n, c.wantEvals)
			}
		})
	}
}

// piste github finds a NixOS host's file on a NixOS machine: its host
// directory's default.nix, by the hostname nixosConfigurations has.
func TestGithubHostFileNixOS(t *testing.T) {
	dir := t.TempDir()
	flake := `{ outputs = inputs@{ piste, self, ... }: piste.lib.mkFlake {
  inherit inputs self;
  user = "u";
  hosts."server" = { system = "x86_64-linux"; nixos = true; modules = [ ./hosts/server ]; };
  hosts."u@box" = { system = "x86_64-linux"; modules = [ ./hosts/box.nix ]; };
}; }
`
	for rel, text := range map[string]string{"flake.nix": flake, "hosts/server/default.nix": "{ }\n", "hosts/box.nix": "{ }\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fakeNixConfigs(t, `["server"]`, `["u@box"]`)
	a, _ := kindApp()
	for _, c := range []struct{ name, hostname, want string }{
		{"its NixOS host", "server", "hosts/server/default.nix"},
		{"a home on NixOS", "box", "hosts/box.nix"},
	} {
		p := platform.Platform{IsLinux: true, IsNixOS: true, Hostname: c.hostname, User: "u", FlakeDir: dir}
		if got := a.githubHostFile(context.Background(), p, ""); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// piste github reads the names it picks a host by as builds do, with the
// engine override: an overlay pinned before NixOS hosts throws without it
// once it has one, and the only home or the only Mac would be missed.
func TestGithubHostFileNamesOverride(t *testing.T) {
	engine := t.TempDir()
	if err := os.WriteFile(filepath.Join(engine, "flake.nix"), []byte("{ }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	flake := `{ outputs = inputs@{ piste, self, ... }: piste.lib.mkFlake {
  inherit inputs self;
  user = "u";
  hosts."jane-mac" = { system = "aarch64-darwin"; modules = [ ./hosts/jane-mac.nix ]; };
  hosts."u@pad" = { system = "x86_64-linux"; modules = [ ./hosts/pad.nix ]; };
  hosts."server" = { system = "x86_64-linux"; nixos = true; modules = [ ./hosts/server ]; };
}; }
`
	for rel, text := range map[string]string{"flake.nix": flake, "hosts/jane-mac.nix": "{ }\n", "hosts/pad.nix": "{ }\n", "hosts/server/default.nix": "{ }\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Only an eval with the override answers; any other throws, as the
	// pinned engine would on the NixOS host.
	fakeNix(t, `case "$*" in
*'--override-input piste'*'(o.darwinConfigurations or { })'*) echo '["jane-mac"]' ;;
*'--override-input piste'*'(o.homeConfigurations or { })'*) echo '["u@pad"]' ;;
*'--override-input piste'*'(o.nixosConfigurations or { })'*) echo '["server"]' ;;
*) echo "error: mkFlake: host server: unknown key nixos" >&2; exit 1 ;;
esac`)
	a, _ := kindApp()
	a.Env.Getenv = envOf(map[string]string{"USER": "u", "PISTE_ENGINE": engine})
	for _, c := range []struct {
		name string
		p    platform.Platform
		want string
	}{
		{"a Mac whose hostname isn't its host's", platform.Platform{IsDarwin: true, Hostname: "JM0001", User: "u", FlakeDir: dir}, "hosts/jane-mac.nix"},
		{"Ubuntu, the only home", platform.Platform{IsLinux: true, Hostname: "box", User: "u", FlakeDir: dir}, "hosts/pad.nix"},
	} {
		if got := a.githubHostFile(context.Background(), c.p, ""); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// overlayHosts lists an overlay's NixOS hosts beside its Macs and homes.
func TestOverlayHostsNixOS(t *testing.T) {
	log := fakeNix(t, `echo '{"darwin": ["mac"], "home": ["u@box"], "nixos": ["server"]}'`)
	a, _ := kindApp()
	h, err := a.overlayHosts(filepath.Join(t.TempDir(), "piste", "overlays", "o"))
	if err != nil || strings.Join(h.NixOS, " ") != "server" || strings.Join(h.Darwin, " ") != "mac" || strings.Join(h.Home, " ") != "u@box" {
		t.Fatalf("got %+v %v", h, err)
	}
	if l := readFile(t, log); !strings.Contains(l, "nixos = builtins.attrNames (o.nixosConfigurations or { })") {
		t.Errorf("hosts eval: %s", l)
	}
}

// hostRoles reads a NixOS host's roles from its user's home-manager home,
// as on a Mac, over nixosConfigurations.
func TestHostRolesNixOS(t *testing.T) {
	for _, c := range []struct {
		kind Kind
		want string
	}{
		{KindHome, `(c.config.piste.roles or { })) (o.homeConfigurations or { })`},
		{KindDarwin, `(c.config.home-manager.users."u".piste.roles or { })) (o.darwinConfigurations or { })`},
		{KindNixOS, `(c.config.home-manager.users."u".piste.roles or { })) (o.nixosConfigurations or { })`},
	} {
		t.Run(c.kind.String(), func(t *testing.T) {
			log := fakeNix(t, `echo '{"roles": {"server": ["dev"]}}'`)
			a, _ := kindApp()
			got, err := a.hostRoles(filepath.Join(t.TempDir(), "piste", "overlays", "o"), "u", c.kind)
			if err != nil || strings.Join(got["server"], " ") != "dev" {
				t.Fatalf("got %v %v", got, err)
			}
			if l := readFile(t, log); !strings.Contains(l, c.want) {
				t.Errorf("roles eval: %s\nwant it to contain %s", l, c.want)
			}
		})
	}
}

// thisMachine's query knows a NixOS machine by its marker.
func TestThisMachineNixOS(t *testing.T) {
	old := platform.NixOSMarker
	t.Cleanup(func() { platform.NixOSMarker = old })
	platform.NixOSMarker = filepath.Join(t.TempDir(), "NIXOS")
	a, _ := kindApp()
	if q, _, err := a.thisMachine(); err != nil || q.NixOS {
		t.Errorf("no marker: got %+v %v", q, err)
	}
	if err := os.WriteFile(platform.NixOSMarker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	q, _, err := a.thisMachine()
	if want := runtime.GOOS != "darwin"; err != nil || q.NixOS != want { // a Mac is never NixOS
		t.Errorf("marker: got %+v %v, want NixOS %v", q, err, want)
	}
}

func kindApp() (*App, *bytes.Buffer) {
	var errs bytes.Buffer
	return &App{Stdout: &bytes.Buffer{}, Stderr: &errs, Env: Env{Getenv: envOf(map[string]string{"USER": "u"})}}, &errs
}

// fakeNix puts on PATH a nix that logs its argv (one line per call, to the
// file it returns) and runs body.
func fakeNix(t *testing.T, body string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "calls")
	t.Setenv("PATH", binDir(t, map[string]string{"nix": "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\n" + body + "\n"}))
	return log
}

// fakeNixConfigs is a fakeNix answering the configuration-name evals.
func fakeNixConfigs(t *testing.T, nixos, home string) string {
	t.Helper()
	return fakeNix(t, `case "$*" in
*'(o.nixosConfigurations or { })'*) echo '`+nixos+`' ;;
*'(o.homeConfigurations or { })'*|*'#homeConfigurations '*) echo '`+home+`' ;;
esac`)
}

// evalOf names a logged configuration-names eval by the output it reads.
func evalOf(call string) string {
	for _, attr := range []string{"nixosConfigurations", "homeConfigurations", "darwinConfigurations"} {
		if strings.Contains(call, "(o."+attr+" or { })") {
			return attr
		}
	}
	ref := strings.Fields(call)[2] // eval --json <flake>#<attr> ...
	return ref[strings.Index(ref, "#")+1:]
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func nonEmptyLines(p string) []string {
	b, _ := os.ReadFile(p)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// status and log read this machine's generations: on a NixOS machine whose
// overlay's NixOS hosts can't be evaluated, the system's (they need no
// overlay), with a warning, where every other command refuses.
func TestGenerationsKindNamesUnread(t *testing.T) {
	fakeNix(t, `case "$*" in
*'(o.nixosConfigurations or { })'*) echo 'error: boom' >&2; exit 1 ;;
*) echo '["u@box"]' ;;
esac`)
	a, errs := kindApp()
	p := platform.Platform{IsLinux: true, IsNixOS: true, Hostname: "box", User: "u", FlakeDir: t.TempDir()}
	got, err := a.generationsKind(p)
	out := a.Stdout.(*bytes.Buffer).String()
	if err != nil || got != KindNixOS || errs.Len() != 0 ||
		out != "can't read nixosConfigurations from "+p.FlakeDir+": error: boom; this machine's generations are the system's\n" {
		t.Errorf("got %v %v, stdout %q, stderr %q", got, err, out, errs)
	}
	// machineKind, for the commands that need the overlay, refuses.
	a, errs = kindApp()
	if _, err := a.machineKind(p); err == nil || errs.String() != "can't read nixosConfigurations from "+p.FlakeDir+": error: boom\n" {
		t.Errorf("machineKind: %v, stderr %q", err, errs)
	}
	// Elsewhere it is machineKind's.
	a, _ = kindApp()
	if got, err := a.generationsKind(platform.Platform{IsLinux: true, Hostname: "box", User: "u", FlakeDir: t.TempDir()}); err != nil || got != KindHome {
		t.Errorf("Ubuntu: %v %v", got, err)
	}
}
