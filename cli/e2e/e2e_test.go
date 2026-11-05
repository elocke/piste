// Package e2e is piste's end-to-end harness.
//
// Each testdata/*.txtar script runs the real piste entry point against
// fixtures, with shims for the external tools (nix, sudo, darwin-rebuild,
// home-manager, sops, yq, claude, codex, brew, pnpm, ssh, ssh-keygen, gh) on PATH. A shim
// appends its argv to $WORK/calls and prints $WORK/replies/<tool> if present,
// so scripts assert the argv a command ran without touching a system. It
// exits with $SHIM_EXIT_<TOOL>_<FIRST ARG> or $SHIM_EXIT_<TOOL> (0).
//
// Outputs are compared with goldens in testdata/golden, with cmpwork /
// cmpworkdir (internal/scripttest).
package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/elocke/piste/cli/internal/cli"
	"github.com/elocke/piste/cli/internal/scripttest"
)

// Tools shimmed in every script.
var shimmed = []string{
	"nix", "darwin-rebuild", "home-manager", "nh", "sops", "age-keygen", "ssh-to-age",
	"claude", "codex", "brew", "npm", "pnpm", "pip", "ssh", "gh", "nix-prefetch-url",
	"sudo", "yq", "xcode-select", "determinate-nixd", "curl",
	"gitleaks",
	"systemd-run", "getent", "dscl", "nix-env",
	"cargo",
}

// ssh-keygen is shimmed apart: it logs its argv, and makes the key (-f <path>
// and <path>.pub, a fixed fake) the way the real one would; -y prints the
// public key.
const keygenShim = `#!/bin/sh
{ printf 'ssh-keygen'; for a in "$@"; do printf ' %s' "$a"; done; printf '\n'; } >> "$WORK/calls"
[ -n "$SHIM_EXIT_SSH_KEYGEN" ] && exit "$SHIM_EXIT_SSH_KEYGEN"
publine="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleExampleExampleExample"
f=""; y=""
while [ $# -gt 0 ]; do case "$1" in -f) f="$2"; shift;; -y) y=1;; esac; shift; done
if [ -n "$y" ]; then echo "$publine"; exit 0; fi
if [ -n "$f" ]; then echo "fake private key" > "$f"; echo "$publine jane@github" > "$f.pub"; fi
`

const shim = `#!/bin/sh
n=$(basename "$0")
{ printf '%s' "$n"; for a in "$@"; do printf ' %s' "$a"; done; printf '\n'; } >> "$WORK/calls"
sub2=$(printf '%s' "$2" | tr -c 'a-zA-Z0-9\n' '_')
if [ -f "$WORK/replies/$n-$1-$sub2" ]; then cat "$WORK/replies/$n-$1-$sub2"; elif [ -f "$WORK/replies/$n-$1" ]; then cat "$WORK/replies/$n-$1"; elif [ -f "$WORK/replies/$n" ]; then cat "$WORK/replies/$n"; fi
code_var="SHIM_EXIT_$(echo "$n" | tr 'a-z-' 'A-Z_')"
sub_var="${code_var}_$(printf '%s' "$1" | tr 'a-z' 'A-Z' | tr -c 'A-Z0-9' '_')"
eval "exit \${$sub_var:-\${$code_var:-0}}"
`

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"piste": func() {
			scripttest.PinMachine()
			os.Exit(cli.Main(os.Args[1:]))
		},
	})
}

func TestE2E(t *testing.T) {
	golden, err := filepath.Abs("testdata/golden")
	if err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, testscript.Params{
		Dir: "testdata",
		Setup: func(e *testscript.Env) error {
			bin := filepath.Join(e.WorkDir, ".shims")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				return err
			}
			for _, n := range shimmed {
				if err := os.WriteFile(filepath.Join(bin, n), []byte(shim), 0o755); err != nil {
					return err
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "ssh-keygen"), []byte(keygenShim), 0o755); err != nil {
				return err
			}
			e.Setenv("PATH", bin+string(os.PathListSeparator)+e.Getenv("PATH"))
			e.Setenv("HOME", filepath.Join(e.WorkDir, "home"))
			e.Setenv("NO_COLOR", "1")
			// No release check after a switch: it would reach a
			// remote. update_release.txtar clears it to test the check.
			e.Setenv("CI", "1")
			scripttest.PinEnv(e)
			e.Setenv("GOLDEN", golden)
			// The engine's starter template (init.txtar copies it into its
			// checkout): set by the Nix build, whose sandbox has only cli/.
			tmpl := os.Getenv("PISTE_TEMPLATE_DIR")
			if tmpl == "" {
				tmpl, _ = filepath.Abs("../../templates/default")
			}
			e.Setenv("TEMPLATE", tmpl)
			// The fixture team layer (init_layer.txtar, layer_setup.txtar).
			lay := os.Getenv("PISTE_LAYER_DIR")
			if lay == "" {
				lay, _ = filepath.Abs("../../tests/fixtures/layer")
			}
			e.Setenv("LAYER", lay)
			// The engine's git hooks (leaks.txtar runs the real pre-push).
			hooks := os.Getenv("PISTE_GITHOOKS")
			if hooks == "" {
				hooks, _ = filepath.Abs("../../.githooks")
			}
			e.Setenv("GITHOOKS", hooks)
			return os.MkdirAll(filepath.Join(e.WorkDir, "home"), 0o755)
		},
		Cmds: scripttest.WithGolden(nil),
	})
}
