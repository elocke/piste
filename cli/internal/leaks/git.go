package leaks

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// Added is one line a commit adds: its new path and line number.
type Added struct {
	Path string
	Line int
	Text string
}

// Commit is the text a commit publishes: its identities and message, the
// paths it adds (new, copied or renamed to) and the lines it adds.
type Commit struct {
	SHA, Author, Committer, Message string
	Paths                           []string
	Added                           []Added
}

// gitLog is one git log of the range: per commit a header led by mark (sha,
// author, committer, message), then --raw lines (every added path, binary,
// empty and symlinks included, with its full new blob name) and the -U0
// patch (the added lines). The flags pin what user config could change:
// prefixes, colour, signatures, relative paths, external diff drivers, text
// conversion; and --text shows every file's lines, so gitattributes (binary,
// -diff) can't hide them: Commits decides what is binary, by content.
func gitLog(revs []string, mark string) []string {
	return slices.Concat([]string{"-c", "core.quotePath=false", "log", "--reverse", "--root",
		"--no-color", "--no-ext-diff", "--no-textconv", "--no-show-signature", "--no-relative",
		"--text", "--no-abbrev", "--src-prefix=a/", "--dst-prefix=b/", "-M", "--raw", "-p", "-U0",
		"--diff-merges=first-parent",
		"--format=" + mark + "%H" + mark + "%an <%ae>" + mark + "%cn <%ce>" + mark + "%B" + mark}, revs, []string{"--"})
}

// Commits is the commits of revs (git log's revision arguments, e.g.
// ["<sha>", "--not", "--remotes=origin"]) in dir, oldest first. A binary
// file's lines are left out: a NUL in its first 8000 bytes, as git (and
// Tree) decide it, whatever gitattributes say.
func Commits(dir string, revs []string) ([]Commit, error) {
	fail := func(msg string) error {
		return fmt.Errorf("git log %s: %s", strings.Join(revs, " "), msg)
	}
	// The records' separator is new each run, so no file or message can
	// hold it (a NUL can: git reads a file with one past its first 8000
	// bytes as text).
	mark := "piste:" + rand.Text()
	cmd := exec.Command("git", gitLog(revs, mark)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fail(firstLine(msg))
	}
	f := strings.Split(string(out), mark)
	if len(f) == 1 && strings.TrimSpace(f[0]) == "" {
		return nil, nil
	}
	if (len(f)-1)%5 != 0 || strings.TrimSpace(f[0]) != "" {
		return nil, fail("output not understood")
	}
	var cs []Commit
	var blobs []map[string]string
	for i := 1; i < len(f); i += 5 {
		if !isSHA(f[i]) {
			return nil, fail("output not understood")
		}
		c := Commit{SHA: f[i], Author: f[i+1], Committer: f[i+2], Message: strings.TrimRight(f[i+3], "\n")}
		var b map[string]string
		c.Paths, c.Added, b = parseDiff(f[i+4])
		cs = append(cs, c)
		blobs = append(blobs, b)
	}
	if err := dropBinary(dir, cs, blobs); err != nil {
		return nil, fail(err.Error())
	}
	return cs, nil
}

// isSHA is whether s is a full object name (sha1 or sha256).
func isSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	return strings.Trim(s, "0123456789abcdef") == ""
}

