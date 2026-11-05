package release

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elocke/piste/cli/internal/platform"
)

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

type cacheFile struct {
	Checked int64  `json:"checked"`
	Newest  string `json:"newest"`
}

func writeCache(t *testing.T, path string, checked time.Time, newest string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	b, _ := json.Marshal(cacheFile{checked.Unix(), newest})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCache(t *testing.T, path string) cacheFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c cacheFile
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("cache %s: %v", b, err)
	}
	return c
}

// tags is a Tags func that counts its calls.
func tags(calls *int, out []string, err error) func() ([]string, error) {
	return func() ([]string, error) { *calls++; return out, err }
}

const line020 = "piste v0.2.0 is out (you're on v0.1.0): piste update shows what changed"

func TestNotice(t *testing.T) {
	remote := []string{"v0.1.0", "v0.2.0", "v0.3.0-rc1"}

	t.Run("stale cache", func(t *testing.T) {
		f, calls := filepath.Join(t.TempDir(), "piste", "release.json"), 0
		writeCache(t, f, now.Add(-25*time.Hour), "v0.1.0")
		if got := Notice(Probe{Yours: "v0.1.0", CacheFile: f, Now: now, Tags: tags(&calls, remote, nil)}); got != line020 {
			t.Errorf("Notice = %q", got)
		}
		if c := readCache(t, f); calls != 1 || c.Newest != "v0.2.0" || c.Checked != now.Unix() {
			t.Errorf("calls %d, cache %+v", calls, c)
		}
	})
	t.Run("no cache yet", func(t *testing.T) {
		f, calls := filepath.Join(t.TempDir(), "piste", "release.json"), 0
		if got := Notice(Probe{Yours: "v0.1.0", CacheFile: f, Now: now, Tags: tags(&calls, remote, nil)}); got != line020 || calls != 1 {
			t.Errorf("Notice = %q, calls %d", got, calls)
		}
	})
	t.Run("fresh cache", func(t *testing.T) {
		f, calls := filepath.Join(t.TempDir(), "release.json"), 0
		writeCache(t, f, now.Add(-time.Hour), "v0.2.0")
		if got := Notice(Probe{Yours: "v0.1.0", CacheFile: f, Now: now, Tags: tags(&calls, nil, errors.New("offline"))}); got != line020 || calls != 0 {
			t.Errorf("Notice = %q, calls %d", got, calls)
		}
	})
	t.Run("fresh cache naming yours", func(t *testing.T) {
		f, calls := filepath.Join(t.TempDir(), "release.json"), 0
		writeCache(t, f, now.Add(-time.Hour), "v0.1.0")
		if got := Notice(Probe{Yours: "v0.1.0", CacheFile: f, Now: now, Tags: tags(&calls, remote, nil)}); got != "" || calls != 0 {
			t.Errorf("Notice = %q, calls %d", got, calls)
		}
	})
	t.Run("the check fails", func(t *testing.T) {
		f, calls := filepath.Join(t.TempDir(), "release.json"), 0
		writeCache(t, f, now.Add(-48*time.Hour), "v0.2.0")
		if got := Notice(Probe{Yours: "v0.1.0", CacheFile: f, Now: now, Tags: tags(&calls, nil, errors.New("offline"))}); got != "" {
			t.Errorf("Notice = %q", got)
		}
		// Recorded: being offline costs one wait a day.
		if c := readCache(t, f); c.Checked != now.Unix() || c.Newest != "" {
			t.Errorf("cache %+v", c)
		}
	})
	t.Run("nothing to compare with", func(t *testing.T) {
		f, calls := filepath.Join(t.TempDir(), "release.json"), 0
		if got := Notice(Probe{CacheFile: f, Now: now, Tags: tags(&calls, remote, nil)}); got != "" || calls != 0 {
			t.Errorf("Notice = %q, calls %d", got, calls)
		}
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("the cache was written: %v", err)
		}
	})
	t.Run("corrupt cache", func(t *testing.T) {
		f, calls := filepath.Join(t.TempDir(), "release.json"), 0
		os.WriteFile(f, []byte("{not json"), 0o644)
		if got := Notice(Probe{Yours: "v0.1.0", CacheFile: f, Now: now, Tags: tags(&calls, remote, nil)}); got != line020 || calls != 1 {
			t.Errorf("Notice = %q, calls %d", got, calls)
		}
	})
	t.Run("newest older than yours", func(t *testing.T) {
		f, calls := filepath.Join(t.TempDir(), "release.json"), 0
		if got := Notice(Probe{Yours: "v0.3.0", CacheFile: f, Now: now, Tags: tags(&calls, remote, nil)}); got != "" {
			t.Errorf("Notice = %q", got)
		}
	})
}

