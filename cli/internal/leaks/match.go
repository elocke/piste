package leaks

import (
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// Hit is one thing found: in a commit (Commit, the short sha) or a tree
// (Commit ""), at Where, described by What.
type Hit struct {
	Commit, Where, What string
}

// The RFC1918 ranges, built here rather than written out, so this file
// passes its own scan.
var privateRanges = []netip.Prefix{
	netip.PrefixFrom(netip.AddrFrom4([4]byte{10, 0, 0, 0}), 8),
	netip.PrefixFrom(netip.AddrFrom4([4]byte{172, 16, 0, 0}), 12),
	netip.PrefixFrom(netip.AddrFrom4([4]byte{192, 168, 0, 0}), 16),
}

var ipv4 = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

type namedRe struct {
	re   *regexp.Regexp
	list string
}

// Matcher finds the lists' terms and regexes and the generic markers (private
// addresses, the running user's home) in a line.
type Matcher struct {
	terms    map[string]string // hash -> the list that holds it
	maxWords int
	res      []namedRe
	home     *regexp.Regexp // nil: no home rule
	homeText string
}

// NewMatcher builds a matcher from lists, in order: a term or regex already
// held by an earlier list is dropped. home is the running user's $HOME; it is
// a marker when it is a directory under /home/ or /Users/.
func NewMatcher(home string, lists []*List) *Matcher {
	m := &Matcher{terms: map[string]string{}}
	seen := map[string]bool{}
	for _, l := range lists {
		for h := range l.terms {
			if _, ok := m.terms[h]; !ok {
				m.terms[h] = l.Name
			}
		}
		m.maxWords = max(m.maxWords, l.maxWords)
		for _, re := range l.res {
			if !seen[re.String()] {
				seen[re.String()] = true
				m.res = append(m.res, namedRe{re, l.Name})
			}
		}
	}
	home = strings.TrimRight(home, "/")
	for _, p := range []string{"/home/", "/Users/"} {
		if strings.HasPrefix(home, p) && len(home) > len(p) {
			m.home = regexp.MustCompile(`(?i)` + regexp.QuoteMeta(home))
			m.homeText = home
		}
	}
	return m
}

type found struct {
	at   int
	what string
}

// Line is what the line holds, one description per distinct match, in the
// order they appear.
func (m *Matcher) Line(s string) []string {
	var fs []found
	if len(m.terms) > 0 {
		ws := words(s)
		for i := range ws {
			for n := 1; n <= m.maxWords && i+n <= len(ws); n++ {
				parts := make([]string, n)
				for j := range parts {
					parts[j] = ws[i+j].s
				}
				if list, ok := m.terms[hashWords(parts)]; ok {
					fs = append(fs, found{ws[i].start, quote(s[ws[i].start:ws[i+n-1].end]) + " (" + list + ")"})
				}
			}
		}
	}
	for _, r := range m.res {
		for _, loc := range r.re.FindAllStringIndex(s, -1) {
			if loc[1] > loc[0] {
				fs = append(fs, found{loc[0], quote(s[loc[0]:loc[1]]) + " (" + r.list + ")"})
			}
		}
	}
	for _, loc := range ipv4.FindAllStringIndex(s, -1) {
		a, err := netip.ParseAddr(s[loc[0]:loc[1]])
		if err != nil {
			continue
		}
		for _, p := range privateRanges {
			if p.Contains(a) {
				fs = append(fs, found{loc[0], quote(a.String()) + " a private address (" + p.String() + ")"})
			}
		}
	}
	if m.home != nil {
		for _, loc := range m.home.FindAllStringIndex(s, -1) {
			if loc[1] == len(s) || !homeCont(s[loc[1]]) {
				fs = append(fs, found{loc[0], quote(m.homeText) + " your home directory"})
			}
		}
	}
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].at < fs[j].at })
	var out []string
	seen := map[string]bool{}
	for _, f := range fs {
		if !seen[f.what] {
			seen[f.what] = true
			out = append(out, f.what)
		}
	}
	return out
}

// homeCont: a byte that continues a path element, so $HOME is only a prefix
// of another name (/home/user in /home/username).
func homeCont(c byte) bool {
	return c == '_' || c == '-' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

func quote(s string) string { return `"` + s + `"` }
