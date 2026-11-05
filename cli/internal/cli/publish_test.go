package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// What the exported tree must hold to, beyond piste leaks: it is the engine's
// tracked files less the export-ignored paths.

// exportedFiles are the tracked regular files git archive would keep,
// relative to root: neither the file nor a directory above it is
// export-ignore (check-attr reports a directory's pattern on the directory
// only).
func exportedFiles(t *testing.T, root string) []string {
	t.Helper()
	ls, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("git ls-files: %v (not a git checkout)", err)
	}
	files := strings.Split(strings.TrimSuffix(string(ls), "\x00"), "\x00")
	var query bytes.Buffer
	seen := map[string]bool{}
	for _, f := range files {
		for p := f; p != "." && !seen[p]; p = filepath.Dir(p) {
			seen[p] = true
			query.WriteString(p + "\x00")
		}
	}
	cmd := exec.Command("git", "-C", root, "check-attr", "-z", "--stdin", "export-ignore")
	cmd.Stdin = &query
	attrs, err := cmd.Output()
	if err != nil {
		t.Fatalf("git check-attr: %v", err)
	}
	// -z: path NUL attribute NUL value NUL, per path.
	a := strings.Split(strings.TrimSuffix(string(attrs), "\x00"), "\x00")
	ignored := map[string]bool{}
	for i := 0; i+2 < len(a); i += 3 {
		ignored[a[i]] = a[i+2] == "set"
	}
	var out []string
	for _, f := range files {
		keep := true
		for p := f; p != "."; p = filepath.Dir(p) {
			keep = keep && !ignored[p]
		}
		if fi, err := os.Lstat(filepath.Join(root, f)); keep && err == nil && fi.Mode().IsRegular() {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		t.Fatal("no exported files")
	}
	return out
}

// A path into the planning documents, which the export drops: a stranger
// following it finds nothing.
var privatePathRE = regexp.MustCompile(`docs/(?:superpowers|plans)/[A-Za-z0-9]`)

func TestExportCitesNoPrivatePath(t *testing.T) {
	root, ok := repoFile(t, "")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	for _, rel := range exportedFiles(t, root) {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := privatePathRE.FindString(line); m != "" {
				t.Errorf("%s:%d cites a path the export drops (%s…): %s", rel, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}

var (
	usesRE = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s*(\S+)`)
	// pinnedRE: an action at a full commit, which a moved tag can't change.
	pinnedRE = regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	cronRE   = regexp.MustCompile(`^\s*-\s*cron:`)
	zoneRE   = regexp.MustCompile(`\b(?:Africa|America|Antarctica|Asia|Atlantic|Australia|Europe|Indian|Pacific)/[A-Z]`)
	clockRE  = regexp.MustCompile(`\b\d{1,2}:\d{2}\b`)
)

// The workflows: every action pinned to a commit (the release job holds
// contents: write), and a schedule's comment in UTC, the zone cron runs in,
// not the maintainer's.
func TestWorkflows(t *testing.T) {
	root, ok := repoFile(t, "")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	files, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no workflows: %v", err)
	}
	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		for i, line := range strings.Split(readRepo(t, root, rel), "\n") {
			if m := usesRE.FindStringSubmatch(line); m != nil && !strings.HasPrefix(m[1], "./") && !pinnedRE.MatchString(m[1]) {
				t.Errorf("%s:%d: %s isn't pinned to a commit (owner/repo@<sha> # vN)", rel, i+1, m[1])
			}
			if !cronRE.MatchString(line) {
				continue
			}
			if z := zoneRE.FindString(line); z != "" {
				t.Errorf("%s:%d: the schedule names a local time zone (%s…): give UTC", rel, i+1, z)
			}
			if _, comment, _ := strings.Cut(line, "#"); clockRE.MatchString(comment) && !strings.Contains(comment, "UTC") {
				t.Errorf("%s:%d: the schedule's time isn't in UTC: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
	}
}