func TestInSwitchUnit(t *testing.T) {
	d := t.TempDir()
	for name, c := range map[string]struct {
		cgroup string
		want   bool
	}{
		"a detached switch": {"0::/user.slice/user-1000.slice/user@1000.service/app.slice/piste-switch-1761900000.service\n", true},
		"a terminal":        {"0::/user.slice/user-1000.slice/session-3.scope\n", false},
		"a runner service":  {"0::/user.slice/user-1000.slice/user@1000.service/app.slice/runner.service\n", false},
		"cgroup v1":         {"12:pids:/user.slice\n1:name=systemd:/user.slice/piste-switch-1.service\n", false},
	} {
		f := filepath.Join(d, name)
		os.WriteFile(f, []byte(c.cgroup), 0o644)
		if got := InSwitchUnit(f); got != c.want {
			t.Errorf("%s: InSwitchUnit = %v", name, got)
		}
	}
	if InSwitchUnit(filepath.Join(d, "missing")) {
		t.Error("a missing file (darwin) is in a unit")
	}
}

// checkEnv: a fresh check that found v0.2.0 in the cache under a temp HOME,
// CI cleared and no switch unit, so Check has a line to print unless
// something skips it; and a repo with v0.1.0 and v0.2.0, main on v0.2.0.
func checkEnv(t *testing.T) (repo string) {
	t.Helper()
	t.Setenv("CI", "")
	h := t.TempDir()
	t.Setenv("HOME", h)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(h, ".cache"))
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	writeCache(t, filepath.Join(dir, "piste", "release.json"), time.Now().Add(-time.Hour), "v0.2.0")
	old := platform.CgroupFile
	platform.CgroupFile = filepath.Join(h, "no-cgroup")
	t.Cleanup(func() { platform.CgroupFile = old })

	repo = filepath.Join(h, "piste")
	os.MkdirAll(repo, 0o755)
	git(t, repo, "init", "-q", "-b", "main")
	for _, v := range []string{"v0.1.0", "v0.2.0"} {
		os.WriteFile(filepath.Join(repo, "VERSION"), []byte(v), 0o644)
		git(t, repo, "add", "VERSION")
		git(t, repo, "commit", "-q", "-m", v)
		git(t, repo, "tag", v)
	}
	return repo
}

// overlayPinned is an overlay whose engine pin is tag.
func overlayPinned(t *testing.T, tag string) string {
	t.Helper()
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "flake.nix"), []byte(attrForm("github:example/piste/"+tag)), 0o644)
	return d
}

// Yours is the checkout's release when it's detached on one; on a branch it's
// the overlay's pin, even when the branch's tip is a release commit (main
// right after a release) — a branch checkout's pins still need the nudge.
func TestCheckYours(t *testing.T) {
	repo := checkEnv(t)
	if got := Check(repo, overlayPinned(t, "v0.1.0")); got != line020 {
		t.Errorf("main on the v0.2.0 commit, pin v0.1.0: Check = %q, want %q", got, line020)
	}
	git(t, repo, "-c", "advice.detachedHead=false", "checkout", "-q", "v0.1.0")
	if got := Check(repo, overlayPinned(t, "v0.2.0")); got != line020 {
		t.Errorf("detached on v0.1.0, pin v0.2.0: Check = %q, want %q", got, line020)
	}
}

// The skips: under CI, outside a checkout (no git run at all) and inside a
// piste-switch-* unit, where the outer piste prints the line instead.
func TestCheckSkips(t *testing.T) {
	repo := checkEnv(t)
	git(t, repo, "-c", "advice.detachedHead=false", "checkout", "-q", "v0.1.0")
	overlay := overlayPinned(t, "v0.1.0")
	if got := Check(repo, overlay); got != line020 {
		t.Fatalf("the control: Check = %q, want %q", got, line020)
	}

	t.Run("CI", func(t *testing.T) {
		t.Setenv("CI", "true")
		if got := Check(repo, overlay); got != "" {
			t.Errorf("Check = %q", got)
		}
	})
	t.Run("in a piste-switch unit", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "cgroup")
		os.WriteFile(f, []byte("0::/user.slice/user-1000.slice/user@1000.service/app.slice/piste-switch-1761900000.service\n"), 0o644)
		old := platform.CgroupFile
		platform.CgroupFile = f
		t.Cleanup(func() { platform.CgroupFile = old })
		if got := Check(repo, overlay); got != "" {
			t.Errorf("Check = %q", got)
		}
	})
	t.Run("outside a checkout", func(t *testing.T) {
		bin, calls := t.TempDir(), filepath.Join(t.TempDir(), "calls")
		os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$@\" >> "+calls+"\nexit 1\n"), 0o755)
		t.Setenv("PATH", bin)
		if got := Check("", overlay); got != "" {
			t.Errorf("Check = %q", got)
		}
		if b, err := os.ReadFile(calls); err == nil {
			t.Errorf("git ran:\n%s", b)
		}
	})
}
