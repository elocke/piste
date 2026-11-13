package outdated

import "regexp"

var (
	freshRe = regexp.MustCompile(`(?m)^[ \t]*fresh\s*=\s*\[([^\]]*)\]`)
	strRe   = regexp.MustCompile(`"([^"]*)"`)
)

// FreshNames: the strings in flake.nix's `fresh = [ … ];`; nil when absent
// or commented out.
func FreshNames(flakeNix string) []string {
	m := freshRe.FindStringSubmatch(flakeNix)
	if m == nil {
		return nil
	}
	var out []string
	for _, s := range strRe.FindAllStringSubmatch(m[1], -1) {
		out = append(out, s[1])
	}
	return out
}
