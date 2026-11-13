package outdated

import "strings"

type PnpmItem struct{ Name, Current, Latest string }

// ParsePnpmOutdated reads `piste-pnpm outdated` lines: name<TAB>current<TAB>latest.
// Blank and malformed lines are skipped.
func ParsePnpmOutdated(tsv string) []PnpmItem {
	var out []PnpmItem
	for _, line := range strings.Split(tsv, "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) != 3 || f[0] == "" {
			continue
		}
		out = append(out, PnpmItem{f[0], f[1], f[2]})
	}
	return out
}
