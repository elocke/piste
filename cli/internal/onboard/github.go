package onboard

// piste github's logic that needs no terminal: gh and ssh-keygen through an
// execx.Runner (tests fake it), key comparison, and the edit of a host file's
// piste.github block.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/elocke/piste/cli/internal/execx"
)

// Account is a GitHub account: its login and numeric id (the noreply
// address is <id>+<login>@users.noreply.github.com).
type Account struct {
	Login string
	ID    int64
}

// Noreply is the account's commit email.
func (a Account) Noreply() string {
	return fmt.Sprintf("%d+%s@users.noreply.github.com", a.ID, a.Login)
}

var loginRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// CheckLogin: a GitHub login (it ends up in a path and in nix).
func CheckLogin(l string) error {
	if !loginRE.MatchString(l) {
		return fmt.Errorf("%q isn't a GitHub login", l)
	}
	return nil
}

// KeyKind is how GitHub uses a key: to sign in over SSH, or to verify the
// commits it signed.
type KeyKind string

const (
	Auth    KeyKind = "authentication"
	Signing KeyKind = "signing"
)

func (k KeyKind) listPath() string {
	if k == Signing {
		return "user/ssh_signing_keys?per_page=100"
	}
	return "user/keys?per_page=100"
}

// ScopeRefresh is the command that gives gh the scopes it needs to manage keys.
const ScopeRefresh = "gh auth refresh -h github.com -s admin:public_key,admin:ssh_signing_key"

// LoginFix is how to log gh in the way piste github expects.
const LoginFix = "gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key"

// ScopeError: gh was refused for want of a token scope.
type ScopeError struct{ Scope, Out string }

func (e *ScopeError) Error() string { return "gh needs the " + e.Scope + " scope" }

// ErrKeyInUse: GitHub has the key on another account (an authentication key
// belongs to one account).
var ErrKeyInUse = errors.New("the key is already in use on another account")

// GH runs gh through R. Token, when set, is GH_TOKEN for every call: the
// account to act as when it isn't gh's active one.
type GH struct {
	R     execx.Runner
	Token string
}

func (g GH) run(ctx context.Context, argv ...string) (stdout, all string, err error) {
	c := execx.Cmd{Argv: argv}
	if g.Token != "" {
		c.Env = []string{"GH_TOKEN=" + g.Token}
	}
	res, err := g.R.Run(ctx, c)
	all = strings.TrimSpace(string(res.Stdout) + "\n" + string(res.Stderr))
	if err != nil {
		if s := scopeIn(all); s != "" {
			return "", all, &ScopeError{Scope: s, Out: all}
		}
		return "", all, err
	}
	return string(res.Stdout), all, nil
}

func scopeIn(out string) string {
	for _, s := range []string{"admin:public_key", "admin:ssh_signing_key"} {
		if strings.Contains(out, s) {
			return s
		}
	}
	return ""
}

// AuthOK: gh is logged in to github.com.
func (g GH) AuthOK(ctx context.Context) error {
	_, all, err := g.run(ctx, "gh", "auth", "status", "--hostname", "github.com")
	if err != nil {
		return fmt.Errorf("gh isn't logged in to github.com: %s", lastLine(all))
	}
	return nil
}

// User is the account the token belongs to (gh api user).
func (g GH) User(ctx context.Context) (Account, error) {
	out, all, err := g.run(ctx, "gh", "api", "user")
	if err != nil {
		return Account{}, fmt.Errorf("gh api user: %s", lastLine(all))
	}
	var u struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &u); err != nil || u.Login == "" || u.ID == 0 {
		return Account{}, errors.New("gh api user: no login and id in its answer")
	}
	if err := CheckLogin(u.Login); err != nil {
		return Account{}, err
	}
	return Account{Login: u.Login, ID: u.ID}, nil
}

// TokenFor is the token gh holds for login (gh auth token).
func (g GH) TokenFor(ctx context.Context, login string) (string, error) {
	out, all, err := g.run(ctx, "gh", "auth", "token", "--hostname", "github.com", "--user", login)
	tok := strings.TrimSpace(out)
	if err != nil || tok == "" {
		return "", fmt.Errorf("gh has no login for %s (%s)", login, lastLine(all))
	}
	return tok, nil
}

