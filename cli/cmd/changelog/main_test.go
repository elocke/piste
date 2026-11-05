package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

const header = "# Changelog\n\nWhat each release changed.\n\n"

// repo is a git repo holding CHANGELOG.md, with the cwd in a subdirectory of
// it (the tool finds the file at the top, as from cli/).
func repo(t *testing.T, changelog string) string {
	t.Helper()
	d := t.TempDir()
	gitT(t, d, "init", "-q")
	if err := os.WriteFile(filepath.Join(d, "CHANGELOG.md"), []byte(changelog), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, d, "add", "CHANGELOG.md")
	gitT(t, d, "commit", "-q", "-m", "init")
	os.Mkdir(filepath.Join(d, "cli"), 0o755)
	t.Chdir(filepath.Join(d, "cli"))
	return d
}

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func runIt(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	rc := run(args, &o, &e, now)
	return rc, o.String(), e.String()
}

func read(t *testing.T, d string) string {
	b, err := os.ReadFile(filepath.Join(d, "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCutMinor(t *testing.T) {
	d := repo(t, header+"## Unreleased\n- A.\n")
	rc, out, errs := runIt("cut", "minor")
	if rc != 0 || out != "v0.1.0\n" {
		t.Fatalf("cut minor: rc %d, out %q, err %q", rc, out, errs)
	}
	if got, want := read(t, d), header+"## Unreleased\n\n## v0.1.0 (2026-11-02)\n- A.\n"; got != want {
		t.Errorf("CHANGELOG.md:\n%s\nwant\n%s", got, want)
	}
}

func TestCutPatchNote(t *testing.T) {
	d := repo(t, header+"## Unreleased\n\n## v0.1.0 (2026-11-01)\n- A.\n")
	rc, out, _ := runIt("cut", "patch", "--note", "Inputs updated (flake.lock).")
	if rc != 0 || out != "v0.1.1\n" {
		t.Fatalf("rc %d, out %q", rc, out)
	}
	if !strings.Contains(read(t, d), "## v0.1.1 (2026-11-02)\n- Inputs updated (flake.lock).\n\n## v0.1.0") {
		t.Errorf("CHANGELOG.md:\n%s", read(t, d))
	}
}

func TestCutRefusedExits3(t *testing.T) {
	text := header + "## Unreleased\n- A.\n### Upgrading\n- Break.\n\n## v0.1.0 (2026-11-01)\n- First.\n"
	d := repo(t, text)
	rc, out, errs := runIt("cut", "patch", "--note", "Inputs updated (flake.lock).")
	if rc != 3 || out != "" || !strings.Contains(errs, "Upgrading") {
		t.Errorf("rc %d, out %q, err %q; want 3 and the reason", rc, out, errs)
	}
	if read(t, d) != text {
		t.Error("a refused cut changed CHANGELOG.md")
	}
}

func TestPendingAndNotes(t *testing.T) {
	d := repo(t, header+"## Unreleased\n\n## v0.1.0 (2026-11-01)\n- A.\n")
	if rc, out, _ := runIt("pending"); rc != 0 || out != "v0.1.0\n" {
		t.Errorf("untagged: rc %d, out %q", rc, out)
	}
	gitT(t, d, "tag", "v0.1.0")
	if rc, out, _ := runIt("pending"); rc != 0 || out != "" {
		t.Errorf("tagged: rc %d, out %q", rc, out)
	}
	if rc, out, _ := runIt("notes", "v0.1.0"); rc != 0 || out != "- A.\n" {
		t.Errorf("notes: rc %d, out %q", rc, out)
	}
	if rc, _, errs := runIt("notes", "v9.9.9"); rc != 1 || errs == "" {
		t.Errorf("notes of a missing release: rc %d", rc)
	}
}

// commit commits CHANGELOG.md as text in d and returns the commit.
func commit(t *testing.T, d, text, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(d, "CHANGELOG.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, d, "commit", "-q", "-am", msg)
	out, err := exec.Command("git", "-C", d, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// The release's tag goes on the commit that cut it, not on main's tip: a
// release retried after later merges (or after a feature merged under a bump
// PR) must not take them in.
func TestTarget(t *testing.T) {
	v010 := header + "## Unreleased\n\n## v0.1.0 (2026-11-01)\n- A.\n"
	d := repo(t, v010)
	gitT(t, d, "tag", "v0.1.0")
	v011 := header + "## Unreleased\n\n## v0.1.1 (2026-11-02)\n- Inputs updated (flake.lock).\n\n## v0.1.0 (2026-11-01)\n- A.\n"
	cut := commit(t, d, v011, "chore(release): v0.1.1")
	// Later merges: a feature with an Upgrading note, and a fix to the
	// release's notes (an edit to its section, not its cut).
	commit(t, d, strings.Replace(v011, "## Unreleased\n", "## Unreleased\n- Feature B.\n### Upgrading\n- Rename x to y.\n", 1), "feat: B")
	commit(t, d, strings.Replace(read(t, d), "- Inputs updated (flake.lock).", "- Inputs updated (flake.lock), nixpkgs too.", 1), "docs: notes")
	if rc, out, errs := runIt("target", "v0.1.1"); rc != 0 || out != cut+"\n" {
		t.Errorf("target v0.1.1: rc %d, out %q, err %q; want %s", rc, out, errs, cut)
	}
	if rc, out, errs := runIt("target", "v0.2.0"); rc != 1 || out != "" || errs == "" {
		t.Errorf("a release CHANGELOG.md doesn't have: rc %d, out %q, err %q; want 1", rc, out, errs)
	}
}

// A bump PR cut from an older main and merged after a feature: its squash
// commit adds the patch's heading and the feature's Unreleased lines (with an
// Upgrading note) at once, with no conflict. Releasing it would ship the
// feature as that patch, so target refuses (exit 3), however the PR merged.
func TestTargetRefusesACutThatCarriesMore(t *testing.T) {
	d := repo(t, header+"## Unreleased\n\n## v0.1.0 (2026-11-01)\n- A.\n")
	gitT(t, d, "tag", "v0.1.0")
	commit(t, d, header+"## Unreleased\n- Feature B.\n### Upgrading\n- Rename x to y.\n\n"+
		"## v0.1.1 (2026-11-02)\n- Inputs updated (flake.lock).\n\n## v0.1.0 (2026-11-01)\n- A.\n", "chore(deps): update flake.lock (#9)")
	rc, out, errs := runIt("target", "v0.1.1")
	if rc != 3 || out != "" || !strings.Contains(errs, "Unreleased") || !strings.Contains(errs, "cut minor") {
		t.Errorf("rc %d, out %q, err %q; want 3, the reason and the fix", rc, out, errs)
	}
}

func TestUsage(t *testing.T) {
	repo(t, header+"## Unreleased\n- A.\n")
	for _, args := range [][]string{nil, {"cut"}, {"cut", "huge"}, {"notes"}, {"pending", "x"}, {"target"}, {"what"}} {
		if rc, _, errs := runIt(args...); rc != 2 || errs == "" {
			t.Errorf("%v: rc %d, want 2 with usage", args, rc)
		}
	}
}
