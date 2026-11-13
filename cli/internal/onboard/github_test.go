package onboard

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
)

const pub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample jane@github"

func rec(replies map[string]execx.Result) *execx.Recorder { return &execx.Recorder{Replies: replies} }

func TestKeyMaterialIgnoresComment(t *testing.T) {
	if a, b := KeyMaterial(pub), KeyMaterial("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample other comment\n"); a != b || a == "" {
		t.Fatalf("%q vs %q", a, b)
	}
	if KeyMaterial("not a key") != "" || KeyMaterial("") != "" {
		t.Fatal("a non-key has material")
	}
}

func TestUserAndKeys(t *testing.T) {
	r := rec(map[string]execx.Result{
		"gh api user?":         {},
		"gh api user":          {Stdout: []byte(`{"login":"jane","id":12345}`)},
		"gh api user/keys":     {Stdout: []byte(`[{"key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample old-comment"}]`)},
		"gh api user/ssh_sign": {Stdout: []byte(`[]`)},
	})
	g := GH{R: r}
	a, err := g.User(context.Background())
	if err != nil || a.Login != "jane" || a.ID != 12345 || a.Noreply() != "12345+jane@users.noreply.github.com" {
		t.Fatalf("%+v %v", a, err)
	}
	auth, err := g.Keys(context.Background(), Auth)
	if err != nil || !Has(auth, KeyMaterial(pub)) {
		t.Fatalf("auth keys %v %v", auth, err)
	}
	sign, err := g.Keys(context.Background(), Signing)
	if err != nil || Has(sign, KeyMaterial(pub)) {
		t.Fatalf("signing keys %v %v", sign, err)
	}
}

func TestActingAsAnotherAccountSetsToken(t *testing.T) {
	var env []string
	g := GH{R: runnerFunc(func(c execx.Cmd) (execx.Result, error) {
		env = c.Env
		return execx.Result{Stdout: []byte(`{"login":"jane","id":1}`)}, nil
	}), Token: "tok"}
	if _, err := g.User(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env[0] != "GH_TOKEN=tok" {
		t.Fatalf("env %v", env)
	}
}

type runnerFunc func(execx.Cmd) (execx.Result, error)

func (f runnerFunc) Run(_ context.Context, c execx.Cmd) (execx.Result, error) { return f(c) }

func TestAddErrors(t *testing.T) {
	fail := func(msg string) GH {
		return GH{R: rec(map[string]execx.Result{"gh ssh-key add": {Stderr: []byte(msg), ExitCode: 1}})}
	}
	err := fail(`HTTP 422: key is already in use (https://api.github.com/user/keys)`).Add(context.Background(), "k.pub", Auth, "t")
	if !errors.Is(err, ErrKeyInUse) {
		t.Fatalf("in use: %v", err)
	}
	err = fail(`This API operation needs the "admin:ssh_signing_key" scope.`).Add(context.Background(), "k.pub", Signing, "t")
	var se *ScopeError
	if !errors.As(err, &se) || se.Scope != "admin:ssh_signing_key" {
		t.Fatalf("scope: %v", err)
	}
	err = fail("boom").Add(context.Background(), "k.pub", Auth, "t")
	if err == nil || errors.Is(err, ErrKeyInUse) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("other: %v", err)
	}
}

func TestAddArgv(t *testing.T) {
	r := rec(nil)
	if err := (GH{R: r}).Add(context.Background(), "/home/user/.ssh/k.pub", Signing, "k (devbox)"); err != nil {
		t.Fatal(err)
	}
	want := "gh ssh-key add /home/user/.ssh/k.pub --type signing --title k (devbox)"
	if got := strings.Join(r.Calls[0], " "); got != want {
		t.Fatalf("%q", got)
	}
}

func TestKeygenArgv(t *testing.T) {
	r := rec(nil)
	if err := Keygen(context.Background(), r, "/home/user/.ssh/k", "jane"); err != nil {
		t.Fatal(err)
	}
	want := "ssh-keygen -q -t ed25519 -N  -C jane@github -f /home/user/.ssh/k"
	if got := strings.Join(r.Calls[0], " "); got != want {
		t.Fatalf("%q", got)
	}
}

