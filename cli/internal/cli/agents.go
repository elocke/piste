package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/agents/engine"
	"github.com/elocke/piste/cli/internal/agents/fsx"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/plan"
	"github.com/elocke/piste/cli/internal/agents/project"
	"github.com/elocke/piste/cli/internal/agents/registry"
	"github.com/elocke/piste/cli/internal/agents/safety"
	"github.com/elocke/piste/cli/internal/agents/userscope"
	"github.com/elocke/piste/cli/internal/agents/zone"
)

func init() { register("agents", newAgents) }

// agentsOpts are the flags shared by the agents subcommands.
type agentsOpts struct {
	home      string
	allowEnv  bool
	zoneRoots []string
	zone      string
	manifest  string // the user-scope manifest (zone roots come from it)
	ledger    string
	root      string
	force     bool
	dryRun    bool
	asJSON    bool

	// The registry (agents_registry.go).
	registryDir string
	workdir     bool
	thirdParty  []string
	rootRevs    []string
}

func newAgents(a *App) *cobra.Command {
	o := &agentsOpts{}
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Agent skills, instructions, hooks and MCP (piste.agents)",
		Long: "Agent content for the harnesses (Claude Code, Codex, Hermes).\n\n" +
			"User scope ($HOME):  apply, check — what the piste.agents module declared.\n" +
			"Project scope (git root, .agents/agents.toml):  init, add, remove, sync, drift, audit.\n" +
			"Registry:  list, show.\n\n" +
			"Zone content is read only from explicit roots: --zone-root name=zone:/dir, or the\n" +
			"sources of the Nix-written manifest (~/.config/piste-agents/manifest.json). Nothing\n" +
			"else in $HOME, parent directories or the environment is consulted;\n" +
			"PISTE_AGENTS_* variables apply only with --allow-env.",
		SilenceUsage: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			fsx.Fault = os.Getenv("PISTE_FAULT") // test-only fault injection
			return nil
		},
	}
	a.agentsRegistryCmds(cmd, o)
	pf := cmd.PersistentFlags()
	pf.StringVar(&o.home, "home", "", "home directory (default $HOME)")
	pf.BoolVar(&o.allowEnv, "allow-env", false, "honour PISTE_AGENTS_HOME / PISTE_AGENTS_ROOTS")
	pf.StringArrayVar(&o.zoneRoots, "zone-root", nil, "zone root name=zone:/dir (repeatable)")
	pf.StringVar(&o.manifest, "manifest", "", "manifest of the user scope (default ~/.config/piste-agents/manifest.json)")
	pf.StringVar(&o.root, "root", "", "project root (default: the git root of the current directory)")
	pf.StringVar(&o.ledger, "ledger", "", "ledger of the user scope (default ~/.local/state/piste-agents/ledger.json)")

	cmd.AddCommand(a.agentsApply(o), a.agentsCheck(o), a.agentsSync(o), a.agentsAdd(o), a.agentsRemove(o),
		a.agentsDrift(o), a.agentsAudit(o), a.agentsInit(o), a.agentsList(o), a.agentsShow(o), a.agentsBundle(o),
		a.agentsUpdate(o), a.agentsBudget(o))
	for _, c := range cmd.Commands() {
		switch c.Name() {
		case "sync", "add", "remove", "drift", "audit", "init", "list", "show", "bundle":
			c.Flags().StringVar(&o.zone, "zone", "", zoneFlagHelp)
		}
	}
	return cmd
}

// zoneFlagHelp is the --zone help of the commands that resolve roots.
const zoneFlagHelp = "this machine's zone (default: from the manifest, else the one non-shared --zone-root)"

