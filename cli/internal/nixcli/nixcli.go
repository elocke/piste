// Package nixcli calls the Nix CLI with machine-readable output.
// There is no stable Go Nix library; `nix … --json` is the
// supported interface. nix itself is never bundled (see cli/default.nix).
package nixcli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/elocke/piste/cli/internal/execx"
)

// Nix runs nix through a Runner.
type Nix struct {
	Run execx.Runner
	// Extra args for every call, e.g. --show-trace for --verbose, or
	// --override-input for sibling checkouts.
	Extra []string
}

// EvalJSON evaluates installable (e.g. "path:/x#darwinConfigurations")
// with --json, optionally applying a Nix function, and decodes into v.
func (n Nix) EvalJSON(ctx context.Context, installable, apply string, v any) error {
	argv := []string{"nix", "eval", "--json", installable}
	if apply != "" {
		argv = append(argv, "--apply", apply)
	}
	argv = append(argv, n.Extra...)
	res, err := n.Run.Run(ctx, execx.Cmd{Argv: argv, Timeout: 10 * time.Minute})
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(res.Stderr)))
	}
	if err := json.Unmarshal(res.Stdout, v); err != nil {
		return fmt.Errorf("nix eval %s: decoding JSON: %w", installable, err)
	}
	return nil
}

// AttrNames lists the attribute names of an attrset installable.
func (n Nix) AttrNames(ctx context.Context, installable string) ([]string, error) {
	var names []string
	err := n.EvalJSON(ctx, installable, "builtins.attrNames", &names)
	return names, err
}

// BuildOutPaths builds installables without a result link and returns the
// output paths.
func (n Nix) BuildOutPaths(ctx context.Context, installables ...string) ([]string, error) {
	argv := append([]string{"nix", "build", "--no-link", "--print-out-paths"}, installables...)
	argv = append(argv, n.Extra...)
	res, err := n.Run.Run(ctx, execx.Cmd{Argv: argv})
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(res.Stderr)))
	}
	return strings.Fields(string(res.Stdout)), nil
}
