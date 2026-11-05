package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/agents/zone"
)

// A zone's second root (an overlay's own agents/ beside its team layer's)
// with [policy] extends = true adds allow entries to the zone's policy; it
// may set nothing else, and two roots that each set a policy are refused
// (lib/agents.nix zonePolicy, userscope.NewZone).
func TestLoadZonePolicyExtends(t *testing.T) {
	root := func(name, toml string) zone.Root {
		d := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "agents.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
		return zone.Root{Name: name, Zone: "work", Dir: d}
	}
	layer := root("work-agents", "[policy]\nallow = [\"github:acme/a\"]\nunlisted = \"deny\"\n")
	own := root("work-own", "[policy]\nextends = true\nallow = [\"github:acme/b\"]\n")
	pol, err := LoadZonePolicy([]zone.Root{own, layer}, "work")
	if err != nil {
		t.Fatal(err)
	}
	if !pol.Deny() || !pol.Allowed("github:acme/a") || !pol.Allowed("github:acme/b") {
		t.Errorf("policy %+v: want the layer's deny, both allow lists", pol)
	}
	loose := root("work-loose", "[policy]\nextends = true\nunlisted = \"warn\"\n")
	if _, err := LoadZonePolicy([]zone.Root{layer, loose}, "work"); err == nil || !strings.Contains(err.Error(), "unlisted") {
		t.Errorf("an extending root setting unlisted: err %v", err)
	}
	second := root("work-second", "[policy]\nunlisted = \"warn\"\n")
	if _, err := LoadZonePolicy([]zone.Root{layer, second}, "work"); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("two policy roots: err %v", err)
	}
}