func (o *agentsOpts) homeDir() string {
	if o.home != "" {
		return o.home
	}
	if h := zone.EnvAllowed(o.allowEnv, os.Getenv, "AGENTS_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func (o *agentsOpts) manifestPath() string {
	if o.manifest != "" {
		return o.manifest
	}
	return userscope.DefaultManifest(o.homeDir())
}

// registry builds the registry from explicit roots only.
func (o *agentsOpts) registry() (*registry.Registry, string, error) {
	var roots []zone.Root
	specs := append([]string(nil), o.zoneRoots...)
	if env := zone.EnvAllowed(o.allowEnv, os.Getenv, "AGENTS_ROOTS"); env != "" {
		specs = append(specs, strings.Split(env, ",")...)
	}
	for _, s := range specs {
		r, err := zone.ParseRoot(s)
		if err != nil {
			return nil, "", err
		}
		roots = append(roots, r)
	}
	machine := o.zone
	var links []manifestLink
	if len(roots) == 0 {
		if reg, ok, err := o.builtRegistry(); ok {
			if err != nil {
				return nil, "", err
			}
			return reg, reg.Zone, nil
		}
		// The Nix-written manifest names this machine's zone roots.
		b, err := os.ReadFile(o.manifestPath())
		if err != nil {
			return nil, "", fmt.Errorf("no registry: pass --zone-root or --registry, run `piste agents registry pull <zone bundle registry>`, or enable piste.agents (%s)", o.manifestPath())
		}
		var m struct {
			Zone    string `json:"zone"`
			Sources []struct {
				Name, Zone, Workdir string
			} `json:"sources"`
			Links []manifestLink `json:"links"`
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return nil, "", err
		}
		for _, s := range m.Sources {
			roots = append(roots, zone.Root{Name: s.Name, Zone: s.Zone, Dir: s.Workdir})
		}
		links = m.Links
		if machine == "" {
			machine = m.Zone
		}
	}
	if machine == "" {
		inferred, err := zone.Infer(roots)
		if err != nil {
			return nil, "", err
		}
		machine = inferred
	}
	if err := zone.Validate(machine, roots); err != nil {
		return nil, "", err
	}
	fetched, err := o.fetchedThirdParty(links)
	if err != nil {
		return nil, "", err
	}
	reg, err := registry.BuildWith(roots, fetched)
	if err != nil {
		return nil, "", err
	}
	return reg, machine, gitRevs(reg, roots, o.rootRevs)
}

// inProject reports whether a project manifest is in reach.
func (o *agentsOpts) inProject() bool {
	root := o.root
	if root == "" {
		wd, _ := os.Getwd()
		r, err := project.Root(wd)
		if err != nil {
			return false
		}
		root = r
	}
	_, err := os.Stat(filepath.Join(root, project.ManifestFile))
	return err == nil
}

func (o *agentsOpts) project() (*engine.Project, error) {
	root := o.root
	if root == "" {
		wd, _ := os.Getwd()
		r, err := project.Root(wd)
		if err != nil {
			return nil, err
		}
		root = r
	}
	reg, _, err := o.registry()
	if err != nil {
		return nil, err
	}
	p, err := engine.Open(root, reg)
	if err != nil {
		return nil, err
	}
	p.Force = o.force
	return p, nil
}

// fail prints "piste agents: <err>" and exits 1.
func fail(w io.Writer, err error) error {
	for _, line := range strings.Split(err.Error(), "\n") {
		fmt.Fprintln(w, "piste agents: "+safety.StripControl(line))
	}
	return ExitError{1}
}

// run prints (and, unless dry-run, applies) a project plan.
func (a *App) run(o *agentsOpts, p *engine.Project, pl *plan.Plan) error {
	for _, w := range p.Warnings {
		fmt.Fprintln(a.Stderr, "piste agents: warning: "+safety.StripControl(w))
	}
	changes := pl.Changes()
	if o.dryRun {
		if len(changes) == 0 {
			fmt.Fprintln(a.Stdout, "piste agents: nothing to do")
			return nil
		}
		for _, c := range changes {
			fmt.Fprintf(a.Stdout, "would %-6s %s  %s\n", c.Op, c.Path, c.What)
		}
		fmt.Fprint(a.Stdout, pl.Diff())
		return nil
	}
	if err := p.Commit(pl); err != nil {
		return fail(a.Stderr, err)
	}
	for _, c := range changes {
		fmt.Fprintf(a.Stdout, "%-6s %s\n", c.Op, c.Path)
	}
	if len(changes) == 0 {
		fmt.Fprintln(a.Stdout, "piste agents: up to date")
	}
	return nil
}

func projectFlags(c *cobra.Command, o *agentsOpts) {
	c.Flags().BoolVarP(&o.dryRun, "dry-run", "n", false, "show the plan and diff; write nothing")
	c.Flags().BoolVar(&o.force, "force", false, "overwrite or remove entries edited by hand since piste wrote them")
}

func (a *App) agentsSync(o *agentsOpts) *cobra.Command {
	var user bool
	c := &cobra.Command{
		Use:   "sync [--user]",
		Short: "Reconcile the project with .agents/agents.toml (never pulls newer versions)",
		Long: "In a project, sync reconciles the project with .agents/agents.toml.\n" +
			"--user, or outside a project, runs the user-scope sync: the third-party lock,\n" +
			"the carrier and hooks.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Outside a project (no .agents/agents.toml at the git root), or
			// with --user, sync means the user scope.
			if user || !o.inProject() {
				return a.userSync(o, cmd)
			}
			if len(args) > 0 {
				return fail(a.Stderr, fmt.Errorf("unexpected arguments %v", args))
			}
			p, err := o.project()
			if err != nil {
				return fail(a.Stderr, err)
			}
			pl, err := p.Sync()
			if err != nil {
				return fail(a.Stderr, err)
			}
			return a.run(o, p, pl)
		},
	}
	projectFlags(c, o)
	c.Flags().BoolVar(&user, "user", false, "sync the user scope (third-party lock, carrier, hooks)")
	f := c.Flags()
	f.Bool("prune", false, "user scope: also list undeclared items for removal (dry run unless --apply)")
	f.Bool("apply", false, "user scope, with --prune: really remove them")
	f.Bool("repin", false, "user scope: switch drifted github marketplaces to a pinned local mirror")
	f.Bool("no-mcp", false, "user scope: don't run piste mcp sync")
	return c
}

