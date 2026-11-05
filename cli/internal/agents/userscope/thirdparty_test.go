package userscope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestRawGitHubSource(t *testing.T) {
	git, ref, path, err := rawGitHubSource("url:https://raw.githubusercontent.com/acme/cfg/main/plugins/.claude-plugin/marketplace.json")
	if err != nil || git != "https://github.com/acme/cfg.git" || ref != "main" || path != "plugins/.claude-plugin/marketplace.json" {
		t.Fatalf("got %q %q %q %v", git, ref, path, err)
	}
	for _, bad := range []string{"url:https://example.com/m.json", "url:https://raw.githubusercontent.com/acme/cfg/main", "github:acme/cfg"} {
		if _, _, _, err := rawGitHubSource(bad); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}

// The canonical hash is stable byte for byte (keys sorted, compact, UTF-8
// and <>& as is): a change here invalidates every locked url marketplace.
func TestJSONSHA256Stable(t *testing.T) {
	for doc, want := range map[string]string{
		`{"name": "malos", "plugins": [{"name": "lsp", "source": "./lsp"}]}`:                "a37cbe6087c35e7890e687dbea565323642d5d67abb9e08b9608d11fed4fec6e",
		"{\n \"z\": [1, 2.5, -3, true, null],\n \"a\": {\"é\": \"<b>&</b>\", \"n\": 10}\n}": "c74ab47b1ba49d94a4c2717014d83f845f21de51352f76a74ffd4c59741c2128",
	} {
		if got, err := JSONSHA256([]byte(doc)); err != nil || got != want {
			t.Errorf("%q: got %s %v, want %s", doc, got, err, want)
		}
	}
	if _, err := JSONSHA256([]byte("not json")); err == nil {
		t.Error("want an error for invalid JSON")
	}
}

// Current Claude stores a url marketplace's file at installLocation itself;
// older installs keep it in installLocation/.claude-plugin/.
func TestMarketplaceJSONSHA256BothLayouts(t *testing.T) {
	dir := t.TempDir()
	doc := `{"name": "malos", "plugins": [{"name": "lsp", "source": "./lsp"}]}`
	file := filepath.Join(dir, "malos")
	if err := os.WriteFile(file, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old")
	if err := os.MkdirAll(filepath.Join(old, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	pretty := "{\n  \"plugins\": [\n    {\"source\": \"./lsp\", \"name\": \"lsp\"}\n  ],\n  \"name\": \"malos\"\n}\n"
	if err := os.WriteFile(filepath.Join(old, ".claude-plugin", "marketplace.json"), []byte(pretty), 0o644); err != nil {
		t.Fatal(err)
	}
	a, okA := marketplaceJSONSHA256(file)
	b, okB := marketplaceJSONSHA256(old)
	if !okA || !okB || a != b || a != "a37cbe6087c35e7890e687dbea565323642d5d67abb9e08b9608d11fed4fec6e" {
		t.Fatalf("file %s %v, dir %s %v", a, okA, b, okB)
	}
	if _, ok := marketplaceJSONSHA256(filepath.Join(dir, "nope")); ok {
		t.Error("missing install: want !ok")
	}
}

func TestFloatsAndWantEnabled(t *testing.T) {
	cases := []struct {
		it   Item
		want bool
	}{
		{Item{"source": "github:o/x"}, false},
		{Item{"source": "github:o/x", "pin": "source"}, true},
		{Item{"source": "url:https://raw.githubusercontent.com/o/x/main/m.json"}, true},
	}
	for _, c := range cases {
		if floats(c.it) != c.want {
			t.Errorf("floats(%v) != %v", c.it, c.want)
		}
	}
	if !wantEnabled(Item{"id": "a@b"}) || !wantEnabled(Item{"enabled": true}) || wantEnabled(Item{"enabled": false}) {
		t.Error("wantEnabled: enabled unless declared enabled = false")
	}
}

// A shared-catalog marketplace (agents.base.toml) is the zone's only when a
// plugin names it; a zone's own declaration always is (lib/agents.nix
// usedMarketplaces, the same rule).
func TestNewZoneDropsUnusedCatalogMarketplaces(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "agents.base.toml")
	zoneF := filepath.Join(dir, "agents.toml")
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(base, `
[[claude.marketplace]]
name   = "official"
source = "github:anthropics/official"
pin    = "source"

[[claude.marketplace]]
name   = "catalog"
source = "github:elsewhere/catalog"

[[claude.plugin]]
id = "lsp@official"
`)
	markets := func(zoneTOML string) []string {
		t.Helper()
		write(zoneF, zoneTOML)
		z, err := NewZone([]string{base, zoneF}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(z.Order["claude.marketplace"]) != len(z.Decls["claude.marketplace"]) {
			t.Fatalf("Order %v out of step with Decls", z.Order["claude.marketplace"])
		}
		return z.Order["claude.marketplace"]
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("marketplaces %v, want %v", got, want)
		}
	}
	// Unused catalog entry dropped; the one a base plugin names is kept.
	eq(markets("[policy]\nunlisted = \"deny\"\n"), "official")
	// A zone plugin opts in.
	eq(markets("[[claude.plugin]]\nid = \"thing@catalog\"\n"), "official", "catalog")
	// A zone-declared marketplace counts with no plugin.
	eq(markets("[[claude.marketplace]]\nname = \"own\"\nsource = \"github:acme/own\"\n"), "official", "own")
}

// Lock values are TOML: strings keep their UTF-8, escape only what TOML
// requires; floats always read back as floats.
func TestTOMLValues(t *testing.T) {
	for in, want := range map[any]string{
		"café 😀":            `"café 😀"`,
		"a\"b\\c\n\x01\x7f": `"a\"b\\c\n\u0001\u007F"`,
		1.0:                 "1.0",
		2.5:                 "2.5",
		1e-05:               "1e-05",
		int64(3):            "3",
		true:                "true",
	} {
		if got := tomlValue(in); got != want {
			t.Errorf("tomlValue(%#v) = %s, want %s", in, got, want)
		}
	}
	var v map[string]any
	if err := toml.Unmarshal([]byte("x = "+tomlValue("é\x7f\"")+"\ny = "+tomlValue(1.0)), &v); err != nil || v["x"] != "é\x7f\"" || v["y"] != 1.0 {
		t.Fatalf("round trip: %v %#v", err, v)
	}
}

// Values in messages read as TOML and JSON do: no None, True or False.
func TestValueText(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{{nil, "none"}, {true, "true"}, {false, "false"}, {int64(3), "3"}, {"x", "x"}, {[]any{"a", true}, `["a",true]`}} {
		if got := pstr(c.v); got != c.want {
			t.Errorf("pstr(%#v) = %q, want %q", c.v, got, c.want)
		}
	}
}

// A managed hook whose version file is missing: sync's note says so in
// words.
func TestSyncNoteHookNotInstalled(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "agents.toml")
	if err := os.WriteFile(f, []byte(`
[[hook]]
name        = "hindsight"
mode        = "managed"
installer   = "npm:@example/hindsight"
version     = "0.7.0"
versionFile = "~/.hindsight/package.json"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	z, err := NewZone([]string{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	lk := emptyByKind()
	lk["hook"]["hindsight"] = Item{"name": "hindsight", "installer": "npm:@example/hindsight", "version": "0.7.0"}
	_, _, notes, err := PlanSync(z, lk, fakeObs{}, false, false, "laptop", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := "hook hindsight: not installed, lock 0.7.0; run its installer (npm:@example/hindsight@0.7.0) by hand"
	if len(notes) != 1 || notes[0] != want {
		t.Errorf("notes %q, want %q", notes, want)
	}
}
