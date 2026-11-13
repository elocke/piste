package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/leaks"
)

func init() { register("leaks", newLeaks) }

// newLeaks is `piste leaks`: the leak gate. Your marker words
// (lists in the checkout's overlays), generic markers (private
// addresses, your home directory) and gitleaks, over the commits a push
// would add or over an exported tree. Exit 0 clean, 1 a hit, 2 couldn't scan.
func newLeaks(a *App) *cobra.Command {
	var markers []string
	var prePush string
	c := &cobra.Command{
		Use:   "leaks [<dir>...]",
		Short: "Check unpushed commits, or a tree, for private words, addresses and secrets",
		Long: "Check what you are about to publish for private words, private addresses and\n" +
			"secrets: with no arguments, the commits on HEAD that no remote has (what a\n" +
			"push would add: files, file names, messages, authors); with directories,\n" +
			"every file under them (an exported tree, not a checkout).\n\n" +
			"The words come from every overlays/*/leak-markers*.txt of this piste\n" +
			"checkout (in a worktree, of its main checkout) and from --markers. A list\n" +
			"has one entry per line: a term of 1 to 4 words (any case, any separator)\n" +
			"or re:<regex>; # starts a comment.\n\n" +
			"Private (RFC1918) addresses and your home directory are always checked,\n" +
			"and gitleaks runs over the same commits or files with its default rules:\n" +
			"a .gitleaks.toml is not read, and a .gitleaksignore is refused (mark the\n" +
			"line gitleaks:allow instead).\n\n" +
			"Exit status: 0 clean, 1 something found, 2 could not check.",
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, dirs []string) error {
			if prePush != "" {
				return a.leaksPrePush(prePush, dirs, markers)
			}
			return a.leaks(dirs, markers)
		},
	}
	c.Flags().StringArrayVar(&markers, "markers", nil, "also check the words of this marker list (repeatable)")
	// The pre-push hook's: git's ref lines on stdin, the remote's name here.
	c.Flags().StringVar(&prePush, "pre-push", "", "check what a push to this remote adds (git's ref lines on stdin)")
	_ = c.Flags().MarkHidden("pre-push")
	return c
}

func (a *App) leaks(dirs, markers []string) error {
	fail := func(err error) error {
		fmt.Fprintf(a.Stderr, "piste leaks: %v\n", err)
		return ExitError{2}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	lists, err := loadLists(cwd, markers)
	if err != nil {
		return fail(err)
	}
	m := leaks.NewMatcher(a.Env.Getenv("HOME"), lists)
	sources := fmt.Sprintf("generic markers, %s, gitleaks", count(len(lists), "list"))
	if len(dirs) > 0 {
		var hits []leaks.Hit
		files := 0
		for _, d := range dirs {
			n, h, err := leaks.ScanTree(d, m)
			if err != nil {
				return fail(err)
			}
			files += n
			hits = append(hits, h...)
		}
		return a.leakReport(a.Stdout, fmt.Sprintf("%s in %s", count(files, "file"), strings.Join(dirs, ", ")),
			sources, hits, "Take these out of the tree.")
	}
	n, hits, err := leaks.ScanRange(cwd, []string{"HEAD", "--not", "--remotes"}, m)
	if err != nil {
		return fail(err)
	}
	return a.leakReport(a.Stdout, count(n, "commit")+" not on a remote", sources, hits,
		"Take these out of the commits (amend or rebase) before you push.")
}

// leaksPrePush is the pre-push hook's check: the commits each pushed ref
// adds that no ref of the remote has (a deleted ref adds none). It fails
// closed: no list, or anything it can't check, blocks the push.
func (a *App) leaksPrePush(remote string, dirs, markers []string) error {
	fail := func(msg string) error {
		fmt.Fprintf(a.Stderr, "piste leaks: %s\n", msg)
		return ExitError{2}
	}
	if len(dirs) > 0 {
		return fail("--pre-push takes no directories; push blocked")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail(err.Error() + "; push blocked")
	}
	lists, err := loadLists(cwd, markers)
	if err != nil {
		return fail(err.Error() + "; push blocked")
	}
	if len(lists) == 0 {
		return fail("no marker list (overlays/*/leak-markers*.txt); nothing checked, push blocked")
	}
	var revs []string
	if a.Stdin != nil {
		sc := bufio.NewScanner(a.Stdin)
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) != 4 {
				return fail(fmt.Sprintf("unexpected ref line from git: %q; push blocked", sc.Text()))
			}
			if strings.Trim(f[1], "0") != "" {
				revs = append(revs, f[1])
			}
		}
		if err := sc.Err(); err != nil {
			return fail(err.Error() + "; push blocked")
		}
	}
	m := leaks.NewMatcher(a.Env.Getenv("HOME"), lists)
	sources := fmt.Sprintf("generic markers, %s, gitleaks", count(len(lists), "list"))
	n, hits := 0, []leaks.Hit(nil)
	if len(revs) > 0 {
		n, hits, err = leaks.ScanRange(cwd, append(revs, "--not", "--remotes="+remote), m)
		if err != nil {
			return fail(err.Error() + "; push blocked")
		}
	}
	return a.leakReport(a.Stderr, fmt.Sprintf("%s for %s", count(n, "commit"), remote), sources, hits,
		"Push blocked: take these out of the commits (amend or rebase), then push again.")
}

// loadLists reads the checkout's marker lists (named relative to it) and the
// --markers ones (named as given).
func loadLists(cwd string, markers []string) ([]*leaks.List, error) {
	root, found := leaks.FindLists(cwd)
	var lists []*leaks.List
	for _, p := range found {
		name, err := filepath.Rel(root, p)
		if err != nil {
			name = p
		}
		l, err := leaks.LoadList(p, name)
		if err != nil {
			return nil, err
		}
		lists = append(lists, l)
	}
	for _, p := range markers {
		l, err := leaks.LoadList(p, p)
		if err != nil {
			return nil, err
		}
		lists = append(lists, l)
	}
	return lists, nil
}

// leakReport prints the scan's result to w: one line when clean, else every
// hit and what to do; a hit is exit 1. (The hook's goes to stderr, where git
// and its callers look for a hook's messages.)
func (a *App) leakReport(w io.Writer, subject, sources string, hits []leaks.Hit, closing string) error {
	if len(hits) == 0 {
		fmt.Fprintf(w, "piste leaks: %s clean (%s)\n", subject, sources)
		return nil
	}
	fmt.Fprintf(w, "piste leaks: %s (%s)\n", subject, sources)
	width := 0
	for _, h := range hits {
		width = max(width, utf8.RuneCountInString(h.Where))
	}
	for _, h := range hits {
		sha := ""
		if h.Commit != "" {
			sha = h.Commit + " "
		}
		fmt.Fprintf(w, "✗ %s%-*s  %s\n", sha, width, h.Where, h.What)
	}
	fmt.Fprintln(w, closing)
	return ExitError{1}
}

func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
