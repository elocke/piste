package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/engine"
)

// containerBundle: `piste agents bundle --zone work --target claude,codex
// <zone>:bundle/<preset>... --out <dir>`. The zone
// roots are explicit (--zone-root); the zone is --zone or inferred from them.
func (a *App) containerBundle(o *agentsOpts, args, targets, revs []string, out string, managedMCP bool) error {
	if len(args) == 0 || out == "" {
		return fail(a.Stderr, errors.New("usage: piste agents bundle --target claude,codex <zone>:bundle/<preset>... --out <dir>"))
	}
	reg, machine, err := o.registry()
	if err != nil {
		return fail(a.Stderr, err)
	}
	opts := engine.ContainerOptions{Zone: machine, Targets: targets, SourceRevs: map[string]string{}, ManagedMCP: managedMCP}
	for _, r := range revs {
		name, rev, ok := strings.Cut(r, "=")
		if !ok || name == "" || rev == "" {
			return fail(a.Stderr, fmt.Errorf("--source-commit %q: want <zone-root-name>=<sha>", r))
		}
		opts.SourceRevs[name] = rev
	}
	for _, id := range args {
		it, ok := reg.Get(id)
		if !ok || it.Bundle == nil {
			return fail(a.Stderr, fmt.Errorf("%s: no such bundle in the registry", id))
		}
		opts.Presets = append(opts.Presets, it)
	}
	cm, warnings, err := engine.WriteContainerBundle(reg, opts, out)
	if err != nil {
		return fail(a.Stderr, err)
	}
	for _, w := range warnings {
		fmt.Fprintln(a.Stderr, "warning: "+w)
	}
	fmt.Fprintf(a.Stdout, "wrote %s: %s container bundle, %d skills, %d plugins\n", out, cm.Zone, len(cm.Skills), len(cm.Plugins))
	return nil
}