func (a *App) agentsAdd(o *agentsOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "add <id>...",
		Short: "Use registry items in this project (zone + policy checked)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := o.project()
			if err != nil {
				return fail(a.Stderr, err)
			}
			args = withRequires(p.Reg, p.Manifest.Uses, args)
			pl, err := p.Add(args...)
			if err != nil {
				return fail(a.Stderr, err)
			}
			return a.run(o, p, pl)
		},
	}
	projectFlags(c, o)
	return c
}

func (a *App) agentsRemove(o *agentsOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "remove <id>...",
		Short: "Stop using items; removes exactly what the lock says piste wrote",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := o.project()
			if err != nil {
				return fail(a.Stderr, err)
			}
			pl, err := p.Remove(args...)
			if err != nil {
				return fail(a.Stderr, err)
			}
			return a.run(o, p, pl)
		},
	}
	projectFlags(c, o)
	return c
}

func printFindings(w io.Writer, fs []engine.Finding, asJSON bool) error {
	if asJSON {
		if fs == nil {
			fs = []engine.Finding{}
		}
		b, err := json.MarshalIndent(fs, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(b))
		return nil
	}
	counts := map[string]int{}
	for _, f := range fs {
		fmt.Fprintf(w, "%-10s %s  %s\n", f.State, safety.StripControl(f.What), safety.StripControl(f.Detail))
		counts[f.State]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	fmt.Fprintf(w, "\n%s\n", strings.Join(parts, ", "))
	return nil
}

func (a *App) agentsDrift(o *agentsOpts) *cobra.Command {
	var ci bool
	c := &cobra.Command{
		Use:   "drift",
		Short: "Lock ↔ disk ↔ registry comparison; exits 1 on drift",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := o.project()
			if err != nil {
				return fail(a.Stderr, err)
			}
			fs := p.Drift()
			bad := false
			for _, f := range fs {
				if !engine.Healthy[f.State] || (ci && f.State == "unknown") {
					bad = true
				}
			}
			if err := printFindings(a.Stdout, fs, o.asJSON); err != nil {
				return err
			}
			if bad {
				return ExitError{1}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&o.asJSON, "json", false, "output as JSON")
	c.Flags().BoolVar(&ci, "ci", false, "also fail on unknown entries (a branch-protection check)")
	return c
}

func (a *App) agentsAudit(o *agentsOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "audit",
		Short: "Inventory of the project's agent content, attributed (works before init)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !o.inProject() {
				p, err := o.survey()
				if err != nil {
					return fail(a.Stderr, err)
				}
				return printFindings(a.Stdout, p.Audit(), o.asJSON)
			}
			p, err := o.project()
			if err != nil {
				return fail(a.Stderr, err)
			}
			return printFindings(a.Stdout, p.Audit(), o.asJSON)
		},
	}
	c.Flags().BoolVar(&o.asJSON, "json", false, "output as JSON")
	return c
}

