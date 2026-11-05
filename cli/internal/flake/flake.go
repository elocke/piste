// Package flake reads flake.lock and resolves home-manager profiles.
package flake

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/elocke/piste/cli/internal/execx"
)

// Lock is the parts of flake.lock piste reads.
type Lock struct {
	Nodes map[string]struct {
		Inputs map[string]json.RawMessage `json:"inputs"`
		Locked map[string]any             `json:"locked"`
	} `json:"nodes"`
}

// ReadLock parses <dir>/flake.lock.
func ReadLock(dir string) (*Lock, error) {
	b, err := os.ReadFile(filepath.Join(dir, "flake.lock"))
	if err != nil {
		return nil, err
	}
	var l Lock
	if err := json.Unmarshal(b, &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// ref is an input reference: a node name, or a follows path.
func ref(raw json.RawMessage) (string, []string) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var l []string
	if json.Unmarshal(raw, &l) == nil {
		return "", l
	}
	return "", nil
}

// ResolveInput is _resolve_input_node: a root input's locked node, following
// any follows chain.
func (l *Lock) ResolveInput(name string) map[string]any {
	raw, ok := l.Nodes["root"].Inputs[name]
	if !ok {
		return nil
	}
	s, path := ref(raw)
	if s != "" {
		return l.Nodes[s].Locked
	}
	if len(path) < 2 {
		return nil
	}
	node := l.Nodes[path[0]]
	for _, step := range path[1:] {
		innerRaw, ok := node.Inputs[step]
		if !ok {
			return nil
		}
		is, ip := ref(innerRaw)
		switch {
		case is != "":
			node = l.Nodes[is]
		case len(ip) > 0:
			node = l.Nodes[ip[0]]
			for _, st := range ip[1:] {
				nr, ok := node.Inputs[st]
				if !ok {
					return nil
				}
				ns, _ := ref(nr)
				if ns == "" {
					return nil
				}
				node = l.Nodes[ns]
			}
		default:
			return nil
		}
	}
	return node.Locked
}

// Now is the clock (tests set it).
var Now = time.Now

// AgeDays is the age of an input's lastModified in whole days (nil if
// unknown). For nixpkgs it falls back to the node named "nixpkgs".
func AgeDays(dir, input string) (int, bool) {
	l, err := ReadLock(dir)
	if err != nil {
		return 0, false
	}
	locked := l.ResolveInput(input)
	if locked == nil && input == "nixpkgs" {
		locked = l.Nodes["nixpkgs"].Locked
	}
	ts, ok := locked["lastModified"].(float64)
	if !ok {
		return 0, false
	}
	d := Now().UTC().Sub(time.Unix(int64(ts), 0).UTC())
	return int(math.Floor(d.Hours() / 24)), true // timedelta.days floors
}

// StaleWarning is warn_if_stale: nixpkgs older than 30 days.
func StaleWarning(dir string) string {
	if d, ok := AgeDays(dir, "nixpkgs"); ok && d > 30 {
		return "nixpkgs is " + itoa(d) + " days old. Run 'piste update' for the newest release."
	}
	return ""
}

// InputCount counts non-root nodes.
func InputCount(dir string) int {
	l, err := ReadLock(dir)
	if err != nil {
		return 0
	}
	n := 0
	for k := range l.Nodes {
		if k != "root" {
			n++
		}
	}
	return n
}

func itoa(i int) string { return strconv.Itoa(i) }

// HMProfiles lists homeConfigurations names (nix eval --json …
// --apply builtins.attrNames), [] on any failure.
func HMProfiles(ctx context.Context, r execx.Runner, flakeRef string) []string {
	return Configurations(ctx, r, flakeRef, "homeConfigurations")
}

// Configurations lists the names of the flake's attribute set attr
// (darwinConfigurations, homeConfigurations), [] on any failure.
func Configurations(ctx context.Context, r execx.Runner, flakeRef, attr string) []string {
	res, err := r.Run(ctx, execx.Cmd{Argv: []string{"nix", "eval", "--json", flakeRef + "#" + attr,
		"--apply", "builtins.attrNames", "--no-write-lock-file"}})
	if err != nil {
		return nil
	}
	var names []string
	if json.Unmarshal(res.Stdout, &names) != nil {
		return nil
	}
	return names
}

// DetectProfile is detect_hm_profile: user@hostname, else the only user@*,
// else user@linux.
func DetectProfile(ctx context.Context, r execx.Runner, flakeRef, user, host string) string {
	profiles := HMProfiles(ctx, r, flakeRef)
	exact := user + "@" + host
	var mine []string
	for _, p := range profiles {
		if p == exact {
			return exact
		}
		if strings.HasPrefix(p, user+"@") {
			mine = append(mine, p)
		}
	}
	if len(mine) == 1 {
		return mine[0]
	}
	return user + "@linux"
}
