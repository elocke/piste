package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
	"github.com/elocke/piste/cli/internal/platform"
)

// brewApp is pkgsApp with brewManifest pointing at a manifest of example
// names (restored after the test).
func brewApp(t *testing.T, rec *execx.Recorder) (*App, *bytes.Buffer) {
	t.Helper()
	old := brewManifest
	t.Cleanup(func() { brewManifest = old })
	a, out := pkgsApp(t, rec)
	brewManifest = filepath.Join(t.TempDir(), "homebrew.json")
	if err := os.WriteFile(brewManifest, []byte(`{"brews":["jq","example/tap/tool"],"casks":["example-app","example/tap/other-app"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return a, out
}

// brew outdated --json=v2's shape: a formula's name is its full name (a tap
// formula's tap-qualified), a cask's is its short token, and there is no
// full_name.
const brewOutdatedJSON = `{
  "formulae": [
    {"name":"jq","installed_versions":["1.6"],"current_version":"1.7.1"},
    {"name":"example/tap/tool","installed_versions":["1.0"],"current_version":"1.1"},
    {"name":"undeclared","installed_versions":["1"],"current_version":"2"}
  ],
  "casks": [
    {"name":"example-app","installed_versions":["2.0"],"current_version":"2.1"},
    {"name":"other-app","installed_versions":["3.0"],"current_version":"3.2"}
  ]
}`

func TestBrewUpgradeArgs(t *testing.T) {
	for _, greedy := range []bool{false, true} {
		rec := &execx.Recorder{Replies: map[string]execx.Result{"brew outdated": {Stdout: []byte(brewOutdatedJSON)}}}
		a, _ := brewApp(t, rec)
		if !a.brewUpgrade(greedy) {
			t.Errorf("greedy %v: brewUpgrade reported a failure", greedy)
		}
		want := [][]string{
			{"brew", "outdated", "--json=v2"},
			{"brew", "upgrade", "--formula", "jq", "example/tap/tool"},
			{"brew", "upgrade", "--cask", "example-app", "example/tap/other-app"},
		}
		if greedy {
			want[0] = append(want[0], "--greedy")
			want[2] = []string{"brew", "upgrade", "--cask", "--greedy", "example-app", "example/tap/other-app"}
		}
		if !reflect.DeepEqual(rec.Calls, want) {
			t.Errorf("greedy %v: calls\n  got  %q\n  want %q", greedy, rec.Calls, want)
		}
	}
}

func TestBrewUpgradeNothing(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"brew outdated": {Stdout: []byte(`{"formulae":[],"casks":[]}`)}}}
	a, out := brewApp(t, rec)
	if !a.brewUpgrade(false) {
		t.Error("brewUpgrade reported a failure")
	}
	if want := [][]string{{"brew", "outdated", "--json=v2"}}; !reflect.DeepEqual(rec.Calls, want) {
		t.Errorf("calls = %q, want only %q", rec.Calls, want)
	}
	if !strings.Contains(out.String(), "Homebrew: up to date") {
		t.Errorf("no up-to-date line:\n%s", out)
	}
}

// Only a cask is behind, a tap's: no formula call, and the cask upgrades
// by its declared, tap-qualified name.
func TestBrewUpgradeOneKind(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"brew outdated": {Stdout: []byte(`{"formulae":[],"casks":[{"name":"other-app","installed_versions":["1"],"current_version":"2"}]}`)}}}
	a, _ := brewApp(t, rec)
	a.brewUpgrade(false)
	if want := [][]string{{"brew", "outdated", "--json=v2"}, {"brew", "upgrade", "--cask", "example/tap/other-app"}}; !reflect.DeepEqual(rec.Calls, want) {
		t.Errorf("calls = %q, want %q", rec.Calls, want)
	}
}

// A failed formula upgrade warns, and the casks still upgrade.
func TestBrewUpgradeFails(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{
		"brew outdated":          {Stdout: []byte(brewOutdatedJSON)},
		"brew upgrade --formula": {ExitCode: 1},
	}}
	a, out := brewApp(t, rec)
	if a.brewUpgrade(false) {
		t.Error("brewUpgrade reported success after brew upgrade --formula failed")
	}
	if n := len(rec.Calls); n != 3 {
		t.Errorf("calls = %q, want the cask upgrade after the failed formula one", rec.Calls)
	}
	if !strings.Contains(out.String(), "pkgs update --only brew") {
		t.Errorf("the warning doesn't say how to try again:\n%s", out)
	}
}

// brew info --json=v2 --installed's shape: a formula's aliases and old
// names, a cask's old tokens (its "name" is a list of display names).
const brewInfoJSON = `{
  "formulae": [
    {"name":"python@3.13","full_name":"python@3.13","tap":"homebrew/core","oldnames":[],"aliases":["python","python3"],
     "installed":[{"version":"3.13.0"}]},
    {"name":"jq","full_name":"jq","tap":"homebrew/core","oldnames":[],"aliases":[],"installed":[{"version":"1.6"}]}
  ],
  "casks": [
    {"token":"example-app","full_token":"example-app","old_tokens":["old-example-app"],"tap":"homebrew/cask",
     "name":["Example App"],"installed":"2.0"}
  ]
}`

// A formula declared by an alias (python, brew's python@3.13) and a cask by
// an old token match through brew info, and keep the declared names: in the
// report and in brew upgrade, which resolves them.
func TestBrewUpgradeAlias(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{
		"brew outdated": {Stdout: []byte(`{
		  "formulae": [
		    {"name":"python@3.13","installed_versions":["3.13.0"],"current_version":"3.13.1"},
		    {"name":"jq","installed_versions":["1.6"],"current_version":"1.7.1"}
		  ],
		  "casks": [{"name":"example-app","installed_versions":["2.0"],"current_version":"2.1"}]}`)},
		"brew info": {Stdout: []byte(brewInfoJSON)},
	}}
	a, out := brewApp(t, rec)
	if err := os.WriteFile(brewManifest, []byte(`{"brews":["python","jq"],"casks":["old-example-app"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !a.brewUpgrade(false) {
		t.Errorf("brewUpgrade reported a failure:\n%s", out)
	}
	want := [][]string{
		{"brew", "outdated", "--json=v2"},
		{"brew", "info", "--json=v2", "--installed"},
		{"brew", "upgrade", "--formula", "python", "jq"},
		{"brew", "upgrade", "--cask", "old-example-app"},
	}
	if !reflect.DeepEqual(rec.Calls, want) {
		t.Errorf("calls\n  got  %q\n  want %q", rec.Calls, want)
	}

	// The report names them as declared.
	out.Reset()
	a.pkgsReport(platform.Platform{FlakeDir: t.TempDir(), IsDarwin: true})
	for _, w := range []string{"python ", "3.13.0 → 3.13.1", "old-example-app", "2.0 → 2.1"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report lacks %q:\n%s", w, out)
		}
	}
}

// brew info runs only when a declared name and an outdated entry of the
// same kind are both left over; when it fails, the direct matches stand.
func TestBrewInfoOnlyForLeftovers(t *testing.T) {
	outdatedJSON := `{"formulae":[{"name":"jq","installed_versions":["1.6"],"current_version":"1.7.1"},
	  {"name":"undeclared","installed_versions":["1"],"current_version":"2"}],
	  "casks":[{"name":"example-app","installed_versions":["2.0"],"current_version":"2.1"}]}`
	for _, c := range []struct {
		name, manifest string
		info           bool
	}{
		{"every declared name matched", `{"brews":["jq"],"casks":["example-app"]}`, false},
		{"a declared formula left, no formula entry left", `{"brews":["jq","undeclared","wget"],"casks":["example-app"]}`, false},
		{"a declared cask left, no cask entry left", `{"brews":["jq","undeclared"],"casks":["example-app","other-app"]}`, false},
		{"a formula left on both sides", `{"brews":["jq","python"],"casks":["example-app"]}`, true},
	} {
		rec := &execx.Recorder{Replies: map[string]execx.Result{
			"brew outdated": {Stdout: []byte(outdatedJSON)},
			"brew info":     {ExitCode: 1},
		}}
		a, _ := brewApp(t, rec)
		if err := os.WriteFile(brewManifest, []byte(c.manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		items, _, err := a.brewOutdated(false)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		want := [][]string{{"brew", "outdated", "--json=v2"}}
		if c.info {
			want = append(want, []string{"brew", "info", "--json=v2", "--installed"})
		}
		if !reflect.DeepEqual(rec.Calls, want) {
			t.Errorf("%s: calls\n  got  %q\n  want %q", c.name, rec.Calls, want)
		}
		// brew info failed: no alias matching, the direct matches stand.
		if len(items) < 2 || items[0].Name != "jq" || items[len(items)-1].Name != "example-app" {
			t.Errorf("%s: items = %+v, want jq and example-app", c.name, items)
		}
	}
}

// No manifest: nothing to upgrade, said so, brew never run.
func TestBrewUpgradeNoManifest(t *testing.T) {
	rec := &execx.Recorder{}
	a, out := brewApp(t, rec)
	brewManifest = filepath.Join(t.TempDir(), "missing.json")
	if !a.brewUpgrade(false) {
		t.Error("brewUpgrade reported a failure without a manifest")
	}
	if len(rec.Calls) != 0 {
		t.Errorf("calls = %q, want none", rec.Calls)
	}
	if !strings.Contains(out.String(), brewManifest) {
		t.Errorf("no line about the missing manifest:\n%s", out)
	}
}

// A Mac whose config turns Homebrew off (homebrew.enable = false): its
// manifest says so. The report has one line for it, the upgrade and the
// dry run skip it quietly, brew is never run, and --only brew is an error.
func TestBrewOff(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"brew outdated": {Stdout: []byte(brewOutdatedJSON)}}}
	a, out := brewApp(t, rec)
	if err := os.WriteFile(brewManifest, []byte(`{"brews":[],"casks":[],"enabled":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	a.pkgsReport(platform.Platform{FlakeDir: t.TempDir(), IsDarwin: true})
	if !strings.Contains(out.String(), "\nHomebrew: not managed by this machine's config\n") {
		t.Errorf("report lacks the Homebrew-off line:\n%s", out)
	}
	if strings.Contains(out.String(), "a switch writes it") {
		t.Errorf("report says a switch writes the list:\n%s", out)
	}

	out.Reset()
	if !a.brewUpgrade(false) || !a.brewWould(false) {
		t.Error("brewUpgrade or brewWould reported a failure with Homebrew off")
	}
	if strings.Contains(out.String(), "Homebrew") {
		t.Errorf("the upgrade isn't quiet with Homebrew off:\n%s", out)
	}
	if len(rec.Calls) != 0 {
		t.Errorf("calls = %q, want none", rec.Calls)
	}

	if _, err := stepsFor([]string{"brew"}, true); err == nil || err.Error() != "brew: Homebrew isn't managed by this machine's config" {
		t.Errorf("--only brew with Homebrew off: err = %v", err)
	}
	if _, err := stepsFor([]string{"nix", "brew"}, true); err == nil {
		t.Error("--only nix,brew with Homebrew off isn't an error")
	}
	// Without --only, the brew step stays: it skips itself, after the
	// switch, on what the switched manifest says then.
	if s, err := stepsFor(nil, true); err != nil || !s.brew {
		t.Errorf("no --only with Homebrew off: steps %+v, err %v", s, err)
	}
}

// A manifest without "enabled" is an older switch's: Homebrew is on.
func TestBrewManifestWithoutEnabled(t *testing.T) {
	rec := &execx.Recorder{Replies: map[string]execx.Result{"brew outdated": {Stdout: []byte(brewOutdatedJSON)}}}
	a, out := brewApp(t, rec)
	if _, err := stepsFor([]string{"brew"}, true); err != nil {
		t.Errorf("--only brew: %v", err)
	}
	a.pkgsReport(platform.Platform{FlakeDir: t.TempDir(), IsDarwin: true})
	if strings.Contains(out.String(), "not managed") || !strings.Contains(out.String(), "example/tap/tool") {
		t.Errorf("a manifest without enabled isn't taken as on:\n%s", out)
	}
	// "enabled": true is on too.
	if err := os.WriteFile(brewManifest, []byte(`{"brews":["jq"],"casks":[],"enabled":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if items, _, err := a.brewOutdated(false); err != nil || len(items) != 1 || items[0].Name != "jq" {
		t.Errorf("enabled true: items %+v, err %v", items, err)
	}
}

func TestPnpmUpgradeAbsent(t *testing.T) {
	rec := &execx.Recorder{}
	a, out := pkgsApp(t, rec)
	if a.pnpmTool() != "" {
		t.Fatal("a piste-pnpm was found")
	}
	if !a.pnpmUpgrade() {
		t.Error("pnpmUpgrade reported a failure with nothing declared")
	}
	if len(rec.Calls) != 0 {
		t.Errorf("calls = %q, want none", rec.Calls)
	}
	if !strings.Contains(out.String(), "pnpm globals: none declared (piste.packages.pnpm in home.nix)") {
		t.Errorf("no info line:\n%s", out)
	}
}

func TestPnpmUpgrade(t *testing.T) {
	rec := &execx.Recorder{}
	a, out := pkgsApp(t, rec)
	bin := filepath.Join(os.Getenv("HOME"), ".nix-profile", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(bin, "piste-pnpm")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !a.pnpmUpgrade() {
		t.Error("pnpmUpgrade reported a failure")
	}
	if want := [][]string{{tool, "upgrade"}}; !reflect.DeepEqual(rec.Calls, want) {
		t.Errorf("calls = %q, want %q", rec.Calls, want)
	}

	// It fails: a warning that says how to try again.
	rec.Calls = nil
	rec.Replies = map[string]execx.Result{tool + " upgrade": {ExitCode: 1}}
	if a.pnpmUpgrade() {
		t.Error("pnpmUpgrade reported success after piste-pnpm upgrade failed")
	}
	if !strings.Contains(out.String(), "pkgs update --only pnpm") {
		t.Errorf("the warning doesn't say how to try again:\n%s", out)
	}
}

func TestPkgsUpdateSteps(t *testing.T) {
	old := brewManifest
	t.Cleanup(func() { brewManifest = old })
	brewManifest = filepath.Join(t.TempDir(), "missing.json")
	for _, c := range []struct {
		name   string
		only   []string
		darwin bool
		want   updateSteps
		err    string
	}{
		{"all, Linux", nil, false, updateSteps{nix: true, pnpm: true}, ""},
		{"all, Mac", nil, true, updateSteps{nix: true, pnpm: true, brew: true}, ""},
		{"pnpm", []string{"pnpm"}, false, updateSteps{pnpm: true}, ""},
		{"nix and brew", []string{"nix", "brew"}, true, updateSteps{nix: true, brew: true}, ""},
		{"brew off a Mac", []string{"brew"}, false, updateSteps{}, "brew: Homebrew is managed on a Mac only"},
		{"unknown", []string{"npm"}, true, updateSteps{}, `--only "npm"`},
	} {
		got, err := stepsFor(c.only, c.darwin)
		switch {
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%s: err = %v, want %q", c.name, err, c.err)
		case c.err == "" && err != nil:
			t.Errorf("%s: err = %v", c.name, err)
		case c.err == "" && got != c.want:
			t.Errorf("%s: steps = %+v, want %+v", c.name, got, c.want)
		}
	}
}

// pkgs update's build and its switch both say which flake and override
// they use: the same pair prints once a run. Another pair prints.
func TestAnnounceFlakeOnce(t *testing.T) {
	var out bytes.Buffer
	a := &App{Stdout: &out, Stderr: &out}
	p := platform.Platform{FlakeDir: "/home/user/piste/overlays/mine", FlakeSource: "checkout /home/user/piste: overlay mine (cwd)"}
	o := platform.EngineOverride{Path: "/home/user/piste", Reason: "the checkout around the overlay"}
	a.announceFlake(p, &o)
	a.announceFlake(p, &o)
	if n := strings.Count(out.String(), "Flake: "); n != 1 {
		t.Errorf("Flake: printed %d times, want 1:\n%s", n, out.String())
	}
	if n := strings.Count(out.String(), "engine override: "); n != 1 {
		t.Errorf("engine override: printed %d times, want 1:\n%s", n, out.String())
	}

	// Another override, then none, then another flake: each prints.
	for _, c := range []struct {
		p platform.Platform
		o *platform.EngineOverride
	}{
		{p, &platform.EngineOverride{Reason: "$PISTE_ENGINE=none"}},
		{p, nil},
		{platform.Platform{FlakeDir: "/home/user/other", FlakeSource: platform.FlakeFromCwd}, nil},
	} {
		out.Reset()
		a.announceFlake(c.p, c.o)
		if !strings.Contains(out.String(), "Flake: "+c.p.FlakeDir) {
			t.Errorf("%+v, %+v: not announced:\n%s", c.p, c.o, out.String())
		}
	}
}

// Ctrl+C at "Switch now?" stops the run, as at any other question
// ("Aborted!", exit 1). Input that ends there (no terminal, no --yes) is a
// no: the caller says "Not switched", exit 0.
func TestAskSwitchNow(t *testing.T) {
	var out, errb bytes.Buffer
	a := &App{Stdout: &out, Stderr: &errb, forms: ptr(true), runForm: drive(t, []any{ctrlC}).run}
	yes, err := a.askSwitchNow()
	if yes || err != (ExitError{1}) {
		t.Errorf("Ctrl+C: askSwitchNow = %v, %v; want false, exit 1", yes, err)
	}
	if errb.String() != "Aborted!\n" {
		t.Errorf("Ctrl+C: stderr = %q, want Aborted!", errb.String())
	}

	// The form answered: yes, and no.
	for _, c := range []struct {
		keys []any
		want bool
	}{{[]any{left, enter}, true}, {[]any{enter}, false}} {
		a = &App{Stdout: &out, Stderr: &errb, forms: ptr(true), runForm: drive(t, c.keys).run}
		if yes, err := a.askSwitchNow(); yes != c.want || err != nil {
			t.Errorf("keys %v: askSwitchNow = %v, %v; want %v", c.keys, yes, err, c.want)
		}
	}

	// No terminal, and stdin at its end.
	empty, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	stdin := os.Stdin
	os.Stdin = empty
	t.Cleanup(func() { os.Stdin = stdin })
	out.Reset()
	errb.Reset()
	a = &App{Stdout: &out, Stderr: &errb, forms: ptr(false)}
	if yes, err := a.askSwitchNow(); yes || err != nil {
		t.Errorf("end of input: askSwitchNow = %v, %v; want false, nil", yes, err)
	}
	if out.String() != "Switch now? [y/N]: \n" || errb.Len() != 0 {
		t.Errorf("end of input: stdout %q, stderr %q", out.String(), errb.String())
	}
}