func (a *App) agentsInit(o *agentsOpts) *cobra.Command {
	var harnesses []string
	var adopt bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Write .agents/agents.toml; --adopt imports .mcp.json (secrets redacted)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root := o.root
			if root == "" {
				wd, _ := os.Getwd()
				r, err := project.Root(wd)
				if err != nil {
					return fail(a.Stderr, err)
				}
				root = r
			}
			z := o.zone
			if z == "" {
				_, machine, err := o.registry()
				if err != nil {
					return fail(a.Stderr, fmt.Errorf("%w (or pass --zone)", err))
				}
				z = machine
			}
			if _, err := os.Stat(filepath.Join(root, project.ManifestFile)); err == nil && !o.force {
				return fail(a.Stderr, fmt.Errorf("%s exists (--force to rewrite it)", project.ManifestFile))
			}
			var ad engine.Adopted
			var found engine.Adoption
			if adopt {
				var err error
				if ad, err = engine.AdoptMCP(root); err != nil {
					return fail(a.Stderr, err)
				}
				sp, err := o.survey()
				if err != nil {
					return fail(a.Stderr, err)
				}
				found = sp.Adoption()
			}
			text, err := project.InitWith(z, harnesses, ad.MCP, found.Uses, found.Locals)
			if err != nil {
				return fail(a.Stderr, err) // nothing written (never a literal)
			}
			for _, w := range ad.Warnings {
				fmt.Fprintln(a.Stderr, "piste agents: warning: "+w)
			}
			if o.dryRun {
				fmt.Fprint(a.Stdout, text)
				return nil
			}
			if err := fsx.Guard(root, filepath.Join(root, project.ManifestFile), nil); err != nil {
				return fail(a.Stderr, err)
			}
			if err := fsx.WriteAtomic(filepath.Join(root, project.ManifestFile), []byte(text), 0o644); err != nil {
				return fail(a.Stderr, err)
			}
			fmt.Fprintf(a.Stdout, "wrote %s (zone = %q)\n", project.ManifestFile, z)
			if !adopt || len(ad.MCP)+len(found.Uses) == 0 {
				return nil
			}
			// Take ownership of the imported servers: .mcp.json is rewritten
			// with the redacted (environment-sourced) definitions.
			reg, _, err := o.registry()
			if err != nil {
				reg = &registry.Registry{Items: map[string]*registry.Item{}}
			}
			p, err := engine.Open(root, reg)
			if err != nil {
				return fail(a.Stderr, err)
			}
			if err := found.Relocate(root); err != nil {
				return fail(a.Stderr, err)
			}
			p.Adopt = found.Adopt
			if p.Adopt == nil {
				p.Adopt = map[string]bool{}
			}
			for _, s := range ad.MCP {
				p.Adopt[lock.Owned{File: ".mcp.json", Kind: lock.OwnedJSON, Path: "/mcpServers/" + s.Name}.ID()] = true
			}
			pl, err := p.Sync()
			if err != nil {
				return fail(a.Stderr, err)
			}
			return a.run(o, p, pl)
		},
	}
	c.Flags().StringSliceVar(&harnesses, "harness", []string{"claude", "codex"}, "harnesses (claude, codex, agentsmd)")
	c.Flags().BoolVar(&adopt, "adopt", false, "import existing .mcp.json servers as project-local entries")
	c.Flags().BoolVar(&o.force, "force", false, "rewrite an existing manifest")
	c.Flags().BoolVarP(&o.dryRun, "dry-run", "n", false, "print the manifest; write nothing")
	return c
}

func (a *App) agentsList(o *agentsOpts) *cobra.Command {
	var kind string
	c := &cobra.Command{
		Use:   "list",
		Short: "List registry items",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			reg, _, err := o.registry()
			if err != nil {
				return fail(a.Stderr, err)
			}
			ms := reg.Search("", kind, "", "")
			if o.asJSON {
				if ms == nil {
					ms = []registry.Meta{}
				}
				return printJSON(a.Stdout, ms)
			}
			for _, m := range ms {
				fmt.Fprintf(a.Stdout, "%s\t%s\n", safety.StripControl(m.ID), safety.StripControl(firstLine(m.Description)))
			}
			return nil
		},
	}
	c.Flags().StringVar(&kind, "kind", "", "only this kind (skill, 3p-skill, agent, instruction, hook, mcp, …)")
	c.Flags().BoolVar(&o.asJSON, "json", false, "output as JSON (the items' metadata)")
	return c
}

func (a *App) agentsShow(o *agentsOpts) *cobra.Command {
	c := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one registry item: metadata, harness support, requirements, version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, _, err := o.registry()
			if err != nil {
				return fail(a.Stderr, err)
			}
			it, ok := reg.Get(args[0])
			if !ok {
				return fail(a.Stderr, fmt.Errorf("%s: not in the registry", args[0]))
			}
			if o.asJSON {
				return printJSON(a.Stdout, reg.Meta(it))
			}
			showMeta(a.Stdout, reg.Meta(it))
			return nil
		},
	}
	c.Flags().BoolVar(&o.asJSON, "json", false, "output as JSON")
	return c
}

