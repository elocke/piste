// Package machine carries a NixOS machine's own configuration into an
// overlay: the files under /etc/nixos, rewritten so a flake can build them.
package machine

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Header is prepended to every carried file.
const Header = "# Carried over from /etc/nixos by piste host add. These are your\n# machine's own settings; trim them as your host file takes them over.\n"

// ErrNoConfiguration is what Carry's error wraps when etcDir has no
// configuration.nix.
var ErrNoConfiguration = errors.New("no configuration.nix")

// Carry reads every .nix file under etcDir (recursively) and returns them
// rewritten for a flake build, keyed by path relative to etcDir, plus one
// note per thing it could not carry. files is nil only with an error.
//
// Only regular .nix files are carried: other files are ignored, and a
// symlink is never followed (etcDir itself may be one). A directory that
// can't be listed is skipped, with the note `<dir>: can't read it as <user>;
// not carried`; a .nix file that can't be read is an error.
//
// Any other note is `<file>:<line>: <reason>`, the line being the offending
// one in the carried file; the same reason is written there as a
// `# piste: <reason>` comment on the line above (above the statement's first
// line instead, when the offending line starts inside a multi-line string).
// Notes are ordered by file path, then position.
//
// Two settings are taken out: networking.hostName (the host's name is its
// key in flake.nix) and system.autoUpgrade (it would rebuild the machine
// from /etc/nixos). On lines of their own they are commented out, under
// their marker; sharing a line with other code, they are only marked. A
// networking.hostName is the host's only at a module's top level (directly
// in its attribute set, its config, or its networking = { … }): a
// container's or any other nested set's is left alone, and one in a form
// that can't be commented out cleanly (an inherit, a lib.mkMerge or
// lib.mkIf, a rec or with block) is only marked (hostNameLiveReason).
func Carry(etcDir string) (files map[string][]byte, notes []string, err error) {
	root, err := filepath.EvalSymlinks(etcDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, noConfiguration(etcDir)
	}
	if err != nil {
		return nil, nil, err
	}

	var rels, unreadable []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that can't be listed (a root-only secrets/) is
			// left out, with a note; the rest is still carried.
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil || rel == "." || d == nil || !d.IsDir() {
				return err
			}
			unreadable = append(unreadable, filepath.ToSlash(rel))
			return fs.SkipDir
		}
		if d.Type().IsRegular() && filepath.Ext(p) == ".nix" {
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			rels = append(rels, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(rels)
	c := &carrier{root: root, carried: map[string]bool{}}
	for _, rel := range rels {
		c.carried[rel] = true
	}
	if !c.carried["configuration.nix"] {
		return nil, nil, noConfiguration(etcDir)
	}

	// Notes grouped by the path they are about, so they come out in path order.
	type pathNotes struct {
		path  string
		notes []string
	}
	var grouped []pathNotes
	files = map[string][]byte{}
	for _, rel := range rels {
		// An unreadable .nix file is an error: leaving it out would drop
		// part of the machine's configuration without a word.
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return nil, nil, err
		}
		out, fileNotes := c.rewrite(rel, src)
		files[rel] = out
		grouped = append(grouped, pathNotes{rel, fileNotes})
	}
	for _, rel := range unreadable {
		grouped = append(grouped, pathNotes{rel, []string{fmt.Sprintf("%s: can't read it as %s; not carried", rel, whoami())}})
	}
	sort.SliceStable(grouped, func(i, j int) bool { return grouped[i].path < grouped[j].path })
	notes = []string{}
	for _, g := range grouped {
		notes = append(notes, g.notes...)
	}
	return files, notes, nil
}

// whoami is the name of the user running piste, for a note.
func whoami() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "this user"
}

func noConfiguration(etcDir string) error {
	return fmt.Errorf("%w in %s", ErrNoConfiguration, etcDir)
}

type carrier struct {
	root    string          // etcDir with symlinks resolved
	carried map[string]bool // slash-separated paths relative to root
}

// An edit replaces src[start:end] with text (an insertion when start == end).
type edit struct {
	start, end int
	text       string
}

// A finding is something to mark: the offset of the token in src.
type finding struct {
	pos    int
	reason string
}

const channelReason = "the flake build doesn't use channels"

