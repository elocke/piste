// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/utils/control-characters.ts, src/utils/confusable-names.ts.
// Copyright (c) 2024 dyoshikawa. MIT License; see cli/THIRD_PARTY_NOTICES.md.

// Package safety keeps hostile text out of terminals and registries
// and literal secrets out of manifests.
package safety

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// isControl: C0, DEL, C1, the Arabic letter mark, LRM/RLM, bidi embeddings
// and isolates, and the line/paragraph separators (CONTROL_CHARACTERS_PATTERN).
func isControl(r rune) bool {
	return r <= 0x1f || (r >= 0x7f && r <= 0x9f) || r == 0x061c || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x2028 || r == 0x2029
}

// isInvisible: format characters (Cf), other default-ignorables and the
// braille blank (INVISIBLE_CHARACTERS_PATTERN).
func isInvisible(r rune) bool {
	return unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
		unicode.Is(unicode.Variation_Selector, r) || r == 0x2800 || r == 0x115f || r == 0x1160 ||
		r == 0x3164 || r == 0xffa0 || r == 0x034f
}

// StripControl removes control characters (for printing a name or path).
func StripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

// StripHidden removes control and invisible characters.
func StripHidden(s string) string {
	return strings.Map(func(r rune) rune {
		if isControl(r) || isInvisible(r) {
			return -1
		}
		return r
	}, s)
}

// Quote is a safe, visible rendering for logs.
func Quote(s string) string { return `"` + strings.ReplaceAll(StripControl(s), `"`, `\"`) + `"` }

// joins reports scripts where ZWJ/ZWNJ and variation selectors are
// legitimate (JOINING_CONTEXT_PATTERN, plus emoji).
func joins(r rune) bool {
	for _, t := range []*unicode.RangeTable{unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Nko,
		unicode.Mongolian, unicode.Devanagari, unicode.Bengali, unicode.Gurmukhi, unicode.Gujarati,
		unicode.Oriya, unicode.Tamil, unicode.Telugu, unicode.Kannada, unicode.Malayalam, unicode.Sinhala,
		unicode.Myanmar, unicode.Khmer, unicode.Tibetan, unicode.Adlam} {
		if unicode.Is(t, r) {
			return true
		}
	}
	return r >= 0x1f000 || (r >= 0x2600 && r <= 0x27bf) // pictographs
}

// HasDeceptiveHidden reports control characters, or invisible ones outside
// the contexts where they render legitimately (hasDeceptiveHiddenCharacters):
// a joiner between two joining characters, a variation selector after one,
// a keycap sequence.
func HasDeceptiveHidden(s string) bool {
	rs := []rune(s)
	at := func(i int) rune {
		if i < 0 || i >= len(rs) {
			return 0
		}
		return rs[i]
	}
	for i, r := range rs {
		if r == '\n' || r == '\t' {
			continue
		}
		if isControl(r) {
			return true
		}
		if !isInvisible(r) {
			continue
		}
		switch {
		case unicode.Is(unicode.Variation_Selector, r):
			if r == 0xfe0f && strings.ContainsRune("0123456789#*", at(i-1)) && at(i+1) == 0x20e3 {
				continue
			}
			if joins(at(i - 1)) {
				continue
			}
			return true
		case r == 0x200c || r == 0x200d:
			if joins(at(i-1)) && joins(at(i+1)) {
				continue
			}
			return true
		default:
			return true
		}
	}
	return false
}

// scriptOf returns the script of a letter, "" for common/inherited.
func scriptOf(r rune) string {
	for name, t := range map[string]*unicode.RangeTable{
		"Latin": unicode.Latin, "Cyrillic": unicode.Cyrillic, "Greek": unicode.Greek,
		"Armenian": unicode.Armenian, "Cherokee": unicode.Cherokee, "Han": unicode.Han,
		"Hebrew": unicode.Hebrew, "Arabic": unicode.Arabic,
	} {
		if unicode.Is(t, r) {
			return name
		}
	}
	if unicode.IsLetter(r) {
		return "Other"
	}
	return ""
}

