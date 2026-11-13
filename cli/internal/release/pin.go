package release

import (
	"os/exec"
	"regexp"
	"strings"
)

// Input is the overlay input that pins the engine.
const Input = "piste"

// The pin, as an overlay's flake.nix writes it: `inputs.piste.url = "…"`
// (or `piste.url` inside `inputs = { … }`), or the block form
// `piste = { …; url = "…"; }`, whose url may follow a block of its own
// (`inputs = { … };`) but isn't read from inside one. Each starts its line,
// so another input's `inputs.piste.follows` and a commented line never
// match.
var (
	attrPin  = regexp.MustCompile(`(?m)^[ \t]*(?:inputs\.)?` + Input + `\.url[ \t]*=[ \t]*"([^"]*)"`)
	blockPin = regexp.MustCompile(`(?m)^[ \t]*(?:inputs\.)?` + Input + `[ \t]*=[ \t]*\{(?:(?:[^{}]|\{[^{}]*\})*?[\s;])?url[ \t]*=[ \t]*"([^"]*)"`)
	// pinBlock opens the block form, whatever it holds.
	pinBlock = regexp.MustCompile(`(?m)^[ \t]*(?:inputs\.)?` + Input + `[ \t]*=[ \t]*\{`)
)

// The two release forms piste init writes (overlay.EngineRef):
// github:<owner>/<repo>/<tag> and git+<url>?ref=refs/tags/<tag>.
var (
	githubTag = regexp.MustCompile(`^(github:[^/?]+/[^/?]+/)(v\d+\.\d+\.\d+)$`)
	gitTag    = regexp.MustCompile(`^(git\+[^?]+\?(?:[^#]*&)?ref=refs/tags/)(v\d+\.\d+\.\d+)((?:&[^#]*)?)$`)
)

// pinAt is the pin's URL and where it sits in flakeNix ([start, end)).
func pinAt(flakeNix string) (url string, start, end int, ok bool) {
	for _, re := range []*regexp.Regexp{attrPin, blockPin} {
		if m := re.FindStringSubmatchIndex(flakeNix); m != nil {
			return flakeNix[m[2]:m[3]], m[2], m[3], true
		}
	}
	return "", 0, 0, false
}

// ReadPin is the URL of the engine input in an overlay's flake.nix.
func ReadPin(flakeNix string) (string, bool) {
	url, _, _, ok := pinAt(flakeNix)
	return url, ok
}

// Unreadable: flakeNix declares the engine input as a block, but not with a
// url ReadPin can find (attributes instead of a url, a url that isn't a
// string), so its pin can't be read or moved.
func Unreadable(flakeNix string) bool {
	_, ok := ReadPin(flakeNix)
	return !ok && pinBlock.MatchString(flakeNix)
}

// PinTag is the release a pin URL names, "" when it names none (a rev, a
// branch, the default branch, a pre-release).
func PinTag(url string) string {
	if m := githubTag.FindStringSubmatch(url); m != nil {
		return m[2]
	}
	if m := gitTag.FindStringSubmatch(url); m != nil {
		return m[2]
	}
	return ""
}

// MovePin rewrites the pin's release to tag, leaving the rest of flakeNix as
// it is; false (and flakeNix unchanged) when the pin isn't a release.
func MovePin(flakeNix, tag string) (string, bool) {
	url, start, end, ok := pinAt(flakeNix)
	if !ok || PinTag(url) == "" {
		return flakeNix, false
	}
	moved := url
	if m := githubTag.FindStringSubmatch(url); m != nil {
		moved = m[1] + tag
	} else if m := gitTag.FindStringSubmatch(url); m != nil {
		moved = m[1] + tag + m[3]
	}
	return flakeNix[:start] + moved + flakeNix[end:], true
}

// HeadRelease is the release tag at repo's HEAD, "" for none. A release
// commit can also carry the pre-release that became it, so this is the
// highest release among HEAD's tags, not whichever `git describe` picks.
func HeadRelease(repo string) string {
	out, err := exec.Command("git", "-C", repo, "tag", "--points-at", "HEAD").Output()
	if err != nil {
		return ""
	}
	return Latest(strings.Fields(string(out)))
}
