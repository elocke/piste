package outdated

import (
	"reflect"
	"testing"
)

const lock = `{
  "root": "root",
  "nodes": {
    "root": {"inputs": {
      "piste": "piste",
      "nixpkgs": ["piste", "nixpkgs"],
      "layer": "layer",
      "lib": "lib_2",
      "worktrunk": "worktrunk",
      "tap": "tap"
    }},
    "piste": {"locked": {"type": "github", "owner": "example", "repo": "piste", "rev": "aaa", "lastModified": 1600000000}},
    "layer": {
      "inputs": {"lib": "lib", "nixpkgs": "nixpkgs_2", "other": ["piste", "nixpkgs"]},
      "locked": {"type": "path", "path": "./layer", "lastModified": 1}
    },
    "lib": {"locked": {"type": "github", "owner": "example", "repo": "lib", "rev": "bbb", "lastModified": 1650000000}},
    "nixpkgs_2": {"locked": {"type": "github", "owner": "example", "repo": "nixpkgs", "rev": "ccc", "lastModified": 1660000000}},
    "lib_2": {"locked": {"type": "github", "owner": "example", "repo": "lib2", "rev": "ddd", "lastModified": 1670000000}},
    "worktrunk": {"locked": {"type": "github", "owner": "example", "repo": "worktrunk", "rev": "abc123", "lastModified": 1700000000}},
    "tap": {"flake": false, "locked": {"type": "git", "url": "https://example.com/tap.git", "rev": "def456", "lastModified": 5}}
  }
}`

func TestDirectInputs(t *testing.T) {
	got, err := DirectInputs([]byte(lock), func(n string) bool { return n == "piste" })
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Input{}
	for _, in := range got {
		by[in.Name] = in
	}
	if _, ok := by["nixpkgs"]; ok {
		t.Error("follows nixpkgs should be skipped")
	}
	if len(got) != 5 {
		t.Fatalf("want 5 inputs, got %d: %+v", len(got), got)
	}
	if by["piste"].Owner != OwnerEngine || by["worktrunk"].Owner != OwnerOverlay {
		t.Errorf("owners wrong: %+v", by)
	}
	if by["layer"].Path != "./layer" || by["layer"].Ref != "" {
		t.Errorf("layer: %+v", by["layer"])
	}
	if by["tap"].Flake {
		t.Error("tap should be Flake=false")
	}
	if !by["worktrunk"].Flake {
		t.Error("worktrunk should be a flake")
	}
	w := by["worktrunk"]
	if w.Modified != 1700000000 || w.Ref != "github:example/worktrunk/abc123" {
		t.Errorf("worktrunk: %+v", w)
	}
	if by["tap"].Ref != "git+https://example.com/tap.git?rev=def456" {
		t.Errorf("tap ref: %q", by["tap"].Ref)
	}
}

func TestUnfollowed(t *testing.T) {
	got, err := Unfollowed([]byte(lock), "layer")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "lib" || got[0].Modified != 1650000000 ||
		got[1].Name != "nixpkgs" || got[1].Modified != 1660000000 {
		t.Errorf("got %+v", got)
	}
	// a root input whose node has a different name
	got, err = Unfollowed([]byte(lock), "lib")
	if err != nil || len(got) != 0 {
		t.Errorf("lib: %+v %v", got, err)
	}
	if got, _ := Unfollowed([]byte(lock), "nixpkgs"); got != nil {
		t.Errorf("follows should give nil, got %+v", got)
	}
}

func TestUnfollowedResolvesNode(t *testing.T) {
	l := `{"nodes":{"root":{"inputs":{"lib":"lib_2"}},
	"lib":{"inputs":{"wrong":"x"}},
	"lib_2":{"inputs":{"dep":"x"}},
	"x":{"locked":{"type":"github","owner":"example","repo":"x","rev":"r","lastModified":7}}}}`
	got, err := Unfollowed([]byte(l), "lib")
	if err != nil || len(got) != 1 || got[0].Name != "dep" || got[0].Modified != 7 {
		t.Errorf("got %+v %v", got, err)
	}
}

