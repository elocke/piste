package cli

// `piste mcp check|sync`. Writes go only through the harness CLIs (`claude
// mcp`, `codex mcp`); their config files are read, never written.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/elocke/piste/cli/internal/mcp"
	"github.com/elocke/piste/cli/internal/platform"
	"github.com/elocke/piste/cli/internal/ui"
)

func init() { register("mcp", newMCP) }

var syncable = []string{"claude-code", "codex"}

// clickErr prints "Error: msg" and exits 1.
func (a *App) clickErr(msg string) error {
	fmt.Fprintln(a.Stderr, "Error: "+msg)
	return ExitError{1}
}

func manifestIn(base string) string {
	zone := filepath.Join(base, "agents", "agents.toml")
	if b, err := os.ReadFile(zone); err == nil {
		var m map[string]any
		if toml.Unmarshal(b, &m) == nil {
			if _, ok := m["mcp"]; ok {
				return zone
			}
		}
	}
	return ""
}

// errNoZoneMCP: this host's zone overlay declares no MCP servers. check and
// sync treat it as a clean no-op (exit 0).
type errNoZoneMCP struct{ zone string }

func (e errNoZoneMCP) Error() string {
	return "no MCP servers declared for this zone (" + e.zone + ")"
}

// zoneOverlays reads the user-scope manifest piste.agents writes
// (~/.config/piste-agents/manifest.json) and returns this host's zone and the
// repo roots of that zone's sources (never shared or another zone's). ok is
// false when there is no readable manifest (a host without piste.agents).
func zoneOverlays(home string) (zone string, bases []string, ok bool) {
	b, err := os.ReadFile(filepath.Join(home, ".config", "piste-agents", "manifest.json"))
	if err != nil {
		return "", nil, false
	}
	var m struct {
		Zone    string `json:"zone"`
		Sources []struct {
			Zone    string `json:"zone"`
			Workdir string `json:"workdir"`
		} `json:"sources"`
	}
	if json.Unmarshal(b, &m) != nil || m.Zone == "" {
		return "", nil, false
	}
	for _, s := range m.Sources {
		if s.Zone == m.Zone && s.Workdir != "" {
			bases = append(bases, filepath.Dir(filepath.Clean(s.Workdir)))
		}
	}
	return m.Zone, bases, true
}

