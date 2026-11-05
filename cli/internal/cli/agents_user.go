package cli

// User-scope `piste agents`: apply, check, sync --user, update, budget. Their
// outputs and exit codes are pinned by the userscope goldens.
//
// The piste-agents wrapper the Nix module installs sets PISTE_AGENTS_TOOL=1: then
// these behave like the piste-agents tool (no `piste mcp sync` step after sync).

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/agents/userscope"
	"github.com/elocke/piste/cli/internal/envvar"
)

// ledgerPath is --ledger, else the default ledger.
func (o *agentsOpts) ledgerPath() string {
	if o.ledger != "" {
		return o.ledger
	}
	return userscope.DefaultLedger(o.homeDir())
}

// loadManifest prints load_manifest's SystemExit message on failure.
func (a *App) loadManifest(o *agentsOpts) (*userscope.Manifest, error) {
	m, err := userscope.LoadManifest(o.manifestPath())
	if err != nil {
		fmt.Fprintln(a.Stderr, err.Error())
		return nil, ExitError{1}
	}
	return m, nil
}

func (a *App) agentsApply(o *agentsOpts) *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "apply",
		Short: "Splice the composed instruction blocks (home-manager activation)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := a.loadManifest(o)
			if err != nil {
				return err
			}
			n, err := userscope.Apply(m, o.ledgerPath(), o.homeDir(), force, a.Stdout, a.Stderr)
			if err != nil {
				return fail(a.Stderr, err)
			}
			if n > 0 {
				return ExitError{1}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite a hand-edited managed block")
	return c
}

func (a *App) agentsCheck(o *agentsOpts) *cobra.Command {
	var asJSON, skillsOnly bool
	c := &cobra.Command{
		Use:   "check",
		Short: "Compare links, instruction blocks, hooks, plugins, marketplaces, tools and pins with the disk",
		Long: "Compare links, instruction blocks, hooks, plugins, marketplaces, tools and pins with the disk.\n\n" +
			"States: ok | missing | drift | undeclared | policy | tolerated (+ deferred / unmanaged for\n" +
			"skills). Exits non-zero on anything but ok/tolerated/deferred/unmanaged, and on\n" +
			"undeclared only when the zone policy says unlisted = \"deny\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := a.loadManifest(o)
			if err != nil {
				return err
			}
			home := o.homeDir()
			zn, locks, err := userscope.ZoneFromManifest(m)
			var lk userscope.ByKind
			if err == nil && zn != nil {
				lk, err = userscope.LoadLocks(locks)
			}
			if err != nil {
				fmt.Fprintf(a.Stderr, "piste-agents: %s\n", err)
				return ExitError{1}
			}
			rows := userscope.Check(m, home)
			hooks, err := userscope.CheckHooks(m, home)
			if err != nil {
				return fail(a.Stderr, err)
			}
			rows = append(rows, hooks...)
			if zn != nil && !skillsOnly {
				tp, err := userscope.ThirdPartyRows(zn, lk, userscope.RealObserver{Home: home}, userscope.Hostname())
				var le userscope.LockError
				switch {
				case errors.As(err, &le):
					rows = append(rows, userscope.Row{State: "error", Kind: "third-party", Target: "harness CLIs", Detail: le.Msg})
				case err != nil:
					return fail(a.Stderr, err)
				default:
					rows = append(rows, tp...)
				}
			}
			healthy := userscope.HealthyStates(zn)
			if asJSON {
				s, err := userscope.JSON(rows)
				if err != nil {
					return err
				}
				fmt.Fprint(a.Stdout, s)
			} else {
				fmt.Fprint(a.Stdout, userscope.TextWith(rows, healthy))
			}
			if userscope.BadWith(rows, healthy) {
				return ExitError{1}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	c.Flags().BoolVar(&skillsOnly, "skills-only", false, "links and instruction blocks only; skip the harness CLIs")
	return c
}

// userSync is `piste agents sync --user` (and sync outside a project).
func (a *App) userSync(o *agentsOpts, cmd *cobra.Command) error {
	fl := cmd.Flags()
	prune, _ := fl.GetBool("prune")
	apply, _ := fl.GetBool("apply")
	repin, _ := fl.GetBool("repin")
	noMCP, _ := fl.GetBool("no-mcp")
	dryRun := o.dryRun
	tool := envvar.Get(a.Env.Getenv, "AGENTS_TOOL") == "1"
	if apply && !prune {
		if tool {
			fmt.Fprintln(a.Stderr, "piste-agents sync: --apply only applies to --prune")
		} else {
			fmt.Fprintln(a.Stderr, "Error: --apply only applies to --prune")
		}
		return ExitError{2}
	}
	rc, err := func() (int, error) {
		m, err := a.loadManifest(o)
		if err != nil {
			return 1, nil
		}
		home := o.homeDir()
		zn, locks, err := userscope.ZoneFromManifest(m)
		var lk userscope.ByKind
		if err == nil && zn != nil {
			lk, err = userscope.LoadLocks(locks)
		}
		if err != nil {
			fmt.Fprintf(a.Stderr, "piste-agents: %s\n", err)
			return 1, nil
		}
		fails, err := userscope.SyncHooks(m, o.ledgerPath(), home, dryRun, a.Stdout, a.Stderr)
		if err != nil {
			return 1, err
		}
		rc := 0
		if fails > 0 {
			rc = 1
		}
		if zn == nil {
			fmt.Fprintln(a.Stdout, "piste-agents sync: this generation declares no third-party items")
			return rc, nil
		}
		r2, err := userscope.CmdSync(zn, lk, userscope.RealObserver{Home: home}, prune, apply, dryRun, repin, a.Stdout, home)
		if err != nil {
			return 1, err
		}
		if r2 != 0 {
			return r2, nil
		}
		return rc, nil
	}()
	if err != nil {
		return fail(a.Stderr, err)
	}
	if !tool && !noMCP {
		argv := []string{"piste", "mcp", "sync"}
		for _, x := range []struct {
			on   bool
			flag string
		}{{prune, "--prune"}, {apply, "--apply"}, {dryRun, "--dry-run"}} {
			if x.on {
				argv = append(argv, x.flag)
			}
		}
		fmt.Fprintf(a.Stdout, "\n$ %s\n", strings.Join(argv, " "))
		c := exec.Command(argv[0], argv[1:]...)
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, a.Stdout, a.Stderr
		mrc := 0
		if err := c.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				mrc = ee.ExitCode()
			} else {
				fmt.Fprintf(a.Stderr, "piste agents: %s: %v\n", strings.Join(argv, " "), err)
				mrc = 1
			}
		}
		if mrc != 0 {
			rc = mrc
		}
	}
	if rc != 0 {
		return ExitError{rc}
	}
	return nil
}

