package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/workspace"
)

// The refusals' text, for jane.
const (
	refuseSSH      = "you're connected over SSH and the new config gives jane no SSH keys: add users.users.jane.openssh.authorizedKeys.keys to your host"
	refuseSSHOff   = "you're connected over SSH and the new config turns the SSH server off: keep services.openssh.enable = true"
	refuseSudo     = `the new config takes sudo away from jane: keep "wheel" in users.users.jane.extraGroups`
	refusePassword = "sudo needs no password today, and the new config would ask for one jane doesn't have: set security.sudo.wheelNeedsPassword = false or give the user a password (if you do have one, run piste switch --force)"
	refuseBoot     = "the new config enables no boot loader: carry over your machine's boot.loader settings (piste host add does)"
	refuseMachine  = "the new config's root filesystem is /dev/disk/by-uuid/0000-other, this machine's is /dev/vda1: this host's config looks like another machine's"
	// Switched as root, the overlay's user jane.
	refuseRootLogin = `you're connected over SSH as root and the new config turns root's SSH login off: set services.openssh.settings.PermitRootLogin = "prohibit-password"; or switch as jane`
)

// Each rule refuses on its own, and only when what it protects is at
// stake: an SSH session, sudo the user has today, a machine that boots
// itself, this machine's disks.
func TestPreflight(t *testing.T) {
	// A config that keeps everything, on an SSH session with passwordless
	// sudo today: the base each row changes.
	okFacts := func() facts {
		return facts{SSHEnabled: true, Keys: 1, KeysInHomedir: true, PermitRootLogin: "prohibit-password", Wheel: true, SudoEnabled: true,
			WheelNeedsPassword: true, HasPassword: true, BootLoader: true, RootDevice: "/dev/disk/by-label/nixos"}
	}
	okMachine := func() machine {
		return machine{User: "jane", OverlayUser: "jane", Piste: "piste", SSHSession: true, SudoPasswordless: true, RunningRoot: "/dev/vda1"}
	}
	resolve := func(dev string) string {
		if dev == "/dev/disk/by-label/nixos" {
			return "/dev/vda1"
		}
		return dev
	}
	for _, c := range []struct {
		name string
		f    func(*facts)
		m    func(*machine)
		want []refusal
	}{
		{name: "keeps everything"},

		{name: "ssh: no keys, none read from home", f: func(f *facts) { f.Keys, f.KeysInHomedir = 0, false },
			m: func(m *machine) { m.HomeKeys = true }, want: []refusal{{"ssh", refuseSSH}}},
		{name: "ssh: no keys, none in ~/.ssh/authorized_keys", f: func(f *facts) { f.Keys = 0 },
			want: []refusal{{"ssh", refuseSSH}}},
		{name: "ssh: keys in ~/.ssh/authorized_keys", f: func(f *facts) { f.Keys = 0 },
			m: func(m *machine) { m.HomeKeys = true }},
		{name: "ssh: a key file", f: func(f *facts) { f.Keys, f.KeyFiles, f.KeysInHomedir = 0, 1, false }},
		{name: "ssh: not over SSH", f: func(f *facts) { f.Keys, f.KeysInHomedir = 0, false },
			m: func(m *machine) { m.SSHSession = false }},
		{name: "ssh: password login on", f: func(f *facts) { f.Keys, f.KeysInHomedir, f.PasswordAuth = 0, false, true }},
		// NixOS's sshd PAM takes a password only with PasswordAuthentication.
		{name: "ssh: the server off", f: func(f *facts) { f.SSHEnabled = false }, want: []refusal{{"ssh", refuseSSHOff}}},
		{name: "ssh: the server off, not over SSH", f: func(f *facts) { f.SSHEnabled = false },
			m: func(m *machine) { m.SSHSession = false }},
		{name: "ssh: the server off and no keys: the server's line only", f: func(f *facts) { f.SSHEnabled, f.Keys, f.KeysInHomedir = false, 0, false },
			want: []refusal{{"ssh", refuseSSHOff}}},
		{name: "ssh: keyboard-interactive alone isn't password login", f: func(f *facts) { f.Keys, f.KeysInHomedir, f.KbdInteractive = 0, false, true },
			want: []refusal{{"ssh", refuseSSH}}},

		{name: "sudo: not in wheel", f: func(f *facts) { f.Wheel = false }, want: []refusal{{"sudo", refuseSudo}}},
		{name: "sudo: off", f: func(f *facts) { f.SudoEnabled = false }, want: []refusal{{"sudo", refuseSudo}}},
		{name: "sudo: a password the user doesn't have", f: func(f *facts) { f.HasPassword = false },
			want: []refusal{{"sudo", refusePassword}}},
		{name: "sudo: a NOPASSWD rule for the user", f: func(f *facts) { f.HasPassword, f.NopasswdForUser = false, true }},
		{name: "sudo: wheel needs no password", f: func(f *facts) { f.HasPassword, f.WheelNeedsPassword = false, false }},
		{name: "sudo: asks a password today already", f: func(f *facts) { f.HasPassword = false },
			m: func(m *machine) { m.SudoPasswordless = false }},
		{name: "sudo: the --force advice runs piste as it runs here", f: func(f *facts) { f.HasPassword = false },
			m:    func(m *machine) { m.Piste = "nix run .#piste --" },
			want: []refusal{{"sudo", strings.Replace(refusePassword, "run piste switch", "run nix run .#piste -- switch", 1)}}},

		// Run as root, sudo isn't what the switch needs: no sudo rule.
		{name: "sudo: root, outside wheel", f: func(f *facts) { f.Wheel = false }, m: func(m *machine) { m.User = "root" }},
		{name: "sudo: root, sudo off", f: func(f *facts) { f.SudoEnabled = false }, m: func(m *machine) { m.User = "root" }},
		{name: "sudo: root, a password root doesn't have", f: func(f *facts) { f.HasPassword = false }, m: func(m *machine) { m.User = "root" }},
		{name: "ssh: root's keys, as anyone's", f: func(f *facts) { f.Keys, f.KeysInHomedir, f.Wheel = 0, false, false },
			m:    func(m *machine) { m.User = "root" },
			want: []refusal{{"ssh", strings.ReplaceAll(refuseSSH, "jane", "root")}}},
		// Root over SSH logs in as root: a config that turns root's login off
		// (the base's "no") locks it out, keys or not.
		{name: "ssh: root over SSH, root's login off", f: func(f *facts) { f.PermitRootLogin = "no" },
			m: func(m *machine) { m.User = "root" }, want: []refusal{{"ssh", refuseRootLogin}}},
		{name: "ssh: root over SSH, root's login for forced commands only", f: func(f *facts) { f.PermitRootLogin = "forced-commands-only" },
			m: func(m *machine) { m.User = "root" }, want: []refusal{{"ssh", refuseRootLogin}}},
		{name: "ssh: root over SSH, root's login off and no keys: the login's line only",
			f: func(f *facts) { f.PermitRootLogin, f.Keys, f.KeysInHomedir = "no", 0, false },
			m: func(m *machine) { m.User = "root" }, want: []refusal{{"ssh", refuseRootLogin}}},
		{name: "ssh: root over SSH, the server off: the server's line only", f: func(f *facts) { f.PermitRootLogin, f.SSHEnabled = "no", false },
			m: func(m *machine) { m.User = "root" }, want: []refusal{{"ssh", refuseSSHOff}}},
		{name: "ssh: root over SSH, the overlay's user unknown", f: func(f *facts) { f.PermitRootLogin = "no" },
			m:    func(m *machine) { m.User, m.OverlayUser = "root", "" },
			want: []refusal{{"ssh", strings.Replace(refuseRootLogin, "switch as jane", "switch as your overlay's user", 1)}}},
		{name: "ssh: root over SSH, root logs in by key", m: func(m *machine) { m.User = "root" }},
		{name: "ssh: root over SSH, root logs in by any means", f: func(f *facts) { f.PermitRootLogin = "yes" },
			m: func(m *machine) { m.User = "root" }},
		// null: sshd's own default, prohibit-password.
		{name: "ssh: root over SSH, sshd's default", f: func(f *facts) { f.PermitRootLogin = "" },
			m: func(m *machine) { m.User = "root" }},
		{name: "ssh: root not over SSH, root's login off", f: func(f *facts) { f.PermitRootLogin = "no" },
			m: func(m *machine) { m.User, m.SSHSession = "root", false }},
		{name: "ssh: jane, root's login off", f: func(f *facts) { f.PermitRootLogin = "no" }},

		{name: "boot: no loader", f: func(f *facts) { f.BootLoader = false }, want: []refusal{{"boot", refuseBoot}}},
		{name: "boot: a container boots no loader", f: func(f *facts) { f.BootLoader, f.IsContainer = false, true }},

		{name: "machine: another root", f: func(f *facts) { f.RootDevice = "/dev/disk/by-uuid/0000-other" },
			want: []refusal{{"machine", refuseMachine}}},
		{name: "machine: the same root by another name", m: func(m *machine) { m.RunningRoot = "/dev/disk/by-label/nixos" }},
		{name: "machine: the config names no root", f: func(f *facts) { f.RootDevice = "" }},
		{name: "machine: in a container", f: func(f *facts) { f.RootDevice = "/dev/disk/by-uuid/0000-other" },
			m: func(m *machine) { m.InContainer = true }},
		{name: "machine: the running root unknown", f: func(f *facts) { f.RootDevice = "/dev/disk/by-uuid/0000-other" },
			m: func(m *machine) { m.RunningRoot = "" }},

		{name: "every rule, in order", f: func(f *facts) {
			*f = facts{SSHEnabled: true, WheelNeedsPassword: true, SudoEnabled: true, RootDevice: "/dev/disk/by-uuid/0000-other"}
		}, want: []refusal{{"ssh", refuseSSH}, {"sudo", refuseSudo}, {"boot", refuseBoot}, {"machine", refuseMachine}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, m := okFacts(), okMachine()
			if c.f != nil {
				c.f(&f)
			}
			if c.m != nil {
				c.m(&m)
			}
			if got := preflight(f, m, resolve); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// runPreflight reads this machine's root as findmnt names it, and the
// machine rule says it so (/dev/mapper/root, not the /dev/dm-0 it resolves
// to: the name the person knows), comparing the two roots resolved. Run as
// root, sudo isn't probed: the sudo rule isn't read. Root over SSH is told
// the overlay's user, from its flake.nix.
func TestRunPreflightMachine(t *testing.T) {
	old := platform.ContainerMarker
	t.Cleanup(func() { platform.ContainerMarker = old })
	platform.ContainerMarker = filepath.Join(t.TempDir(), "container")
	dev := t.TempDir()
	for _, d := range []string{"mapper", "by-uuid"} {
		if err := os.MkdirAll(filepath.Join(dev, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"dm-0", "vdb1"} {
		if err := os.WriteFile(filepath.Join(dev, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, l := range []string{"mapper/root", "by-uuid/aaaa"} {
		if err := os.Symlink("../dm-0", filepath.Join(dev, l)); err != nil {
			t.Fatal(err)
		}
	}
	mapper := filepath.Join(dev, "mapper", "root")
	tmp := t.TempDir()
	calls, factsFile := filepath.Join(tmp, "calls"), filepath.Join(tmp, "facts.json")
	t.Setenv("PATH", binDir(t, map[string]string{
		"nix":     "#!/bin/sh\necho nix >> '" + calls + "'\nIFS= read -r f < '" + factsFile + "'\nprintf '%s\\n' \"$f\"\n",
		"findmnt": "#!/bin/sh\necho findmnt >> '" + calls + "'\necho '" + mapper + "'\n",
		"sudo":    "#!/bin/sh\necho \"sudo $*\" >> '" + calls + "'\n",
	}))
	keeps := facts{SSHEnabled: true, Keys: 1, Wheel: true, SudoEnabled: true, WheelNeedsPassword: true, HasPassword: true, BootLoader: true}
	noSudo := keeps
	noSudo.Wheel, noSudo.HasPassword = false, false
	rootOff := keeps
	rootOff.PermitRootLogin = "no"
	uuid := filepath.Join(dev, "by-uuid", "aaaa")
	for _, c := range []struct {
		name, user, root string
		f                facts
		want             []refusal
		calls            []string
		ssh              bool   // the session is over SSH
		flake            string // the overlay's flake.nix, "" for none
	}{
		{name: "another root", user: "jane", root: filepath.Join(dev, "vdb1"), f: keeps,
			want: []refusal{{"machine", "the new config's root filesystem is " + filepath.Join(dev, "vdb1") + ", this machine's is " + mapper +
				": this host's config looks like another machine's"}},
			calls: []string{"nix", "findmnt"}},
		{name: "the same root by uuid", user: "jane", root: uuid, f: keeps, calls: []string{"nix", "findmnt"}},
		{name: "root: no sudo rule, sudo not probed", user: "root", root: uuid, f: noSudo, calls: []string{"nix", "findmnt"}},
		{name: "jane: sudo probed", user: "jane", root: uuid, f: noSudo, want: []refusal{{"sudo", refuseSudo}},
			calls: []string{"nix", "sudo -n -k true", "findmnt"}},
		// Root over SSH, root's login off: the advice names the overlay's
		// user, from its flake.nix.
		{name: "root over SSH: the overlay's user", user: "root", root: uuid, f: rootOff, ssh: true,
			flake: "{\n  outputs = inputs: inputs.piste.lib.mkFlake {\n    user = \"jane\";\n  };\n}\n",
			want:  []refusal{{"ssh", refuseRootLogin}}, calls: []string{"nix", "findmnt"}},
		{name: "root over SSH: no flake.nix user", user: "root", root: uuid, f: rootOff, ssh: true,
			want:  []refusal{{"ssh", strings.Replace(refuseRootLogin, "switch as jane", "switch as your overlay's user", 1)}},
			calls: []string{"nix", "findmnt"}},
		{name: "root, not over SSH", user: "root", root: uuid, f: rootOff, calls: []string{"nix", "findmnt"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			c.f.RootDevice = c.root
			b, err := json.Marshal(c.f)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(factsFile, b, 0o644); err != nil {
				t.Fatal(err)
			}
			os.Remove(calls)
			a, errs := kindApp()
			if c.ssh {
				a.Env.Getenv = envOf(map[string]string{"SSH_CONNECTION": "192.0.2.1 50000 192.0.2.2 22"})
			}
			p := platform.Platform{IsLinux: true, IsNixOS: true, User: c.user, HomeDir: t.TempDir(), FlakeDir: t.TempDir()}
			if c.flake != "" {
				if err := os.WriteFile(filepath.Join(p.FlakeDir, "flake.nix"), []byte(c.flake), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := a.runPreflight(p, target{Kind: KindNixOS, Name: "box"}, platform.EngineOverride{})
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q %v (stderr %q)\nwant %q", got, err, errs, c.want)
			}
			if lines := nonEmptyLines(calls); !reflect.DeepEqual(lines, c.calls) {
				t.Errorf("calls %q, want %q", lines, c.calls)
			}
		})
	}
}

// A session is over SSH when sshd set any of its variables: SSH_CONNECTION,
// SSH_CLIENT (older or trimmed environments) or SSH_TTY (a login with a
// terminal); none of them, or only empty ones, is a local session.
func TestSSHSession(t *testing.T) {
	for _, c := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, false},
		{map[string]string{"SSH_CONNECTION": "192.0.2.1 50000 192.0.2.2 22"}, true},
		{map[string]string{"SSH_CLIENT": "192.0.2.1 50000 22"}, true},
		{map[string]string{"SSH_TTY": "/dev/pts/0"}, true},
		{map[string]string{"SSH_CONNECTION": "", "SSH_CLIENT": "", "SSH_TTY": ""}, false},
		{map[string]string{"SSH_AUTH_SOCK": "/tmp/agent.sock"}, false}, // an agent, not a session
	} {
		if got := sshSession(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("%v: got %v, want %v", c.env, got, c.want)
		}
	}
}

// A key counts in ~/.ssh/authorized_keys only in a regular file sshd reads
// (a link into the store is refused under StrictModes), on a key line.
func TestHomeKeys(t *testing.T) {
	home := t.TempDir()
	if homeKeys(home) {
		t.Error("no ~/.ssh: keys")
	}
	// No home known: not the cwd's .ssh.
	t.Chdir(home)
	if err := os.MkdirAll(".ssh", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".ssh/authorized_keys", []byte("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample jane@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if homeKeys("") {
		t.Error(`home "": read the cwd's keys`)
	}
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	ak := filepath.Join(ssh, "authorized_keys")
	for _, c := range []struct {
		body string
		want bool
	}{
		{"", false},
		{"# no keys yet\n\n", false},
		{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample jane@example.com\n", true},
		{"# mine\n  ecdsa-sha2-nistp256 AAAAE2VjZHNh jane@example.com\n", true},
		{"sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5 jane@example.com\n", true},
	} {
		if err := os.WriteFile(ak, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := homeKeys(home); got != c.want {
			t.Errorf("%q: got %v, want %v", c.body, got, c.want)
		}
	}
	// The same key line, through a link.
	keys := filepath.Join(home, "keys")
	if err := os.Rename(ak, keys); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(keys, ak); err != nil {
		t.Fatal(err)
	}
	if homeKeys(home) {
		t.Error("a link: keys")
	}
}

// findmnt's SOURCE for /: the visible (last) mount, a btrfs subvolume's
// [/path] dropped.
func TestRootSource(t *testing.T) {
	for _, c := range []struct{ out, want string }{
		{"/dev/vda1\n", "/dev/vda1"},
		{"/dev/sda2[/@root]\n", "/dev/sda2"},
		{"rpool/root\n", "rpool/root"},
		{"/dev/sda1\n/dev/mapper/root\n", "/dev/mapper/root"},
		{"", ""},
	} {
		if got := rootSource(c.out); got != c.want {
			t.Errorf("%q: got %q, want %q", c.out, got, c.want)
		}
	}
}

// A device path resolves through its links (by-uuid, by-label, mapper);
// anything else (a ZFS dataset, a path that isn't here) stays as it is.
func TestResolveDevice(t *testing.T) {
	dir := t.TempDir()
	dev := filepath.Join(dir, "vda1")
	if err := os.WriteFile(dev, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "by-label-nixos")
	if err := os.Symlink("vda1", link); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dev)
	for _, c := range []struct{ in, want string }{
		{link, real},
		{dev, real},
		{filepath.Join(dir, "gone"), filepath.Join(dir, "gone")},
		{"rpool/root", "rpool/root"},
		{"none", "none"},
	} {
		if got := resolveDevice(c.in); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

// The eval applies preflight.nix on one comment-free line to the user, as a
// Nix string, never evaluating more than the configuration's options.
func TestPreflightArgv(t *testing.T) {
	got := preflightArgv("/f", "box", "jane", []string{"--override-input", "piste", "git+file:///ck"})
	if want := `nix eval --json /f#nixosConfigurations."box".config --override-input piste git+file:///ck --apply`; strings.Join(got[:8], " ") != want {
		t.Errorf("argv %q", got)
	}
	apply := got[8]
	if !strings.HasPrefix(apply, "(user: config: ") || !strings.HasSuffix(apply, `) "jane"`) || strings.ContainsAny(apply, "\n#") {
		t.Errorf("--apply %q", apply)
	}
	if got[len(got)-1] != "--no-write-lock-file" || len(got) != 10 {
		t.Errorf("argv %q", got)
	}
	if got := nixString(`a"b\c${d}`); got != `"a\"b\\c\${d}"` {
		t.Errorf("nixString: %s", got)
	}
}

// A failed eval is an error with nix's message, never facts that would pass
// or refuse; so is output that isn't the facts.
func TestEvalFactsFails(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {ExitCode: 1,
		Stderr: []byte("warning: Git tree is dirty\nerror: attribute 'devbox' missing\n")}}}
	if _, err := evalFacts(context.Background(), rec, []string{"nix", "eval"}); err == nil || err.Error() != "error: attribute 'devbox' missing" {
		t.Errorf("failed eval: %v", err)
	}
	rec = &execx.Recorder{Replies: map[string]execx.Result{"nix eval": {Stdout: []byte(`["testhost"]`)}}}
	if _, err := evalFacts(context.Background(), rec, []string{"nix", "eval"}); err == nil {
		t.Error("not the facts: no error")
	}
}

// preflight.nix against the template's NixOS instance (sshd on, one key for
// jane, in wheel, grub on /dev/vda, root by label), evaluated with this engine as
// switch evaluates it. Needs nix and the engine's inputs: skipped without
// nix (the Nix sandbox's checks.piste has none).
func TestPreflightNix(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("no nix on PATH")
	}
	if testing.Short() {
		t.Skip("evaluates a NixOS system")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	instance := filepath.Join(root, "tests", "fixtures", "template", "nixos")
	override := []string{"--override-input", "piste", workspace.OverrideRef(root)}
	eval := func(user string) facts {
		t.Helper()
		f, err := evalFacts(context.Background(), execx.Exec{}, preflightArgv(instance, "devbox", user, override))
		if err != nil {
			t.Fatalf("%s: %v", user, err)
		}
		return f
	}

	f := eval("jane")
	want := facts{SSHEnabled: true, Keys: 1, KeysInHomedir: true, PermitRootLogin: "no", Wheel: true, SudoEnabled: true,
		WheelNeedsPassword: true, BootLoader: true, RootDevice: "/dev/disk/by-label/nixos"}
	if f != want {
		t.Errorf("jane:\ngot  %+v\nwant %+v", f, want)
	}
	// Over SSH, on a machine whose root is that label: nothing refused.
	m := machine{User: "jane", OverlayUser: "jane", Piste: "piste", SSHSession: true, RunningRoot: "/dev/vda1"}
	resolve := func(dev string) string { return strings.Replace(dev, "/dev/disk/by-label/nixos", "/dev/vda1", 1) }
	if r := preflight(f, m, resolve); len(r) != 0 {
		t.Errorf("the template refused: %q", r)
	}
	// Root over SSH (an image reached as root): the base turns root's
	// login off, and the switch says so instead of locking root out.
	m.User = "root"
	if r := preflight(eval("root"), m, resolve); !reflect.DeepEqual(r, []refusal{{"ssh", refuseRootLogin}}) {
		t.Errorf("root over SSH: %q", r)
	}

	// A user the config doesn't declare evaluates (no keys, no wheel)
	// rather than failing the eval.
	if f := eval("nobody-here"); f.Keys != 0 || f.Wheel || f.HasPassword || !f.BootLoader {
		t.Errorf("an undeclared user: %+v", f)
	}
}

// preflightConfigs are small NixOS configurations, one per case, each a
// module beside a wheel user jane with no password.
const preflightConfigs = `{
  external = { boot.loader.external = { enable = true; installHook = "/run/current-system/sw/bin/true"; }; };
  limine = { boot.loader.limine.enable = true; };
  refind = { boot.loader.refind.enable = true; };
  none = { };
  narrow = { security.sudo.extraRules = [ { users = [ "jane" ];
    commands = [ { command = "/run/current-system/sw/bin/systemctl"; options = [ "NOPASSWD" ]; } ]; } ]; };
  all = { security.sudo.extraRules = [ { users = [ "jane" ];
    commands = [ { command = "ALL"; options = [ "NOPASSWD" ]; } ]; } ]; };
  wheelAll = { security.sudo.extraRules = [ { groups = [ "wheel" ];
    commands = [ "ALL" { command = "ALL"; options = [ "SETENV" "NOPASSWD" ]; } ]; } ]; };
  allNoOption = { security.sudo.extraRules = [ { users = [ "jane" ]; commands = [ "ALL" ]; } ]; };
  rootLoginOff = { services.openssh = { enable = true; settings.PermitRootLogin = "no"; }; };
  rootLoginDefault = { services.openssh.enable = true; };
  rootLoginNull = { services.openssh = { enable = true; settings.PermitRootLogin = null; }; };
}`

// preflight.nix's boot loader, NOPASSWD and root login reads, against
// NixOS systems built from the engine's nixpkgs in one eval: every loader
// NixOS has counts (lanzaboote's external one too), only a NOPASSWD rule for
// ALL commands spares a user with no password the sudo password trap, as
// switch, rollback and the undo run nixos-rebuild, nix-env and
// switch-to-configuration, and root's SSH login is the config's setting
// (nixpkgs' default prohibit-password; null, sshd's own default, reads "").
// Skipped as TestPreflightNix is.
func TestPreflightNixRules(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("no nix on PATH")
	}
	if testing.Short() {
		t.Skip("evaluates NixOS systems")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	expr := `let
  nixpkgs = (builtins.getFlake ` + nixString(workspace.OverrideRef(root)) + `).inputs.nixpkgs;
  preflight = ` + nixOneLine(preflightNix) + `;
  system = m: (nixpkgs.lib.nixosSystem { system = "x86_64-linux"; modules = [ m {
    users.users.jane = { isNormalUser = true; extraGroups = [ "wheel" ]; };
    system.stateVersion = "26.11";
  } ]; }).config;
in builtins.mapAttrs (_: m: preflight "jane" (system m)) ` + preflightConfigs
	res, err := execx.Exec{}.Run(context.Background(), execx.Cmd{Argv: []string{"nix", "eval", "--json", "--impure", "--expr", expr}})
	if err != nil {
		t.Fatalf("%v\n%s", err, res.Stderr)
	}
	var got map[string]facts
	if err := json.Unmarshal(res.Stdout, &got); err != nil {
		t.Fatalf("%v: %s", err, res.Stdout)
	}

	for _, c := range []struct {
		name             string
		loader, nopasswd bool
	}{
		{"external", true, false}, {"limine", true, false}, {"refind", true, false}, {"none", false, false},
		{"narrow", false, false}, {"all", false, true}, {"wheelAll", false, true}, {"allNoOption", false, false},
	} {
		f := got[c.name]
		if f.BootLoader != c.loader || f.NopasswdForUser != c.nopasswd {
			t.Errorf("%s: BootLoader %v NopasswdForUser %v, want %v %v", c.name, f.BootLoader, f.NopasswdForUser, c.loader, c.nopasswd)
		}
	}

	// A host whose only loader is the external one (Secure Boot through
	// lanzaboote) isn't refused for it.
	m := machine{User: "jane", Piste: "piste"}
	if r := preflight(got["external"], m, resolveDevice); slices.ContainsFunc(r, func(r refusal) bool { return r.Rule == "boot" }) {
		t.Errorf("external loader refused: %q", r)
	}
	// NOPASSWD for systemctl alone, sudo passwordless today, no password
	// declared: the trap.
	m.SudoPasswordless = true
	if r := preflight(got["narrow"], m, resolveDevice); !slices.Contains(r, refusal{"sudo", refusePassword}) {
		t.Errorf("a NOPASSWD rule for one command spared the password trap: %q", r)
	}
	if r := preflight(got["all"], m, resolveDevice); slices.ContainsFunc(r, func(r refusal) bool { return r.Rule == "sudo" }) {
		t.Errorf("a NOPASSWD rule for ALL refused: %q", r)
	}

	for name, want := range map[string]string{"rootLoginOff": "no", "rootLoginDefault": "prohibit-password", "rootLoginNull": ""} {
		if got := got[name].PermitRootLogin; got != want {
			t.Errorf("%s: PermitRootLogin %q, want %q", name, got, want)
		}
	}
	// Root over SSH: root's login off refuses; nixpkgs' default doesn't.
	asRoot := machine{User: "root", OverlayUser: "jane", Piste: "piste", SSHSession: true}
	if r := preflight(got["rootLoginOff"], asRoot, resolveDevice); !slices.Contains(r, refusal{"ssh", refuseRootLogin}) {
		t.Errorf("root's login off, root over SSH: %q", r)
	}
	if r := preflight(got["rootLoginDefault"], asRoot, resolveDevice); slices.Contains(r, refusal{"ssh", refuseRootLogin}) {
		t.Errorf("nixpkgs' default refused root over SSH: %q", r)
	}
}
