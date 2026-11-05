package release

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CHANGELOG.md, as piste writes it:
//
//	<preamble>
//	## Unreleased
//	- <a user-visible change, one line, written in the PR that makes it>
//	### Upgrading
//	- <what an overlay must change; breaking changes only>
//
//	## v0.5.1 (2026-11-09)
//	- Inputs updated (flake.lock).
//
// Releases are newest first. Text before the first "## " is kept as it is.

// Section is one "## " section: Unreleased (Version "") or a release.
// Changes and Upgrading are its raw lines, without blank lines at either end.
type Section struct {
	Version, Date      string
	Changes, Upgrading []string
}

var (
	headingRe = regexp.MustCompile(`^## (\S+)(?: \(([^)]*)\))?\s*$`)
	upgrading = "### Upgrading"
)

// span is a section and the lines [start, end) it takes in the file.
type span struct {
	Section
	start, end int
}

func spans(lines []string) []span {
	var out []span
	for i, l := range lines {
		if !strings.HasPrefix(l, "## ") {
			continue
		}
		if n := len(out); n > 0 {
			out[n-1].end = i
		}
		s := span{start: i, end: len(lines)}
		if m := headingRe.FindStringSubmatch(l); m != nil && m[1] != "Unreleased" {
			s.Version, s.Date = m[1], m[2]
		} else if m == nil {
			s.Version = strings.TrimSpace(strings.TrimPrefix(l, "## "))
		}
		out = append(out, s)
	}
	for i := range out {
		body := lines[out[i].start+1 : out[i].end]
		at := len(body)
		for j, l := range body {
			if strings.TrimSpace(l) == upgrading {
				at = j
				break
			}
		}
		out[i].Changes = trimBlank(body[:at])
		if at < len(body) {
			out[i].Upgrading = trimBlank(body[at+1:])
		}
	}
	return out
}

func trimBlank(l []string) []string {
	for len(l) > 0 && strings.TrimSpace(l[0]) == "" {
		l = l[1:]
	}
	for len(l) > 0 && strings.TrimSpace(l[len(l)-1]) == "" {
		l = l[:len(l)-1]
	}
	if len(l) == 0 {
		return nil
	}
	return append([]string(nil), l...)
}

// Sections are the file's sections, in file order.
func Sections(text string) []Section {
	var out []Section
	for _, s := range spans(strings.Split(text, "\n")) {
		out = append(out, s.Section)
	}
	return out
}

// releases are the sections whose heading is a release, any order.
func releases(text string) []Section {
	var out []Section
	for _, s := range Sections(text) {
		if _, ok := ParseVersion(s.Version); ok {
			out = append(out, s)
		}
	}
	return out
}

// Between is every release after from, up to and including to, oldest first.
func Between(text, from, to string) []Section {
	var out []Section
	for _, s := range releases(text) {
		if Newer(s.Version, from) && !Newer(s.Version, to) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := ParseVersion(out[i].Version)
		b, _ := ParseVersion(out[j].Version)
		return a.Less(b)
	})
	return out
}

// body is a section as markdown, without its heading.
func body(s Section) string {
	var b strings.Builder
	for _, l := range s.Changes {
		b.WriteString(l + "\n")
	}
	if len(s.Upgrading) > 0 {
		if len(s.Changes) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(upgrading + "\n")
		for _, l := range s.Upgrading {
			b.WriteString(l + "\n")
		}
	}
	return b.String()
}

// Notes is a release's notes as markdown (its changes, then its Upgrading
// notes), for a GitHub release; false if the file has no such release.
func Notes(text, version string) (string, bool) {
	for _, s := range releases(text) {
		if s.Version == version {
			return body(s), true
		}
	}
	return "", false
}

// Top is the highest release in the file, "" before the first.
func Top(text string) string {
	var tags []string
	for _, s := range releases(text) {
		tags = append(tags, s.Version)
	}
	return Latest(tags)
}

// ErrRefused is a cut the rules don't allow (changelog cut exits 3).
var ErrRefused = errors.New("refused")

// Cut releases Unreleased: its heading becomes "## vX.Y.Z (date)" under a
// fresh, empty "## Unreleased". kind is patch, minor or major, counted from
// the top release (0.0.0 before the first); note, if any, is added as one
// more change. A patch is refused when Unreleased has Upgrading notes (they
// are breaking: a minor) or before the first release; so is a release with
// nothing in it. Everything outside Unreleased is kept byte for byte.
func Cut(text, kind, note, date string) (out, version string, err error) {
	lines := strings.Split(text, "\n")
	ss := spans(lines)
	if len(ss) == 0 || ss[0].Version != "" {
		return "", "", errors.New("CHANGELOG.md has no \"## Unreleased\" section at the top of its releases")
	}
	u := ss[0]
	top, released := ParseVersion(Top(text))
	next := top
	switch kind {
	case "patch":
		if !released {
			return "", "", fmt.Errorf("%w: no release yet, so no patch: the first release is a minor (changelog cut minor)", ErrRefused)
		}
		if len(u.Upgrading) > 0 {
			return "", "", fmt.Errorf("%w: Unreleased has Upgrading notes, so it's breaking: cut a minor", ErrRefused)
		}
		next.Patch++
	case "minor":
		next = Version{top.Major, top.Minor + 1, 0}
	case "major":
		next = Version{top.Major + 1, 0, 0}
	default:
		return "", "", fmt.Errorf("unknown kind %q: patch, minor or major", kind)
	}
	r := Section{Version: next.String(), Date: date, Changes: u.Changes, Upgrading: u.Upgrading}
	if note != "" {
		r.Changes = append(append([]string(nil), u.Changes...), "- "+note)
	}
	if len(r.Changes) == 0 && len(r.Upgrading) == 0 {
		return "", "", fmt.Errorf("%w: Unreleased is empty: nothing to release", ErrRefused)
	}
	cut := "## Unreleased\n\n## " + r.Version + " (" + date + ")\n" + body(r)
	before := strings.Join(lines[:u.start], "\n")
	if u.start > 0 {
		before += "\n"
	}
	after := ""
	if u.end < len(lines) {
		after = "\n" + strings.Join(lines[u.end:], "\n")
	}
	return before + cut + after, r.Version, nil
}

// Pending is the release to tag: the file's top release when it has no tag
// and is above every release tag, else "".
func Pending(text string, tags []string) string {
	top := Top(text)
	if top == "" {
		return ""
	}
	if l := Latest(tags); l != "" && !Newer(top, l) {
		return ""
	}
	return top
}
