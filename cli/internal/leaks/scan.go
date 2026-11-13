package leaks

import "sort"

// ScanRange is the leak scan of the commits of revs in dir: the matcher over
// their text, then gitleaks (none when there are no commits). commits counts
// them; hits are in commit order, gitleaks' after the matcher's.
func ScanRange(dir string, revs []string, m *Matcher) (commits int, hits []Hit, err error) {
	cs, err := Commits(dir, revs)
	if err != nil {
		return 0, nil, err
	}
	hits = m.Commits(cs)
	if len(cs) == 0 {
		return 0, hits, nil
	}
	gl, err := GitleaksRange(dir, revs)
	if err != nil {
		return len(cs), nil, err
	}
	hits = append(hits, gl...)
	order := map[string]int{}
	for i, c := range cs {
		order[c.SHA[:min(7, len(c.SHA))]] = i
	}
	pos := func(h Hit) int {
		if i, ok := order[h.Commit]; ok {
			return i
		}
		return len(cs)
	}
	sort.SliceStable(hits, func(i, j int) bool { return pos(hits[i]) < pos(hits[j]) })
	return len(cs), hits, nil
}

// ScanTree is the leak scan of the files under dir: the matcher, then
// gitleaks; hits sorted by where they are.
func ScanTree(dir string, m *Matcher) (files int, hits []Hit, err error) {
	hits, files, err = m.Tree(dir)
	if err != nil {
		return 0, nil, err
	}
	gl, err := GitleaksDir(dir)
	if err != nil {
		return files, nil, err
	}
	hits = append(hits, gl...)
	sortByWhere(hits)
	return files, hits, nil
}
