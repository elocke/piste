// Package remotemove moves blocked Claude Code background jobs from this
// machine to a remote dev box over ssh, so their conversations can be
// resumed there (`piste remote move`).
//
// Per job: find its transcript and the directory it was last working in. On
// the box, clone that repo from its origin at the same home-relative path,
// push the branch straight from here into the clone (commits that only exist
// here come along; origin is never pushed to), check it out in the same
// worktree layout, then rewrite the transcript into a new box session with
// session-migrate (version pinned) and print the resume command.
//
// Refuses a job whose checkout has uncommitted changes or a detached HEAD,
// and one whose transcript matches a secret pattern unless AllowSecrets:
// whoever has root on the box can read it. The pattern list is a guard, not
// a guarantee. Not moved: subagent transcripts, the job's tmp dir, signed
// thinking, and the job itself (stop it here once moved). Safe to re-run: a
// session already imported on the box is reported, not imported again.
// DryRun never contacts the box.
package remotemove

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/safety"
	"github.com/elocke/piste/cli/internal/execx"
)

// SessionMigrate is the pinned session-migrate run on the box through uvx.
const SessionMigrate = "session-migrate==0.11.0"

// Stage is the box's transcript staging dir, relative to its $HOME. A
// <session>.jsonl.result.json there marks a session as already moved.
const Stage = ".cache/piste-remote-move"

// PushRef is where the local branch lands in the box clone; the box branch
// only ever fast-forwards to it.
const PushRef = "refs/remotes/piste-move/"

// SSHOptions match `piste remote status|drift|exec`.
var SSHOptions = []string{"-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=accept-new"}

// Options is one `piste remote move` run.
type Options struct {
	Host         string   // ssh destination (already resolved from [remote.hosts])
	IDs          []string // job or session ids; empty with AllBlocked
	AllBlocked   bool
	DryRun       bool
	AllowSecrets bool
	ClaudeDir    string // $CLAUDE_CONFIG_DIR or ~/.claude
	Home         string // the local $HOME; every moved path must be under it
	Runner       execx.Runner
	// Say prints one line; kind is "info", "warn" (a skip) or "err" (a
	// failure). nil prints to Out.
	Say func(kind, msg string)
	Out io.Writer
}

// Summary counts jobs moved (or already moved) and not moved.
type Summary struct{ Moved, NotMoved int }

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidID reports a job or session id safe to use in paths and remote
// scripts (no separators, no glob or shell characters).
func ValidID(id string) bool { return idRe.MatchString(id) }

// Validate checks the arguments: exactly one of ids or allBlocked, and every
// id well-formed. It runs before anything contacts the box.
func Validate(ids []string, allBlocked bool) error {
	switch {
	case allBlocked && len(ids) > 0:
		return errors.New("give job/session ids or --all-blocked, not both")
	case !allBlocked && len(ids) == 0:
		return errors.New("give job/session ids or --all-blocked")
	}
	for _, id := range ids {
		if !ValidID(id) {
			return fmt.Errorf("%s is not a job or session id", safety.Quote(id))
		}
	}
	return nil
}

// ClaudeDir is $CLAUDE_CONFIG_DIR, else ~/.claude.
func ClaudeDir(getenv func(string) string, home string) string {
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude")
}

// Job is a background job (or a bare session) to move.
type Job struct {
	ID      string // job id, or the session id for a bare session
	Session string // the job's latest session
	Name    string // "-" when unknown
}

type jobState struct {
	State           string `json:"state"`
	Name            string `json:"name"`
	SessionID       string `json:"sessionId"`
	ResumeSessionID string `json:"resumeSessionId"`
}

// ResolveJob reads jobs/<id>/state.json; a resumed job's latest session is
// resumeSessionId. An id with no job dir is taken as a session id.
func ResolveJob(claudeDir, id string) (Job, error) {
	b, err := os.ReadFile(filepath.Join(claudeDir, "jobs", id, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Job{ID: id, Session: id, Name: "-"}, nil
	}
	if err != nil {
		return Job{}, err
	}
	var s jobState
	if err := json.Unmarshal(b, &s); err != nil {
		return Job{}, fmt.Errorf("jobs/%s/state.json: %w", id, err)
	}
	j := Job{ID: id, Session: s.ResumeSessionID, Name: s.Name}
	if j.Session == "" {
		j.Session = s.SessionID
	}
	if j.Name == "" {
		j.Name = "-"
	}
	return j, nil
}

// BlockedJobs lists the ids of jobs waiting on the user, sorted.
func BlockedJobs(claudeDir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(claudeDir, "jobs", "*", "state.json"))
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s jobState
		if json.Unmarshal(b, &s) == nil && s.State == "blocked" {
			ids = append(ids, filepath.Base(filepath.Dir(f)))
		}
	}
	return ids, nil
}

