package cli

import (
	"bytes"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
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

// A GitHub Actions workflow, as much of it as the pages tests read.
type ghWorkflow struct {
	On   map[string]any   `yaml:"on"`
	Jobs map[string]ghJob `yaml:"jobs"`
}

type ghJob struct {
	If          string            `yaml:"if"`
	Needs       any               `yaml:"needs"`
	Outputs     map[string]string `yaml:"outputs"`
	Permissions map[string]string `yaml:"permissions"`
	Environment any               `yaml:"environment"`
	Steps       []ghStep          `yaml:"steps"`
}

type ghStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
	Env  map[string]any `yaml:"env"`
}

func readWorkflow(t *testing.T, root, name string) ghWorkflow {
	t.Helper()
	var w ghWorkflow
	if err := yaml.Unmarshal([]byte(readRepo(t, root, filepath.Join(".github", "workflows", name))), &w); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return w
}

// checkPagesJob: job j of workflow wf deploys the site built at ref (an
// expression), and only from a public repo: Pages' permissions and
// environment, the checkout and the build at ref, and the Pages actions
// pinned to a commit, as the repo's other actions are.
func checkPagesJob(t *testing.T, wf string, j ghJob, ref string) {
	t.Helper()
	if !strings.Contains(j.If, "!github.event.repository.private") {
		t.Errorf("%s: the pages job runs on a private repo too (if: %q)", wf, j.If)
	}
	if j.Permissions["pages"] != "write" || j.Permissions["id-token"] != "write" {
		t.Errorf("%s: the pages job's permissions are %v, want pages and id-token write", wf, j.Permissions)
	}
	if env, _ := j.Environment.(map[string]any); env["name"] != "github-pages" {
		t.Errorf("%s: the pages job's environment is %v, want github-pages", wf, j.Environment)
	}
	var checkout, build, upload, deploy bool
	for _, s := range j.Steps {
		action, _, _ := strings.Cut(s.Uses, "@")
		switch {
		case action == "actions/checkout":
			checkout = s.With["ref"] == ref
		case strings.Contains(s.Run, "docs-site-for"):
			// The ref reaches the build through the step's env, never
			// pasted into the script, and the source built is the commit
			// checked out at it (not, say, github:elocke/piste's main).
			for k, v := range s.Env {
				if v == ref && strings.Contains(s.Run, `docs-site-for \"$`+k+`\"`) &&
					strings.Contains(s.Run, `builtins.getFlake \"git+file://$PWD?rev=$(git rev-parse HEAD)&shallow=1\")`) {
					build = true
				}
			}
		case action == "actions/upload-pages-artifact":
			upload = pinnedRE.MatchString(s.Uses)
		case action == "actions/deploy-pages":
			deploy = pinnedRE.MatchString(s.Uses)
		}
	}
	if !checkout || !build {
		t.Errorf("%s: the pages job checks out %v and builds docs-site-for %v at %s, want both", wf, checkout, build, ref)
	}
	if !upload || !deploy {
		t.Errorf("%s: upload-pages-artifact (%v) and deploy-pages (%v) aren't both there, pinned to a commit", wf, upload, deploy)
	}
}

// The site built for a release links GitHub at the release's tag, not
// main: piste-release.yml's pages job runs once the release job made the
// tag (its version output, set only then), never on a private repo, and
// builds docs-site-for at that tag. docs.yml, the manual redeploy, builds
// the ref it is given (main unless told), with the same guard.
func TestReleasePagesJob(t *testing.T) {
	root, ok := repoFile(t, "")
	if !ok {
		t.Skip("no docs/ here (the Nix build's source is cli/ only)")
	}
	w := readWorkflow(t, root, "piste-release.yml")
	if got := w.Jobs["release"].Outputs["version"]; got != "${{ steps.pending.outputs.version }}" {
		t.Errorf("piste-release.yml: the release job's version output is %q", got)
	}
	pages, ok := w.Jobs["pages"]
	if !ok {
		t.Fatal("piste-release.yml has no pages job")
	}
	if pages.Needs != "release" {
		t.Errorf("piste-release.yml: the pages job needs %v, want release", pages.Needs)
	}
	if !strings.Contains(pages.If, "needs.release.outputs.version != ''") {
		t.Errorf("piste-release.yml: the pages job runs without a release (if: %q)", pages.If)
	}
	checkPagesJob(t, "piste-release.yml", pages, "${{ needs.release.outputs.version }}")

	d := readWorkflow(t, root, "docs.yml")
	in, _ := d.On["workflow_dispatch"].(map[string]any)
	inputs, _ := in["inputs"].(map[string]any)
	ref, _ := inputs["ref"].(map[string]any)
	if len(d.On) != 1 || ref["default"] != "main" {
		t.Errorf("docs.yml: on %v, want workflow_dispatch only, with a ref input (default main)", d.On)
	}
	if len(d.Jobs) != 1 {
		t.Fatalf("docs.yml: jobs %v, want one", slices.Collect(maps.Keys(d.Jobs)))
	}
	for _, j := range d.Jobs {
		checkPagesJob(t, "docs.yml", j, "${{ inputs.ref }}")
	}
}
