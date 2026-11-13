package machine

import "bytes"

// scanned is a lexical view of Nix source: just enough of the language to
// tell code from comments and string text, so a `<nixpkgs/...>` in a comment
// or a string is left alone.
type scanned struct {
	// code is src with every comment and all string text (quotes
	// included) overwritten by spaces. It is as long as src, so an offset in
	// code is an offset in src, and newlines stay where they were. The
	// ${...} of an interpolation is code and is kept.
	code []byte
	// unsafe holds the offsets of the lines that start inside a string or a
	// block comment, where a comment line can't be inserted.
	unsafe map[int]bool
}

type frameKind int

const (
	quoted   frameKind = iota // "..."
	indented                  // ''...''
	interp                    // ${...} in either; depth counts its open braces
)

type frame struct {
	kind  frameKind
	depth int
}

func scan(src []byte) scanned {
	code := bytes.Clone(src)
	unsafe := map[int]bool{}
	var stack []frame
	n := len(src)

	blank := func(i int) {
		if code[i] != '\n' {
			code[i] = ' '
		}
	}
	// pass blanks src[i] and notes a line starting inside a literal.
	pass := func(i int) {
		blank(i)
		if src[i] == '\n' {
			unsafe[i+1] = true
		}
	}
	at := func(i int, c byte) bool { return i < n && src[i] == c }

	for i := 0; i < n; {
		if len(stack) > 0 && stack[len(stack)-1].kind != interp {
			// Inside a string.
			top := stack[len(stack)-1]
			c := src[i]
			switch {
			case c == '$' && at(i+1, '$'): // literal: "$${x}" is the text $${x
				pass(i)
				pass(i + 1)
				i += 2
			case c == '$' && at(i+1, '{'):
				blank(i)
				blank(i + 1)
				stack = append(stack, frame{kind: interp})
				i += 2
			case top.kind == quoted && c == '\\' && i+1 < n:
				pass(i)
				pass(i + 1)
				i += 2
			case top.kind == quoted && c == '"':
				blank(i)
				stack = stack[:len(stack)-1]
				i++
			case top.kind == indented && c == '\'' && at(i+1, '\''):
				switch {
				case at(i+2, '\'') || at(i+2, '$'): // ''' and ''$ are escapes
					blank(i)
					blank(i + 1)
					blank(i + 2)
					i += 3
				case at(i+2, '\\'): // ''\x is an escape
					blank(i)
					blank(i + 1)
					blank(i + 2)
					i += 3
					if i < n {
						pass(i)
						i++
					}
				default: // the closing ''
					blank(i)
					blank(i + 1)
					stack = stack[:len(stack)-1]
					i += 2
				}
			default:
				pass(i)
				i++
			}
			continue
		}

		// Code: the top level, or inside an interpolation.
		c := src[i]
		switch {
		case c == '\n':
			if len(stack) > 0 {
				unsafe[i+1] = true
			}
			i++
		case c == '#':
			for i < n && src[i] != '\n' {
				blank(i)
				i++
			}
		case c == '/' && at(i+1, '*'):
			end := bytes.Index(src[i+2:], []byte("*/"))
			if end < 0 {
				end = n
			} else {
				end += i + 4
			}
			for ; i < end; i++ {
				pass(i)
			}
		case c == '"':
			blank(i)
			stack = append(stack, frame{kind: quoted})
			i++
		case c == '\'' && at(i+1, '\''):
			blank(i)
			blank(i + 1)
			stack = append(stack, frame{kind: indented})
			i += 2
		case isIdentStart(c):
			// An identifier may contain quotes (x'' is one name, not the
			// start of a string).
			i = skipIdent(src, i)
		case c == '{' && len(stack) > 0:
			stack[len(stack)-1].depth++
			i++
		case c == '}' && len(stack) > 0:
			if top := &stack[len(stack)-1]; top.depth > 0 {
				top.depth--
			} else {
				blank(i) // the end of the interpolation
				stack = stack[:len(stack)-1]
			}
			i++
		default:
			i++
		}
	}
	return scanned{code: code, unsafe: unsafe}
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '\'' || c == '-'
}

