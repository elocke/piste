// Command changelog cuts piste's releases in CHANGELOG.md, for CI and
// maintainers (not shipped: the piste package builds cmd/piste only).
//
//	changelog cut patch|minor|major [--note <line>]
//	    Unreleased becomes the next release, dated today (UTC), under a fresh
//	    empty Unreleased; prints its version. Exits 3 when the cut is refused:
//	    a patch with Upgrading notes or before the first release, or nothing
//	    to release.
//	changelog pending
//	    Prints the top release if it has no tag and is above every tag (the
//	    release to make), else nothing.
//	changelog notes <version>
//	    Prints that release's notes (markdown), for its GitHub release.
//	changelog target <version>
//	    Prints the commit that cut that release: the newest commit on HEAD's
//	    first-parent line whose CHANGELOG.md has it and whose parent's
//	    doesn't. Its tag goes there, not on HEAD, so what merged after the
//	    cut isn't released with it. Exits 3 when that commit's Unreleased
//	    isn't empty: the cut merged with other changes (a bump PR merged
//	    behind main), which the release would ship unannounced.
//
// It works on the CHANGELOG.md at the top of the git repo it's run in.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elocke/piste/cli/internal/release"
)

const usage = `usage: changelog cut patch|minor|major [--note <line>]
       changelog pending
       changelog notes <version>
       changelog target <version>`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now())) }

func run(args []string, stdout, stderr io.Writer, now time.Time) int {
	fail := func(code int, format string, a ...any) int {
		fmt.Fprintf(stderr, "changelog: "+format+"\n", a...)
		return code
	}
	bad := func() int { fmt.Fprintln(stderr, usage); return 2 }
	if len(args) == 0 {
		return bad()
	}
	top, err := git("", "rev-parse", "--show-toplevel")
	if err != nil {
		return fail(1, "not in a git repository: %v", err)
	}
	file := filepath.Join(strings.TrimSpace(top), "CHANGELOG.md")
	b, err := os.ReadFile(file)
	if err != nil {
		return fail(1, "%v", err)
	}
	text := string(b)
	switch args[0] {
	case "cut":
		if len(args) < 2 || !strings.Contains("|patch|minor|major|", "|"+args[1]+"|") {
			return bad()
		}
		fs := flag.NewFlagSet("cut", flag.ContinueOnError)
		fs.SetOutput(stderr)
		note := fs.String("note", "", "one more change line for the release")
		if fs.Parse(args[2:]) != nil || fs.NArg() > 0 {
			return bad()
		}
		out, v, err := release.Cut(text, args[1], *note, now.UTC().Format("2006-01-02"))
		if errors.Is(err, release.ErrRefused) {
			return fail(3, "%v", err)
		}
		if err != nil {
			return fail(1, "%v", err)
		}
		if err := os.WriteFile(file, []byte(out), 0o644); err != nil {
			return fail(1, "%v", err)
		}
		fmt.Fprintln(stdout, v)
	case "pending":
		if len(args) != 1 {
			return bad()
		}
		tags, err := git(filepath.Dir(file), "tag", "-l")
		if err != nil {
			return fail(1, "git tag: %v", err)
		}
		if v := release.Pending(text, strings.Fields(tags)); v != "" {
			fmt.Fprintln(stdout, v)
		}
	case "notes":
		if len(args) != 2 {
			return bad()
		}
		n, ok := release.Notes(text, args[1])
		if !ok {
			return fail(1, "CHANGELOG.md has no release %s", args[1])
		}
		fmt.Fprint(stdout, n)
	case "target":
		if len(args) != 2 {
			return bad()
		}
		c, err := cutCommit(filepath.Dir(file), args[1])
		if err != nil {
			return fail(1, "%v", err)
		}
		// A cut leaves Unreleased empty; anything there came in with it.
		at, _ := git(filepath.Dir(file), "show", c+":CHANGELOG.md")
		if ss := release.Sections(at); len(ss) > 0 && ss[0].Version == "" && (len(ss[0].Changes) > 0 || len(ss[0].Upgrading) > 0) {
			return fail(3, "%s's cut (%.12s) also brings changes under Unreleased, merged with it: releasing it would ship them as %s. "+
				"Cut a release over them instead (go run ./cmd/changelog cut minor, from cli/)", args[1], c, args[1])
		}
		fmt.Fprintln(stdout, c)
	default:
		return bad()
	}
	return 0
}

// cutCommit is the commit that cut version in the repo at top: the newest
// on HEAD's first-parent line whose CHANGELOG.md has the release and whose
// parent's doesn't (a later edit to its notes isn't its cut).
func cutCommit(top, version string) (string, error) {
	log, err := git(top, "log", "--first-parent", "--format=%H", "--", "CHANGELOG.md")
	if err != nil {
		return "", fmt.Errorf("git log: %v", err)
	}
	has := func(rev string) bool {
		text, err := git(top, "show", rev+":CHANGELOG.md")
		if err != nil {
			return false
		}
		_, ok := release.Notes(text, version)
		return ok
	}
	for _, c := range strings.Fields(log) {
		if has(c) && !has(c+"^") {
			return c, nil
		}
	}
	return "", fmt.Errorf("no commit on this branch adds %s to CHANGELOG.md", version)
}

func git(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.Output()
	return string(out), err
}
