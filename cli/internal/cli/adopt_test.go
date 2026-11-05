package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/drift"
	"github.com/elocke/piste/cli/internal/platform"
)

var offered = []drift.Find{
	{Tool: drift.Brew, Name: "ripgrep", Class: drift.Untracked, To: drift.ToNixpkgs, Pkg: &drift.Package{Pname: "ripgrep", Version: "15.2.0"}},
	{Tool: drift.Cask, Name: "slack", Class: drift.Untracked, To: drift.ToCasks},
	{Tool: drift.Npm, Name: "prettier", Class: drift.Untracked, To: drift.ToPnpm},
}

func names(fs []drift.Find) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name)
	}
	return out
}

// plainApp answers plain prompts from stdin.
func plainApp(stdin string) (*App, *bytes.Buffer) {
	var out bytes.Buffer
	return &App{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &bytes.Buffer{}, forms: ptr(false)}, &out
}

// Everything offered starts ticked: Enter adopts it all; Space (or x)
// unticks; Ctrl+C stops.
func TestPickAdoptForm(t *testing.T) {
	for _, c := range []struct {
		steps []any
		want  []string
	}{
		{[]any{enter}, []string{"ripgrep", "slack", "prettier"}},
		{steps(down, text("x"), enter), []string{"ripgrep", "prettier"}},
	} {
		got, err := formApp(t, c.steps).pickAdopt("Adopt into overlays/jane (testhost)", offered)
		if err != nil || !reflect.DeepEqual(names(got), c.want) {
			t.Errorf("%d steps: %v, %v; want %v", len(c.steps), names(got), err, c.want)
		}
	}
	if _, err := formApp(t, []any{ctrlC}).pickAdopt("t", offered); !errors.Is(err, errStopped) {
		t.Errorf("Ctrl+C: %v, want errStopped", err)
	}
}

// The labels say where each would go.
func TestPickAdoptFormLabels(t *testing.T) {
	a := formApp(t, steps(viewHas(t, "brew → nixpkgs ripgrep 15.2.0", true), viewHas(t, "cask → Homebrew casks", true),
		viewHas(t, "npm → pnpm globals", true), viewHas(t, "Space toggles, Enter adopts", true), enter))
	if _, err := a.pickAdopt("Adopt into overlays/jane (testhost)", offered); err != nil {
		t.Fatal(err)
	}
}

// Without forms: one yes-by-default question per item; the end of the
// input stops with nothing picked.
func TestPickAdoptPlain(t *testing.T) {
	a, out := plainApp("\nn\ny\n")
	got, err := a.pickAdopt("Adopt into overlays/jane (testhost)", offered)
	if err != nil || !reflect.DeepEqual(names(got), []string{"ripgrep", "prettier"}) {
		t.Errorf("%v, %v", names(got), err)
	}
	if !strings.Contains(out.String(), "  ripgrep   brew → nixpkgs ripgrep 15.2.0? [Y/n]: ") {
		t.Errorf("prompt: %q", out.String())
	}
	for _, in := range []string{"", "\n"} {
		a, _ := plainApp(in)
		if _, err := a.pickAdopt("t", offered[:2]); !errors.Is(err, errStopped) {
			t.Errorf("stdin %q: %v, want errStopped", in, err)
		}
	}
}

func TestYesNo(t *testing.T) {
	a, out := plainApp("maybe\ny\n")
	if ok, err := a.yesNo("Go?"); !ok || err != nil {
		t.Errorf("%v, %v", ok, err)
	}
	if n := strings.Count(out.String(), "Go? [Y/n]"); n != 2 || !strings.Contains(out.String(), "Answer y or n.") {
		t.Errorf("asked %d times: %q", n, out.String())
	}
	for in, want := range map[string]bool{"\n": true, "Y\n": true, "yes\n": true, "n\n": false, "No\n": false} {
		a, _ := plainApp(in)
		if ok, err := a.yesNo("Go?"); ok != want || err != nil {
			t.Errorf("%q: %v, %v", in, ok, err)
		}
	}
}

