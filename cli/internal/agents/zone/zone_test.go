package zone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRoot(t *testing.T) {
	r, err := ParseRoot("piste-agents=shared:/nix/store/x-agents")
	if err != nil || r.Name != "piste-agents" || r.Zone != "shared" || r.Dir != "/nix/store/x-agents" {
		t.Fatal(r, err)
	}
	if r, err := ParseRoot("x=agent:/d"); err != nil || r.Zone != "agent" {
		t.Fatal(r, err)
	}
	for _, bad := range []string{"x", "x=shared", "x=shared:rel/dir", "x=Bad:/d", "x=-x:/d", "x=a b:/d"} {
		if _, err := ParseRoot(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := []Root{{"n", Shared, "/a"}, {"w", Work, "/b"}}
	if err := Validate(Work, ok); err != nil {
		t.Fatal(err)
	}
	if err := Validate(Work, append(ok, Root{"p", Personal, "/c"})); err == nil {
		t.Fatal("personal root on a work machine accepted")
	}
	if err := Validate(Work, []Root{{"n", Shared, "/a"}, {"n", Shared, "/b"}}); err == nil {
		t.Fatal("duplicate accepted")
	}
	if err := Validate("", ok); err == nil {
		t.Fatal("no machine zone accepted")
	}
	agent := []Root{{"n", Shared, "/a"}, {"a", "agent", "/b"}}
	if err := Validate("agent", agent); err != nil {
		t.Fatal(err)
	}
	if err := Validate("agent", append(agent, Root{"w", Work, "/c"})); err == nil {
		t.Fatal("work root on an agent machine accepted")
	}
	for _, bad := range []string{Shared, "a:b"} {
		if err := Validate(bad, []Root{{"n", Shared, "/a"}}); err == nil {
			t.Errorf("machine zone %q accepted", bad)
		}
	}
}

func TestParseID(t *testing.T) {
	id, err := ParseID("agent:skill/x")
	if err != nil || id.Zone != "agent" || id.Kind != "skill" || id.Name != "x" {
		t.Fatal(id, err)
	}
	for _, bad := range []string{"skill/x", "Home:skill/x", "a:b:skill/x", "shared:skill/../x", "shared:Skill/x"} {
		if _, err := ParseID(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestInfer(t *testing.T) {
	sh := Root{"n", Shared, "/a"}
	ag := Root{"a", "agent", "/b"}
	for _, c := range []struct {
		roots []Root
		want  string
	}{
		{[]Root{sh}, Personal},
		{[]Root{sh, ag}, "agent"},
		{[]Root{ag, {"a2", "agent", "/c"}}, "agent"},
	} {
		if got, err := Infer(c.roots); err != nil || got != c.want {
			t.Errorf("Infer(%v) = %q, %v; want %q", c.roots, got, err, c.want)
		}
	}
	_, err := Infer([]Root{{"w", Work, "/c"}, sh, ag})
	if err == nil || !strings.Contains(err.Error(), "agent, work") {
		t.Fatalf("mixed zones: %v", err)
	}
}

// Shared items are valid everywhere; a work project refuses personal:*.
func TestAdmit(t *testing.T) {
	cases := []struct {
		project, id string
		ok          bool
	}{
		{Work, "shared:skill/nix-option", true},
		{Work, "work:mcp/jira", true},
		{Work, "personal:skill/notes-clip", false},
		{Personal, "work:mcp/jira", false},
		{Personal, "personal:3p-skill/example", true},
	}
	for _, c := range cases {
		id, err := ParseID(c.id)
		if err != nil {
			t.Fatal(err)
		}
		if got := Admit(c.project, id) == nil; got != c.ok {
			t.Errorf("Admit(%s, %s) = %v", c.project, c.id, got)
		}
	}
}

func TestEnvAllowed(t *testing.T) {
	both := func(k string) string {
		return map[string]string{"PISTE_AGENTS_HOME": "P", "OTHER_AGENTS_ROOTS": "R", "PISTE_CONFIG": "C"}[k]
	}
	if EnvAllowed(false, both, "AGENTS_HOME") != "" || EnvAllowed(false, both, "AGENTS_ROOTS") != "" {
		t.Fatal("env read without --allow-env")
	}
	if got := EnvAllowed(true, both, "AGENTS_HOME"); got != "P" {
		t.Fatalf("AGENTS_HOME = %q, want PISTE_ to win", got)
	}
	if got := EnvAllowed(true, both, "AGENTS_ROOTS"); got != "" {
		t.Fatalf("AGENTS_ROOTS = %q: only PISTE_ names are read", got)
	}
	if got := EnvAllowed(true, both, "CONFIG"); got != "" {
		t.Fatalf("CONFIG = %q: only AGENTS_* names are gated inputs", got)
	}
}

func TestSnapshot(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o644)
	s, err := Snapshot([]Root{{"n", Shared, d}})
	if err != nil || len(s) != 1 || s[0].SHA256 == "" {
		t.Fatal(s, err)
	}
	if _, err := Snapshot([]Root{{"n", Shared, filepath.Join(d, "missing")}}); err == nil {
		t.Fatal("missing root accepted")
	}
}