// Keys is the key material (type and base64) of the account's keys of kind.
func (g GH) Keys(ctx context.Context, kind KeyKind) ([]string, error) {
	out, all, err := g.run(ctx, "gh", "api", kind.listPath())
	if err != nil {
		var se *ScopeError
		if errors.As(err, &se) {
			return nil, err
		}
		return nil, fmt.Errorf("gh api %s: %s", kind.listPath(), lastLine(all))
	}
	var keys []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(out), &keys); err != nil {
		return nil, fmt.Errorf("gh api %s: %w", kind.listPath(), err)
	}
	var m []string
	for _, k := range keys {
		if x := KeyMaterial(k.Key); x != "" {
			m = append(m, x)
		}
	}
	return m, nil
}

// Add uploads the public key file as kind.
func (g GH) Add(ctx context.Context, pubPath string, kind KeyKind, title string) error {
	_, all, err := g.run(ctx, "gh", "ssh-key", "add", pubPath, "--type", string(kind), "--title", title)
	if err == nil {
		return nil
	}
	var se *ScopeError
	if errors.As(err, &se) {
		return err
	}
	if strings.Contains(strings.ToLower(all), "already in use") {
		return ErrKeyInUse
	}
	return fmt.Errorf("gh ssh-key add (%s): %s", kind, lastLine(all))
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// KeyMaterial is the type and base64 of a public key line ("ssh-ed25519
// AAAA… comment"), without its comment: what GitHub compares. "" for a line
// that isn't one.
func KeyMaterial(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 || !strings.HasPrefix(f[0], "ssh-") && !strings.HasPrefix(f[0], "ecdsa-") && !strings.HasPrefix(f[0], "sk-") {
		return ""
	}
	return f[0] + " " + f[1]
}

// Has: material is among keys.
func Has(keys []string, material string) bool {
	for _, k := range keys {
		if k == material {
			return true
		}
	}
	return false
}

// sshKeygenErr words a failed ssh-keygen. With no output (the binary is
// missing: a container, a minimal image) it says what to do, since piste no
// longer carries its own OpenSSH.
func sshKeygenErr(what string, res execx.Result, err error) error {
	if out := lastLine(string(res.Stderr) + "\n" + string(res.Stdout)); out != "" {
		return fmt.Errorf("%s: %s", what, out)
	}
	if err == nil {
		err = errors.New("no key printed")
	}
	return fmt.Errorf("%s: %v (is OpenSSH's client installed? piste uses the system's ssh-keygen)", what, err)
}

// Keygen makes the key at path: ed25519, no passphrase (one can be added with
// ssh-keygen -p -f <path>).
func Keygen(ctx context.Context, r execx.Runner, path, login string) error {
	res, err := r.Run(ctx, execx.Cmd{Argv: []string{"ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", login + "@github", "-f", path}})
	if err != nil {
		return sshKeygenErr("ssh-keygen", res, err)
	}
	return nil
}

// PublicKey derives the public key line from the private key at path
// (ssh-keygen -y), for a key whose .pub is missing.
func PublicKey(ctx context.Context, r execx.Runner, path string) (string, error) {
	res, err := r.Run(ctx, execx.Cmd{Argv: []string{"ssh-keygen", "-y", "-f", path}})
	line := strings.TrimSpace(string(res.Stdout))
	if err != nil || KeyMaterial(line) == "" {
		return "", sshKeygenErr("ssh-keygen -y -f "+path, res, err)
	}
	return line, nil
}

// --- the overlay's host file ---------------------------------------------------

// ErrNoAnchor: the host file has no place piste knows to put the block.
var ErrNoAnchor = errors.New("no place piste can safely put piste.github in this file")

// DefaultKey is where a login's key lives under home.
func DefaultKey(home, login string) string { return home + "/.ssh/id_ed25519_github_" + login }

// Block is the piste.github block for the account, indented by indent; key
// is written only when it isn't the login's default under home.
func Block(indent string, acct Account, key, home string) string {
	var b strings.Builder
	b.WriteString(indent + "piste.github = {\n")
	fmt.Fprintf(&b, "%s  login = %s;\n", indent, strconv.Quote(acct.Login))
	fmt.Fprintf(&b, "%s  id = %d;\n", indent, acct.ID)
	if key != DefaultKey(home, acct.Login) {
		fmt.Fprintf(&b, "%s  key = %s;\n", indent, strconv.Quote(key))
	}
	b.WriteString(indent + "};\n")
	return b.String()
}

var (
	githubBlock = regexp.MustCompile(`(?m)^([ \t]*)piste\.github[ \t]*=[ \t]*\{`)
	githubAny   = regexp.MustCompile(`(?m)^[ \t]*piste\.github\b`)
	checkoutRE  = regexp.MustCompile(`(?m)^([ \t]*)piste\.checkout[ \t]*=[^;\n]*;[ \t]*\n`)
)

// HasGithub: the host file already sets piste.github in some form.
func HasGithub(src []byte) bool { return githubAny.Match(src) }

// SetGithub puts the account's block in a host file: it replaces the file's
// own piste.github block, else goes right after its piste.checkout
// line (the starter's host files have one in the home-manager user block, on
// Linux and on a Mac), else into the home's attrset (insertInHome). changed
// is false when the file already has exactly that block. ErrNoAnchor when the
// file sets piste.github some other way (piste.github.login = …) or
// its shape isn't one piste can edit safely.
func SetGithub(src []byte, acct Account, key, home string) (out []byte, changed bool, err error) {
	s := string(src)
	if loc := githubBlock.FindStringSubmatchIndex(s); loc != nil {
		indent := s[loc[2]:loc[3]]
		end, ok := blockEnd(s, loc[1]-1)
		if !ok {
			return nil, false, ErrNoAnchor
		}
		// Through the closing "};" and its line break.
		rest := s[end:]
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, ";") {
			return nil, false, ErrNoAnchor
		}
		end += len(s[end:]) - len(rest) + 1
		for end < len(s) && (s[end] == ' ' || s[end] == '\t') {
			end++
		}
		if end < len(s) && s[end] == '\n' {
			end++
		}
		nb := Block(indent, acct, key, home)
		if s[loc[0]:end] == nb {
			return src, false, nil
		}
		return []byte(s[:loc[0]] + nb + s[end:]), true, nil
	}
	if githubAny.MatchString(s) {
		return nil, false, ErrNoAnchor
	}
	if loc := checkoutRE.FindStringSubmatchIndex(s); loc != nil {
		indent := s[loc[2]:loc[3]]
		return []byte(s[:loc[1]] + Block(indent, acct, key, home) + s[loc[1]:]), true, nil
	}
	return insertInHome(s, acct, key, home)
}

