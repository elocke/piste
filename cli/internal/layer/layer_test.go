package layer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	d := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(d, Marker), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestRead(t *testing.T) {
	d := write(t, "zone = \"work\"\n\n[secrets]\nEXAMPLE_TOKEN = \"example/token\"\n")
	m, err := Read(d)
	if err != nil {
		t.Fatal(err)
	}
	if m.Zone != "work" || m.Secrets["EXAMPLE_TOKEN"] != "example/token" || len(m.Secrets) != 1 {
		t.Fatalf("%+v", m)
	}
	for name, body := range map[string]string{
		"no zone":     "[secrets]\nA = \"a\"\n",
		"shared zone": "zone = \"shared\"\n",
		"bad zone":    "zone = \"Bad Zone\"\n",
		"bad toml":    "zone = \n",
		"no file":     "",
	} {
		if _, err := Read(write(t, body)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestIsLayer(t *testing.T) {
	if !IsLayer(write(t, "zone = \"work\"\n")) || IsLayer(write(t, "")) {
		t.Fatal("IsLayer is wrong")
	}
}

func TestRef(t *testing.T) {
	abs := t.TempDir()
	for in, want := range map[string]string{
		"git@github.com:acme/team.git":        "git+ssh://git@github.com/acme/team.git",
		"ssh://git@github.com/acme/team.git":  "git+ssh://git@github.com/acme/team.git",
		"https://github.com/acme/team":        "git+https://github.com/acme/team",
		"https://github.com/acme/team.git":    "git+https://github.com/acme/team.git",
		"file:///srv/team.git":                "git+file:///srv/team.git",
		abs:                                   "git+file://" + abs,
		"git+ssh://git@github.com/acme/t.git": "git+ssh://git@github.com/acme/t.git",
	} {
		got, err := Ref(in)
		if err != nil || got != want {
			t.Errorf("Ref(%q) = %q, %v; want %q", in, got, err, want)
		}
		if strings.HasPrefix(got, "github:") {
			t.Errorf("Ref(%q) is a github: ref", in)
		}
	}
	for _, bad := range []string{"", "team", "./team"} {
		if _, err := Ref(bad); err == nil {
			t.Errorf("Ref(%q): no error", bad)
		}
	}
}

func TestSkeleton(t *testing.T) {
	got := string(Skeleton(map[string]string{"A": "x/a", "B": "x/b", "C": "c"}))
	if want := "c: \"\"\nx:\n  a: \"\"\n  b: \"\"\n"; got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if got := string(Skeleton(nil)); got != "" {
		t.Fatalf("empty: %q", got)
	}
}
