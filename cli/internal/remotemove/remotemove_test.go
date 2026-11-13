package remotemove

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLastCwd(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	write(t, p, `{"type":"user","cwd":"/a"}
not json
{"type":"assistant","cwd":"/b"}
{"type":"x","cwd":7}
["cwd","/c"]
{"type":"summary"}`) // no trailing newline
	if got, err := LastCwd(p); err != nil || got != "/b" {
		t.Fatalf("LastCwd = %q, %v; want /b", got, err)
	}
	write(t, p, "{}\n")
	if got, _ := LastCwd(p); got != "" {
		t.Fatalf("LastCwd = %q; want empty", got)
	}
}

func TestHomeRel(t *testing.T) {
	for _, c := range []struct {
		p, want string
		ok      bool
	}{
		{"/Users/e/git/x", "git/x", true},
		{"/Users/e", ".", true},
		{"/Users/e/git/platform-images.pr-triage", "git/platform-images.pr-triage", true},
		{"/Users/ex/git", "", false},
		{"/tmp/x", "", false},
		{"/Users", "", false},
		{"git/x", "", false},
	} {
		got, ok := HomeRel("/Users/e", c.p)
		if got != c.want || ok != c.ok {
			t.Errorf("HomeRel(%q) = %q, %v; want %q, %v", c.p, got, ok, c.want, c.ok)
		}
	}
}

func TestSecretHitsNamesAndCountsOnly(t *testing.T) {
	dir := t.TempDir()
	key := "AKIA" + strings.Repeat("Q", 16)
	gh := "ghp_" + strings.Repeat("a", 36)
	p := filepath.Join(dir, "t.jsonl")
	write(t, p, "a "+key+"\nb "+key+"\nclean\n{\"t\":\""+gh+"\"}\n")
	hits, err := SecretHits(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []Hit{{"github-token", 1}, {"aws-access-key", 2}}
	if !reflect.DeepEqual(hits, want) {
		t.Fatalf("hits = %v; want %v", hits, want)
	}
	for _, h := range hits {
		if s := h.String(); strings.Contains(s, key) || strings.Contains(s, gh) || strings.Contains(s, "AKIA") {
			t.Fatalf("hit %q leaks the match", s)
		}
	}
	write(t, p, "{\"cwd\":\"/x\"}\nsk-ant is fine alone\n")
	if hits, _ := SecretHits(p); len(hits) != 0 {
		t.Fatalf("clean transcript: %v", hits)
	}
}

func TestResolveJob(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "jobs", "abcd1234", "state.json"),
		`{"state":"blocked","name":"trial","sessionId":"old-id","resumeSessionId":"new-id"}`)
	write(t, filepath.Join(dir, "jobs", "plain0000", "state.json"), `{"state":"done","sessionId":"s1"}`)
	for id, want := range map[string]Job{
		"abcd1234":  {"abcd1234", "new-id", "trial"},
		"plain0000": {"plain0000", "s1", "-"},
		"11111111-2222-3333-4444-555555555555": {"11111111-2222-3333-4444-555555555555",
			"11111111-2222-3333-4444-555555555555", "-"},
	} {
		got, err := ResolveJob(dir, id)
		if err != nil || got != want {
			t.Errorf("ResolveJob(%s) = %+v, %v; want %+v", id, got, err, want)
		}
	}
	write(t, filepath.Join(dir, "jobs", "bad", "state.json"), "{")
	if _, err := ResolveJob(dir, "bad"); err == nil {
		t.Error("bad state.json: want error")
	}
}

func TestBlockedJobs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "jobs", "bbbb", "state.json"), `{"state":"blocked"}`)
	write(t, filepath.Join(dir, "jobs", "aaaa", "state.json"), `{"state":"blocked"}`)
	write(t, filepath.Join(dir, "jobs", "done", "state.json"), `{"state":"done"}`)
	write(t, filepath.Join(dir, "jobs", "junk", "state.json"), `nope`)
	got, err := BlockedJobs(dir)
	if err != nil || !reflect.DeepEqual(got, []string{"aaaa", "bbbb"}) {
		t.Fatalf("BlockedJobs = %v, %v", got, err)
	}
	if got, err := BlockedJobs(filepath.Join(dir, "missing")); err != nil || len(got) != 0 {
		t.Fatalf("missing dir: %v, %v", got, err)
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []struct {
		ids []string
		all bool
		ok  bool
	}{
		{nil, false, false},
		{[]string{"abcd1234"}, true, false},
		{nil, true, true},
		{[]string{"abcd1234", "11111111-2222-3333-4444-555555555555"}, false, true},
		{[]string{"../etc"}, false, false},
		{[]string{"a b"}, false, false},
		{[]string{"x*"}, false, false},
		{[]string{"-rf"}, false, false},
	} {
		if err := Validate(c.ids, c.all); (err == nil) != c.ok {
			t.Errorf("Validate(%v, %v) = %v; want ok=%v", c.ids, c.all, err, c.ok)
		}
	}
}

func TestClaudeDir(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if got := ClaudeDir(get, "/h"); got != "/h/.claude" {
		t.Error(got)
	}
	env["CLAUDE_CONFIG_DIR"] = "/c"
	if got := ClaudeDir(get, "/h"); got != "/c" {
		t.Error(got)
	}
}

func TestResumeCommand(t *testing.T) {
	if got, want := ResumeCommand("pb", "git/repo.wt", "new-1111"),
		`ssh -t pb 'cd ~/git/repo.wt && claude --resume new-1111'`; got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
	if got, want := ResumeCommand("pb", "my dir", "s"),
		`ssh -t pb 'cd ~/'\''my dir'\'' && claude --resume s'`; got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}

func TestScriptsQuoteValues(t *testing.T) {
	s := prepareScript("git/it's", "git@x:o/r.git")
	if !strings.Contains(s, `main="$HOME"/'git/it'\''s'`) || !strings.Contains(s, `origin='git@x:o/r.git'`) {
		t.Fatal(s)
	}
	if got := pushArgv("/l/top", "pb", "git/repo", "abc", "feat"); !reflect.DeepEqual(got[len(got)-2:],
		[]string{"pb:git/repo", "abc:refs/remotes/piste-move/feat"}) {
		t.Fatal(got)
	}
}
