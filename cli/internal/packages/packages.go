// Package packages searches nixpkgs / Homebrew and edits Nix package lists
// in place.
package packages

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/execx"
)

// Match is one search hit.
type Match struct{ Attr, Name, Version string }

// SearchNixpkgs is `nix search nixpkgs#<name> --json`, in output order.
func SearchNixpkgs(ctx context.Context, r execx.Runner, name string) []Match {
	res, err := r.Run(ctx, execx.Cmd{Argv: []string{"nix", "search", "nixpkgs#" + name, "--json"}})
	if err != nil || strings.TrimSpace(string(res.Stdout)) == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(res.Stdout)))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var out []Match
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return out
		}
		attr := t.(string)
		var info map[string]any
		if dec.Decode(&info) != nil {
			return nil
		}
		m := Match{Attr: attr, Name: attr, Version: "?"}
		if s, ok := info["pname"].(string); ok {
			m.Name = s
		}
		if s, ok := info["version"].(string); ok {
			m.Version = s
		}
		out = append(out, m)
	}
	return out
}

// ExactInNixpkgs is `nix eval nixpkgs#<name>.name` succeeding.
func ExactInNixpkgs(ctx context.Context, r execx.Runner, name string) bool {
	_, err := r.Run(ctx, execx.Cmd{Argv: []string{"nix", "eval", "nixpkgs#" + name + ".name"}})
	return err == nil
}

func brewLines(ctx context.Context, r execx.Runner, argv ...string) []string {
	res, err := r.Run(ctx, execx.Cmd{Argv: argv})
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// SearchBrew / SearchBrewCask are `brew search [--casks] <name>`.
func SearchBrew(ctx context.Context, r execx.Runner, name string) []string {
	return brewLines(ctx, r, "brew", "search", name)
}
func SearchBrewCask(ctx context.Context, r execx.Runner, name string) []string {
	return brewLines(ctx, r, "brew", "search", "--casks", name)
}

// InFile reports whether "<name>" (quoted) appears in the file.
func InFile(name, path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), `"`+name+`"`)
}

// ListAt is the line of lines that opens a list for the first of cands
// to have one, as `<cand> = [` or `<cand> = with pkgs; [` (withPkgs: the
// second form); -1 when none does. cands are an option's path and its short
// opener (drift.List): each matches only where it starts an attribute path,
// in code (not a comment or a string).
func ListAt(lines []string, cands []string) (i int, withPkgs bool) {
	text := strings.Join(lines, "\n")
	code := nixCode(text)
	for _, cand := range cands {
		if o, ok := findOpener(text, code, cand, true); ok {
			return strings.Count(text[:o.bracket], "\n"), o.with
		}
	}
	return -1, false
}

// AddToNixList inserts entry, as written (`"slack"`, `ripgrep`,
// `pkgs.ripgrep`), into the list of the first candidate that opens one
// anywhere in a line (`casks` finds `homebrew.casks = [`; piste pkg add's
// candidates); false if none, or if that list can't be written (see
// AddToOptionList).
func AddToNixList(entry, path string, candidates []string) (bool, error) {
	return addToList(entry, path, candidates, false)
}

// AddToOptionList is AddToNixList for an option's list (piste adopt):
// candidates are the option's path and its short opener, each matched only
// where it starts an attribute path (ListAt's). It writes only into a list
// that is the whole value, `<opener> [ … ];` with its `]` found by depth,
// and leaves the file as it is otherwise (`[ … ] ++ more;`), so the caller
// can say to add the entry by hand.
func AddToOptionList(entry, path string, candidates []string) (bool, error) {
	return addToList(entry, path, candidates, true)
}

// addToList: the first candidate with an opener decides; its list is
// expanded if inline, and entry goes on its own line before the `]`.
func addToList(entry, path string, candidates []string, anchored bool) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(b)
	code := nixCode(text)
	for _, cand := range candidates {
		o, ok := findOpener(text, code, cand, anchored)
		if !ok {
			continue
		}
		out, ok := insertEntry(text, code, o.bracket, entry)
		if !ok {
			return false, nil
		}
		return true, os.WriteFile(path, []byte(out), 0o644)
	}
	return false, nil
}

// opener is where a candidate's list opens: the index of its `[`.
type opener struct {
	bracket int
	with    bool // `= with pkgs; [`
}

// findOpener is cand's first `<cand> = [` or `<cand> = with pkgs; [` in
// code; anchored: only where cand starts an attribute path (after the start,
// whitespace, `{`, `;` or `config.`), never after `.` or a name's character.
func findOpener(text string, code []bool, cand string, anchored bool) (opener, bool) {
	for from := 0; ; {
		k := strings.Index(text[from:], cand)
		if k < 0 {
			return opener{}, false
		}
		p := from + k
		from = p + 1
		if !code[p] || anchored && !startsPath(text, p) {
			continue
		}
		rest := skipBlank(text, p+len(cand))
		if rest >= len(text) || text[rest] != '=' || rest+1 < len(text) && text[rest+1] == '=' {
			continue
		}
		rest = skipBlank(text, rest+1)
		with := strings.HasPrefix(text[rest:], "with pkgs;")
		if with {
			rest = skipBlank(text, rest+len("with pkgs;"))
		}
		if rest < len(text) && text[rest] == '[' && code[rest] {
			return opener{rest, with}, true
		}
	}
}

// startsPath: an attribute path starts at p (`config.` may lead it).
func startsPath(text string, p int) bool {
	if strings.HasSuffix(text[:p], "config.") {
		p -= len("config.")
	}
	return p == 0 || strings.ContainsRune(" \t\r\n{;", rune(text[p-1]))
}

