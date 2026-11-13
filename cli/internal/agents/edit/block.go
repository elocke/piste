package edit

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ErrAmbiguous: the markers are unbalanced or repeated; nothing is written.
var ErrAmbiguous = errors.New("ambiguous managed block")

// Markers delimit one managed block.
type Markers struct{ Begin, End string }

// MDMarkers are the piste-agents markers in CLAUDE.md / AGENTS.md.
var MDMarkers = Markers{"<!-- piste-agents:begin -->", "<!-- piste-agents:end -->"}

const tomlBegin, tomlEnd = "# >>> piste:managed ", "# <<< piste:managed "

// TOMLMarkers returns the markers of the managed block named id.
func TOMLMarkers(id string) Markers {
	return Markers{tomlBegin + id, tomlEnd + id}
}

func (m Markers) span(text string) (start, endStart int, ok bool, err error) {
	b, e := strings.Count(text, m.Begin), strings.Count(text, m.End)
	if b != e || b > 1 {
		return 0, 0, false, fmt.Errorf("%w: found %d begin and %d end marker(s); expected 0 or 1 of each", ErrAmbiguous, b, e)
	}
	if b == 0 {
		return 0, 0, false, nil
	}
	start, endStart = strings.Index(text, m.Begin), strings.Index(text, m.End)
	if endStart < start {
		return 0, 0, false, fmt.Errorf("%w: end marker precedes begin marker", ErrAmbiguous)
	}
	return start, endStart, true, nil
}

// Get returns the block's content (without markers, outer newlines trimmed).
func (m Markers) Get(text string) (string, bool, error) {
	start, endStart, ok, err := m.span(text)
	if err != nil || !ok {
		return "", ok, err
	}
	return strings.Trim(text[start+len(m.Begin):endStart], "\n"), true, nil
}

// Set replaces the block, or appends it after a blank line. Everything
// outside the markers is kept byte for byte (the piste-agents splice).
func (m Markers) Set(text, block string) (string, error) {
	managed := m.Begin + "\n" + strings.Trim(block, "\n") + "\n" + m.End + "\n"
	start, endStart, ok, err := m.span(text)
	if err != nil {
		return "", err
	}
	if !ok {
		if text == "" {
			return managed, nil
		}
		sep := ""
		if !strings.HasSuffix(text, "\n") {
			sep = "\n"
		}
		return text + sep + "\n" + managed, nil
	}
	return text[:start] + strings.TrimSuffix(managed, "\n") + text[endStart+len(m.End):], nil
}

// Remove deletes the block and the blank line Set added before it.
func (m Markers) Remove(text string) (string, error) {
	start, endStart, ok, err := m.span(text)
	if err != nil || !ok {
		return text, err
	}
	end := endStart + len(m.End)
	if end < len(text) && text[end] == '\n' {
		end++
	}
	head := text[:start]
	if strings.HasSuffix(head, "\n\n") {
		head = head[:len(head)-1]
	}
	return head + text[end:], nil
}

// --- TOML ----------------------------------------------------------------------

var tomlHeader = regexp.MustCompile(`^\s*\[`)

// SetTOML splices body (whole owned tables) into the managed block id of a
// TOML document and validates the result: a parse error or a duplicate key
// (say, a hand-written [mcp_servers.x] and ours) aborts with no change.
func SetTOML(doc, id, body string) (string, error) {
	if err := checkTOMLBody(body); err != nil {
		return "", err
	}
	if err := validTOML(doc); err != nil {
		return "", fmt.Errorf("existing document does not parse: %w", err)
	}
	out, err := TOMLMarkers(id).Set(doc, body)
	if err != nil {
		return "", err
	}
	if err := validTOML(out); err != nil {
		return "", fmt.Errorf("managed block %q would make the document invalid: %w", id, err)
	}
	return out, nil
}

// RemoveTOML deletes the managed block id.
func RemoveTOML(doc, id string) (string, error) {
	out, err := TOMLMarkers(id).Remove(doc)
	if err != nil {
		return "", err
	}
	if err := validTOML(out); err != nil {
		return "", err
	}
	return out, nil
}

// GetTOML returns the managed block id's body.
func GetTOML(doc, id string) (string, bool, error) { return TOMLMarkers(id).Get(doc) }

// The block is appended at the end of the file, so any bare key before its
// first table header would land in the preceding table.
func checkTOMLBody(body string) error {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if !tomlHeader.MatchString(line) {
			return fmt.Errorf("a managed TOML block must start with a table header, got %q", t)
		}
		break
	}
	return validTOML(body)
}

func validTOML(s string) error {
	var v map[string]any
	return toml.Unmarshal([]byte(s), &v)
}
