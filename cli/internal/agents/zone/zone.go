// Package zone holds the trust-zone rules of the agents engine.
//
// The engine reads only explicit zone roots: passed by the Nix module (the
// manifest), the bundle build, or the project lock. It never looks in $HOME
// dotfiles, parent directories or *.local.* overrides for zone content, and
// it ignores PISTE_AGENTS_* variables unless the command was given --allow-env.
package zone

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/envvar"
)

// Zones. A zone is any name matching nameRe; these are the ones the
// engine knows by name. Shared is the engine's own and is never a
// machine's zone; Personal is the default when no root names one.
const (
	Shared   = "shared"
	Personal = "personal"
	Work     = "work"
)

// nameRe is the zone-name rule, the same as the Nix module's zoneName. No
// ':' — item ids are <zone>:<kind>/<name>.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// ValidName reports whether z is a well-formed zone name (shared included).
func ValidName(z string) bool { return nameRe.MatchString(z) }

// ValidMachine checks a machine, project or registry zone: a valid name
// other than shared.
func ValidMachine(z string) error {
	if !ValidName(z) || z == Shared {
		return fmt.Errorf("zone %q: want lowercase letters, digits and dashes, not \"shared\"", z)
	}
	return nil
}

// Infer picks the machine zone from the roots when none was given: the one
// non-shared zone among them, Personal when there is none, an error when
// they name more than one.
func Infer(roots []Root) (string, error) {
	seen := map[string]bool{}
	var zones []string
	for _, r := range roots {
		if r.Zone != Shared && !seen[r.Zone] {
			seen[r.Zone] = true
			zones = append(zones, r.Zone)
		}
	}
	switch len(zones) {
	case 0:
		return Personal, nil
	case 1:
		return zones[0], nil
	}
	sort.Strings(zones)
	return "", fmt.Errorf("roots from zones %s: pass --zone", strings.Join(zones, ", "))
}

// Root is one zone source directory (an agents/ tree).
type Root struct {
	Name string `json:"name"`
	Zone string `json:"zone"`
	Dir  string `json:"dir"`
}

// ParseRoot parses "name=zone:/abs/dir" (the --zone-root flag).
func ParseRoot(s string) (Root, error) {
	name, rest, ok := strings.Cut(s, "=")
	if !ok {
		return Root{}, fmt.Errorf("zone root %q: want name=zone:/dir", s)
	}
	z, dir, ok := strings.Cut(rest, ":")
	if !ok || !filepath.IsAbs(dir) {
		return Root{}, fmt.Errorf("zone root %q: want name=zone:/absolute/dir", s)
	}
	r := Root{Name: name, Zone: z, Dir: filepath.Clean(dir)}
	return r, r.valid()
}

func (r Root) valid() error {
	if ValidName(r.Zone) {
		return nil
	}
	return fmt.Errorf("zone root %s: zone %q: want lowercase letters, digits and dashes", r.Name, r.Zone)
}

// Validate applies the rule the Nix resolver also enforces: every
// root is shared or the machine's own zone.
func Validate(machine string, roots []Root) error {
	if err := ValidMachine(machine); err != nil {
		return fmt.Errorf("machine %w", err)
	}
	seen := map[string]bool{}
	for _, r := range roots {
		if err := r.valid(); err != nil {
			return err
		}
		if r.Zone != Shared && r.Zone != machine {
			return fmt.Errorf("zone root %s is %s content; this is a %s zone", r.Name, r.Zone, machine)
		}
		if seen[r.Name] {
			return fmt.Errorf("zone root %s given twice", r.Name)
		}
		seen[r.Name] = true
	}
	return nil
}

// Snapshot hashes the roots for the lock, so check can tell when a zone
// changed under a render.
func Snapshot(roots []Root) ([]lock.ZoneRoot, error) {
	out := make([]lock.ZoneRoot, 0, len(roots))
	for _, r := range roots {
		s, err := lock.TreeSum(r.Dir)
		if err != nil {
			return nil, fmt.Errorf("zone root %s: %w", r.Name, err)
		}
		out = append(out, lock.ZoneRoot{Name: r.Name, Zone: r.Zone, Root: r.Dir, SHA256: s})
	}
	return out, nil
}

var idRe = regexp.MustCompile(`^([a-z][a-z0-9-]*):([a-z0-9-]+)/([A-Za-z0-9._-]+)$`)

// ID is a registry item id: <zone>:<kind>/<name>.
type ID struct{ Zone, Kind, Name string }

func (i ID) String() string { return i.Zone + ":" + i.Kind + "/" + i.Name }

// ParseID parses and validates an item id.
func ParseID(s string) (ID, error) {
	m := idRe.FindStringSubmatch(s)
	if m == nil {
		return ID{}, fmt.Errorf("item id %q: want <zone>:<kind>/<name>", s)
	}
	return ID{m[1], m[2], m[3]}, nil
}

// Admit checks the project zone boundary: shared items are valid
// everywhere; anything else must be the project's own zone. A personal:*
// id in a work project is refused even on a personal machine.
func Admit(project string, id ID) error {
	if id.Zone == Shared || id.Zone == project {
		return nil
	}
	return fmt.Errorf("%s is %s content; this project is zone = %q (refused)", id, id.Zone, project)
}

// EnvAllowed: PISTE_AGENTS_* inputs (name is the suffix, e.g. "AGENTS_HOME")
// are ignored unless --allow-env.
func EnvAllowed(allow bool, getenv func(string) string, name string) string {
	if !allow || !strings.HasPrefix(name, "AGENTS_") {
		return ""
	}
	return envvar.Get(getenv, name)
}