// MixedScripts lists the scripts of a name that mixes more than one
// (mixedScriptsOf), e.g. a Latin name with one Cyrillic letter.
func MixedScripts(name string) []string {
	seen := map[string]bool{}
	for _, r := range name {
		if s := scriptOf(r); s != "" {
			seen[s] = true
		}
	}
	if len(seen) < 2 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// lookalikes folds common Cyrillic/Greek homoglyphs to Latin, and
// digit/letter swaps (a condensed foldLookalikes).
var lookalikes = map[rune]rune{
	'а': 'a', 'е': 'e', 'о': 'o', 'р': 'p', 'с': 'c', 'х': 'x', 'у': 'y', 'і': 'i', 'ј': 'j', 'ѕ': 's',
	'ԁ': 'd', 'һ': 'h', 'ӏ': 'l', 'ԛ': 'q', 'ԝ': 'w', 'ɡ': 'g', 'ο': 'o', 'α': 'a', 'ν': 'v', 'ρ': 'p',
	'ι': 'i', 'κ': 'k', 'τ': 't', 'υ': 'u', 'Α': 'a', 'Β': 'b', 'Ε': 'e', 'Η': 'h', 'Ι': 'i', 'Κ': 'k',
	'Μ': 'm', 'Ν': 'n', 'Ο': 'o', 'Ρ': 'p', 'Τ': 't', 'Χ': 'x', 'Υ': 'y', 'Ζ': 'z', 'А': 'a', 'В': 'b',
	'Е': 'e', 'К': 'k', 'М': 'm', 'Н': 'h', 'О': 'o', 'Р': 'p', 'С': 'c', 'Т': 't', 'Х': 'x',
	'0': 'o', '1': 'l', '|': 'l', 'I': 'l',
}

// Skeleton is the comparison form of a name: hidden characters removed,
// lower-cased, lookalikes folded, separators unified, "rn" read as "m".
func Skeleton(name string) string {
	s := StripHidden(name)
	var b strings.Builder
	for _, r := range s {
		if f, ok := lookalikes[r]; ok {
			r = f
		}
		r = unicode.ToLower(r)
		if r == '_' || r == ' ' || r == '.' {
			r = '-'
		}
		b.WriteRune(r)
	}
	return strings.ReplaceAll(b.String(), "rn", "m")
}

// Confusable groups names that differ but share a skeleton, e.g. "paseo"
// and "pаseo" (Cyrillic а).
func Confusable(names []string) [][]string {
	by := map[string][]string{}
	for _, n := range names {
		k := Skeleton(n)
		by[k] = append(by[k], n)
	}
	var out [][]string
	for _, g := range by {
		uniq := map[string]bool{}
		for _, n := range g {
			uniq[n] = true
		}
		if len(uniq) > 1 {
			sort.Strings(g)
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// --- secrets ------------------------------------------------------------------

var (
	envRef      = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*(:-[^}]*)?\}$`)
	bearerRef   = regexp.MustCompile(`(?i)^bearer \$\{[A-Za-z_][A-Za-z0-9_]*\}$`)
	secretKey   = regexp.MustCompile(`(?i)(token|secret|passw|api[-_]?key|apikey|auth|bearer|credential|private[-_]?key|session)`)
	tokenShapes = regexp.MustCompile(`(?i)(^bearer\s+\S{8,}$)|(gh[pousr]_[A-Za-z0-9]{20,})|(github_pat_[A-Za-z0-9_]{20,})|` +
		`(sk-[A-Za-z0-9_-]{16,})|(xox[baprs]-[A-Za-z0-9-]{10,})|(AKIA[0-9A-Z]{16})|(eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.)|` +
		`(glpat-[A-Za-z0-9_-]{16,})|(AIza[0-9A-Za-z_-]{30,})`)
	longOpaque = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{32,}$`)
	urlCreds   = regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@|[?&](token|key|secret|access_token|api_key)=[^&\s$]{6,}`)
)

// LooksSecret reports a literal credential: a known token shape, a long
// opaque value under a secret-sounding key, or credentials in a URL.
// ${VAR} references are never secrets.
func LooksSecret(key, value string) bool {
	v := strings.TrimSpace(value)
	if v == "" || envRef.MatchString(v) || bearerRef.MatchString(v) {
		return false
	}
	if tokenShapes.MatchString(v) || urlCreds.MatchString(v) {
		return true
	}
	if secretKey.MatchString(key) && len(v) >= 8 && !strings.Contains(v, "${") {
		return true
	}
	return longOpaque.MatchString(v) && secretKey.MatchString(key)
}

// Redact replaces a literal secret with a ${VAR} placeholder derived from
// the key and server name, and says what it did (for import).
func Redact(server, key, value string) (string, string, bool) {
	if !LooksSecret(key, value) {
		return value, "", false
	}
	name := strings.ToUpper(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(server+"_"+key, "_"))
	name = strings.Trim(name, "_")
	if strings.EqualFold(key, "authorization") {
		name = strings.ToUpper(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(server, "_")) + "_TOKEN"
		return "Bearer ${" + name + "}", name, true
	}
	return "${" + name + "}", name, true
}