func TestCheckLogin(t *testing.T) {
	for _, ok := range []string{"jane", "jane-doe", "a1"} {
		if CheckLogin(ok) != nil {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-jane", "ja ne", "jane/../x", `ja"ne`, strings.Repeat("a", 40)} {
		if CheckLogin(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

var jane = Account{Login: "jane", ID: 12345}

const linuxHost = `{ config, pkgs, user, ... }:
{
  imports = [ ../home.nix ];

  home.packages = with pkgs; [ ];

  piste.checkout = "/home/user/piste";
}
`

const darwinHost = `{ user, piste, pkgs, ... }:
{
  home-manager.users.${user} =
    { config, ... }:
    {
      imports = [ ../home.nix ];
      piste.checkout = "/home/user/piste";
    };
}
`

func TestSetGithubInsertsAfterCheckout(t *testing.T) {
	for name, src := range map[string]string{"linux": linuxHost, "darwin": darwinHost} {
		out, changed, err := SetGithub([]byte(src), jane, "/home/user/.ssh/id_ed25519_github_jane", "/home/user")
		if err != nil || !changed {
			t.Fatalf("%s: %v %v", name, changed, err)
		}
		indent := map[string]string{"linux": "  ", "darwin": "      "}[name]
		want := indent + "piste.github = {\n" + indent + "  login = \"jane\";\n" + indent + "  id = 12345;\n" + indent + "};\n"
		i := strings.Index(string(out), "piste.checkout")
		j := strings.Index(string(out)[i:], "\n") + i + 1
		if !strings.HasPrefix(string(out)[j:], want) {
			t.Fatalf("%s: block not right after checkout:\n%s", name, out)
		}
		// Re-run: nothing changes.
		again, changed, err := SetGithub(out, jane, "/home/user/.ssh/id_ed25519_github_jane", "/home/user")
		if err != nil || changed || string(again) != string(out) {
			t.Fatalf("%s: second run changed=%v err=%v", name, changed, err)
		}
	}
}

func TestSetGithubReplacesBlock(t *testing.T) {
	src := "{\n  a = 1;\n  piste.github = { login = \"old\"; id = 1; }; # mine\n  b = 2;\n}\n"
	// One-line block: replaced, the rest kept.
	out, changed, err := SetGithub([]byte(src), jane, "/home/user/.ssh/k", "/home/user")
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	s := string(out)
	if strings.Count(s, "piste.github") != 1 || !strings.Contains(s, `login = "jane";`) || !strings.Contains(s, `key = "/home/user/.ssh/k";`) ||
		!strings.Contains(s, "  a = 1;\n") || !strings.Contains(s, "  b = 2;\n") || strings.Contains(s, "old") {
		t.Fatalf("%s", s)
	}
	// A multi-line block with a nested brace in a string.
	src = "{\n  piste.github = {\n    login = \"o}ld\";\n    id = 1;\n  };\n  b = 2;\n}\n"
	out, _, err = SetGithub([]byte(src), jane, "/home/user/.ssh/id_ed25519_github_jane", "/home/user")
	if err != nil || strings.Count(string(out), "piste.github") != 1 || !strings.Contains(string(out), "  b = 2;\n}\n") || strings.Contains(string(out), "o}ld") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestSetGithubNoAnchor(t *testing.T) {
	for _, src := range []string{"{ }\n", "{\n  piste.github.login = \"jane\";\n}\n"} {
		if _, _, err := SetGithub([]byte(src), jane, DefaultKey("/home/user", "jane"), "/home/user"); !errors.Is(err, ErrNoAnchor) {
			t.Fatalf("%q: %v", src, err)
		}
	}
	if !HasGithub([]byte("  piste.github.login = \"x\";\n")) || HasGithub([]byte(linuxHost)) {
		t.Fatal("HasGithub")
	}
}

// With no ssh-keygen on the box the exec fails with no output: the message
// must still say what to do.
func TestKeygenMissingBinaryHints(t *testing.T) {
	r := rec(map[string]execx.Result{"ssh-keygen": {ExitCode: 127}})
	err := Keygen(context.Background(), r, "/tmp/k", "jane")
	if err == nil || !strings.Contains(err.Error(), "OpenSSH") {
		t.Fatalf("Keygen error = %v, want an OpenSSH install hint", err)
	}
	_, err = PublicKey(context.Background(), r, "/tmp/k")
	if err == nil || !strings.Contains(err.Error(), "OpenSSH") {
		t.Fatalf("PublicKey error = %v, want an OpenSSH install hint", err)
	}
}

const linuxNoCheckout = `{ user, pkgs, piste, ... }:

{
  imports = [ piste.homeManagerModules.dev ../modules/git.nix ];

  # A comment
  piste.programs.zellij.timezone = "America/New_York";

  targets.genericLinux.enable = true;

  piste.packages = {
    terminal.enable = false;
    extraPackages = [ pkgs.hello ];
  };

  home.stateVersion = "24.05";
}
`

const darwinNoCheckout = `{ user, piste, pkgs, ... }:
{
  piste.packages.casks.extra = [ ];

  home-manager.users.${user} =
    { config, ... }:
    {
      imports = [ ../home.nix ];

      home.packages = with pkgs; [ hello ];
      piste.packages.extraPackages = [ ];

      home.stateVersion = "26.11";
    };
}
`

func TestSetGithubWithoutCheckout(t *testing.T) {
	key, home := DefaultKey("/home/user", "jane"), "/home/user"
	cases := map[string]struct{ src, want string }{
		"linux: after the last piste statement": {linuxNoCheckout, `  piste.packages = {
    terminal.enable = false;
    extraPackages = [ pkgs.hello ];
  };

  piste.github = {
    login = "jane";
    id = 12345;
  };

  home.stateVersion = "24.05";
}
`},
		"darwin: inside home-manager.users": {darwinNoCheckout, `      piste.packages.extraPackages = [ ];

      piste.github = {
        login = "jane";
        id = 12345;
      };

      home.stateVersion = "26.11";
    };
}
`},
		"no piste statement: before the closing brace": {"{ pkgs, ... }:\n{\n  home.packages = with pkgs; [ ];\n  a = \"x;}\";\n}\n", `  a = "x;}";

  piste.github = {
    login = "jane";
    id = 12345;
  };
}
`},
		"empty attrset": {"{ ... }:\n{\n}\n", "{\n  piste.github = {\n    login = \"jane\";\n    id = 12345;\n  };\n}\n"},
		"no argument pattern": {"{\n  imports = [ ];\n  let_ = 1;\n}\n", `  let_ = 1;

  piste.github = {
    login = "jane";
    id = 12345;
  };
}
`},
	}
	for name, c := range cases {
		out, changed, err := SetGithub([]byte(c.src), jane, key, home)
		if err != nil || !changed {
			t.Fatalf("%s: %v %v", name, changed, err)
		}
		if !strings.HasSuffix(string(out), c.want) {
			t.Errorf("%s: got\n%s", name, out)
		}
		if strings.Count(string(out), "piste.github") != 1 {
			t.Errorf("%s: block not added once", name)
		}
		// The rest of the file is kept, and a re-run changes nothing.
		if !strings.HasPrefix(string(out), c.src[:strings.Index(c.src, "\n")+1]) {
			t.Errorf("%s: head changed", name)
		}
		again, changed, err := SetGithub(out, jane, key, home)
		if err != nil || changed || string(again) != string(out) {
			t.Errorf("%s: second run changed=%v err=%v", name, changed, err)
		}
	}
}

func TestSetGithubUnparseable(t *testing.T) {
	for name, src := range map[string]string{
		"one line":           "{ ... }: { }\n",
		"let before body":    "{ pkgs, ... }:\nlet x = 1; in\n{\n  a = x;\n}\n",
		"function, no attrs": "{ pkgs, ... }:\nlib.mkMerge [ { a = 1; } ]\n",
		"unbalanced":         "{ pkgs, ... }:\n{\n  a = 1;\n",
		"statement unended":  "{ ... }:\n{\n  a = 1;\n  b = 2\n}\n",
		"dotted hm user":     "{ user, ... }:\n{\n  home-manager.users.${user}.home.stateVersion = \"1\";\n}\n",
		"nested github":      "{ ... }:\n{\n  piste = {\n    github = 1;\n  };\n  a = 1;\n}\n",
	} {
		if _, _, err := SetGithub([]byte(src), jane, DefaultKey("/home/user", "jane"), "/home/user"); !errors.Is(err, ErrNoAnchor) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestHostFile(t *testing.T) {
	flake := []byte(`{
  outputs = inputs: mk {
    hosts."jane@devbox" = {
      system = "x86_64-linux";
      # modules = [ ./hosts/old.nix ];
      modules = [ ./hosts/devbox-file.nix ];
    };
    hosts."laptop" = { modules = [ ./hosts/laptop.nix ]; };
    hosts = { "jane@other" = { modules = [ ./hosts/other.nix ./x.nix ]; }; };
    hosts."jane@bare" = { system = "x"; };
    hosts."server" = { system = "x86_64-linux"; nixos = true; modules = [ ./hosts/server ]; };
    hosts."vm" = { nixos = true; modules = [(./hosts/vm)]; };
    hosts."keyed" = { keys = builtins.readFile ./hosts/keys.txt; modules = [ ./hosts/keyed.nix ]; };
  };
}
`)
	for key, want := range map[string]string{"jane@devbox": "hosts/devbox-file.nix", "laptop": "hosts/laptop.nix", "jane@other": "hosts/other.nix", "jane@bare": "", "nope": "",
		"server": "hosts/server/default.nix", "vm": "hosts/vm/default.nix", "keyed": "hosts/keyed.nix"} {
		got, ok := HostFile(flake, key)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v", key, got, ok)
		}
	}
}

// An entry with several ./hosts/ paths: the one named for the host (its
// key's part after user@) wins, in either order and for a NixOS host's
// directory too; else the first file, else the first directory.
func TestHostFileSeveralPaths(t *testing.T) {
	flake := []byte(`{
  outputs = inputs: mk {
    hosts."jane@box" = { modules = [ ./hosts/common ./hosts/box.nix ]; };
    hosts."jane@pad" = { modules = [ ./hosts/pad.nix ./hosts/common ]; };
    hosts."server" = { nixos = true; modules = [ ./hosts/common ./hosts/server ]; };
    hosts."vm" = { nixos = true; modules = [ ./hosts/vm ./hosts/common/default.nix ]; };
    hosts."jane@lab" = { modules = [ ./hosts/common ./hosts/shared.nix ]; };
    hosts."edge" = { nixos = true; modules = [ ./hosts/common ./hosts/other ]; };
    mini = { aliases = [ "MINI01" ]; modules = [ ./hosts/common ./hosts/mini.nix ]; };
  };
}
`)
	for key, want := range map[string]string{
		"jane@box": "hosts/box.nix",
		"jane@pad": "hosts/pad.nix",
		"server":   "hosts/server/default.nix",
		"vm":       "hosts/vm/default.nix",
		"jane@lab": "hosts/shared.nix",
		"edge":     "hosts/common/default.nix",
		"MINI01":   "hosts/mini.nix",
	} {
		got, ok := HostFile(flake, key)
		if got != want || !ok {
			t.Errorf("%s: %q %v, want %q", key, got, ok, want)
		}
	}
}

func TestHostFileBareKeyAndAlias(t *testing.T) {
	flake := []byte("{\n  outputs = inputs@{ self, piste, ... }:\n    piste.lib.mkFlake {\n      inherit inputs self;\n      user = \"jane\";\n\n" + `      hosts = {
        # ` + "`jane-mac`" + ` is the canonical name; ` + "`JM0001`" + ` is the actual macOS
        # hostname (can't be changed) and exists as an alias
        # so the piste hostname guard passes without --host on the real machine.
        jane-mac = {
          system = "aarch64-darwin";
          aliases = [ "JM0001" ];
          # a comment with (parens) and a path-ish name (86-foo-darwin-tests.nix)
          overlays = [ piste.overlays.ai piste.overlays.shared ];
          modules = [ ./hosts/jane-mac.nix ];
        };
        "jane@devbox" = {
          system = "x86_64-linux";
          overlays = [ piste.overlays.shared ];
          modules = [ ./hosts/devbox.nix ];
        };
      };
    };
}
`)
	for key, want := range map[string]string{"jane-mac": "hosts/jane-mac.nix", "JM0001": "hosts/jane-mac.nix", "jane@devbox": "hosts/devbox.nix", "devbox": ""} {
		got, ok := HostFile(flake, key)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v", key, got, ok)
		}
	}
}