func (a *App) agentsUpdate(o *agentsOpts) *cobra.Command {
	var all, fromInstalled bool
	var zoneDir, base string
	c := &cobra.Command{
		Use:   "update [NAME...]",
		Short: "Resolve pins and rewrite agents/agents.lock (run inside a zone repo)",
		Long: "Resolve pins and rewrite agents/agents.lock (run inside a zone repo).\n\n" +
			"`piste agents update` fills in missing entries; `update NAME…` moves those;\n" +
			"`update --all` moves everything. `--from-installed`\n" +
			"pins plugins and marketplaces to what this machine has.",
		RunE: func(cmd *cobra.Command, names []string) error {
			b := base
			if b == "" {
				if mb, err := os.ReadFile(o.manifestPath()); err == nil {
					if v, err := userscope.ParseOrdered(mb); err == nil {
						if mo, ok := v.(*userscope.Obj); ok {
							for _, e := range asList(mo.Get("thirdParty")) {
								eo, _ := e.(*userscope.Obj)
								if s, _ := eo.Get("file").(string); s == "agents.base.toml" {
									b, _ = eo.Get("path").(string)
									break
								}
							}
						}
					}
				}
			}
			var files []string
			if b != "" {
				files = append(files, b)
			}
			own := filepath.Join(zoneDir, "agents.toml")
			files = append(files, own)
			lockPath := filepath.Join(zoneDir, "agents.lock")
			zn, err := userscope.NewZone(files, nil)
			// A root that extends its zone (an overlay's own agents/ beside
			// its team layer's) locks only what it declares: the shared
			// catalog's items are in the lock of the root with the policy.
			extends := err == nil && zn.Extends(own)
			if extends {
				zn = zn.Own(own)
			}
			var old, nw userscope.ByKind
			if err == nil {
				old, err = userscope.LoadLock(lockPath)
			}
			if err == nil {
				var sel map[string]bool
				_, statErr := os.Stat(lockPath)
				switch {
				case !all && len(names) == 0 && statErr == nil:
					sel = map[string]bool{} // only fill in what is missing
				case all || len(names) == 0:
					sel = nil
				default:
					sel = map[string]bool{}
					for _, n := range names {
						sel[n] = true
					}
				}
				nw, err = userscope.Update(zn, old, sel, fromInstalled, userscope.RealObserver{Home: o.homeDir()}, a.Stdout, "nix", "claude")
			}
			var le userscope.LockError
			if errors.As(err, &le) {
				fmt.Fprintf(a.Stderr, "piste-agents update: %s\n", le.Msg)
				return ExitError{1}
			}
			if err != nil {
				return fail(a.Stderr, err)
			}
			if extends {
				// The catalog marketplaces its plugins name stay in this
				// lock unless the policy root's lock pins them; that lock
				// comes from the installed manifest when it is this zone's.
				var taken userscope.ByKind
				if _, statErr := os.Stat(o.manifestPath()); statErr == nil {
					m, err := userscope.LoadManifest(o.manifestPath())
					if err == nil {
						taken, err = userscope.PolicyRootLock(m, zoneDir)
					}
					if err != nil {
						return fail(a.Stderr, err)
					}
				}
				nw = userscope.OwnLock(zn, nw, old, own, taken)
			}
			changes := userscope.LockChanges(old, nw)
			if err := os.WriteFile(lockPath+".piste-tmp", []byte(userscope.DumpLock(nw, "")), 0o644); err != nil {
				return fail(a.Stderr, err)
			}
			if err := os.Rename(lockPath+".piste-tmp", lockPath); err != nil {
				return fail(a.Stderr, err)
			}
			if len(changes) > 0 {
				fmt.Fprintln(a.Stdout, strings.Join(changes, "\n"))
			} else {
				fmt.Fprintln(a.Stdout, "agents.lock: no changes")
			}
			bs := b
			if bs == "" {
				bs = "none"
			}
			if extends {
				bs += "; this root extends its zone: its own items only"
			}
			fmt.Fprintf(a.Stdout, "wrote %s (base: %s)\n", lockPath, bs)
			return nil
		},
	}
	f := c.Flags()
	f.BoolVar(&all, "all", false, "move every item")
	f.StringVar(&zoneDir, "zone", "agents", "the zone's agents/ dir")
	f.StringVar(&base, "base", "", "piste agents/agents.base.toml (default: this host's switched copy)")
	f.BoolVar(&fromInstalled, "from-installed", false, "pin plugins/marketplaces to what this machine has")
	return c
}

