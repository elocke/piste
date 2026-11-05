package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Nix puts the message on the error: line and a code excerpt after it (or a
// trace before it); the hint wants the message.
func TestStderrTailErrorLine(t *testing.T) {
	for _, in := range []string{
		"warning: Git tree is dirty\nerror: attribute 'hosts' missing\n       at /x/flake.nix:3:5:\n            2|\n            3|   foo.hosts\n             |   ^\n            4|\n",
		"error:\n       … while calling the 'throw' builtin\n         at /x/flake.nix:3:13:\n            3|     hosts = throw \"boom\";\n             |             ^\n\n       error: attribute 'hosts' missing\n",
	} {
		got := stderrTail([]byte(in), 3)
		if !strings.HasPrefix(got, "error: attribute 'hosts' missing") || strings.Contains(got, "|") {
			t.Errorf("stderrTail = %q, want Nix's error line", got)
		}
	}
	if got := stderrTail([]byte("a\nb\nc\nd\n"), 3); got != "b / c / d" {
		t.Errorf("no error: line: %q, want the last 3", got)
	}
}

func TestRepoName(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:o/r.git": "r", "https://h/o/r/": "r", "/p/r.git": "r", "host:r": "r",
	} {
		if got := repoName(url); got != want {
			t.Errorf("repoName(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestCloneable(t *testing.T) {
	for arg, want := range map[string]bool{
		"https://h/o/r": true, "git@h:o/r": true, t.TempDir(): true, "jq": false, "cask": false,
	} {
		if got := cloneable(arg); got != want {
			t.Errorf("cloneable(%q) = %v, want %v", arg, got, want)
		}
	}
}

// git's cause comes before its "fatal: Could not read from remote
// repository." (ssh); else its first fatal: line says it.
func TestGitFailure(t *testing.T) {
	for in, want := range map[string]string{
		"ssh: Could not resolve hostname nosuchhost: Name or service not known\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights\nand the repository exists.\n": "ssh: Could not resolve hostname nosuchhost",
		"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.\n":                                                                                                           "Permission denied (publickey)",
		"fatal: '/x/nope.git' does not appear to be a git repository\nfatal: Could not read from remote repository.\n\nPlease make sure\nand the repository exists.\n":                                              "does not appear to be a git repository",
	} {
		if got := gitFailure(in); !strings.Contains(got, want) {
			t.Errorf("gitFailure = %q, want it to contain %q", got, want)
		}
	}
}

func TestCallsEngineMkFlake(t *testing.T) {
	for body, want := range map[string]bool{
		`outputs = inputs@{ piste, ... }: piste.lib.mkFlake { };`:                                 true,
		`outputs = inputs: inputs.piste.lib.mkFlake { };`:                                         true,
		`outputs = inputs@{ piste, ... }: let inherit (piste.lib) mkFlake; in mkFlake { };`:       true,
		`outputs = inputs@{ piste, ... }: with piste.lib; mkFlake { };`:                           true,
		`outputs = inputs@{ flake-parts, ... }: flake-parts.lib.mkFlake { inherit inputs; } { };`: false,
		`outputs = inputs@{ parts, piste, ... }: parts.lib.mkFlake { } { x = piste.lib.y; };`:     false,
		`outputs = _: { };`: false,
	} {
		if got := callsEngineMkFlake([]byte(body)); got != want {
			t.Errorf("callsEngineMkFlake(%q) = %v, want %v", body, got, want)
		}
	}
}

// The hint says where (store paths shortened), and not a download's body.
func TestStderrTailLocation(t *testing.T) {
	for in, want := range map[string]string{
		"error: undefined variable 'x'\n       at /nix/store/0123456789abcdfghijklmnpqrsvwxyz-source/hosts/box.nix:3:5:\n            2| {\n            3|   x\n             |   ^\n":           "error: undefined variable 'x' / at hosts/box.nix:3:5",
		"error:\n       … while calling the 'throw' builtin\n         at /x/flake.nix:3:13:\n            3|     hosts = throw \"boom\";\n             |             ^\n\n       error: boom\n": "error: boom / at /x/flake.nix:3:13",
		"error: unable to download 'https://h/x': HTTP error 404\n\n       response body:\n\n       {\n  \"message\": \"Not Found\"\n}\n":                                                      "error: unable to download 'https://h/x': HTTP error 404",
	} {
		if got := stderrTail([]byte(in), 3); got != want {
			t.Errorf("stderrTail = %q, want %q", got, want)
		}
	}
}

// piste init's overlays link their skills from overlays/<name>/agents in
// home.nix (what's theirs on every machine); older ones, in a host file.
func TestExpectedNameReadsHomeNix(t *testing.T) {
	const workdir = "{ config, ... }: { piste.agents.sources.personal-agents.workdir = \"${config.piste.checkout}/overlays/personal/agents\"; }\n"
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "home.nix"), []byte(workdir), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := expectedName(home); got != "personal" {
		t.Errorf("home.nix: expectedName = %q, want personal", got)
	}
	host := t.TempDir()
	if err := os.MkdirAll(filepath.Join(host, "hosts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(host, "hosts", "box.nix"), []byte(workdir), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := expectedName(host); got != "personal" {
		t.Errorf("hosts/box.nix: expectedName = %q, want personal", got)
	}
}

// Nix's own output (Determinate Nix 3.15.2, the linux fixture under
// --show-trace), from the last trace frame on. A trace frame before the
// error is where nixpkgs threw, not the person's file: an option's
// definitions ("- In `<file>'") say where, and a message that runs on
// (an untracked file's git command) is kept whole.
func TestStderrTailNixOutput(t *testing.T) {
	const (
		frame = "       … while calling the 'throw' builtin\n" +
			"         at «github:nixos/nixpkgs/31ecbac»/lib/modules.nix:376:13:\n" +
			"          375|           else\n" +
			"          376|             throw baseMsg\n" +
			"             |             ^\n" +
			"          377|         else\n\n"
		typo = "       error: The option `piste.pakages' does not exist. Definition values:\n" +
			"       - In `/nix/store/rf1bn2jv3w2mib999yn2anfw8n62xvr1-source/hosts/devbox.nix':\n" +
			"           {\n" +
			"             extraPackages = [ ];\n" +
			"           }\n\n" +
			"       Did you mean `piste.agents', `piste.roles' or `piste.programs'?\n"
		typoWant = "error: The option `piste.pakages' does not exist. Definition values: / in hosts/devbox.nix" +
			" / Did you mean `piste.agents', `piste.roles' or `piste.programs'?"
	)
	for name, c := range map[string][2]string{
		"unknown option": {frame + typo, typoWant},
		// Without lazy trees nixpkgs' frame is a store path like the person's.
		"unknown option, no lazy trees":   {strings.Replace(frame, "«github:nixos/nixpkgs/31ecbac»", "/nix/store/0123456789abcdfghijklmnpqrsvwxyz-source", 1) + typo, typoWant},
		"unknown option, no Did you mean": {frame + strings.Split(typo, "\n\n")[0] + "\n", "error: The option `piste.pakages' does not exist. Definition values: / in hosts/devbox.nix"},
		"untracked file": {
			"       … while calling the 'import' builtin\n" +
				"         at «github:nixos/nixpkgs/31ecbac»/lib/modules.nix:452:53:\n" +
				"          451|           unifyModuleSyntax (toString m) (toString m) (\n" +
				"          452|             applyModuleArgsIfFunction (toString m) (import m) args\n" +
				"             |                                                     ^\n" +
				"          453|           );\n\n" +
				"       error: Path 'hosts/extra.nix' in the repository \"/home/user/piste/overlays/mine\" is not tracked by Git.\n\n" +
				"       To make it visible to Nix, run:\n\n" +
				"       git -C \"/home/user/piste/overlays/mine\" add \"hosts/extra.nix\"\n",
			`error: Path 'hosts/extra.nix' in the repository "/home/user/piste/overlays/mine" is not tracked by Git.` +
				` / To make it visible to Nix, run: / git -C "/home/user/piste/overlays/mine" add "hosts/extra.nix"`,
		},
		"conflicting definitions": {
			"       … while calling the 'throw' builtin\n" +
				"         at «github:nixos/nixpkgs/31ecbac»/lib/options.nix:513:11:\n" +
				"          513|           throw \"The option `${showOption loc}' has conflicting definition values:${\n" +
				"             |           ^\n\n" +
				"       error: The option `programs.tmux.enable' has conflicting definition values:\n" +
				"       - In `/nix/store/vzmvp6hvgk71pvpi47cak0dg84ybpwaz-source/modules/programs/tmux.nix': true\n" +
				"       - In `/nix/store/pv94cpyq6hywfyj7hp1bgria25wdkz25-source/hosts/devbox.nix': false\n" +
				"       Use `lib.mkForce value` or `lib.mkDefault value` to change the priority on any of these definitions.\n",
			"error: The option `programs.tmux.enable' has conflicting definition values: / in modules/programs/tmux.nix: true / in hosts/devbox.nix: false",
		},
		// A short error thrown inside a locked input: no pointer into it.
		"thrown in an input": {frame + "       error: boom\n", "error: boom"},
		"attribute missing": {
			"       … while calling the 'elemAt' builtin\n" +
				"         at «github:nixos/nixpkgs/31ecbac»/lib/lists.nix:346:43:\n" +
				"          346|   imap1 = f: list: genList (n: f (n + 1) (elemAt list n)) (length list);\n" +
				"             |                                           ^\n\n" +
				"       error: attribute 'tmuxx' missing\n" +
				"       at /home/user/piste/overlays/mine/hosts/devbox.nix:19:21:\n" +
				"           18|   # The piste CLI, so `piste switch` works from now on.\n" +
				"           19|   home.packages = [ pkgs.tmuxx piste.packages.${pkgs.stdenv.hostPlatform.system}.piste ];\n" +
				"             |                     ^\n" +
				"           20|\n" +
				"       Did you mean one of tmux, tmuxp, cmux, gmpxx or kmsxx?\n",
			"error: attribute 'tmuxx' missing / at /home/user/piste/overlays/mine/hosts/devbox.nix:19:21 / Did you mean one of tmux, tmuxp, cmux, gmpxx or kmsxx?",
		},
	} {
		if got := stderrTail([]byte(c[0]), 3); got != c[1] {
			t.Errorf("%s:\n got  %q\n want %q", name, got, c[1])
		}
	}
}
