// Package leaks is piste's leak gate: your marker words (lists
// in the checkout's overlays), generic markers (private addresses, the running
// user's home) and gitleaks, over the commits a push would add or over an
// exported tree.
package leaks

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// A term has at most this many words; longer ones need an re: line.
const maxTermWords = 4

// List is one marker list: its terms (stored as sha256 hashes of their words)
// and re: regexes.
type List struct {
	Name     string // shown in hits
	terms    map[string]struct{}
	maxWords int
	res      []*regexp.Regexp
}

// Words lower-cases s (ASCII) and returns its runs of a-z0-9; every other
// byte separates words.
func Words(s string) []string {
	var out []string
	for _, w := range words(s) {
		out = append(out, w.s)
	}
	return out
}

type word struct {
	s          string
	start, end int // byte offsets in the line
}

func words(s string) []word {
	var out []word
	start := -1
	var b strings.Builder
	for i := 0; i <= len(s); i++ {
		c := byte(0)
		if i < len(s) {
			c = s[i]
		}
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if ('a' <= c && c <= 'z') || ('0' <= c && c <= '9') {
			if start < 0 {
				start = i
				b.Reset()
			}
			b.WriteByte(c)
			continue
		}
		if start >= 0 {
			out = append(out, word{b.String(), start, i})
			start = -1
		}
	}
	return out
}

func hashWords(ws []string) string {
	h := sha256.Sum256([]byte(strings.Join(ws, " ")))
	return hex.EncodeToString(h[:])
}

func checkWords(n int) error {
	if n == 0 {
		return fmt.Errorf("no words (a term is letters and digits)")
	}
	if n > maxTermWords {
		return fmt.Errorf("%d words; a term is 1 to %d (use an re: line)", n, maxTermWords)
	}
	return nil
}

// entry is one list line, comments and blanks removed: its kind ("term" or
// "re") and its value (the term's hash, or the regex source).
type entry struct {
	kind, value string
	words       int
	re          *regexp.Regexp
}

// stripComment drops a comment: `#` at the start of the line or after a
// space or tab.
func stripComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t') {
			return line[:i]
		}
	}
	return line
}

func parseLine(line string) (*entry, error) {
	line = strings.TrimSpace(stripComment(line))
	switch {
	case line == "":
		return nil, nil
	case strings.HasPrefix(line, "re:"):
		src := strings.TrimPrefix(line, "re:")
		if src == "" {
			return nil, fmt.Errorf("re: is empty")
		}
		re, err := regexp.Compile("(?i)" + src)
		if err != nil {
			return nil, fmt.Errorf("re: does not compile: %v", err)
		}
		return &entry{kind: "re", value: src, re: re}, nil
	case strings.HasPrefix(line, "sha256:"):
		// The hashed form is gone; read as a term it would never match.
		return nil, fmt.Errorf("sha256: lines are no longer read; put the plain term back")
	}
	ws := Words(line)
	if err := checkWords(len(ws)); err != nil {
		return nil, err
	}
	return &entry{kind: "term", value: hashWords(ws), words: len(ws)}, nil
}

func parseEntries(name string, r io.Reader) ([]*entry, error) {
	var out []*entry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		e, err := parseLine(sc.Text())
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %v", name, n, err)
		}
		if e != nil {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %v", name, err)
	}
	return out, nil
}

// ParseList reads a marker list: one entry per line (a term, or an re:<regex>
// matched in any case), `#` comments. Errors name the line, never the term on it.
func ParseList(name string, r io.Reader) (*List, error) {
	es, err := parseEntries(name, r)
	if err != nil {
		return nil, err
	}
	l := &List{Name: name, terms: map[string]struct{}{}}
	for _, e := range es {
		switch e.kind {
		case "term":
			l.terms[e.value] = struct{}{}
			l.maxWords = max(l.maxWords, e.words)
		case "re":
			l.res = append(l.res, e.re)
		}
	}
	return l, nil
}

// LoadList reads the list at path, shown in hits as name.
func LoadList(path, name string) (*List, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseList(name, f)
}