// hostNameReason marks a carried networking.hostName: the host's name is
// its key in flake.nix, which mkFlake sets as a default that a plain
// setting carried over would beat (the switch would rename the machine,
// and the next one not find its host).
const hostNameReason = "the host's name is its key in flake.nix (mkFlake sets networking.hostName)"

// hostNameLiveReason marks a networking.hostName carry leaves live: one at
// the module's top level in a form it can't comment out without breaking
// what is around it (an inherit, a lib.mkMerge or lib.mkIf, a rec or with
// block), which may still beat mkFlake's.
const hostNameLiveReason = "networking.hostName here may override the host's name; remove it if this machine's name should be its key in flake.nix"

// autoUpgradeReason marks a carried system.autoUpgrade: it rebuilds the
// machine from /etc/nixos (channels and all) on a timer, putting back the
// system piste switched away from.
const autoUpgradeReason = "system.autoUpgrade would rebuild from /etc/nixos, not this overlay"

const (
	pathChar = `[A-Za-z0-9._+-]`
	pathTail = `(?:/` + pathChar + `+)`
)

var (
	// A <...> lookup, as Nix lexes one.
	lookupRe  = regexp.MustCompile(`<` + pathChar + `+` + pathTail + `*>`)
	modulesRe = regexp.MustCompile(`^<nixpkgs/nixos/modules/(` + pathChar + `+` + pathTail + `*)>$`)
	// A path literal: relative (including ./ and ../), absolute or ~/.
	pathRe = regexp.MustCompile(pathChar + `*` + pathTail + `+/?|~` + pathTail + `+/?`)
	// An unquoted URL, which Nix lexes as one token.
	uriRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*:[A-Za-z0-9%/?:@&=+$,_.!~*'-]+`)
	// What a flake build doesn't use, dotted (nix.nixPath; the same in the
	// module's top-level nix = { ... } block is topLevelSites'). Group 2 is
	// the name; a name must not run on into more identifier. A leading `.`
	// excludes reads such as config.nix.nixPath.
	settingRe = regexp.MustCompile(`(^|[^\w.'-])(nix\.(?:nixPath|channel))`)
	// modulesPath among a pattern's formals.
	formalRe = regexp.MustCompile(`[{,]\s*modulesPath\s*[,?}]`)
	// system.autoUpgrade, which carry takes out (optionBindings): dotted
	// (group 2 the option's path; the match ends at its last name), and its
	// parent's block (system = { ... }; the match ends at its {). A leading
	// `.` excludes reads such as config.system.autoUpgrade.
	autoUpgradeRe = regexp.MustCompile(`(^|[^\w.'-])(system\s*\.\s*autoUpgrade)`)
	systemRe      = regexp.MustCompile(`(^|[^\w.'-])system\s*=\s*\{`)
)

// rewrite returns src for a flake build, with Header, and the notes for what
// it marked in it.
func (c *carrier) rewrite(rel string, src []byte) ([]byte, []string) {
	sc := scan(src)
	var edits []edit
	var finds []finding

	// <nixpkgs/nixos/modules/PATH> becomes (modulesPath + "/PATH"); any
	// other lookup is a channel a flake build doesn't have.
	rewrote := false
	for _, m := range lookupRe.FindAllIndex(sc.code, -1) {
		tok := string(src[m[0]:m[1]])
		if sub := modulesRe.FindStringSubmatch(tok); sub != nil {
			edits = append(edits, edit{m[0], m[1], `(modulesPath + "/` + sub[1] + `")`})
			rewrote = true
		} else {
			finds = append(finds, finding{m[0], tok + " is a channel lookup, which the flake build can't resolve"})
		}
	}

	if rewrote {
		if e, f := modulesPathFix(src, sc.code); e != nil {
			edits = append(edits, *e)
		} else if f != nil {
			finds = append(finds, *f)
		}
	}

	for _, m := range settingRe.FindAllSubmatchIndex(sc.code, -1) {
		if m[5] < len(sc.code) && isIdentChar(sc.code[m[5]]) {
			continue // nix.channels, not nix.channel
		}
		finds = append(finds, finding{m[4], channelReason})
	}
	top := topLevelSites(sc.code)
	for _, at := range top.channels {
		finds = append(finds, finding{at, channelReason})
	}

	// The host's name is flake.nix's, and an automatic upgrade would
	// rebuild from /etc/nixos: a networking.hostName at the module's top
	// level or a system.autoUpgrade binding on lines of its own is
	// commented out (below, once the markers are in), one sharing a line
	// with other code only marked; a hostName in a form that can't be
	// taken out cleanly is only marked.
	for _, at := range top.liveHostNames {
		finds = append(finds, finding{at, hostNameLiveReason})
	}
	var commentOut []span
	for _, o := range []struct {
		bindings []span
		reason   string
	}{
		{top.hostNames, hostNameReason},
		{optionBindings(sc.code, autoUpgradeRe, systemRe, "autoUpgrade"), autoUpgradeReason},
	} {
		for _, st := range o.bindings {
			finds = append(finds, finding{st.start, o.reason})
			if ownLines(src, sc, st) {
				commentOut = append(commentOut, st)
			}
		}
	}

	// Paths that leave etcDir, or name something that isn't carried. Look
	// where the lookups, operators and URLs are not.
	pcode := bytes.Clone(sc.code)
	for _, re := range []*regexp.Regexp{lookupRe, uriRe} {
		for _, m := range re.FindAllIndex(pcode, -1) {
			blankRange(pcode, m[0], m[1])
		}
	}
	for i := 0; i+1 < len(pcode); i++ {
		if pcode[i] == '/' && pcode[i+1] == '/' {
			blankRange(pcode, i, i+2)
			i++
		}
	}
	dir := path.Dir(rel)
	for _, m := range pathRe.FindAllIndex(pcode, -1) {
		if reason := c.pathProblem(dir, string(src[m[0]:m[1]])); reason != "" {
			finds = append(finds, finding{m[0], reason})
		}
	}

	sort.SliceStable(finds, func(i, j int) bool { return finds[i].pos < finds[j].pos })
	var notes []finding
	type onLine struct {
		start  int
		reason string
	}
	seen := map[onLine]bool{}
	for _, f := range finds {
		key := onLine{lineStart(src, f.pos), f.reason}
		if seen[key] {
			continue
		}
		seen[key] = true
		notes = append(notes, f)
		// The marker goes above the line; above the nearest line that
		// starts in code, if this one starts inside a string.
		ls := lineStart(src, f.pos)
		for sc.unsafe[ls] {
			ls = lineStart(src, ls-1)
		}
		edits = append(edits, edit{ls, ls, indentAt(src, ls) + "# piste: " + f.reason + "\n"})
	}
	// After the markers, so a marker and a "# " at the same offset come out
	// in that order. Each non-blank line of the binding starts with "# ",
	// after its indentation.
	for _, st := range commentOut {
		for ls := lineStart(src, st.start); ls <= st.end; ls = lineEnd(src, ls) + 1 {
			if i := ls + len(indentAt(src, ls)); i < lineEnd(src, ls) {
				edits = append(edits, edit{i, i, "# "})
			}
		}
	}

	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].end < edits[j].end
	})
	var buf bytes.Buffer
	buf.WriteString(Header)
	prev := 0
	for _, e := range edits {
		buf.Write(src[prev:e.start])
		buf.WriteString(e.text)
		prev = e.end
	}
	buf.Write(src[prev:])
	out := buf.Bytes()

	var lines []string
	for _, f := range notes {
		// Where the token ended up: its offset moves by every edit before it.
		at := f.pos
		for _, e := range edits {
			if e.end <= f.pos {
				at += len(e.text) - (e.end - e.start)
			}
		}
		line := 1 + bytes.Count(out[:len(Header)+at], []byte("\n"))
		lines = append(lines, fmt.Sprintf("%s:%d: %s", rel, line, f.reason))
	}
	return out, lines
}