// skipIdent returns the index just past the identifier starting at i.
func skipIdent(b []byte, i int) int {
	for i < len(b) && isIdentChar(b[i]) {
		i++
	}
	return i
}

// skipSpace returns the index of the first byte of b at or after i that
// is not whitespace.
func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// matchBrace returns the index of the } closing the { at open in code, or
// -1.
func matchBrace(code []byte, open int) int {
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

type topShape int

const (
	otherShape   topShape = iota // anything else: a lambda, an import...
	formalsShape                 // { formals }: body, { formals }@x: body or x@{ formals }: body
	attrsShape                   // an attribute set, possibly after let...in or with...;
)

// top is what a file's expression starts with.
type top struct {
	shape       topShape
	first       int // the first byte of code
	open, close int // the formals' braces, for a formalsShape
}

// topLevel looks at the start of a file's code (code is a scanned view).
func topLevel(code []byte) top {
	n := len(code)
	t := top{shape: otherShape, first: skipSpace(code, 0)}
	// A let...in or with...; in front of an attribute set doesn't make the
	// file a function, so it can take a function in front of it. In front
	// of formals it would be a function inside a let: not touched.
	i, prologue := t.first, false
	for {
		next, ok := skipPrologue(code, i)
		if !ok {
			break
		}
		i, prologue = next, true
	}
	named := false // x@{ ... }
	if i < n && isIdentStart(code[i]) {
		end := skipIdent(code, i)
		if string(code[i:end]) == "rec" {
			if j := skipSpace(code, end); j < n && code[j] == '{' && matchBrace(code, j) >= 0 {
				t.shape = attrsShape
			}
			return t
		}
		i = skipSpace(code, end)
		if i >= n || code[i] != '@' {
			return t // x: ..., import ...
		}
		i = skipSpace(code, i+1)
		named = true
	}
	if i >= n || code[i] != '{' {
		return t
	}
	closing := matchBrace(code, i)
	if closing < 0 {
		return t
	}
	k := skipSpace(code, closing+1)
	switch {
	case k < n && code[k] == ':':
	case !named && k < n && code[k] == '@': // { ... }@x:
		k = skipSpace(code, k+1)
		if k >= n || !isIdentStart(code[k]) {
			return t
		}
		if k = skipSpace(code, skipIdent(code, k)); k >= n || code[k] != ':' {
			return t
		}
	case !named:
		t.shape = attrsShape
		return t
	default:
		return t
	}
	if prologue {
		return t
	}
	t.shape, t.open, t.close = formalsShape, i, closing
	return t
}

// wordAt returns the keyword-eligible identifier at code[i] and where it
// ends; end is i when there is no identifier there. An identifier that is
// a segment of a path (./in/x) is not eligible.
func wordAt(code []byte, i int) (word string, end int) {
	if i >= len(code) || !isIdentStart(code[i]) {
		return "", i
	}
	end = skipIdent(code, i)
	if i > 0 && (isIdentChar(code[i-1]) || code[i-1] == '.' || code[i-1] == '/' || code[i-1] == '+') ||
		end < len(code) && code[end] == '/' {
		return "", end
	}
	return string(code[i:end]), end
}

// skipPrologue returns where the expression after a leading `let ... in`
// or `with ...;` starts, if code[i] starts one.
func skipPrologue(code []byte, i int) (int, bool) {
	w, end := wordAt(code, i)
	switch w {
	case "let":
		depth := 0
		for j := i; j < len(code); {
			w, e := wordAt(code, j)
			if e == j {
				j++
				continue
			}
			switch w {
			case "let":
				depth++
			case "in":
				if depth--; depth == 0 {
					return skipSpace(code, e), true
				}
			}
			j = e
		}
	case "with":
		brackets, lets := 0, 0
		for j := end; j < len(code); {
			w, e := wordAt(code, j)
			if e > j {
				switch w {
				case "let":
					lets++
				case "in":
					lets--
				}
				j = e
				continue
			}
			switch code[j] {
			case '(', '[', '{':
				brackets++
			case ')', ']', '}':
				brackets--
			case ';':
				if brackets == 0 && lets <= 0 {
					return skipSpace(code, j+1), true
				}
			}
			j++
		}
	}
	return i, false
}

// formalEdit adds modulesPath to the formals opened at src[open]: on its
// own line when they are one per line, else first on the line.
func formalEdit(src []byte, open int) edit {
	k := open + 1
	for k < len(src) && (src[k] == ' ' || src[k] == '\t') {
		k++
	}
	switch {
	case k >= len(src) || src[k] == '\n' || src[k] == '\r':
		indent := "  "
		for l := k; l < len(src); {
			next := bytes.IndexByte(src[l:], '\n')
			if next < 0 {
				break
			}
			l += next + 1
			if line := src[l:]; len(bytes.TrimSpace(firstLine(line))) > 0 {
				indent = indentAt(src, l)
				break
			}
		}
		return edit{open + 1, open + 1, "\n" + indent + "modulesPath,"}
	case src[k] == '}': // no formals yet
		return edit{k, k, "modulesPath "}
	}
	return edit{k, k, "modulesPath, "}
}

func firstLine(b []byte) []byte {
	if i := bytes.IndexByte(b, '\n'); i >= 0 {
		return b[:i]
	}
	return b
}

// lineStart returns the offset of the start of the line holding src[pos].
func lineStart(src []byte, pos int) int {
	return bytes.LastIndexByte(src[:pos], '\n') + 1
}

// lineEnd returns the offset of the newline ending the line holding
// src[pos] (len(src) on the last line).
func lineEnd(src []byte, pos int) int {
	if i := bytes.IndexByte(src[pos:], '\n'); i >= 0 {
		return pos + i
	}
	return len(src)
}

// indentAt returns the spaces and tabs that start the line at ls.
func indentAt(src []byte, ls int) string {
	i := ls
	for i < len(src) && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return string(src[ls:i])
}

// blankRange overwrites b[i:j] with spaces, keeping newlines.
func blankRange(b []byte, i, j int) {
	for ; i < j; i++ {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
}

// A setBinding is one binding directly in an attribute set (scan's view):
// path = value; (names: its attribute path's names, "" for one that isn't
// a plain identifier, quoted or interpolated) or inherit … names;
// (inherit, and nameAt: where each name is). start is its first byte, eq
// its = (-1 for an inherit), end the ; that ends it.
type setBinding struct {
	names          []string
	nameAt         []int
	inherit        bool
	start, eq, end int
}

// setBindings are the bindings directly in the attribute set whose braces
// are at open and close in code (scan's view), in order, up to the first
// it can't read (one with no = or ; of its own: half-typed Nix).
func setBindings(code []byte, open, close int) []setBinding {
	var out []setBinding
	for i := skipSpace(code, open+1); i < close; i = skipSpace(code, i) {
		if w, e := wordAt(code, i); w == "inherit" {
			b := setBinding{inherit: true, start: i, eq: -1, end: -1}
			depth := 0
			for j := e; j < close && b.end < 0; j++ {
				switch c := code[j]; {
				case c == '(' || c == '[' || c == '{':
					depth++
				case c == ')' || c == ']' || c == '}':
					depth--
				case c == ';' && depth == 0:
					b.end = j
				case depth == 0 && isIdentStart(c):
					k := skipIdent(code, j)
					b.names, b.nameAt = append(b.names, string(code[j:k])), append(b.nameAt, j)
					j = k - 1
				}
			}
			if b.end < 0 {
				return out
			}
			out = append(out, b)
			i = b.end + 1
			continue
		}
		eq := bytes.IndexByte(code[i:close], '=')
		if eq < 0 {
			return out
		}
		eq += i
		end := bindingEnd(code, eq+1)
		if end < 0 || end >= close {
			return out
		}
		out = append(out, setBinding{names: pathNames(code[i:eq]), start: i, eq: eq, end: end})
		i = end + 1
	}
	return out
}

// pathNames are the names of an attribute path (scan's view, so a quoted
// name is blanks): each plain identifier, else "".
func pathNames(path []byte) []string {
	var names []string
	for _, p := range bytes.Split(path, []byte(".")) {
		p = bytes.TrimSpace(p)
		if len(p) > 0 && isIdentStart(p[0]) && skipIdent(p, 0) == len(p) {
			names = append(names, string(p))
		} else {
			names = append(names, "")
		}
	}
	return names
}

// bindingSets are the attribute sets a binding's value is made of
// (reachableSets), and plain: the value is one attribute set, nothing
// around it (path = { … };).
func bindingSets(code []byte, b setBinding) (sets [][2]int, plain bool) {
	if b.inherit {
		return nil, false
	}
	v := skipSpace(code, b.eq+1)
	if v < b.end && code[v] == '{' {
		if close := matchBrace(code, v); close >= 0 && skipSpace(code, close+1) == b.end {
			return [][2]int{{v, close}}, true
		}
	}
	return reachableSets(code, b.eq+1, b.end), false
}

// reachableSets are the attribute sets the expression in code[i:end]
// (scan's view) is made of, without entering any binding's value: through
// function heads, let … in and with …; (to their bodies), rec, parentheses,
// lists, function arguments, if … then … else and //. Each is the offsets
// of its braces.
func reachableSets(code []byte, i, end int) [][2]int {
	var out [][2]int
	for i < end {
		c := code[i]
		switch {
		case isIdentStart(c):
			if next, ok := skipPrologue(code, i); ok {
				if next > end {
					return out
				}
				i = next
				continue
			}
			i = skipIdent(code, i)
		case c == '{':
			close := matchBrace(code, i)
			if close < 0 || close >= end {
				return out
			}
			if k := skipSpace(code, close+1); k < len(code) && (code[k] == ':' || code[k] == '@') {
				i = close + 1 // a function's formals: their defaults are values
				continue
			}
			out = append(out, [2]int{i, close})
			i = close + 1
		default:
			i++
		}
	}
	return out
}

// moduleBody is the attribute set a module file is, past its function head
// (formals or one argument), a let … in or with …;, rec and parentheses:
// its braces' offsets in code (scan's view); ok false when the file is
// something else (a function call, an import, a list).
func moduleBody(code []byte) (open, close int, ok bool) {
	for i := skipSpace(code, 0); i < len(code); {
		if next, ok := skipPrologue(code, i); ok {
			i = next
			continue
		}
		switch c := code[i]; {
		case c == '(':
			i = skipSpace(code, i+1)
		case isIdentStart(c):
			end := skipIdent(code, i)
			j := skipSpace(code, end)
			switch {
			case string(code[i:end]) == "rec":
				i = j
			case j < len(code) && code[j] == ':': // x: body
				i = skipSpace(code, j+1)
			case j < len(code) && code[j] == '@': // x@{ formals }: body
				i = skipSpace(code, j+1)
			default:
				return 0, 0, false
			}
		case c == '{':
			close := matchBrace(code, i)
			if close < 0 {
				return 0, 0, false
			}
			k := skipSpace(code, close+1)
			if k < len(code) && code[k] == '@' { // { formals }@x: body
				k = skipSpace(code, skipIdent(code, skipSpace(code, k+1)))
			}
			if k < len(code) && code[k] == ':' {
				i = skipSpace(code, k+1)
				continue
			}
			return i, close, true
		default:
			return 0, 0, false
		}
	}
	return 0, 0, false
}
