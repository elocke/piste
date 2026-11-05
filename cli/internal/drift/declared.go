package drift

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/elocke/piste/cli/internal/execx"
)

// declaredNix is the expression Declared applies (declared.nix).
//
//go:embed declared.nix
var declaredNix string

// Package is a nixpkgs package found for a name.
type Package struct {
	Pname   string `json:"pname"`
	Version string `json:"version"`
}

// Declarations is what a host declares, from one eval of declared.nix.
type Declarations struct {
	Nix     []string            `json:"nix"`     // every declared Nix package's name
	Casks   []string            `json:"casks"`   // homebrew.casks
	Brews   []string            `json:"brews"`   // homebrew.brews
	Exclude []string            `json:"exclude"` // piste.packages' three exclude lists
	Files   map[string][]string `json:"files"`   // option path -> the files defining it
	Pkgs    map[string]Package  `json:"pkgs"`    // scanned name -> its package here
}

// EvalError is a failed eval; Stderr is nix's, for the caller to show.
type EvalError struct {
	Stderr []byte
	Err    error
}

func (e *EvalError) Error() string { return e.Err.Error() }
func (e *EvalError) Unwrap() error { return e.Err }

// lookupSafe: no top-level attribute has any other character, so a name
// like this needs no escaping in a Nix string, and any other isn't looked up.
var lookupSafe = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)

// oneLine is declared.nix without its full-line comments, on one line.
func oneLine(nix string) string {
	var keep []string
	for _, l := range strings.Split(nix, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, " ")
}

// applyExpr is the eval's --apply value: declared.nix applied to the
// lookup-safe names (once each, in order).
func applyExpr(names []string) string {
	var b strings.Builder
	b.WriteString("(" + oneLine(declaredNix) + ") [ ")
	seen := map[string]bool{}
	for _, n := range names {
		if lookupSafe.MatchString(n) && !seen[n] {
			seen[n] = true
			b.WriteString(`"` + n + `" `)
		}
	}
	b.WriteString("]")
	return b.String()
}

// Declared evaluates what the host at installable (a flake ref to its
// darwinConfigurations or homeConfigurations entry) declares, in one nix
// eval with the switch's override args, and looks names up in its pkgs.
func Declared(ctx context.Context, r execx.Runner, installable string, override, names []string) (Declarations, error) {
	argv := append(append([]string{"nix", "eval", "--json", installable}, override...), "--apply", applyExpr(names))
	res, err := r.Run(ctx, execx.Cmd{Argv: argv})
	if err != nil {
		return Declarations{}, &EvalError{Stderr: res.Stderr, Err: err}
	}
	var d Declarations
	if err := json.Unmarshal(res.Stdout, &d); err != nil {
		return Declarations{}, &EvalError{Stderr: res.Stderr, Err: fmt.Errorf("nix eval's output: %w", err)}
	}
	return d, nil
}