func skipBlank(text string, i int) int {
	for i < len(text) && (text[i] == ' ' || text[i] == '\t') {
		i++
	}
	return i
}

// insertEntry puts entry on its own line before the `]` matching the `[` at
// open, expanding an inline list; false unless that `]` ends the value
// (`;` follows it).
func insertEntry(text string, code []bool, open int, entry string) (string, bool) {
	depth, end := 0, -1
	for i := open; i < len(text) && end < 0; i++ {
		if !code[i] {
			continue
		}
		switch text[i] {
		case '[':
			depth++
		case ']':
			if depth--; depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		return "", false
	}
	if j := skipBlank(text, end+1); j >= len(text) || text[j] != ';' {
		return "", false
	}
	lineStart := func(i int) int { return strings.LastIndexByte(text[:i], '\n') + 1 }
	indentAt := func(i int) string {
		l := text[i:]
		return l[:len(l)-len(strings.TrimLeft(l, " \t"))]
	}
	openLine, endLine := lineStart(open), lineStart(end)
	indent := indentAt(openLine)
	if openLine == endLine { // inline: one line each for what it had and entry
		lines := []string{text[openLine : open+1]}
		if inner := strings.TrimSpace(text[open+1 : end]); inner != "" {
			lines = append(lines, indent+"  "+inner)
		}
		lines = append(lines, indent+"  "+entry, indent+text[end:])
		return text[:openLine] + strings.Join(lines, "\n"), true
	}
	if before := text[endLine:end]; strings.TrimSpace(before) != "" { // `  last ];`
		item := indentAt(endLine)
		return strings.TrimRight(text[:end], " \t") + "\n" + item + entry + "\n" + indent + text[end:], true
	}
	// The items' indent: the last non-blank line's, or the `]`'s and two
	// more when the list has none.
	item := indentAt(endLine) + "  "
	for l := endLine - 1; l > openLine; {
		s := lineStart(l)
		if strings.TrimSpace(text[s:l]) != "" {
			if s != openLine {
				item = indentAt(s)
			}
			break
		}
		l = s - 1
	}
	return text[:endLine] + item + entry + "\n" + text[endLine:], true
}

// nixCode marks the bytes of text that are Nix code: not in a comment
// (`#`, `/* */`) or a string, double-quoted or indented; a string's
// `${ }` is code.
func nixCode(text string) []bool {
	code := make([]bool, len(text))
	const (
		inCode = iota
		inLine
		inBlock
		inStr
		inInd
	)
	state := inCode
	type frame struct{ str, depth int } // an interpolation: the string to return to
	var stack []frame
	for i := 0; i < len(text); i++ {
		c := text[i]
		next := byte(0)
		if i+1 < len(text) {
			next = text[i+1]
		}
		switch state {
		case inCode:
			switch {
			case c == '#':
				state = inLine
			case c == '/' && next == '*':
				state, i = inBlock, i+1
			case c == '"':
				state = inStr
			case c == '\'' && next == '\'':
				state, i = inInd, i+1
			case c == '{' && len(stack) > 0:
				stack[len(stack)-1].depth++
				code[i] = true
			case c == '}' && len(stack) > 0:
				if top := &stack[len(stack)-1]; top.depth > 0 {
					top.depth--
					code[i] = true
				} else {
					state, stack = top.str, stack[:len(stack)-1]
				}
			default:
				code[i] = true
			}
		case inLine:
			if c == '\n' {
				state, code[i] = inCode, true
			}
		case inBlock:
			if c == '*' && next == '/' {
				state, i = inCode, i+1
			}
		case inStr:
			switch {
			case c == '\\':
				i++
			case c == '"':
				state = inCode
			case c == '$' && next == '{':
				stack, state, i = append(stack, frame{inStr, 0}), inCode, i+1
			}
		case inInd:
			switch {
			case c == '\'' && next == '\'':
				if i+2 < len(text) && (text[i+2] == '\'' || text[i+2] == '$') {
					i += 2
				} else if i+2 < len(text) && text[i+2] == '\\' {
					i += 3
				} else {
					state, i = inCode, i+1
				}
			case c == '$' && next == '{':
				stack, state, i = append(stack, frame{inInd, 0}), inCode, i+1
			}
		}
	}
	return code
}

// PnpmListName is the nix let-binding that declares pnpm globals.
const PnpmListName = "pnpmPackages"

var pnpmList = regexp.MustCompile(`(?m)^\s*` + PnpmListName + `\s*=\s*\[`)

// FindPnpmGlobalsFiles: modules/**/*.nix declaring pnpmPackages, with
// js-globals.nix first, then by name.
func FindPnpmGlobalsFiles(flakeDir string) []string {
	mods := filepath.Join(flakeDir, "modules")
	var found []string
	filepath.WalkDir(mods, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".nix") {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && pnpmList.Match(b) {
			found = append(found, p)
		}
		return nil
	})
	sort.Strings(found) // rglob sorted by path first
	sort.SliceStable(found, func(i, j int) bool {
		pi, pj := filepath.Base(found[i]) != "js-globals.nix", filepath.Base(found[j]) != "js-globals.nix"
		if pi != pj {
			return !pi
		}
		return filepath.Base(found[i]) < filepath.Base(found[j])
	})
	return found
}

// AddToPnpmGlobals adds to the pnpmPackages list of path.
func AddToPnpmGlobals(name, path string) (bool, error) {
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return false, nil
	}
	return AddToNixList(`"`+name+`"`, path, []string{PnpmListName})
}