// dropBinary leaves out the added lines of each file whose new content is
// binary. blobs[i] maps commit i's changed regular files to their new blobs.
func dropBinary(dir string, cs []Commit, blobs []map[string]string) error {
	var ids []string
	seen := map[string]bool{}
	for i, c := range cs {
		for _, a := range c.Added {
			if id := blobs[i][a.Path]; id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	bin, err := binaryBlobs(dir, ids)
	if err != nil {
		return err
	}
	for i := range cs {
		cs[i].Added = slices.DeleteFunc(cs[i].Added, func(a Added) bool { return bin[blobs[i][a.Path]] })
	}
	return nil
}

// binaryBlobs is which of the blobs have a NUL in their first 8000 bytes,
// read with one git cat-file --batch.
func binaryBlobs(dir string, ids []string) (map[string]bool, error) {
	bin := map[string]bool{}
	if len(ids) == 0 {
		return bin, nil
	}
	cmd := exec.Command("git", "cat-file", "--batch")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git cat-file: %s", firstLine(msg))
	}
	for _, id := range ids {
		hdr, rest, _ := bytes.Cut(out, []byte("\n"))
		f := strings.Fields(string(hdr))
		n := -1
		if len(f) == 3 && f[0] == id && f[1] == "blob" {
			n, _ = strconv.Atoi(f[2])
		}
		if n < 0 || n >= len(rest) {
			return nil, fmt.Errorf("git cat-file: %q not understood", hdr)
		}
		bin[id] = bytes.IndexByte(rest[:min(n, binaryPeek)], 0) >= 0
		out = rest[n+1:]
	}
	return bin, nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// parseDiff reads a commit's --raw lines and -U0 patch: the paths it adds,
// the lines it adds and each changed regular file's new blob, by path.
func parseDiff(s string) (paths []string, added []Added, blobs map[string]string) {
	blobs = map[string]string{}
	const (
		raw = iota
		header
		hunk
	)
	state, path, n := raw, "", 0
	for _, line := range strings.Split(s, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			state, path = header, ""
		case state == raw && strings.HasPrefix(line, ":"):
			if p, blob, adds, ok := rawLine(line); ok {
				if adds {
					paths = append(paths, p)
				}
				if blob != "" {
					blobs[p] = blob
				}
			}
		case state == header && strings.HasPrefix(line, "+++ "):
			path = patchPath(line[4:])
		case state != raw && strings.HasPrefix(line, "@@ "):
			state, n = hunk, hunkStart(line)
		case state == hunk && line != "":
			switch line[0] {
			case '+':
				added = append(added, Added{path, n, line[1:]})
				n++
			case ' ':
				n++
			}
		}
	}
	return paths, added, blobs
}

// rawLine reads a --raw line (":<mode> <mode> <blob> <blob> <status>\t<path>
// [\t<new path>]"): the path it leaves (the new name), its new blob when that
// is a regular file, and whether it adds the path (status A, C or R).
func rawLine(line string) (path, blob string, adds, ok bool) {
	meta, rest, ok := strings.Cut(line, "\t")
	fields := strings.Fields(meta)
	if !ok || len(fields) != 5 {
		return "", "", false, false
	}
	ps := strings.Split(rest, "\t")
	path = unquote(ps[len(ps)-1])
	if strings.HasPrefix(fields[1], "100") {
		blob = fields[3]
	}
	return path, blob, strings.ContainsAny(fields[4][:1], "ACR"), true
}

// patchPath is the new path of a "+++ " line ("" for /dev/null).
func patchPath(s string) string {
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(unquote(s), "b/")
}

// unquote undoes git's C-style quoting of an unusual path.
func unquote(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}
	return s
}

// hunkStart is c of "@@ -a[,b] +c[,d] @@".
func hunkStart(line string) int {
	_, rest, _ := strings.Cut(line, " +")
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(rest)
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// Commits is what the commits publish that the lists or the generic markers
// flag, in commit order: per commit the author, the committer, the message,
// the added paths (and any under overlays/), then the added lines.
func (m *Matcher) Commits(cs []Commit) []Hit {
	var hits []Hit
	for _, c := range cs {
		sha := c.SHA[:min(7, len(c.SHA))]
		add := func(where, text string) {
			for _, what := range m.Line(text) {
				hits = append(hits, Hit{sha, where, what})
			}
		}
		add("author", c.Author)
		if c.Committer != c.Author {
			add("committer", c.Committer)
		}
		for _, l := range strings.Split(c.Message, "\n") {
			add("commit message", l)
		}
		for _, p := range c.Paths {
			add(p+" (name)", p)
			if strings.HasPrefix(p, "overlays/") {
				hits = append(hits, Hit{sha, p, "under overlays/: an overlay's files never go in the engine"})
			}
		}
		for _, a := range c.Added {
			add(fmt.Sprintf("%s:%d", a.Path, a.Line), a.Text)
		}
	}
	return hits
}
