package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// zoneHome lays out ~/src/{personal,work}/agents (the
// personal one declares [[mcp]], the work one does not) and the
// piste.agents user-scope manifest for zone z, and points $HOME and the
// cwd at it.
func zoneHome(t *testing.T, z string) string {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "h")
	personal := filepath.Join(home, "src", "personal", "agents")
	work := filepath.Join(home, "src", "work", "agents")
	for _, d := range []string{personal, work, filepath.Join(home, ".config", "piste-agents")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(personal, "agents.toml"), "[[mcp]]\nname = \"playwright\"\ntransport = \"stdio\"\ncommand = \"playwright-mcp\"\nharnesses = [\"codex\"]\n")
	write(filepath.Join(work, "agents.toml"), "[policy]\nunlisted = \"deny\"\n")
	own := personal
	if z == "work" {
		own = work
	}
	m, _ := json.Marshal(map[string]any{"zone": z, "sources": []map[string]string{
		{"name": "piste-agents", "zone": "shared", "workdir": filepath.Join(home, "piste", "agents")},
		{"name": z + "-agents", "zone": z, "workdir": own},
	}})
	write(filepath.Join(home, ".config", "piste-agents", "manifest.json"), string(m))
	t.Setenv("HOME", home)
	t.Chdir(tmp)
	return home
}

func TestFindManifestWorkHostNeverReadsPersonal(t *testing.T) {
	zoneHome(t, "work")
	a := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	_, err := a.findManifest()
	var nz errNoZoneMCP
	if !errors.As(err, &nz) || err.Error() != "no MCP servers declared for this zone (work)" {
		t.Fatalf("got %v, want the work no-op", err)
	}
}

func TestMCPWorkHostSyncAndCheckAreNoops(t *testing.T) {
	zoneHome(t, "work")
	t.Setenv("PATH", t.TempDir()) // no claude / codex: a no-op must not call them
	for _, args := range [][]string{{"sync"}, {"sync", "--prune"}, {"check"}} {
		var out, errb bytes.Buffer
		a := &App{Stdout: &out, Stderr: &errb}
		cmd := newMCP(a)
		cmd.SetArgs(args)
		cmd.SetOut(&out)
		cmd.SetErr(&errb)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("mcp %v: %v (stderr %q)", args, err, errb.String())
		}
		if !bytes.Contains(out.Bytes(), []byte("no MCP servers declared for this zone (work)")) {
			t.Fatalf("mcp %v: stdout %q", args, out.String())
		}
	}
}

func TestFindManifestPersonalHostUsesItsOverlay(t *testing.T) {
	home := zoneHome(t, "personal")
	a := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	got, err := a.findManifest()
	want := filepath.Join(home, "src", "personal", "agents", "agents.toml")
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

func TestFindManifestNoManifestOnWorkCheckoutMachine(t *testing.T) {
	home := zoneHome(t, "work")
	os.Remove(filepath.Join(home, ".config", "piste-agents", "manifest.json"))
	a := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	if _, err := a.findManifest(); err == nil {
		t.Fatal("found a manifest; the personal overlay must not be read on a work checkout machine")
	}
}
