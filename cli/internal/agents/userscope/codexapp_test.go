package userscope

import (
	"strings"
	"testing"
)

// withCodex is a fakeObs whose Codex has plugins installed.
type withCodex struct {
	fakeObs
	codex map[string]Item
}

func (f withCodex) CodexPlugins() (map[string]Item, error) { return f.codex, nil }

// The Codex app installs plugins of its own, from its openai-bundled and
// openai-primary-runtime marketplaces (browser, documents, pdf, …): no
// agents.toml declares them, and they aren't the person's to remove. check
// tolerates them, even under unlisted = "deny", and sync --prune leaves them;
// an undeclared plugin of any other marketplace is still undeclared.
func TestCodexAppPluginsTolerated(t *testing.T) {
	_, m := writeCatalogFixture(t, "[]", "[]")
	z, locks, err := ZoneFromManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if z.Policy.Unlisted != "deny" {
		t.Fatalf("fixture policy %+v: want unlisted = deny", z.Policy)
	}
	lk, err := LoadLocks(locks)
	if err != nil {
		t.Fatal(err)
	}
	obs := withCodex{fakeObs: inSync, codex: map[string]Item{
		"browser@openai-bundled":                  {"version": "1.0.0", "enabled": true},
		"computer-use@openai-bundled":             {"version": "1.0.0", "enabled": true},
		"spreadsheets@openai-primary-runtime":     {"version": "0.3.0", "enabled": true},
		"template-creator@openai-primary-runtime": {"version": "0.3.0", "enabled": true},
		"stray@someone-else":                      {"version": "2.0.0", "enabled": true},
	}}
	rows, err := ThirdPartyRows(z, lk, obs, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		if r.Kind == "codex.plugin" {
			got[r.Target] = r.State
		}
	}
	for pid := range obs.codex {
		want := "tolerated"
		if pid == "stray@someone-else" {
			want = "undeclared"
		}
		if got[pid] != want {
			t.Errorf("codex.plugin %s: state %q, want %q", pid, got[pid], want)
		}
	}
	healthy := HealthyStates(z)
	if !healthy["tolerated"] || healthy["undeclared"] {
		t.Errorf("healthy states %v", healthy)
	}

	_, removals, _, err := PlanSync(z, lk, obs, true, false, "laptop", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var rm []string
	for _, s := range removals {
		rm = append(rm, strings.Join(s.Argv, " "))
	}
	if strings.Join(rm, "\n") != "codex plugin remove stray@someone-else" {
		t.Errorf("sync --prune removals %q: want only the stray plugin", rm)
	}
}
