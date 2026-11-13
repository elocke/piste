package remotemove

// End to end, against real git: a throwaway "local" machine and "box" under
// one temp dir. A bare origin, a local clone with a sibling worktree on feat
// (one commit that exists only locally), a transcript that ends in that
// worktree, and a blocked job pointing at it. PATH shims stand in for ssh
// (runs the remote command here with HOME=box, for `bash -s` scripts and for
// git's git-receive-pack alike) and uvx (records the import, prints a
// session-migrate-style result).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elocke/piste/cli/internal/execx"
)

const sid = "11111111-2222-3333-4444-555555555555"

type world struct {
	t                       *testing.T
	root, local, box, calls string
	transcript, sha         string
}

// The shims get an absolute bash shebang: the Linux Nix build sandbox has no
// /usr/bin/env (macOS does).
const sshShim = `echo "ssh $*" >> "$CALLS"
while [[ $# -gt 0 && "$1" == -* ]]; do
  case "$1" in -o|-p|-i|-l|-F) shift ;; esac
  shift
done
shift # host
[[ "${1:-}" == "--" ]] && shift
cd "$BOX" || exit 1
HOME="$BOX" exec bash -c "$*"
`

const uvxShim = `echo "uvx $*" >> "$CALLS"
cp "$6" "$IMPORTED"
echo '{"session_id":"new-1111","warnings":[{"code":"w1","message":"m1"}],"dropped_events":{"thinking:signed":2}}'
`