// Switching and removing both default to yes; Ctrl+C and the end of the
// input are Later and Keep.
func TestAskSwitchAndRemove(t *testing.T) {
	cmds := [][]string{{"brew", "uninstall", "ripgrep"}, {"npm", "rm", "-g", "prettier", "typescript"}}
	if !formApp(t, []any{enter}).askSwitch() || !formApp(t, []any{enter}).askRemove(cmds) {
		t.Error("Enter isn't yes")
	}
	if formApp(t, []any{left, enter}).askSwitch() {
		t.Error("Later picked, switched")
	}
	if formApp(t, []any{ctrlC}).askSwitch() || formApp(t, []any{ctrlC}).askRemove(cmds) {
		t.Error("Ctrl+C isn't no")
	}
	a := formApp(t, steps(viewHas(t, "Remove 3 copies outside Nix? Nix provides them now.", true),
		viewHas(t, "npm rm -g prettier typescript", true), enter))
	if !a.askRemove(cmds) {
		t.Error("removal view")
	}
	p, out := plainApp("\n")
	if !p.askSwitch() || !strings.Contains(out.String(), "Switch now? Nix then provides them. [Y/n]") {
		t.Errorf("plain switch: %q", out.String())
	}
	p, out = plainApp("\n")
	if !p.askRemove(cmds) || !strings.Contains(out.String(), "  brew uninstall ripgrep\n  npm rm -g prettier typescript\n") {
		t.Errorf("plain remove: %q", out.String())
	}
	if p, _ := plainApp(""); p.askSwitch() {
		t.Error("EOF switched")
	}
	if p, _ := plainApp(""); p.askRemove(cmds) {
		t.Error("EOF removed")
	}
}

// More than fit: the list scrolls, so the last one can still be unticked.
func TestPickAdoptFormScrolls(t *testing.T) {
	var many []drift.Find
	for i := range 20 {
		many = append(many, drift.Find{Tool: drift.Cask, Name: fmt.Sprintf("app%02d", i), Class: drift.Untracked, To: drift.ToCasks})
	}
	keys := []any{}
	for range 19 {
		keys = append(keys, down)
	}
	keys = append(keys, viewHas(t, "app19", true), text("x"), enter)
	got, err := formApp(t, keys).pickAdopt("t", many)
	if err != nil || len(got) != 19 || got[18].Name != "app18" {
		t.Errorf("%v, %v", names(got), err)
	}
}

// A name two tools found goes into its list once; both finds
// count as written (both copies outside Nix go once Nix has it), and the
// report names it once.
func TestWriteAdoptedOnce(t *testing.T) {
	dir := t.TempDir()
	put := func(rel, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("hosts/testhost.nix", "{ pkgs, ... }:\n{\n  home.packages = [ ];\n}\n")
	put("modules/js.nix", "let\n  pnpmPackages = [\n  ];\nin\n{ }\n")
	f := found{p: platform.Platform{FlakeDir: dir}, host: "jane@testhost",
		decl: drift.Declarations{Files: map[string][]string{"home.packages": {"/nix/store/0000000000000000000000000000000a-source/hosts/testhost.nix"}}}}
	black := &drift.Package{Pname: "black", Version: "25.1.0"}
	picked := []drift.Find{
		{Tool: drift.Npm, Name: "tsx", Class: drift.Untracked, To: drift.ToPnpm},
		{Tool: drift.Pnpm, Name: "tsx", Class: drift.Untracked, To: drift.ToPnpm},
		{Tool: drift.Cargo, Name: "black", Class: drift.Untracked, To: drift.ToNixpkgs, Pkg: black},
		{Tool: drift.Pip, Name: "black", Class: drift.Untracked, To: drift.ToNixpkgs, Pkg: black},
	}
	a, out := plainApp("")
	written, byHand := a.writeAdopted(f, picked)
	if len(written) != 4 || byHand {
		t.Errorf("written %v, by hand %v", names(written), byHand)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hosts/testhost.nix")); string(b) != "{ pkgs, ... }:\n{\n  home.packages = [\n    pkgs.black\n  ];\n}\n" {
		t.Errorf("host file:\n%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "modules/js.nix")); strings.Count(string(b), `"tsx"`) != 1 {
		t.Errorf("js.nix:\n%s", b)
	}
	if o := out.String(); !strings.Contains(o, "  pnpmPackages tsx\n") || !strings.Contains(o, "  home.packages black\n") {
		t.Errorf("report: %q", o)
	}
}

// A brew service offered into Homebrew's brews says why not
// nixpkgs.
func TestTargetLabelBrewService(t *testing.T) {
	f := drift.Find{Tool: drift.Brew, Name: "tailscale", Class: drift.Untracked, To: drift.ToBrews, Why: drift.BrewServiceWhy}
	if got := targetLabel(f); got != "Homebrew brews ("+drift.BrewServiceWhy+")" {
		t.Errorf("got %q", got)
	}
	if got := targetLabel(drift.Find{To: drift.ToBrews}); got != "Homebrew brews" {
		t.Errorf("plain: %q", got)
	}
}