func (a *App) findManifest() (string, error) {
	cwd, _ := os.Getwd()
	for p := cwd; ; p = filepath.Dir(p) {
		if f := manifestIn(p); f != "" {
			return f, nil
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	home, _ := os.UserHomeDir()
	// The overlay of this host's own zone: a work host never reads the
	// personal overlay, and a zone without [[mcp]] is a no-op.
	if zone, bases, ok := zoneOverlays(home); ok {
		for _, base := range bases {
			if f := manifestIn(base); f != "" {
				return f, nil
			}
		}
		return "", errNoZoneMCP{zone}
	}
	// No piste.agents manifest: the overlay piste resolves for this machine
	// (in the piste checkout: the one whose hosts include it).
	if p, err := a.machine(); err == nil && p.FlakeErr == nil && p.FlakeDir != "" {
		if f := manifestIn(p.FlakeDir); f != "" {
			return f, nil
		}
		return "", a.clickErr(fmt.Sprintf("no [[mcp]] in agents/agents.toml found — run from inside your overlay, or check %s", p.FlakeDir))
	}
	return "", a.clickErr("no [[mcp]] in agents/agents.toml found — run from inside your overlay (no piste checkout found)")
}

// manifestOrNoop is findManifest for check / sync: a zone with no MCP
// servers prints that and ends the command with exit 0 (done = true).
func (a *App) manifestOrNoop() (path string, done bool, err error) {
	path, err = a.findManifest()
	var nz errNoZoneMCP
	if errors.As(err, &nz) {
		a.info(nz.Error())
		return "", true, nil
	}
	return path, false, err
}

// BuildAdd is build_add_command.
func buildAdd(harness string, s mcp.Server) ([]string, error) {
	switch harness {
	case "claude-code":
		cmd := []string{"claude", "mcp", "add", "--scope", "user"}
		if s.Transport == "http" {
			if s.URL == "" {
				return nil, fmt.Errorf("server '%s': http server has no url", s.Name)
			}
			cmd = append(cmd, "--transport", "http", s.Name, s.URL)
			if s.BearerEnv != "" {
				cmd = append(cmd, "--header", "Authorization: Bearer ${"+s.BearerEnv+"}")
			}
			return cmd, nil
		}
		if s.Command == "" {
			return nil, fmt.Errorf("server '%s': stdio server has no command", s.Name)
		}
		cmd = append(cmd, s.Name)
		for _, kv := range s.EnvFor(harness) {
			cmd = append(cmd, "-e", kv.K+"="+kv.V)
		}
		return append(append(cmd, "--", s.Command), s.Args...), nil
	case "codex":
		cmd := []string{"codex", "mcp", "add", s.Name}
		if s.Transport == "http" {
			if s.URL == "" {
				return nil, fmt.Errorf("server '%s': http server has no url", s.Name)
			}
			cmd = append(cmd, "--url", s.URL)
			if s.BearerEnv != "" {
				cmd = append(cmd, "--bearer-token-env-var", s.BearerEnv)
			}
			return cmd, nil
		}
		if s.Command == "" {
			return nil, fmt.Errorf("server '%s': stdio server has no command", s.Name)
		}
		for _, kv := range s.EnvFor(harness) {
			cmd = append(cmd, "--env", kv.K+"="+kv.V)
		}
		return append(append(cmd, "--", s.Command), s.Args...), nil
	}
	return nil, fmt.Errorf("no add-command builder for harness %s", harness)
}

func buildRemove(harness, name string) []string {
	if harness == "claude-code" {
		return []string{"claude", "mcp", "remove", "--scope", "user", name}
	}
	return []string{"codex", "mcp", "remove", name}
}

func homeDir() string { h, _ := os.UserHomeDir(); return h }

func codexConfigTables() map[string]any {
	b, err := os.ReadFile(filepath.Join(homeDir(), ".codex", "config.toml"))
	if err != nil {
		return nil
	}
	var m map[string]any
	if toml.Unmarshal(b, &m) != nil {
		return nil
	}
	t, _ := m["mcp_servers"].(map[string]any)
	return t
}

func codexNamesFromConfig() []string {
	t := codexConfigTables()
	names := make([]string, 0, len(t))
	for n := range t {
		names = append(names, n)
	}
	sort.Strings(names) // tomllib order is file order; config keys are rarely reordered
	return names
}

var claudeListLine = regexp.MustCompile(`^(\S.*?): (.*)$`)

// parseClaudeMCPList is _parse_claude_mcp_list.
func parseClaudeMCPList(text string) []string {
	var names []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		i := strings.Index(line, ": ")
		if i < 0 {
			continue
		}
		name, rest := line[:i], line[i+2:]
		if !strings.Contains(rest, " - ") {
			continue
		}
		if n := strings.TrimSpace(name); n != "" {
			names = append(names, n)
		}
	}
	return names
}

func runCapture(argv []string, timeout time.Duration) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb strings.Builder
	c.Stdout, c.Stderr = &out, &errb
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), errb.String(), ee.ExitCode(), nil
	}
	return out.String(), errb.String(), 0, err
}

func (a *App) observed(harness string) ([]string, error) {
	if harness == "claude-code" {
		out, errs, code, err := runCapture([]string{"claude", "mcp", "list"}, 180*time.Second)
		if err != nil {
			return nil, err
		}
		if code != 0 {
			return nil, a.clickErr(fmt.Sprintf("claude mcp list failed (exit %d): %s", code, strings.TrimSpace(errs)))
		}
		return parseClaudeMCPList(out), nil
	}
	out, _, code, err := runCapture([]string{"codex", "mcp", "list", "--json"}, 60*time.Second)
	if err != nil || code != 0 {
		return codexNamesFromConfig(), nil
	}
	var data any
	dec := json.NewDecoder(strings.NewReader(out))
	if dec.Decode(&data) != nil {
		return codexNamesFromConfig(), nil
	}
	bad := func() error {
		b, _ := json.Marshal(data)
		return a.clickErr(fmt.Sprintf("codex mcp list --json returned unexpected JSON (not a server listing): %s", string(b)))
	}
	switch d := data.(type) {
	case map[string]any:
		var names []string
		for k, v := range d {
			if _, ok := v.(map[string]any); !ok {
				return nil, bad()
			}
			names = append(names, k)
		}
		sort.Strings(names)
		return names, nil
	case []any:
		var names []string
		for _, e := range d {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, bad()
			}
			n, _ := m["name"].(string)
			names = append(names, n)
		}
		return names, nil
	}
	return nil, bad()
}

func hostname() string { return platform.ShortHostname() }