// brew outdated --json=v2 names a formula by its full name (tap-qualified
// for a tap's) and a cask by its short token, with no full_name: a declared
// tap cask matches its token, and keeps the declared name.
func TestBrewOutdated(t *testing.T) {
	manifest := `{"brews":["jq","example/tap/tool"],"casks":["example-app","example/tap/other-app","absent-app"]}`
	out := `{"formulae":[
	 {"name":"undeclared","installed_versions":["1"],"current_version":"2"},
	 {"name":"example/tap/tool","installed_versions":["0.9","1.0"],"current_version":"1.1"},
	 {"name":"jq","installed_versions":["1.6"],"current_version":"1.7"}],
	 "casks":[
	 {"name":"example-app","installed_versions":["2.0"],"current_version":"2.1"},
	 {"name":"other-app","installed_versions":["3.0"],"current_version":"3.2"}]}`
	m, err := BrewOutdated([]byte(manifest), []byte(out))
	if err != nil {
		t.Fatal(err)
	}
	got := m.Items()
	want := []BrewItem{
		{"jq", "1.6", "1.7", false},
		{"example/tap/tool", "1.0", "1.1", false},
		{"example-app", "2.0", "2.1", true},
		{"example/tap/other-app", "3.0", "3.2", true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

// A full name wins over a short entry of the same last segment: a declared
// tap formula isn't core's of that name.
func TestBrewOutdatedFullNameFirst(t *testing.T) {
	out := `{"formulae":[
	 {"name":"tool","installed_versions":["5"],"current_version":"6"},
	 {"name":"example/tap/tool","installed_versions":["1.0"],"current_version":"1.1"}],"casks":[]}`
	m, err := BrewOutdated([]byte(`{"brews":["example/tap/tool"],"casks":[]}`), []byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := m.Items(), []BrewItem{{"example/tap/tool", "1.0", "1.1", false}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// A name declared by an alias or an old name isn't brew outdated's: brew
// info --json=v2 --installed pairs the leftovers. A formula's alias or old
// name (bare, or tap-qualified for a tap's), a cask's old token; an entry
// pairs once; the items keep the declared names, in manifest order.
func TestBrewAliases(t *testing.T) {
	manifest := `{"brews":["jq","python","python3","example/tap/old-tool","wget"],"casks":["old-example-app","other-app"]}`
	out := `{"formulae":[
	 {"name":"jq","installed_versions":["1.6"],"current_version":"1.7"},
	 {"name":"python@3.13","installed_versions":["3.13.0"],"current_version":"3.13.1"},
	 {"name":"example/tap/tool","installed_versions":["1.0"],"current_version":"1.1"},
	 {"name":"undeclared","installed_versions":["1"],"current_version":"2"}],
	 "casks":[
	 {"name":"example-app","installed_versions":["2.0"],"current_version":"2.1"},
	 {"name":"other-app","installed_versions":["3.0"],"current_version":"3.2"}]}`
	info := `{"formulae":[
	 {"name":"python@3.13","full_name":"python@3.13","tap":"homebrew/core","oldnames":[],"aliases":["python","python3"]},
	 {"name":"tool","full_name":"example/tap/tool","tap":"example/tap","oldnames":["old-tool"],"aliases":[]},
	 {"name":"wget","full_name":"wget","tap":"homebrew/core","oldnames":[],"aliases":[]}],
	 "casks":[
	 {"token":"example-app","full_token":"example-app","old_tokens":["old-example-app"],"tap":"homebrew/cask","name":["Example App"]},
	 {"token":"other-app","full_token":"other-app","old_tokens":[],"tap":"homebrew/cask","name":["Other App"]}]}`
	m, err := BrewOutdated([]byte(manifest), []byte(out))
	if err != nil {
		t.Fatal(err)
	}
	direct := []BrewItem{{"jq", "1.6", "1.7", false}, {"other-app", "3.0", "3.2", true}}
	if got := m.Items(); !reflect.DeepEqual(got, direct) {
		t.Errorf("direct: got %+v, want %+v", got, direct)
	}
	if !m.Left() {
		t.Fatal("Left() = false with declared names and entries left on both sides")
	}
	m, err = BrewAliases(m, []byte(info))
	if err != nil {
		t.Fatal(err)
	}
	want := []BrewItem{
		{"jq", "1.6", "1.7", false},
		{"python", "3.13.0", "3.13.1", false},
		{"example/tap/old-tool", "1.0", "1.1", false},
		{"old-example-app", "2.0", "2.1", true},
		{"other-app", "3.0", "3.2", true},
	}
	if got := m.Items(); !reflect.DeepEqual(got, want) {
		t.Errorf("aliases: got %+v, want %+v", got, want)
	}
	// python3 is python@3.13's alias too, but python took the entry: it
	// pairs once. wget and undeclared don't pair.

	// brew info's output isn't JSON: an error, and the match it was given is
	// the caller's to keep.
	if _, err := BrewAliases(m, []byte("not json")); err == nil {
		t.Error("BrewAliases took a non-JSON brew info")
	}
}

// Left is a declared name and an outdated entry of the same kind, both
// unpaired: only then can brew info pair more.
func TestBrewLeft(t *testing.T) {
	out := `{"formulae":[{"name":"undeclared","installed_versions":["1"],"current_version":"2"}],
	 "casks":[{"name":"example-app","installed_versions":["2.0"],"current_version":"2.1"}]}`
	for _, c := range []struct {
		manifest string
		want     bool
	}{
		{`{"brews":[],"casks":["example-app"]}`, false},
		{`{"brews":[],"casks":["example-app","other-app"]}`, false},
		{`{"brews":["wget"],"casks":["example-app"]}`, true},
		{`{"brews":[],"casks":["old-example-app"]}`, true},
	} {
		m, err := BrewOutdated([]byte(c.manifest), []byte(out))
		if err != nil {
			t.Fatal(err)
		}
		if m.Left() != c.want {
			t.Errorf("%s: Left() = %v, want %v", c.manifest, m.Left(), c.want)
		}
	}
}

func TestParsePnpmOutdated(t *testing.T) {
	got := ParsePnpmOutdated("tsx\t4.0.0\t4.19.0\n\nbroken line\n")
	want := []PnpmItem{{"tsx", "4.0.0", "4.19.0"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestFreshNames(t *testing.T) {
	got := FreshNames("{\n  fresh = [ \"gh\" \"python3Packages.foo\" ];\n}")
	if !reflect.DeepEqual(got, []string{"gh", "python3Packages.foo"}) {
		t.Errorf("got %v", got)
	}
	if FreshNames("# fresh = [ \"gh\" ];\n") != nil {
		t.Error("commented should be nil")
	}
	if FreshNames("{ a = 1; }") != nil {
		t.Error("absent should be nil")
	}
}