var (
	homeUsersRE = regexp.MustCompile(`home-manager\.users\.[^\s=]+\s*=\s*`)
	pisteNestRE = regexp.MustCompile(`(?m)^\s*piste\s*=\s*\{`)
	githubKeyRE = regexp.MustCompile(`(?m)^\s*github\s*=`)
	pisteStmtRE = regexp.MustCompile(`^piste\.`)
)

// HostFile is the file flake.nix gives the host named key: the first
// ./hosts/<x>.nix in the modules of the hosts."<key>" = { … } entry (the
// shape piste host add writes; the key may be bare, jane-mac = { … }) whose name is key or lists key in its aliases.
// ok is false when flake.nix has no such entry or it names no such file.
func HostFile(flake []byte, key string) (rel string, ok bool) {
	s := string(flake)
	// Each match consumes its { as the next one's lead-in, so step to it.
	for pos := 0; pos < len(s); {
		loc := hostEntryRE.FindStringSubmatchIndex(s[pos:])
		if loc == nil {
			break
		}
		for i := range loc {
			if loc[i] >= 0 {
				loc[i] += pos
			}
		}
		pos = loc[1] - 1
		end, ok := blockEnd(s, loc[1]-1)
		if !ok {
			continue
		}
		body := stripNonCode(s[loc[1]-1 : end])
		name := ""
		if loc[2] >= 0 { // a quoted key, else a bare one
			name = s[loc[2]:loc[3]]
		} else {
			name = s[loc[4]:loc[5]]
		}
		if name == "hosts" || name != key && !hasAlias(body, key) {
			continue
		}
		if m := hostModuleRE.FindStringSubmatch(body); m != nil {
			return m[1], true
		}
	}
	return "", false
}

var (
	hostEntryRE = regexp.MustCompile(`(?m)(?:^|[{;])\s*(?:hosts\.)?(?:"([^"]+)"|([A-Za-z_][A-Za-z0-9_'-]*))\s*=\s*\{`)
	aliasesRE   = regexp.MustCompile(`aliases\s*=\s*\[([^\]]*)\]`)
)

