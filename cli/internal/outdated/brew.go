package outdated

import (
	"encoding/json"
	"strings"
)

type BrewItem struct {
	Name, Current, Latest string
	Cask                  bool
}

type brewEntry struct {
	Name        string   `json:"name"`
	Installed   []string `json:"installed_versions"`
	CurrentVers string   `json:"current_version"`
}

// brewSlot is a declared name and the outdated entry paired with it (nil:
// none).
type brewSlot struct {
	name  string
	cask  bool
	entry *brewEntry
}

// BrewMatch is the manifest's declared names paired with brew's outdated
// entries: every declared name, brews first, in manifest order, and the
// entries no declared name took.
type BrewMatch struct {
	slots           []brewSlot
	formulae, casks []brewEntry
}

// BrewOutdated pairs the declared names (manifest JSON) with brew's
// `outdated --json=v2` output. Its "name" is a formula's full name
// (tap-qualified for a tap's: "a/b/c") but a cask's short token ("c"), so a
// declared name matches an entry named the same or, failing that, a short
// entry named its last segment. A name declared by an alias or an old name
// is left over: BrewAliases pairs it.
func BrewOutdated(manifestJSON, brewJSON []byte) (BrewMatch, error) {
	var m struct {
		Brews []string `json:"brews"`
		Casks []string `json:"casks"`
	}
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return BrewMatch{}, err
	}
	var o struct {
		Formulae []brewEntry `json:"formulae"`
		Casks    []brewEntry `json:"casks"`
	}
	if err := json.Unmarshal(brewJSON, &o); err != nil {
		return BrewMatch{}, err
	}
	var bm BrewMatch
	// find pairs declared with an entry; taken marks the entries paired.
	find := func(declared string, entries []brewEntry, taken []bool, cask bool) {
		short := declared[strings.LastIndex(declared, "/")+1:]
		match := -1
		for i, e := range entries {
			if e.Name == declared {
				match = i
				break
			}
			if match < 0 && !strings.Contains(e.Name, "/") && e.Name == short {
				match = i
			}
		}
		s := brewSlot{name: declared, cask: cask}
		if match >= 0 {
			s.entry = &entries[match]
			taken[match] = true
		}
		bm.slots = append(bm.slots, s)
	}
	left := func(entries []brewEntry, taken []bool) []brewEntry {
		var out []brewEntry
		for i, e := range entries {
			if !taken[i] {
				out = append(out, e)
			}
		}
		return out
	}
	tf, tc := make([]bool, len(o.Formulae)), make([]bool, len(o.Casks))
	for _, d := range m.Brews {
		find(d, o.Formulae, tf, false)
	}
	for _, d := range m.Casks {
		find(d, o.Casks, tc, true)
	}
	bm.formulae, bm.casks = left(o.Formulae, tf), left(o.Casks, tc)
	return bm, nil
}

// Items are the paired declared names, as declared (brew upgrade takes
// them so), brews first, in manifest order.
func (m BrewMatch) Items() []BrewItem {
	var out []BrewItem
	for _, s := range m.slots {
		if s.entry == nil {
			continue
		}
		cur := ""
		if n := len(s.entry.Installed); n > 0 {
			cur = s.entry.Installed[n-1]
		}
		out = append(out, BrewItem{s.name, cur, s.entry.CurrentVers, s.cask})
	}
	return out
}

// Left: a declared name and an outdated entry of the same kind are both
// unpaired. Only then can BrewAliases pair more.
func (m BrewMatch) Left() bool {
	var formula, cask bool
	for _, s := range m.slots {
		if s.entry == nil {
			formula = formula || !s.cask
			cask = cask || s.cask
		}
	}
	return formula && len(m.formulae) > 0 || cask && len(m.casks) > 0
}

// BrewAliases pairs what m left over through brew's `info --json=v2
// --installed` output: a formula declared by one of its aliases or old
// names (bare, or tap-qualified as a tap's), a cask by one of its old tokens.
// The items keep the declared names. m is unchanged.
func BrewAliases(m BrewMatch, infoJSON []byte) (BrewMatch, error) {
	var info struct {
		Formulae []struct {
			FullName string   `json:"full_name"`
			Aliases  []string `json:"aliases"`
			Oldnames []string `json:"oldnames"`
		} `json:"formulae"`
		Casks []struct {
			Token     string   `json:"token"`
			FullToken string   `json:"full_token"`
			OldTokens []string `json:"old_tokens"`
		} `json:"casks"`
	}
	if err := json.Unmarshal(infoJSON, &info); err != nil {
		return m, err
	}
	// Another name → the name brew outdated gives: a formula's full name, a
	// cask's token. The first to claim a name keeps it.
	formulae, casks := map[string]string{}, map[string]string{}
	add := func(to map[string]string, full, canonical string, others []string) {
		tap := full[:strings.LastIndex(full, "/")+1]
		for _, o := range others {
			for _, k := range []string{o, tap + o} {
				if _, ok := to[k]; !ok {
					to[k] = canonical
				}
			}
		}
	}
	for _, f := range info.Formulae {
		add(formulae, f.FullName, f.FullName, append(append([]string(nil), f.Aliases...), f.Oldnames...))
	}
	for _, c := range info.Casks {
		add(casks, c.FullToken, c.Token, c.OldTokens)
	}
	out := BrewMatch{
		slots:    append([]brewSlot(nil), m.slots...),
		formulae: append([]brewEntry(nil), m.formulae...),
		casks:    append([]brewEntry(nil), m.casks...),
	}
	for i, s := range out.slots {
		if s.entry != nil {
			continue
		}
		names, entries := formulae, &out.formulae
		if s.cask {
			names, entries = casks, &out.casks
		}
		canonical, ok := names[s.name]
		if !ok {
			continue
		}
		for j, e := range *entries {
			if e.Name == canonical {
				out.slots[i].entry = &e
				*entries = append((*entries)[:j:j], (*entries)[j+1:]...)
				break
			}
		}
	}
	return out, nil
}