func (w *world) git(dir string, args ...string) string {
	w.t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		w.t.Fatalf("git -C %s %v: %v\n%s", dir, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newWorld(t *testing.T) *world {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not on PATH")
	}
	root, err := filepath.EvalSymlinks(t.TempDir()) // macOS: /var is /private/var
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, root: root, local: filepath.Join(root, "local"), box: filepath.Join(root, "box"),
		calls: filepath.Join(root, "calls")}
	// Isolate git from the user's config (it signs commits).
	for k, v := range map[string]string{
		"GIT_CONFIG_GLOBAL": "/dev/null", "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
		"CALLS": w.calls, "BOX": w.box, "IMPORTED": filepath.Join(root, "imported.jsonl"),
	} {
		t.Setenv(k, v)
	}
	t.Setenv("GIT_SSH_COMMAND", "")
	os.Unsetenv("GIT_SSH_COMMAND") // t.Setenv restores it after the test
	bin := filepath.Join(root, "bin")
	write(t, filepath.Join(bin, "ssh"), "#!"+bash+"\n"+sshShim)
	write(t, filepath.Join(bin, "uvx"), "#!"+bash+"\n"+uvxShim)
	for _, n := range []string{"ssh", "uvx"} {
		if err := os.Chmod(filepath.Join(bin, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	write(t, w.calls, "")
	if err := os.MkdirAll(filepath.Join(w.local, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.box, 0o755); err != nil {
		t.Fatal(err)
	}

	origin := filepath.Join(root, "origin.git")
	w.git(root, "init", "--quiet", "--bare", "-b", "main", origin)
	repo, wt := filepath.Join(w.local, "git", "repo"), filepath.Join(w.local, "git", "repo.wt")
	w.git(root, "clone", "--quiet", origin, repo)
	w.git(repo, "commit", "--quiet", "--allow-empty", "-m", "base")
	w.git(repo, "push", "--quiet", "origin", "HEAD:main")
	w.git(repo, "worktree", "add", "--quiet", "-b", "feat", wt)
	w.git(wt, "commit", "--quiet", "--allow-empty", "-m", "local-only")
	w.sha = w.git(wt, "rev-parse", "HEAD")

	claude := filepath.Join(w.local, ".claude")
	w.transcript = filepath.Join(claude, "projects", "-local-git-repo", sid+".jsonl")
	write(t, w.transcript, `{"type":"user","cwd":"`+repo+`"}
not json
{"type":"assistant","cwd":"`+wt+`"}
{"type":"summary"}
`)
	write(t, filepath.Join(claude, "jobs", "abcd1234", "state.json"),
		`{"state":"blocked","name":"trial","sessionId":"old-id","resumeSessionId":"`+sid+`"}`)
	return w
}

// move runs a move of ids (or --all-blocked when ids is nil) and returns
// its output and summary.
func (w *world) move(o Options, ids ...string) (string, Summary) {
	w.t.Helper()
	var out strings.Builder
	o.Host, o.IDs, o.AllBlocked = "pb", ids, ids == nil
	o.ClaudeDir, o.Home = filepath.Join(w.local, ".claude"), w.local
	o.Runner, o.Out = execx.Exec{}, &out
	s, err := Run(context.Background(), o)
	if err != nil {
		w.t.Fatal(err)
	}
	return out.String(), s
}

func (w *world) callLog() string {
	b, _ := os.ReadFile(w.calls)
	return string(b)
}

func has(t *testing.T, out string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestE2EDryRunTouchesNothing(t *testing.T) {
	w := newWorld(t)
	out, s := w.move(Options{DryRun: true}, "abcd1234")
	if s != (Summary{1, 0}) {
		t.Fatalf("summary %+v\n%s", s, out)
	}
	has(t, out, "move   abcd1234 (trial): feat @ "+w.sha[:12]+" -> pb:~/git/repo.wt",
		"would: clone "+filepath.Join(w.root, "origin.git")+" at ~/git/repo",
		"would: push feat from here into pb:git/repo (as refs/remotes/piste-move/feat)",
		"would: check out feat at ~/git/repo.wt", "import it with session-migrate==0.11.0")
	if c := w.callLog(); c != "" || exists(filepath.Join(w.box, "git")) {
		t.Fatalf("dry run contacted the box: %q", c)
	}
}

func TestE2EMove(t *testing.T) {
	w := newWorld(t)
	out, s := w.move(Options{}, "abcd1234")
	if s != (Summary{1, 0}) {
		t.Fatalf("summary %+v\n%s\n%s", s, out, w.callLog())
	}
	boxRepo, boxWt := filepath.Join(w.box, "git", "repo"), filepath.Join(w.box, "git", "repo.wt")
	if got := w.git(boxRepo, "remote", "get-url", "origin"); got != filepath.Join(w.root, "origin.git") {
		t.Errorf("box origin = %s", got)
	}
	if got := w.git(boxWt, "symbolic-ref", "--short", "HEAD"); got != "feat" {
		t.Errorf("box worktree branch = %s", got)
	}
	if got := w.git(boxWt, "rev-parse", "HEAD"); got != w.sha {
		t.Errorf("box worktree HEAD = %s; want the local-only commit %s", got, w.sha)
	}
	calls := w.callLog()
	has(t, calls, "uvx --quiet --from session-migrate==0.11.0 session-migrate import "+
		filepath.Join(w.box, Stage, sid+".jsonl")+" --to claude --cwd "+boxWt)
	has(t, calls, "ssh -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new pb -- bash -s",
		"git-receive-pack 'git/repo'") // git push went to the box, home-relative
	want, _ := os.ReadFile(w.transcript)
	if got, _ := os.ReadFile(os.Getenv("IMPORTED")); string(got) != string(want) {
		t.Error("imported transcript differs from the local one")
	}
	if exists(filepath.Join(w.box, Stage, sid+".jsonl")) || exists(filepath.Join(w.box, Stage, sid+".jsonl.result.json.tmp")) {
		t.Error("staged copy left on the box")
	}
	if !exists(filepath.Join(w.box, Stage, sid+".jsonl.result.json")) {
		t.Error("no import marker")
	}
	has(t, out, "resume: ssh -t pb 'cd ~/git/repo.wt && claude --resume new-1111'",
		"warning: w1: m1", "dropped: thinking:signed x2", "then stop job abcd1234 here")
	if err := exec.Command("git", "-C", filepath.Join(w.root, "origin.git"),
		"show-ref", "--verify", "--quiet", "refs/heads/feat").Run(); err == nil {
		t.Error("origin received the branch")
	}

	// Re-run: reports the existing box session, imports nothing.
	out, s = w.move(Options{}, "abcd1234")
	if s != (Summary{1, 0}) {
		t.Fatalf("re-run summary %+v\n%s", s, out)
	}
	has(t, out, "already moved: ssh -t pb 'cd ~/git/repo.wt && claude --resume new-1111'")
	if n := strings.Count(w.callLog(), "uvx --quiet"); n != 1 {
		t.Errorf("uvx ran %d times; want 1", n)
	}
}

func TestE2ERefusesDirty(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.local, "git", "repo.wt", "scratch"), "")
	out, s := w.move(Options{}, "abcd1234")
	if s != (Summary{0, 1}) {
		t.Fatalf("summary %+v", s)
	}
	has(t, out, "skip   abcd1234 (trial): "+filepath.Join(w.local, "git", "repo.wt")+" has 1 uncommitted changes; commit them first")
	if c := w.callLog(); c != "" {
		t.Fatalf("contacted the box: %q", c)
	}
}

func TestE2ESecrets(t *testing.T) {
	w := newWorld(t)
	token := "ghp_" + strings.Repeat("a", 36)
	f, _ := os.OpenFile(w.transcript, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"type":"user","cwd":"` + filepath.Join(w.local, "git", "repo.wt") + `","t":"` + token + `"}` + "\n")
	f.Close()
	out, s := w.move(Options{}, "abcd1234")
	if s != (Summary{0, 1}) {
		t.Fatalf("summary %+v", s)
	}
	has(t, out, "transcript matches secret patterns (github-token x1)")
	if strings.Contains(out, "ghp_") {
		t.Fatal("printed the secret")
	}
	if c := w.callLog(); c != "" {
		t.Fatalf("contacted the box: %q", c)
	}
	out, s = w.move(Options{AllowSecrets: true}, "abcd1234")
	if s != (Summary{1, 0}) {
		t.Fatalf("--allow-secrets summary %+v\n%s", s, out)
	}
	has(t, out, "resume: ")
	if strings.Contains(out, "ghp_") {
		t.Fatal("printed the secret")
	}
}

func TestE2EAllBlockedSkipsExpired(t *testing.T) {
	w := newWorld(t)
	write(t, filepath.Join(w.local, ".claude", "jobs", "dead0000", "state.json"),
		`{"state":"blocked","name":"old","sessionId":"gone"}`)
	write(t, filepath.Join(w.local, ".claude", "jobs", "done0000", "state.json"), `{"state":"done","sessionId":"x"}`)
	out, s := w.move(Options{})
	if s != (Summary{1, 1}) {
		t.Fatalf("summary %+v\n%s", s, out)
	}
	has(t, out, "skip   dead0000 (old): no transcript for session gone (expired?)", "resume: ")
	if strings.Contains(out, "done0000") {
		t.Error("moved a job that is not blocked")
	}
}

func TestE2EDivergedBoxBranchNotOverwritten(t *testing.T) {
	w := newWorld(t)
	if _, s := w.move(Options{}, "abcd1234"); s != (Summary{1, 0}) {
		t.Fatalf("first move %+v", s)
	}
	boxWt := filepath.Join(w.box, "git", "repo.wt")
	w.git(boxWt, "commit", "--quiet", "--allow-empty", "-m", "box-only")
	boxSha := w.git(boxWt, "rev-parse", "HEAD")
	w.git(filepath.Join(w.local, "git", "repo.wt"), "commit", "--quiet", "--allow-empty", "-m", "local-later")
	if err := os.Remove(filepath.Join(w.box, Stage, sid+".jsonl.result.json")); err != nil {
		t.Fatal(err)
	}
	out, s := w.move(Options{}, "abcd1234")
	if s != (Summary{0, 1}) {
		t.Fatalf("summary %+v\n%s", s, out)
	}
	has(t, out, "FAIL   abcd1234 (trial): checking out feat on the box")
	if got := w.git(boxWt, "rev-parse", "HEAD"); got != boxSha {
		t.Errorf("box branch overwritten: %s; want %s", got, boxSha)
	}
}