// hasAlias: an entry's body lists "key" in its aliases.
func hasAlias(body, key string) bool {
	m := aliasesRE.FindStringSubmatch(body)
	return m != nil && strings.Contains(m[1], strconv.Quote(key))
}

var hostModuleRE = regexp.MustCompile(`\./(hosts/[^\s\];"]+\.nix)`)

// stripNonCode is s with its comments blanked, so a commented-out module
// isn't read as one.
func stripNonCode(s string) string {
	out := []byte(s)
	for i := 0; i < len(out); i++ {
		switch out[i] {
		case '"':
			for i++; i < len(out) && out[i] != '"'; i++ {
				if out[i] == '\\' {
					i++
				}
			}
		case '#':
			for ; i < len(out) && out[i] != '\n'; i++ {
				out[i] = ' '
			}
		}
	}
	return string(out)
}

// insertInHome adds the block to the home's attrset when the file has no
// piste.checkout line: after the last top-level piste.<x> = …; statement
// of that attrset, else before its closing }. The attrset is the value of
// home-manager.users.<user> = … on a Mac, else the module's own body. Shapes
// it can't read with certainty are ErrNoAnchor.
func insertInHome(s string, acct Account, key, home string) ([]byte, bool, error) {
	if pisteNestRE.MatchString(s) && githubKeyRE.MatchString(s) {
		return nil, false, ErrNoAnchor
	}
	var open int
	var ok bool
	if loc := homeUsersRE.FindStringIndex(s); loc != nil {
		open, ok = bodyOpen(s, loc[1])
	} else if strings.Contains(s, "home-manager.users") {
		return nil, false, ErrNoAnchor
	} else {
		open, ok = bodyOpen(s, 0)
	}
	if !ok {
		return nil, false, ErrNoAnchor
	}
	end, ok := blockEnd(s, open)
	if !ok {
		return nil, false, ErrNoAnchor
	}
	closeAt := end - 1
	lineStart := func(i int) int { return strings.LastIndexByte(s[:i], '\n') + 1 }
	indentOf := func(i int) string {
		l := s[lineStart(i):]
		return l[:len(l)-len(strings.TrimLeft(l, " \t"))]
	}
	// An attrset on one line has no line to put the block on.
	if !strings.Contains(s[open:closeAt], "\n") {
		return nil, false, ErrNoAnchor
	}

	// The statements at the attrset's top level: where each starts and ends.
	type stmt struct{ start, end int }
	var stmts []stmt
	depth, pending, lets := 0, 0, 0
	start := -1
	// word: the keyword w starts at i, as a whole word.
	word := func(i int, w string) bool {
		return strings.HasPrefix(s[i:], w) && (i == 0 || !isIdent(s[i-1])) && (i+len(w) >= len(s) || !isIdent(s[i+len(w)]))
	}
	code(s, open+1, closeAt, func(i int) {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			return
		}
		if start < 0 {
			start = i
		}
		switch c {
		case '{', '[', '(':
			depth++
		case '}', ']', ')':
			depth--
		case ';':
			if depth != 0 {
				return
			}
			switch {
			case lets > 0, pending > 0:
				if pending > 0 && lets == 0 {
					pending--
				}
			default:
				stmts = append(stmts, stmt{start, i + 1})
				start = -1
			}
		case 'w', 'a':
			if depth == 0 && (word(i, "with") || word(i, "assert")) {
				pending++
			}
		case 'l':
			if depth == 0 && word(i, "let") {
				lets++
			}
		case 'i':
			if depth == 0 && lets > 0 && word(i, "in") {
				lets--
			}
		}
	})
	if start >= 0 || depth != 0 || lets != 0 || pending != 0 {
		return nil, false, ErrNoAnchor // a statement piste couldn't see the end of
	}

	var ins int
	var text string
	last := -1
	for i, st := range stmts {
		if pisteStmtRE.MatchString(s[st.start:st.end]) {
			last = i
		}
	}
	if last >= 0 {
		st := stmts[last]
		rest := s[st.end:]
		eol := strings.IndexByte(rest, '\n')
		if eol < 0 {
			return nil, false, ErrNoAnchor
		}
		tail := strings.TrimSpace(rest[:eol])
		if tail != "" && !strings.HasPrefix(tail, "#") {
			return nil, false, ErrNoAnchor // another statement on the same line
		}
		ins = st.end + eol + 1
		text = "\n" + Block(indentOf(st.start), acct, key, home)
		// A blank line after it too, unless one (or the closing }) follows.
		if next := s[ins:]; strings.TrimSpace(next[:strings.IndexByte(next+"\n", '\n')]) != "" && ins < lineStart(closeAt) {
			text += "\n"
		}
	} else {
		if strings.TrimSpace(s[lineStart(closeAt):closeAt]) != "" {
			return nil, false, ErrNoAnchor
		}
		ins = lineStart(closeAt)
		indent := indentOf(closeAt) + "  "
		if len(stmts) > 0 {
			indent = indentOf(stmts[0].start)
		}
		text = Block(indent, acct, key, home)
		if strings.TrimSpace(s[open+1:ins]) != "" && !strings.HasSuffix(s[:ins], "\n\n") {
			text = "\n" + text
		}
	}
	return []byte(s[:ins] + text + s[ins:]), true, nil
}