// bundle renders a preset whole for Hermes (a removed skill is absent)
// and can verify an existing bundle tree against its manifest.json. With
// --target hermes it renders every hermes-<profile> preset into the
// hermes-skills image layout.
func (a *App) agentsBundle(o *agentsOpts) *cobra.Command {
	var out, check, target string
	var commits []string
	var managedMCP bool
	c := &cobra.Command{
		Use:    "bundle [<zone>:bundle/<preset>...] | --target hermes | --target claude,codex <presets…>",
		Hidden: true, // plumbing for the zone bundles
		Short:  "Render a preset (or every hermes-<profile> preset) as a Hermes skills bundle, presets as a claude,codex container bundle, or --check a bundle tree",
		RunE: func(cmd *cobra.Command, args []string) error {
			if target != "" && target != "hermes" {
				return a.containerBundle(o, args, strings.Split(target, ","), commits, out, managedMCP)
			}
			if len(args) > 1 {
				return fail(a.Stderr, errors.New("a Hermes bundle is one preset; pass --target claude,codex for a container bundle"))
			}
			if check != "" {
				probs, err := engine.CheckBundle(check)
				if err != nil {
					return fail(a.Stderr, err)
				}
				for _, p := range probs {
					fmt.Fprintln(a.Stdout, "FAIL "+p)
				}
				if len(probs) > 0 {
					return ExitError{1}
				}
				fmt.Fprintln(a.Stdout, "OK: bundle matches its manifest.json")
				return nil
			}
			if target == "hermes" && len(args) == 0 && out != "" {
				reg, machine, err := o.registry()
				if err != nil {
					return fail(a.Stderr, err)
				}
				cm := map[string]string{}
				for _, s := range commits {
					k, v, ok := strings.Cut(s, "=")
					if !ok || k == "" || v == "" {
						return fail(a.Stderr, fmt.Errorf("--source-commit %q: want <zone-root-name>=<sha>", s))
					}
					cm[k] = v
				}
				m, warnings, err := engine.WriteHermesBundle(reg, machine, cm, out)
				for _, w := range warnings {
					fmt.Fprintln(a.Stderr, "warning: "+w)
				}
				if err != nil {
					return fail(a.Stderr, err)
				}
				ps := make([]string, 0, len(m.Profiles))
				for p := range m.Profiles {
					ps = append(ps, fmt.Sprintf("%s=%d", p, len(m.Profiles[p])))
				}
				sort.Strings(ps)
				fmt.Fprintf(a.Stdout, "wrote %s: %d skills; profiles %s\n", out, len(m.Skills), strings.Join(ps, " "))
				return nil
			}
			if target != "" || len(args) != 1 || out == "" {
				return fail(a.Stderr, errors.New("usage: piste agents bundle <zone>:bundle/<preset> --out <dir> | --target hermes --out <dir> | --check <dir>"))
			}
			reg, machine, err := o.registry()
			if err != nil {
				return fail(a.Stderr, err)
			}
			it, ok := reg.Get(args[0])
			if !ok || it.Bundle == nil {
				return fail(a.Stderr, fmt.Errorf("%s: no such bundle in the registry", args[0]))
			}
			n, err := engine.WriteBundle(reg, machine, it, out)
			if err != nil {
				return fail(a.Stderr, err)
			}
			fmt.Fprintf(a.Stdout, "wrote %s: %d skills\n", out, n)
			return nil
		},
	}
	c.Flags().StringVar(&out, "out", "", "output directory (replaced whole)")
	c.Flags().StringVar(&check, "check", "", "verify a bundle directory: no extra, missing or changed skill")
	c.Flags().StringVar(&target, "target", "", "hermes: every hermes-<profile> preset → tree/ + profiles/<p>/ + manifest.json; claude,codex: a container bundle of the given presets")
	c.Flags().StringArrayVar(&commits, "source-commit", nil, "record a zone root's git commit in manifest.json: <name>=<sha> (repeatable)")
	c.Flags().BoolVar(&managedMCP, "managed-mcp", false, "container bundle: render the zone's [[mcp]] into managed-mcp.json (exclusive control of MCP in Claude Code)")
	return c
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