// claudeGlobalConfig is _claude_global_config.
func claudeGlobalConfig() string {
	cd := os.Getenv("CLAUDE_CONFIG_DIR")
	home := homeDir()
	base := filepath.Join(home, ".claude")
	if cd != "" {
		base = cd
	}
	legacy := filepath.Join(base, ".config.json")
	if fi, err := os.Stat(legacy); err == nil && fi.Mode().IsRegular() {
		return legacy
	}
	suffix := ""
	if os.Getenv("CLAUDE_CODE_CUSTOM_OAUTH_URL") != "" {
		suffix = "-custom-oauth"
	}
	d := home
	if cd != "" {
		d = cd
	}
	return filepath.Join(d, ".claude"+suffix+".json")
}

func (a *App) reportStaleClaudeConfigs() {
	active := claudeGlobalConfig()
	base := os.Getenv("CLAUDE_CONFIG_DIR")
	if base == "" {
		base = homeDir()
	}
	for _, suffix := range []string{"", "-staging-oauth", "-local-oauth", "-custom-oauth"} {
		p := filepath.Join(base, ".claude"+suffix+".json")
		fi, err := os.Stat(p)
		if p == active || err != nil || !fi.Mode().IsRegular() {
			continue
		}
		var names []string
		b, err := os.ReadFile(p)
		var d struct {
			MCPServers map[string]any `json:"mcpServers"`
		}
		if err != nil || json.Unmarshal(b, &d) != nil {
			names = []string{"<unreadable>"}
		} else {
			for n := range d.MCPServers {
				names = append(names, n)
			}
			sort.Strings(names)
		}
		if suffix == "" || len(names) > 0 {
			list := strings.Join(names, ", ")
			if list == "" {
				list = "none"
			}
			a.warn(fmt.Sprintf("claude-code: stale config %s (Claude reads %s); its servers are not live and are left untouched: %s", p, active, list))
		}
	}
}

func (a *App) claudeOwned() (map[string]mcp.Def, error) {
	p := claudeGlobalConfig()
	var data struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	for attempt := 1; attempt <= 2; attempt++ {
		b, err := os.ReadFile(p)
		if err != nil {
			return map[string]mcp.Def{}, nil
		}
		if err := json.Unmarshal(b, &data); err == nil {
			break
		}
		if attempt == 2 {
			return nil, a.clickErr(p + " is not valid JSON")
		}
	}
	out := map[string]mcp.Def{}
	for n, e := range data.MCPServers {
		out[n] = mcp.ClaudeDef(e)
	}
	return out, nil
}

var codexBuiltin = map[string]bool{"codex_app": true, "codex_apps": true, "computer-use": true, "cua_repl": true, "mcp-search": true, "node_repl": true}

func (a *App) codexOwned() map[string]mcp.Def {
	out := map[string]mcp.Def{}
	stdout, _, code, err := runCapture([]string{"codex", "mcp", "list", "--json"}, 60*time.Second)
	if err == nil && code == 0 {
		var data []map[string]any
		if json.Unmarshal([]byte(stdout), &data) == nil {
			for _, e := range data {
				if n, _ := e["name"].(string); n != "" && !codexBuiltin[n] {
					out[n] = mcp.CodexDef(e)
				}
			}
			return out
		}
	}
	for n, t := range codexConfigTables() {
		if m, ok := t.(map[string]any); ok && !codexBuiltin[n] {
			out[n] = mcp.CodexDef(m)
		}
	}
	return out
}

func (a *App) loadMCP(path string) ([]mcp.Server, map[string]bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	servers, err := mcp.LoadManifest(string(b))
	var me mcp.ManifestError
	if errors.As(err, &me) {
		return nil, nil, a.clickErr(path + ": " + me.Msg)
	}
	if err != nil {
		return nil, nil, err
	}
	retired, err := mcp.LoadRetired(string(b))
	if errors.As(err, &me) {
		return nil, nil, a.clickErr(path + ": " + me.Msg)
	}
	return servers, retired, err
}

type harnessPlan struct {
	harness string
	plan    mcp.Plan
	byName  map[string]mcp.Server
}

func (a *App) plans(servers []mcp.Server, retired map[string]bool, host string) ([]harnessPlan, error) {
	home := homeDir()
	declared := map[string]bool{}
	for _, s := range servers {
		declared[s.Name] = true
	}
	var out []harnessPlan
	for _, h := range syncable {
		var desired []mcp.Server
		for _, s := range mcp.ServersFor(servers, h, host) {
			desired = append(desired, s.Expanded(home))
		}
		var owned map[string]mcp.Def
		if h == "claude-code" {
			var err error
			if owned, err = a.claudeOwned(); err != nil {
				return nil, err
			}
		} else {
			owned = a.codexOwned()
		}
		listed, err := a.observed(h)
		if err != nil {
			return nil, err
		}
		by := map[string]mcp.Server{}
		for _, s := range desired {
			by[s.Name] = s
		}
		out = append(out, harnessPlan{h, mcp.PlanHarness(h, desired, owned, listed, declared, retired), by})
	}
	return out, nil
}