// bodyOpen is the { of the attrset that starts at from: the first code there,
// or, when that is a function's argument pattern ({ … }:), the { after it.
func bodyOpen(s string, from int) (int, bool) {
	open := nextCode(s, from)
	if open < 0 || s[open] != '{' {
		return 0, false
	}
	end, ok := blockEnd(s, open)
	if !ok {
		return 0, false
	}
	if c := nextCode(s, end); c >= 0 && s[c] == ':' {
		open = nextCode(s, c+1)
		if open < 0 || s[open] != '{' {
			return 0, false
		}
	}
	return open, true
}

func isIdent(c byte) bool {
	return c == '_' || c == '\'' || c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// nextCode is the index of the first non-space code byte at or after i, or -1.
func nextCode(s string, i int) int {
	at := -1
	codeUntil(s, i, len(s), func(j int) bool {
		if c := s[j]; c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			return true
		}
		at = j
		return false
	})
	return at
}

// blockEnd is the index just past the } that closes the { at open, skipping
// strings and comments.
func blockEnd(s string, open int) (int, bool) {
	depth, end := 0, 0
	codeUntil(s, open, len(s), func(i int) bool {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
				return false
			}
		}
		return true
	})
	return end, end > 0
}

// code calls fn for each byte of s[from:to] that is Nix code: not in a
// string or a comment.
func code(s string, from, to int, fn func(i int)) {
	codeUntil(s, from, to, func(i int) bool { fn(i); return true })
}

// codeUntil is code that stops when fn says false. Strings ("…" and ”…”)
// may interpolate ${…}, which is code again.
func codeUntil(s string, from, to int, fn func(i int) bool) {
	type frame struct {
		mode  byte // 'c' code, '"' string, '\'' indented string
		depth int  // braces open in a code frame
	}
	st := []frame{{mode: 'c'}}
	for i := from; i < to; i++ {
		top := &st[len(st)-1]
		c := s[i]
		switch top.mode {
		case 'c':
			switch {
			case c == '#':
				for i < to && s[i] != '\n' {
					i++
				}
			case c == '/' && i+1 < to && s[i+1] == '*':
				i += 2
				for i+1 < to && !(s[i] == '*' && s[i+1] == '/') {
					i++
				}
				i++
			case c == '"':
				st = append(st, frame{mode: '"'})
			case c == '\'' && i+1 < to && s[i+1] == '\'':
				st = append(st, frame{mode: '\''})
				i++
			case c == '}' && top.depth == 0 && len(st) > 1:
				st = st[:len(st)-1]
			default:
				if c == '{' {
					top.depth++
				} else if c == '}' {
					top.depth--
				}
				if !fn(i) {
					return
				}
			}
		case '"':
			switch {
			case c == '\\':
				i++
			case c == '"':
				st = st[:len(st)-1]
			case c == '$' && i+1 < to && s[i+1] == '{':
				st = append(st, frame{mode: 'c'})
				i++
			}
		case '\'':
			switch {
			case c == '\'' && i+2 < to && s[i+1] == '\'' && (s[i+2] == '\'' || s[i+2] == '$' || s[i+2] == '\\'):
				i += 2
			case c == '\'' && i+1 < to && s[i+1] == '\'':
				st = st[:len(st)-1]
				i++
			case c == '$' && i+1 < to && s[i+1] == '{':
				st = append(st, frame{mode: 'c'})
				i++
			}
		}
	}
}