// A span is a binding in src: the offset of its first token, and of the ;
// that ends it.
type span struct{ start, end int }

// moduleSites are what carry takes out or marks at a module's top level, in
// code (scan's view): the networking.hostName bindings it comments out
// (hostNames: directly in the module's attribute set or its config = { … },
// dotted or in their networking = { … }), the hostNames it only marks
// (liveHostNames, the offsets of their names: an inherit, or one in a set
// the module's value or its config's is made of (a lib.mkMerge [ … ], a
// lib.mkIf … { … }) or its networking's (rec { … }, with …; { … })), and
// the channel settings (channels: nixPath and channel.enable in such a
// set's nix = { … }). A binding's value is entered no further: a
// container's or a specialisation's name is its own.
type moduleSites struct {
	hostNames     []span
	liveHostNames []int
	channels      []int
}

// topLevelSites reads moduleSites from a file's code: its module's own
// attribute set (moduleBody), and the other sets its value is made of.
func topLevelSites(code []byte) moduleSites {
	var s moduleSites
	open, _, isModule := moduleBody(code)
	for _, set := range reachableSets(code, 0, len(code)) {
		body := isModule && set[0] == open
		s.set(code, set, body, body)
	}
	sort.Slice(s.hostNames, func(i, j int) bool { return s.hostNames[i].start < s.hostNames[j].start })
	return s
}