// FindTranscript is projects/*/<session>.jsonl, or "" (expired).
func FindTranscript(claudeDir, session string) string {
	if !ValidID(session) {
		return ""
	}
	m, _ := filepath.Glob(filepath.Join(claudeDir, "projects", "*", session+".jsonl"))
	for _, p := range m {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// eachLine calls f with every line of path (any length).
func eachLine(path string, f func([]byte)) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	r := bufio.NewReader(fh)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			f(bytes.TrimRight(line, "\r\n"))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// LastCwd is the last string "cwd" of a JSON-object line in the transcript:
// the directory the session was last working in. Other lines are ignored.
func LastCwd(transcript string) (string, error) {
	var last string
	err := eachLine(transcript, func(line []byte) {
		var obj map[string]json.RawMessage
		if json.Unmarshal(line, &obj) != nil {
			return
		}
		var cwd string
		if raw, ok := obj["cwd"]; ok && json.Unmarshal(raw, &cwd) == nil {
			last = cwd
		}
	})
	return last, err
}

// HomeRel maps p to a path relative to home ("." for home itself); ok is
// false outside home. Both must be physical paths.
func HomeRel(home, p string) (string, bool) {
	rel, err := filepath.Rel(home, p)
	if err != nil || !filepath.IsAbs(p) || rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// secretPatterns are named, never printed matches. Ported from
// claude-session-move rather than safety.LooksSecret, which judges one
// key/value pair and doesn't name what it saw.
var secretPatterns = []struct {
	Name string
	Re   *regexp.Regexp
}{
	{"github-token", regexp.MustCompile(`(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{40,}`)},
	{"anthropic-key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`)},
	{"aws-access-key", regexp.MustCompile(`(AKIA|ASIA)[0-9A-Z]{16}`)},
	{"slack-token", regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
}

// Hit is a secret pattern and the number of transcript lines it matched.
type Hit struct {
	Name  string
	Count int
}

func (h Hit) String() string { return fmt.Sprintf("%s x%d", h.Name, h.Count) }

// SecretHits names and counts the patterns a file matches, line by line.
// It never returns the matched text.
func SecretHits(path string) ([]Hit, error) {
	counts := make([]int, len(secretPatterns))
	err := eachLine(path, func(line []byte) {
		for i, p := range secretPatterns {
			if p.Re.Match(line) {
				counts[i]++
			}
		}
	})
	var hits []Hit
	for i, n := range counts {
		if n > 0 {
			hits = append(hits, Hit{secretPatterns[i].Name, n})
		}
	}
	return hits, err
}

// Run moves every job and reports how many were (not) moved.
func Run(ctx context.Context, o Options) (Summary, error) {
	if err := Validate(o.IDs, o.AllBlocked); err != nil {
		return Summary{}, err
	}
	home, err := filepath.EvalSymlinks(o.Home)
	if err != nil {
		return Summary{}, fmt.Errorf("home %s: %w", o.Home, err)
	}
	ids := o.IDs
	if o.AllBlocked {
		if ids, err = BlockedJobs(o.ClaudeDir); err != nil {
			return Summary{}, err
		}
	}
	m := &mover{o: o, home: home}
	var s Summary
	for _, id := range ids {
		if m.one(ctx, id) {
			s.Moved++
		} else {
			s.NotMoved++
		}
	}
	return s, nil
}

type mover struct {
	o    Options
	home string
}

func (m *mover) say(kind, msg string) {
	if m.o.Say != nil {
		m.o.Say(kind, msg)
		return
	}
	if m.o.Out != nil {
		fmt.Fprintln(m.o.Out, msg)
	}
}

// git runs git -C dir args… and returns its trimmed stdout.
func (m *mover) git(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := m.o.Runner.Run(ctx, execx.Cmd{Argv: append([]string{"git", "-C", dir}, args...)})
	return strings.TrimRight(string(res.Stdout), "\n"), err
}

// plan is everything known locally about one job before the box is touched.
type plan struct {
	job                     Job
	transcript              string
	cwd, top, main          string // physical, local
	relCwd, relTop, relMain string // home-relative, for the box
	branch, sha, origin     string
}

// prepare resolves a job locally; reason is why it can't be moved.
func (m *mover) prepare(ctx context.Context, j Job) (p plan, reason string) {
	p.job = j
	if !ValidID(j.Session) {
		return p, fmt.Sprintf("job has no usable session id (%s)", safety.Quote(j.Session))
	}
	if p.transcript = FindTranscript(m.o.ClaudeDir, j.Session); p.transcript == "" {
		return p, fmt.Sprintf("no transcript for session %s (expired?)", j.Session)
	}
	cwd, err := LastCwd(p.transcript)
	if err != nil {
		return p, "reading the transcript: " + err.Error()
	}
	if cwd == "" {
		return p, "transcript records no working directory"
	}
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		return p, fmt.Sprintf("last working directory %s no longer exists", cwd)
	}
	// git reports physical paths; HomeRel needs one form (macOS /var).
	if p.cwd, err = filepath.EvalSymlinks(cwd); err != nil {
		return p, err.Error()
	}
	if p.top, err = m.git(ctx, p.cwd, "rev-parse", "--show-toplevel"); err != nil {
		return p, p.cwd + " is not a git checkout"
	}
	common, err := m.git(ctx, p.cwd, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || !strings.HasSuffix(common, "/.git") {
		return p, fmt.Sprintf("unexpected repo layout (%s)", common)
	}
	p.main = strings.TrimSuffix(common, "/.git")
	if p.branch, err = m.git(ctx, p.top, "symbolic-ref", "--short", "-q", "HEAD"); err != nil || p.branch == "" {
		return p, p.top + " is on a detached HEAD"
	}
	st, err := m.git(ctx, p.top, "status", "--porcelain")
	if err != nil {
		return p, "git status in " + p.top + " failed"
	}
	if n := countLines(st); n > 0 {
		return p, fmt.Sprintf("%s has %d uncommitted changes; commit them first", p.top, n)
	}
	if p.origin, err = m.git(ctx, p.top, "remote", "get-url", "origin"); err != nil || p.origin == "" {
		return p, p.main + " has no origin remote"
	}
	if p.sha, err = m.git(ctx, p.top, "rev-parse", "HEAD"); err != nil {
		return p, "git rev-parse HEAD in " + p.top + " failed"
	}
	var ok1, ok2, ok3 bool
	p.relMain, ok1 = HomeRel(m.home, p.main)
	p.relTop, ok2 = HomeRel(m.home, p.top)
	p.relCwd, ok3 = HomeRel(m.home, p.cwd)
	if !ok1 || !ok2 || !ok3 {
		return p, fmt.Sprintf("%s is outside %s", p.cwd, m.home)
	}
	hits, err := SecretHits(p.transcript)
	if err != nil {
		return p, "scanning the transcript: " + err.Error()
	}
	if len(hits) > 0 && !m.o.AllowSecrets {
		names := make([]string, len(hits))
		for i, h := range hits {
			names[i] = h.String()
		}
		return p, fmt.Sprintf("transcript matches secret patterns (%s); check it, then rerun with --allow-secrets",
			strings.Join(names, ", "))
	}
	return p, ""
}

func countLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// one moves a job; true when moved (or already moved).
func (m *mover) one(ctx context.Context, id string) bool {
	label := id + " (-)"
	j, err := ResolveJob(m.o.ClaudeDir, id)
	if err == nil {
		label = fmt.Sprintf("%s (%s)", id, safety.StripControl(j.Name))
	}
	skip := func(why string) bool { m.say("warn", "skip   "+label+": "+why); return false }
	fail := func(why string, err error) bool {
		msg := "FAIL   " + label + ": " + why
		if d := errDetail(err); d != "" {
			msg += ": " + d
		}
		m.say("err", msg)
		return false
	}
	if err != nil {
		return skip(err.Error())
	}
	p, reason := m.prepare(ctx, j)
	if reason != "" {
		return skip(reason)
	}
	host, staged := m.o.Host, Stage+"/"+j.Session+".jsonl"
	short := p.sha
	if len(short) > 12 {
		short = short[:12]
	}
	m.say("info", fmt.Sprintf("move   %s: %s @ %s -> %s:~/%s", label, p.branch, short, host, p.relCwd))

	if m.o.DryRun {
		m.say("info", fmt.Sprintf("  would: clone %s at ~/%s (or fetch if present)", p.origin, p.relMain))
		m.say("info", fmt.Sprintf("  would: push %s from here into %s:%s (as %s%s)", p.branch, host, p.relMain, PushRef, p.branch))
		m.say("info", fmt.Sprintf("  would: check out %s at ~/%s", p.branch, p.relTop))
		m.say("info", fmt.Sprintf("  would: copy %s and import it with %s", p.transcript, SessionMigrate))
		return true
	}

	out, err := m.ssh(ctx, alreadyScript(staged))
	if err != nil {
		return fail("contacting "+host, err)
	}
	if strings.TrimSpace(out) != "" {
		r, err := parseResult(out)
		if err != nil {
			return fail("reading the existing import marker on the box", err)
		}
		m.say("info", "       already moved: "+ResumeCommand(host, p.relCwd, r.SessionID))
		return true
	}
	if _, err := m.ssh(ctx, prepareScript(p.relMain, p.origin)); err != nil {
		return fail("preparing ~/"+p.relMain+" on the box", err)
	}
	if _, err := m.o.Runner.Run(ctx, execx.Cmd{Argv: pushArgv(p.top, host, p.relMain, p.sha, p.branch)}); err != nil {
		return fail("pushing "+p.branch+" to the box", err)
	}
	if _, err := m.ssh(ctx, checkoutScript(p.relMain, p.relTop, p.branch, p.sha)); err != nil {
		return fail("checking out "+p.branch+" on the box", err)
	}
	f, err := os.Open(p.transcript)
	if err != nil {
		return fail("reading the transcript", err)
	}
	_, err = m.o.Runner.Run(ctx, execx.Cmd{Argv: m.sshArgv(copyCommand(staged)), Stdin: f})
	f.Close()
	if err != nil {
		return fail("copying the transcript", err)
	}
	out, err = m.ssh(ctx, importScript(staged, p.relCwd))
	if err != nil {
		return fail("session-migrate import on the box", err)
	}
	r, err := parseResult(out)
	if err != nil {
		return fail("session-migrate import on the box", err)
	}
	if _, err := m.ssh(ctx, finishScript(staged)); err != nil {
		return fail("recording the import on the box", err)
	}
	m.say("info", "       resume: "+ResumeCommand(host, p.relCwd, r.SessionID))
	for _, w := range r.Warnings {
		m.say("info", fmt.Sprintf("       warning: %s: %s", safety.StripControl(w.Code), safety.StripControl(w.Message)))
	}
	for _, k := range sortedKeys(r.DroppedEvents) {
		m.say("info", fmt.Sprintf("       dropped: %s x%v", safety.StripControl(k), r.DroppedEvents[k]))
	}
	if j.ID != j.Session {
		m.say("info", fmt.Sprintf("       then stop job %s here so two agents don't work %s", j.ID, p.branch))
	}
	return true
}

// errDetail is the last stderr line of a failed command, if any.
func errDetail(err error) string {
	var de detailErr
	if errors.As(err, &de) {
		return de.detail
	}
	if err != nil && !errors.Is(err, execx.ErrExit) {
		return err.Error()
	}
	return ""
}

type detailErr struct {
	err    error
	detail string
}

func (e detailErr) Error() string { return e.err.Error() + ": " + e.detail }
func (e detailErr) Unwrap() error { return e.err }

// withStderr attaches the command's last stderr line to err.
func withStderr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return detailErr{err, safety.StripControl(last)}
	}
	return err
}

// result is session-migrate's (content-free) import result.
type result struct {
	SessionID string `json:"session_id"`
	Warnings  []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"warnings"`
	DroppedEvents map[string]any `json:"dropped_events"`
}

func parseResult(s string) (result, error) {
	var r result
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return r, fmt.Errorf("unreadable result JSON: %w", err)
	}
	if !ValidID(r.SessionID) {
		return r, errors.New("session-migrate returned no session id")
	}
	return r, nil
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
