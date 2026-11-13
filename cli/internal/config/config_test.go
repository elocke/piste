package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPath(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := Path(env(nil), "/h"); got != "/h/.config/piste/config.toml" {
		t.Error(got)
	}
	if got := Path(env(map[string]string{"XDG_CONFIG_HOME": "/x"}), "/h"); got != "/x/piste/config.toml" {
		t.Error(got)
	}
	if got := Path(env(map[string]string{"PISTE_CONFIG": "/c.toml", "XDG_CONFIG_HOME": "/x"}), "/h"); got != "/c.toml" {
		t.Error(got)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil || len(c.Remote.Hosts) != 0 {
		t.Fatal(c, err)
	}
}

func TestLoadHosts(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("[remote.hosts.devbox]\nssh = \"devbox\"\nflake = \"~/piste/overlays/mine\"\n\n[remote.hosts.b]\nssh = \"b.lan\"\n"), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.HostNames(); len(got) != 2 || got[0] != "b" || got[1] != "devbox" {
		t.Fatal(got)
	}
	if h := c.Remote.Hosts["devbox"]; h.Name != "devbox" || h.SSH != "devbox" || h.Flake != "~/piste/overlays/mine" {
		t.Fatalf("%+v", h)
	}
}

func TestLoadBadTOML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte("[remote\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("want error")
	}
}