// set reads the bindings of a top-level set: own, the module's own
// definitions (its attribute set or its config = { … }), whose hostName is
// taken out; else only marked. body: the module's attribute set, where
// config is the module's config.
func (s *moduleSites) set(code []byte, set [2]int, own, body bool) {
	for _, b := range setBindings(code, set[0], set[1]) {
		if b.inherit {
			continue
		}
		names := b.names
		if body && names[0] == "config" {
			if len(names) == 1 {
				sets, plain := bindingSets(code, b)
				for _, in := range sets {
					s.set(code, in, plain, false)
				}
				continue
			}
			names = names[1:]
		}
		switch {
		case len(names) >= 2 && names[0] == "networking" && names[1] == "hostName":
			s.hostName(span{b.start, b.end}, own)
		case len(names) == 1 && names[0] == "networking":
			sets, plain := bindingSets(code, b)
			for _, in := range sets {
				s.networking(code, in, own && plain)
			}
		case len(names) == 1 && names[0] == "nix":
			if sets, plain := bindingSets(code, b); plain {
				s.nix(code, sets[0])
			}
		}
	}
}

// networking reads a top-level networking = { … }: its hostName is taken
// out when own, else marked, and an inherited one is marked.
func (s *moduleSites) networking(code []byte, set [2]int, own bool) {
	for _, b := range setBindings(code, set[0], set[1]) {
		switch {
		case b.inherit:
			for i, n := range b.names {
				if n == "hostName" {
					s.liveHostNames = append(s.liveHostNames, b.nameAt[i])
				}
			}
		case b.names[0] == "hostName":
			s.hostName(span{b.start, b.end}, own)
		}
	}
}

// hostName is a networking.hostName binding at the top level: taken out
// when own, else marked.
func (s *moduleSites) hostName(st span, own bool) {
	if own {
		s.hostNames = append(s.hostNames, st)
	} else {
		s.liveHostNames = append(s.liveHostNames, st.start)
	}
}

// nix reads a top-level nix = { … }: its nixPath and channel.enable.
func (s *moduleSites) nix(code []byte, set [2]int) {
	for _, b := range setBindings(code, set[0], set[1]) {
		if !b.inherit && (b.names[0] == "nixPath" || len(b.names) >= 2 && b.names[0] == "channel" && b.names[1] == "enable") {
			s.channels = append(s.channels, b.start)
		}
	}
}