func asList(v any) []any { l, _ := v.([]any); return l }

func (a *App) agentsBudget(o *agentsOpts) *cobra.Command {
	var transcript, projects string
	var threshold int
	var asJSON bool
	c := &cobra.Command{
		Use:    "budget",
		Hidden: true, // maintainer tool
		Short:  "Start-of-session context of the latest Claude transcript; warns above a threshold",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if projects == "" {
				d := a.Env.Getenv("CLAUDE_CONFIG_DIR")
				if d == "" {
					d = filepath.Join(o.homeDir(), ".claude")
				}
				projects = filepath.Join(d, "projects")
			}
			var m *userscope.Obj
			var err error
			if transcript != "" {
				m, err = userscope.MeasureTranscript(transcript)
			} else {
				m, err = userscope.LatestMeasure(projects, 40)
			}
			if err != nil {
				return fail(a.Stderr, err)
			}
			if m == nil {
				fmt.Fprintln(a.Stderr, "piste-agents budget: no transcript with a skill listing found")
				return ExitError{2}
			}
			if rc := userscope.BudgetOutput(m, threshold, asJSON, a.Stdout); rc != 0 {
				return ExitError{rc}
			}
			return nil
		},
	}
	f := c.Flags()
	f.StringVar(&transcript, "transcript", "", "a transcript file (default: the latest with a skill listing)")
	f.StringVar(&projects, "projects", "", "transcript root (default $CLAUDE_CONFIG_DIR or ~/.claude, + /projects)")
	f.IntVar(&threshold, "threshold", 20000, "warn above this many start tokens")
	f.BoolVar(&asJSON, "json", false, "output as JSON")
	return c
}

var _ = strconv.Itoa