func (a *App) reportPlan(h string, p mcp.Plan) {
	if p.Clean() {
		a.success(fmt.Sprintf("%s: in sync (%d managed servers)", h, len(p.Matching)))
	}
	if len(p.Add) > 0 {
		a.errorf(fmt.Sprintf("%s: missing %s", h, strings.Join(p.Add, ", ")))
	}
	for _, r := range p.Redefine {
		a.errorf(fmt.Sprintf("%s: %s drifted (%s differ)", h, r.Name, strings.Join(r.Fields, ", ")))
	}
	for _, r := range p.Prune {
		a.warn(fmt.Sprintf("%s: %s would be pruned (%s)", h, r.Name, r.Why))
	}
	if len(p.Unknown) > 0 {
		a.warn(fmt.Sprintf("%s: undeclared, never removed: %s — declare it, or add it to `retired` to have --prune remove it", h, strings.Join(p.Unknown, ", ")))
	}
	if len(p.Foreign) > 0 {
		a.info(fmt.Sprintf("%s: not ours (plugin/connector/other scope): %s", h, strings.Join(p.Foreign, ", ")))
	}
}

// shlexJoin quotes argv for a POSIX shell: a word of only safe characters
// as is, any other in single quotes.
func shlexJoin(argv []string) string {
	safe := regexp.MustCompile(`^[\w@%+=:,./-]+$`)
	out := make([]string, len(argv))
	for i, a := range argv {
		switch {
		case a == "":
			out[i] = "''"
		case safe.MatchString(a):
			out[i] = a
		default:
			out[i] = "'" + strings.ReplaceAll(a, "'", `'"'"'`) + "'"
		}
	}
	return strings.Join(out, " ")
}

type mcpStep struct {
	verb, done string
	cmd        []string
}

func (a *App) runStep(h string, st mcpStep, readd *mcpStep) bool {
	out, errs, code, err := runCapture(st.cmd, 180*time.Second)
	if err == nil && code == 0 {
		a.success(fmt.Sprintf("%s: %s", h, st.done))
		return true
	}
	if readd != nil && strings.Contains(errs+out, "already exists") {
		where := "codex mcp list"
		if h == "claude-code" {
			where = claudeGlobalConfig()
		}
		v := st.verb
		if i := strings.Index(v, " "); i >= 0 {
			v = v[i+1:]
		}
		a.warn(fmt.Sprintf("%s: %s already exists but piste did not see it in %s; treating it as drift and re-adding", h, v, where))
		return a.runStep(h, *readd, nil) && a.runStep(h, st, nil)
	}
	detail := errs
	if detail == "" {
		detail = out
	}
	if err != nil {
		detail = err.Error()
	}
	a.errorf(fmt.Sprintf("%s: failed to %s — %s", h, st.verb, strings.TrimSpace(detail)))
	return false
}

