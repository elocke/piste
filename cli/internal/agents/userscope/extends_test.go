package userscope

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An overlay on a team layer declares third-party items of its own: a second
// root of the zone (the overlay's agents/) whose [policy] says extends = true.
// It adds allow entries to the zone's policy (the layer's, which stays the
// one with unlisted, require, installers, tolerate) and has its own lock with
// only what it declares. check and sync read both roots' manifests and both
// locks.
const ownToml = `
[policy]
extends = true
allow   = ["github:acme/own-market"]

[[claude.marketplace]]
name   = "own"
source = "github:acme/own-market"

[[claude.plugin]]
id    = "mine@own"
scope = "user"
`

const ownLock = `version = 1

[[claude.marketplace]]
name   = "own"
source = "github:acme/own-market"
commit = "` + c40b + `"

[[claude.plugin]]
id      = "mine@own"
version = "3.0.0"
`

// writeTwoRoots is the catalog fixture plus an own/ root, with a manifest
// listing both roots' agents.toml and agents.lock.
func writeTwoRoots(t *testing.T, own, lock string) (dir string, m *Manifest) {
	t.Helper()
	dir, _ = writeCatalogFixture(t, "[]", "[]")
	for p, s := range map[string]string{"own/agents.toml": own, "own/agents.lock": lock} {
		f := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mj := `{"version": 2, "links": [], "instructions": [], "deferred": [],
 "thirdParty": [
  {"source": "piste-agents", "file": "agents.base.toml", "path": "` + filepath.Join(dir, "base/agents.base.toml") + `"},
  {"source": "work-agents", "file": "agents.toml", "path": "` + filepath.Join(dir, "work/agents.toml") + `"},
  {"source": "work-agents", "file": "agents.lock", "path": "` + filepath.Join(dir, "work/agents.lock") + `"},
  {"source": "work-own", "file": "agents.toml", "path": "` + filepath.Join(dir, "own/agents.toml") + `"},
  {"source": "work-own", "file": "agents.lock", "path": "` + filepath.Join(dir, "own/agents.lock") + `"}]}`
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

var withOwn = fakeObs{
	markets: map[string]Item{
		"claude-plugins-official": {"source": "github:anthropics/claude-plugins-official", "commit": c40a},
		"acme":                    {"source": "github:acme/claude-toolkit", "commit": c40b},
		"own":                     {"source": "github:acme/own-market", "commit": c40b},
	},
	plugins: map[string]Item{
		"commit-commands@claude-plugins-official": {"version": "1.0.0", "enabled": true},
		"jira@acme": {"version": "2.0.0", "enabled": true},
		"mine@own":  {"version": "3.0.0", "enabled": true},
	},
}

func TestExtendsRootChecksAndSyncs(t *testing.T) {
	_, m := writeTwoRoots(t, ownToml, ownLock)
	z, locks, err := ZoneFromManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if z.Policy.Unlisted != "deny" || !strings.Contains(strings.Join(z.Policy.Allow, " "), "github:acme/own-market") {
		t.Errorf("policy %+v: want the layer's deny with the own root's allow added", z.Policy)
	}
	lk, err := LoadLocks(locks)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lk["claude.plugin"]["mine@own"]; !ok {
		t.Error("the own root's lock wasn't read")
	}
	if _, ok := lk["claude.plugin"]["jira@acme"]; !ok {
		t.Error("the layer's lock wasn't read")
	}
	rows, err := ThirdPartyRows(z, lk, withOwn, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	healthy := HealthyStates(z)
	for _, r := range rows {
		if !healthy[r.State] {
			t.Errorf("unexpected row %v", r)
		}
	}
	// The own plugin missing here: sync installs it from its marketplace.
	missing := fakeObs{markets: withOwn.markets, plugins: map[string]Item{
		"commit-commands@claude-plugins-official": {"version": "1.0.0", "enabled": true},
		"jira@acme": {"version": "2.0.0", "enabled": true},
	}}
	steps, _, _, err := PlanSync(z, lk, missing, false, false, "laptop", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, s := range steps {
		all = append(all, strings.Join(s.Argv, " "))
	}
	if !strings.Contains(strings.Join(all, "\n"), "mine@own") {
		t.Errorf("sync doesn't install the own root's plugin: %q", all)
	}
}

// The layer's deny still holds: an own marketplace the own root doesn't
// allow is a policy row.
func TestExtendsRootUnderTheZonePolicy(t *testing.T) {
	_, m := writeTwoRoots(t, strings.Replace(ownToml, `allow   = ["github:acme/own-market"]`, `allow   = []`, 1), ownLock)
	z, locks, err := ZoneFromManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	lk, err := LoadLocks(locks)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ThirdPartyRows(z, lk, withOwn, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		if r.State == "policy" && r.Target == "own" {
			found = true
		}
	}
	if !found {
		t.Errorf("no policy row for the own root's unallowed marketplace: %v", rows)
	}
}

// What an own root may not do: set the zone's policy (unlisted and the
// rest), be a second policy root without extends, or lock what the layer's
// lock already has.
func TestExtendsRootRefusals(t *testing.T) {
	var le LockError
	_, m := writeTwoRoots(t, strings.Replace(ownToml, "extends = true", "extends = true\nunlisted = \"warn\"", 1), ownLock)
	if _, _, err := ZoneFromManifest(m); !errors.As(err, &le) || !strings.Contains(le.Msg, "unlisted") {
		t.Errorf("extends with unlisted: err %v", err)
	}
	_, m = writeTwoRoots(t, strings.Replace(ownToml, "extends = true", "unlisted = \"warn\"", 1), ownLock)
	if _, _, err := ZoneFromManifest(m); !errors.As(err, &le) || !strings.Contains(le.Msg, "more than one") {
		t.Errorf("two policy roots: err %v", err)
	}
	_, m = writeTwoRoots(t, ownToml, ownLock+"\n[[claude.plugin]]\nid      = \"jira@acme\"\nversion = \"9.9.9\"\n")
	_, locks, err := ZoneFromManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocks(locks); !errors.As(err, &le) || !strings.Contains(le.Msg, "jira@acme") {
		t.Errorf("an item in both locks: err %v", err)
	}
}

// update in the own root locks what it declares and nothing of the shared
// catalog's (the layer's lock has those): here every pin is already in the
// old lock, so nothing is fetched.
func TestExtendsRootLocksItsOwn(t *testing.T) {
	dir, _ := writeTwoRoots(t, ownToml, ownLock)
	own := filepath.Join(dir, "own/agents.toml")
	z, err := NewZone([]string{filepath.Join(dir, "base/agents.base.toml"), own}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !z.Extends(own) {
		t.Fatal("the own root doesn't extend its zone")
	}
	old := emptyByKind()
	old["claude.marketplace"]["own"] = Item{"name": "own", "source": "github:acme/own-market", "commit": c40b}
	old["claude.marketplace"]["claude-plugins-official"] = Item{"name": "claude-plugins-official", "source": "github:anthropics/claude-plugins-official", "commit": c40a}
	old["claude.plugin"]["mine@own"] = Item{"id": "mine@own", "version": "3.0.0"}
	old["claude.plugin"]["commit-commands@claude-plugins-official"] = Item{"id": "commit-commands@claude-plugins-official", "version": "1.0.0"}
	own1 := z.Own(own)
	nw, err := Update(own1, old, map[string]bool{}, false, withOwn, os.Stderr, "nix", "claude")
	if err != nil {
		t.Fatal(err)
	}
	nw = OwnLock(own1, nw, old, own, emptyByKind())
	var got []string
	for _, k := range tpKinds {
		for key := range nw[k.name] {
			got = append(got, k.name+":"+key)
		}
	}
	if strings.Join(got, " ") != "claude.marketplace:own claude.plugin:mine@own" {
		t.Errorf("the own root's lock holds %q", got)
	}
}

// A plugin the own root declares on a marketplace only the shared catalog
// declares (astral@astral-sh at the work root): when the policy root's lock
// doesn't pin that marketplace, the own root's lock keeps it, or update would
// drop the pin; when the policy root's lock does, it stays there alone. The
// policy root's lock comes from an installed manifest of the same zone; with
// none, the own lock keeps the catalog marketplaces it already had.
func TestExtendsRootLocksCatalogMarketplaceItUses(t *testing.T) {
	toml := ownToml + `
[[claude.plugin]]
id    = "polish@impeccable"
scope = "user"

[[claude.plugin]]
id    = "feature-dev@claude-plugins-official"
scope = "user"
`
	dir, m := writeTwoRoots(t, toml, ownLock)
	own := filepath.Join(dir, "own/agents.toml")
	z, err := NewZone([]string{filepath.Join(dir, "base/agents.base.toml"), own}, nil)
	if err != nil {
		t.Fatal(err)
	}
	full := emptyByKind()
	full["claude.marketplace"]["own"] = Item{"name": "own", "source": "github:acme/own-market", "commit": c40b}
	full["claude.marketplace"]["impeccable"] = Item{"name": "impeccable", "source": "github:pbakaus/impeccable", "ref": "skill-v4.5.0", "commit": c40a}
	full["claude.marketplace"]["claude-plugins-official"] = Item{"name": "claude-plugins-official", "source": "github:anthropics/claude-plugins-official", "commit": c40a}
	full["claude.plugin"]["mine@own"] = Item{"id": "mine@own", "version": "3.0.0"}
	full["claude.plugin"]["polish@impeccable"] = Item{"id": "polish@impeccable", "version": "1.0.0"}
	full["claude.plugin"]["feature-dev@claude-plugins-official"] = Item{"id": "feature-dev@claude-plugins-official", "version": "1.0.0"}
	own1 := z.Own(own)
	nw, err := Update(own1, full, map[string]bool{}, false, withOwn, os.Stderr, "nix", "claude")
	if err != nil {
		t.Fatal(err)
	}
	keys := func(lk ByKind) string {
		var got []string
		for _, k := range tpKinds {
			for _, key := range sortedKeys(lk[k.name]) {
				got = append(got, k.name+":"+key)
			}
		}
		return strings.Join(got, " ")
	}
	const plugins = " claude.plugin:feature-dev@claude-plugins-official claude.plugin:mine@own claude.plugin:polish@impeccable"

	// The installed manifest is another zone's: no policy root lock.
	if taken, err := PolicyRootLock(m, filepath.Join(dir, "own")); err != nil || taken != nil {
		t.Fatalf("a manifest without this root as a source: %v %v", taken, err)
	}
	// Then update keeps what the own lock had: impeccable, with an old lock
	// that pinned it (the work root's astral-sh) ...
	old := emptyByKind()
	old["claude.marketplace"]["impeccable"] = full["claude.marketplace"]["impeccable"]
	if got, want := keys(OwnLock(own1, nw, old, own, nil)), "claude.marketplace:impeccable claude.marketplace:own"+plugins; got != want {
		t.Errorf("unknown policy lock, impeccable pinned before: %q, want %q", got, want)
	}
	// ... and not, with one that didn't.
	if got, want := keys(OwnLock(own1, nw, emptyByKind(), own, nil)), "claude.marketplace:own"+plugins; got != want {
		t.Errorf("unknown policy lock, nothing pinned before: %q, want %q", got, want)
	}

	// The installed manifest of this zone (its sources name this root's
	// workdir): the policy root's lock pins claude-plugins-official, not
	// impeccable, so impeccable stays here even if this lock never had it.
	mp := filepath.Join(dir, "manifest.json")
	b, err := os.ReadFile(mp)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.Replace(string(b), `"thirdParty":`, `"sources": [{"name": "work-own", "zone": "work", "workdir": "`+filepath.Join(dir, "own")+`"}], "thirdParty":`, 1))
	if err := os.WriteFile(mp, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if m, err = LoadManifest(mp); err != nil {
		t.Fatal(err)
	}
	taken, err := PolicyRootLock(m, filepath.Join(dir, "own"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := taken["claude.marketplace"]["claude-plugins-official"]; !ok {
		t.Fatalf("the policy root's lock: %v", taken)
	}
	if _, ok := taken["claude.plugin"]["mine@own"]; ok {
		t.Fatal("PolicyRootLock read the extending root's lock")
	}
	old["claude.marketplace"]["claude-plugins-official"] = full["claude.marketplace"]["claude-plugins-official"]
	if got, want := keys(OwnLock(own1, nw, emptyByKind(), own, taken)), "claude.marketplace:impeccable claude.marketplace:own"+plugins; got != want {
		t.Errorf("policy lock known: %q, want %q", got, want)
	}
	if got, want := keys(OwnLock(own1, nw, old, own, taken)), "claude.marketplace:impeccable claude.marketplace:own"+plugins; got != want {
		t.Errorf("policy lock known, both pinned before: %q, want %q", got, want)
	}
}
