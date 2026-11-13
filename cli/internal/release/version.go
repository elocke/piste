// Package release is piste's releases: versions, CHANGELOG.md
// (its sections, the notes between two releases, cutting one), the engine pin
// in an overlay's flake.nix, and the once-a-day check for a newer release.
package release

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is a release: vMAJOR.MINOR.PATCH.
type Version struct{ Major, Minor, Patch int }

var versionRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// ParseVersion reads a release tag, vX.Y.Z; a pre-release (v1.0.0-rc1) or
// any other tag is not one.
func ParseVersion(s string) (Version, bool) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}
	var n [3]int
	for i := range n {
		var err error
		if n[i], err = strconv.Atoi(m[i+1]); err != nil {
			return Version{}, false
		}
	}
	return Version{n[0], n[1], n[2]}, true
}

func (v Version) String() string { return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less orders releases numerically by major, minor, then patch.
func (v Version) Less(w Version) bool {
	if v.Major != w.Major {
		return v.Major < w.Major
	}
	if v.Minor != w.Minor {
		return v.Minor < w.Minor
	}
	return v.Patch < w.Patch
}

// Latest is the highest release among tags (numerically; pre-releases and
// other tags skipped), "" if there is none.
func Latest(tags []string) string {
	best, bestV := "", Version{}
	for _, t := range tags {
		v, ok := ParseVersion(t)
		if ok && (best == "" || bestV.Less(v)) {
			best, bestV = t, v
		}
	}
	return best
}

// Newer: a is a release above b (b "" or not a release counts as none).
func Newer(a, b string) bool {
	av, ok := ParseVersion(a)
	if !ok {
		return false
	}
	bv, ok := ParseVersion(b)
	return !ok || bv.Less(av)
}