func newMCP(a *App) *cobra.Command {
	root := &cobra.Command{Use: "mcp", Short: "Reconcile MCP servers against the declarative manifest"}
	root.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Report unpinned manifest entries, and drift between the manifest and each harness",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			path, done, err := a.manifestOrNoop()
			if err != nil || done {
				return err
			}
			servers, retired, err := a.loadMCP(path)
			if err != nil {
				return err
			}
			host := hostname()
			a.info("manifest: " + path)
			a.info("host: " + host)
			a.reportStaleClaudeConfigs()
			dirty := false
			for _, s := range servers {
				if r := mcp.UnpinnedReason(s); r != "" {
					a.errorf(fmt.Sprintf("%s: unpinned — %s", s.Name, r))
					dirty = true
				}
			}
			ps, err := a.plans(servers, retired, host)
			if err != nil {
				return err
			}
			for _, hp := range ps {
				a.reportPlan(hp.harness, hp.plan)
				dirty = dirty || !hp.plan.Clean()
			}
			for _, h := range []string{"claude-desktop", "chatgpt-desktop"} {
				if d := mcp.ServersFor(servers, h, host); len(d) > 0 {
					a.warn(fmt.Sprintf("%s: %d server(s) declared but this harness has no CLI — configure by hand", h, len(d)))
				}
			}
			if dirty {
				return ExitError{1}
			}
			return nil
		},
	})
	var dryRun, prune, apply bool
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Add missing servers and re-add drifted ones; with --prune, remove ours that are no longer wanted",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if apply && !prune {
				fmt.Fprint(a.Stderr, "Usage: piste mcp sync [OPTIONS]\nTry 'piste mcp sync --help' for help.\n\nError: --apply only applies to --prune\n")
				return ExitError{2}
			}
			dry := dryRun || (prune && !apply)
			path, done, err := a.manifestOrNoop()
			if err != nil || done {
				return err
			}
			servers, retired, err := a.loadMCP(path)
			if err != nil {
				return err
			}
			ps, err := a.plans(servers, retired, hostname())
			if err != nil {
				return err
			}
			a.reportStaleClaudeConfigs()
			if !dry {
				need := map[string]bool{}
				for _, hp := range ps {
					for _, s := range hp.byName {
						if s.BearerEnv != "" {
							need[s.BearerEnv] = true
						}
					}
				}
				var missing []string
				for v := range need {
					if os.Getenv(v) == "" {
						missing = append(missing, v)
					}
				}
				sort.Strings(missing)
				if len(missing) > 0 {
					return a.clickErr(fmt.Sprintf("environment variable(s) not set: %s. These come from sops via the piste secrets module — run `piste switch`, then start a fresh login shell.", strings.Join(missing, ", ")))
				}
			}
			if dry {
				msg := "dry run: nothing is changed"
				if prune {
					msg += " (add --apply to prune)"
				}
				a.info(msg)
			}
			failed := false
			dim := ui.For(a.Stdout).Dim
			for _, hp := range ps {
				h, p := hp.harness, hp.plan
				type entry struct {
					st    mcpStep
					readd *mcpStep
				}
				var groups [][]entry
				for _, n := range p.Add {
					add, err := buildAdd(h, hp.byName[n])
					if err != nil {
						return err
					}
					rm := mcpStep{"remove " + n, "removed stale " + n, buildRemove(h, n)}
					groups = append(groups, []entry{{mcpStep{"add " + n, "added " + n, add}, &rm}})
				}
				for _, r := range p.Redefine {
					a.info(fmt.Sprintf("%s: %s drifted (%s differ); re-adding", h, r.Name, strings.Join(r.Fields, ", ")))
					add, err := buildAdd(h, hp.byName[r.Name])
					if err != nil {
						return err
					}
					groups = append(groups, []entry{
						{mcpStep{"remove drifted " + r.Name, "removed drifted " + r.Name, buildRemove(h, r.Name)}, nil},
						{mcpStep{"re-add " + r.Name, "re-added " + r.Name, add}, nil},
					})
				}
				for _, r := range p.Prune {
					if prune {
						a.info(fmt.Sprintf("%s: pruning %s (%s)", h, r.Name, r.Why))
						groups = append(groups, []entry{{mcpStep{"prune " + r.Name, "pruned " + r.Name, buildRemove(h, r.Name)}, nil}})
					} else {
						a.warn(fmt.Sprintf("%s: %s is %s; `piste mcp sync --prune` removes it", h, r.Name, r.Why))
					}
				}
				if len(groups) == 0 {
					a.success(fmt.Sprintf("%s: nothing to change (%d managed servers)", h, len(p.Matching)))
				}
				for _, g := range groups {
					for _, e := range g {
						if dry {
							fmt.Fprintf(a.Stdout, "  %s %s\n", dim.Render("$"), shlexJoin(e.st.cmd))
						} else if !a.runStep(h, e.st, e.readd) {
							failed = true
							break
						}
					}
				}
				if len(p.Unknown) > 0 {
					a.warn(fmt.Sprintf("%s: undeclared, left alone: %s. Declare it, add it to `retired`, or remove it by hand.", h, strings.Join(p.Unknown, ", ")))
				}
				if len(p.Foreign) > 0 {
					a.info(fmt.Sprintf("%s: not ours, never touched: %s", h, strings.Join(p.Foreign, ", ")))
				}
			}
			if failed {
				return ExitError{1}
			}
			return nil
		},
	}
	f := syncCmd.Flags()
	f.BoolVar(&dryRun, "dry-run", false, "Print commands without running them.")
	f.BoolVar(&prune, "prune", false, "Also remove retired servers and servers declared for another host or harness. A dry run unless --apply is given.")
	f.BoolVar(&apply, "apply", false, "With --prune: actually make the changes.")
	root.AddCommand(syncCmd)
	return root
}

var _ = claudeListLine