// optionBindings are the bindings of an option in code (scan's view):
// dotted, matched by dotted (system.autoUpgrade = ...;, and with more of
// its path, system.autoUpgrade.enable = ...;) anywhere, and name (with any
// more of its path) directly inside its parent's block, matched by block
// (system = { autoUpgrade.enable = ...; }). Ordered by offset.
func optionBindings(code []byte, dotted, block *regexp.Regexp, name string) []span {
	var out []span
	add := func(start, eq int) {
		if eq < 0 || eq+1 < len(code) && code[eq+1] == '=' {
			return // not a binding, or ==, a comparison
		}
		if end := bindingEnd(code, eq+1); end >= 0 {
			out = append(out, span{start, end})
		}
	}
	for _, m := range dotted.FindAllSubmatchIndex(code, -1) {
		add(m[4], pathEq(code, m[1]))
	}
	for _, m := range block.FindAllIndex(code, -1) {
		open := m[1] - 1
		end := matchBrace(code, open)
		if end < 0 {
			continue
		}
		depth := 0
		for i := open + 1; i < end; i++ {
			c := code[i]
			switch {
			case c == '{' || c == '[' || c == '(':
				depth++
			case c == '}' || c == ']' || c == ')':
				depth--
			case isIdentStart(c):
				j := skipIdent(code, i)
				if depth == 0 && string(code[i:j]) == name && bindingStarts(code, i) {
					if k := pathEq(code, j); k >= 0 && k < end {
						add(i, k)
					}
				}
				i = j - 1
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// pathEq returns the offset of the = that follows an attribute path's
// first names, which end at i in code, after any more of its names
// (.enable), or -1 when no = does.
func pathEq(code []byte, i int) int {
	for {
		k := skipSpace(code, i)
		switch {
		case k < len(code) && code[k] == '=':
			return k
		case k < len(code) && code[k] == '.':
			if k = skipSpace(code, k+1); k < len(code) && isIdentStart(code[k]) {
				i = skipIdent(code, k)
				continue
			}
		}
		return -1
	}
}

// bindingStarts: a binding can start at code[i] (after the set's { or the
// last binding's ;), so a name there is the one bound, not a read.
func bindingStarts(code []byte, i int) bool {
	j := i - 1
	for j >= 0 && (code[j] == ' ' || code[j] == '\t' || code[j] == '\n' || code[j] == '\r') {
		j--
	}
	return j < 0 || code[j] == '{' || code[j] == ';'
}

// bindingEnd returns the offset of the ; that ends a binding whose value
// starts at i in code, or -1: the first ; outside brackets that isn't a
// let's binding's or a with's or assert's own.
func bindingEnd(code []byte, i int) int {
	depth, lets, pending := 0, 0, 0
	for i < len(code) {
		c := code[i]
		switch {
		case isIdentStart(c):
			j := skipIdent(code, i)
			if depth == 0 {
				switch string(code[i:j]) {
				case "let":
					lets++
				case "in":
					if lets > 0 {
						lets--
					}
				case "with", "assert":
					pending++
				}
			}
			i = j
			continue
		case c == '{' || c == '[' || c == '(':
			depth++
		case c == '}' || c == ']' || c == ')':
			if depth == 0 {
				return -1 // the set ends first: no ; of its own
			}
			depth--
		case c == ';' && depth == 0:
			switch {
			case pending > 0:
				pending--
			case lets == 0:
				return i
			}
		}
		i++
	}
	return -1
}

// ownLines: the binding st has its lines to itself (only blanks before it on
// its first line, and after its ; only blanks or a # comment), so
// commenting those lines out takes nothing else with it.
func ownLines(src []byte, sc scanned, st span) bool {
	ls := lineStart(src, st.start)
	if sc.unsafe[ls] || strings.TrimSpace(string(src[ls:st.start])) != "" {
		return false
	}
	rest := strings.TrimSpace(string(src[st.end+1 : lineEnd(src, st.end)]))
	return rest == "" || strings.HasPrefix(rest, "#")
}

// pathProblem says why the path literal tok, written in a file in dir, does
// not come along ("" if it does).
func (c *carrier) pathProblem(dir, tok string) string {
	outside := tok + " is outside /etc/nixos, so it isn't carried over"
	if strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~") {
		return outside
	}
	target := path.Join(dir, tok)
	if target == ".." || strings.HasPrefix(target, "../") {
		return outside
	}
	if c.carried[target] {
		return ""
	}
	for rel := range c.carried { // a directory with carried files in it
		if target == "." || strings.HasPrefix(rel, target+"/") {
			return ""
		}
	}
	if _, err := os.Lstat(filepath.Join(c.root, filepath.FromSlash(target))); err != nil {
		return "" // not there either way: the build will say so
	}
	return tok + " isn't carried over (only .nix files are)"
}

// modulesPathFix makes the rewritten lookups bind: it returns the edit that
// adds modulesPath to the file's top-level arguments, or a finding when it
// can't, or neither when the file already takes modulesPath.
func modulesPathFix(src, code []byte) (*edit, *finding) {
	top := topLevel(code)
	switch top.shape {
	case formalsShape:
		if formalRe.Match(code[top.open : top.close+1]) {
			return nil, nil
		}
		e := formalEdit(src, top.open)
		return &e, nil
	case attrsShape:
		// Not a function: make it one that takes modulesPath.
		return &edit{top.first, top.first, "{ modulesPath, ... }:\n"}, nil
	}
	return nil, &finding{top.first, "modulesPath is used here but is not an argument of this file; add it to the file's arguments"}
}
