package userscope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeObs is a machine whose harnesses match the lock.
type fakeObs struct{ markets, plugins map[string]Item }

func (f fakeObs) ClaudeMarketplaces() (map[string]Item, error) { return f.markets, nil }
func (f fakeObs) ClaudePlugins() (map[string]Item, error)      { return f.plugins, nil }
func (f fakeObs) CodexPlugins() (map[string]Item, error)       { return map[string]Item{}, nil }
func (f fakeObs) PnpmGlobals() map[string]string               { return map[string]string{} }
func (f fakeObs) WhichAll(string) []string                     { return nil }
func (f fakeObs) FileVersion(string) (string, bool)            { return "", false }

const c40a = "1111111111111111111111111111111111111111"
const c40b = "2222222222222222222222222222222222222222"

// A fixture shaped like the real work zone after #61: the shared catalog
// (agents.base.toml) declares the impeccable skill and marketplace, which no
// work preset or plugin uses, next to items the zone does use.
var catalogFixture = map[string]string{
	"base/agents.base.toml": `
[policy]
allow = ["github:anthropics/claude-plugins-official", "github:manutej/luxor-claude-marketplace", "github:pbakaus/impeccable"]
require = "commit"

[[skill]]
name   = "vuejs-development"
source = "github:manutej/luxor-claude-marketplace"
path   = "plugins/vue"
ref    = "` + c40a + `"

[[skill]]
name   = "impeccable"
source = "github:pbakaus/impeccable"
path   = ".agents/skills/impeccable"
ref    = "skill-v4.5.0"

[[claude.marketplace]]
name   = "claude-plugins-official"
source = "github:anthropics/claude-plugins-official"
pin    = "source"

[[claude.marketplace]]
name   = "impeccable"
source = "github:pbakaus/impeccable"
ref    = "skill-v4.5.0"

[[claude.plugin]]
id    = "commit-commands@claude-plugins-official"
scope = "user"
`,
	"base/presets/base.toml": "targets = [\"claude\", \"codex\"]\nskills = [\"owned-skill\"]\n",
	"base/presets/web.toml":  "targets = [\"claude\"]\nskills = [\"vuejs-development\"]\n",
	"work/agents.toml": `
[policy]
allow    = ["github:anthropics/claude-plugins-official", "github:acme/claude-toolkit"]
require  = "commit"
unlisted = "deny"

[[claude.marketplace]]
name   = "acme"
source = "github:acme/claude-toolkit"

[[claude.plugin]]
id    = "jira@acme"
scope = "user"
`,
	"work/presets/work.toml": "targets = [\"claude\", \"codex\"]\nskills = []\n",
	"work/agents.lock": `version = 1

[[skill]]
name    = "vuejs-development"
source  = "github:manutej/luxor-claude-marketplace"
path    = "plugins/vue"
ref     = "` + c40a + `"
commit  = "` + c40a + `"
narHash = "sha256-x"

[[claude.marketplace]]
name   = "claude-plugins-official"
source = "github:anthropics/claude-plugins-official"
commit = "` + c40a + `"

[[claude.marketplace]]
name   = "acme"
source = "github:acme/claude-toolkit"
commit = "` + c40b + `"

[[claude.plugin]]
id      = "commit-commands@claude-plugins-official"
version = "1.0.0"

[[claude.plugin]]
id      = "jira@acme"
version = "2.0.0"
`,
}

func writeCatalogFixture(t *testing.T, links, deferred string) (dir string, m *Manifest) {
	t.Helper()
	dir = t.TempDir()
	for p, s := range catalogFixture {
		f := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mj := `{"version": 2, "links": ` + links + `, "instructions": [], "deferred": ` + deferred + `,
 "thirdParty": [
  {"source": "piste-agents", "file": "agents.base.toml", "path": "` + filepath.Join(dir, "base/agents.base.toml") + `"},
  {"source": "work-agents", "file": "agents.toml", "path": "` + filepath.Join(dir, "work/agents.toml") + `"},
  {"source": "work-agents", "file": "agents.lock", "path": "` + filepath.Join(dir, "work/agents.lock") + `"}]}`
	mp := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(mp, []byte(mj), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(mp)
	if err != nil {
		t.Fatal(err)
	}
	return dir, m
}

var inSync = fakeObs{
	markets: map[string]Item{
		"claude-plugins-official": {"source": "github:anthropics/claude-plugins-official", "commit": c40a},
		"acme":                    {"source": "github:acme/claude-toolkit", "commit": c40b},
	},
	plugins: map[string]Item{
		"commit-commands@claude-plugins-official": {"version": "1.0.0", "enabled": true},
		"jira@acme": {"version": "2.0.0", "enabled": true},
	},
}

// `piste agents check` / `sync` on a work host whose presets select no
// third-party skill: nothing about the unused catalog skill or marketplace,
// and an in-sync machine is all healthy.
func TestWorkCheckIgnoresUnusedCatalog(t *testing.T) {
	_, m := writeCatalogFixture(t, "[]", "[]")
	z, locks, err := ZoneFromManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	lk, err := LoadLocks(locks)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ThirdPartyRows(z, lk, inSync, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	healthy := HealthyStates(z)
	for _, r := range rows {
		if strings.Contains(r.Target+" "+r.Detail, "impeccable") || !healthy[r.State] {
			t.Errorf("unexpected row %v", r)
		}
	}
	steps, removals, notes, err := PlanSync(z, lk, inSync, true, false, "laptop", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(steps)+len(removals)+len(notes) != 0 {
		t.Errorf("sync plan not empty: %v %v %v", steps, removals, notes)
	}
	if _, ok := z.Decls["skill"]["vuejs-development"]; ok {
		t.Error("vuejs-development: this host's presets don't select it")
	}
}

// The host's own selection (manifest links / deferred, lib/agents.nix
// usedSkills) keeps a catalog skill; `update` (NewZone) keeps every skill a
// preset of the zone's roots names, and drops the rest.
func TestCatalogSkillUse(t *testing.T) {
	dir, m := writeCatalogFixture(t, `[{"target": "x", "kind": "skills", "name": "vuejs-development", "harness": "claude", "repo": "third-party", "source": "/nix/store/x"}]`, "[]")
	z, _, err := ZoneFromManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(z.Order["skill"], ","); got != "vuejs-development" {
		t.Errorf("host skills %q", got)
	}
	_, m = writeCatalogFixture(t, "[]", `["vuejs-development"]`)
	if z, _, _ = ZoneFromManifest(m); strings.Join(z.Order["skill"], ",") != "vuejs-development" {
		t.Errorf("deferred: host skills %v", z.Order["skill"])
	}
	z, err = NewZone([]string{filepath.Join(dir, "base/agents.base.toml"), filepath.Join(dir, "work/agents.toml")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(z.Order["skill"], ","); got != "vuejs-development" {
		t.Errorf("update skills %q, want vuejs-development (impeccable unused)", got)
	}
	// A zone-declared skill counts with no preset naming it.
	os.WriteFile(filepath.Join(dir, "work/agents.toml"), []byte(catalogFixture["work/agents.toml"]+"\n[[skill]]\nname = \"own\"\nsource = \"github:acme/own\"\n"), 0o644)
	if z, err = NewZone([]string{filepath.Join(dir, "base/agents.base.toml"), filepath.Join(dir, "work/agents.toml")}, nil); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(z.Order["skill"], ","); got != "vuejs-development,own" {
		t.Errorf("skills %q", got)
	}
}
