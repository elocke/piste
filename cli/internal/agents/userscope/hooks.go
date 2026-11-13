package userscope

// The user-scope hooks:
//
//	Claude  the local carrier plugin piste-harness@piste-local, installed and
//	        updated through the claude CLI (settings.json is never edited).
//	Codex   owned matcher groups in ~/.codex/hooks.json next to the foreign
//	        ones; the ledger records each owned group by content hash and
//	        only those are ever removed. Owned hooks are trusted
//	        through `codex app-server` (hooks/list, config/value/write), so
//	        config.toml keeps its comments and foreign hooks are never
//	        trusted on the user's behalf.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Harness CLIs; found on PATH (tests put fakes there).
var (
	ClaudeBin = "claude"
	CodexBin  = "codex"
)

// CLITimeout bounds each harness CLI call.
const CLITimeout = 120 * time.Second

// HookError: a harness CLI call failed; nothing further was changed for
// that harness.
type HookError struct{ Msg string }

func (e HookError) Error() string { return e.Msg }

// groupID identifies a matcher group: sha256(event NUL canonical JSON).
func groupID(event string, group any) string { return sha(event + "\x00" + canonical(group)) }

func childEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LD_LIBRARY_PATH=") {
			out = append(out, kv)
		}
	}
	return out
}

// cli runs argv and returns stdout, or a HookError.
func cli(argv []string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = childEnv()
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", HookError{fmt.Sprintf("`%s`: timed out after %d seconds", strings.Join(argv, " "), int(timeout.Seconds()))}
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg := errb.String()
		if strings.TrimSpace(msg) == "" {
			msg = out.String()
		}
		return "", HookError{fmt.Sprintf("`%s` exited %d: %s", strings.Join(argv, " "), ee.ExitCode(), truncate(strings.TrimSpace(msg), 300))}
	}
	if err != nil {
		return "", HookError{fmt.Sprintf("`%s`: %s", strings.Join(argv, " "), err)}
	}
	return out.String(), nil
}

func claudeJSON(args ...string) (any, error) {
	argv := append(append([]string{ClaudeBin}, args...), "--json")
	out, err := cli(argv, CLITimeout)
	if err != nil {
		return nil, err
	}
	v, err := ParseOrdered([]byte(out))
	if err != nil {
		return nil, HookError{fmt.Sprintf("`claude %s --json`: unreadable output (%s)", strings.Join(args, " "), err)}
	}
	return v, nil
}

// HooksSpec is the manifest's "hooks" section.
type HooksSpec struct {
	Claude, Codex, Replaces *Obj
}

func hooksSpec(m *Manifest) HooksSpec {
	h, _ := m.Raw.Get("hooks").(*Obj)
	get := func(k string) *Obj {
		o, _ := h.Get(k).(*Obj)
		if o == nil || len(o.Keys) == 0 {
			return nil
		}
		return o
	}
	return HooksSpec{Claude: get("claude"), Codex: get("codex"), Replaces: get("replaces")}
}

type declGroup struct {
	event string
	group any
}

func declaredGroups(spec *Obj) []declGroup {
	var out []declGroup
	hooks, _ := spec.Get("hooks").(*Obj)
	if hooks == nil {
		return nil
	}
	for _, e := range hooks.Keys {
		gs, _ := hooks.Vals[e].([]any)
		for _, g := range gs {
			out = append(out, declGroup{e, g})
		}
	}
	return out
}

func hookCommands(g any) []string {
	o, _ := g.(*Obj)
	hs, _ := o.Get("hooks").([]any)
	var out []string
	for _, h := range hs {
		ho, _ := h.(*Obj)
		out = append(out, str(ho.Get("command")))
	}
	return out
}

func declaredCommands(spec *Obj) map[string]bool {
	out := map[string]bool{}
	for _, dg := range declaredGroups(spec) {
		for _, c := range hookCommands(dg.group) {
			out[c] = true
		}
	}
	return out
}

// get returns o[k] as a string ("" when absent or null).
func getS(o *Obj, k string) string {
	if s, ok := o.Get(k).(string); ok {
		return s
	}
	return ""
}

func syncClaude(spec *Obj, ledger *Obj, dryRun bool, out io.Writer) error {
	owned, _ := ledger.Get("claude").(*Obj)
	var actions [][]string
	if spec == nil {
		if owned != nil && len(owned.Keys) > 0 {
			actions = append(actions, []string{"plugin", "uninstall", str(owned.Get("plugin"))},
				[]string{"plugin", "marketplace", "remove", str(owned.Get("marketplace"))})
		}
	} else {
		name, plugin := getS(spec, "marketplace_name"), getS(spec, "plugin")
		mv, err := claudeJSON("plugin", "marketplace", "list")
		if err != nil {
			return err
		}
		markets := byField(mv, "name")
		m, have := markets[name]
		// Registered from another directory (one that moved): `update` would
		// read the old one, which home-manager removed. Re-add it from ours;
		// claude's remove uninstalls the marketplace's plugins, so ours is
		// installed again below.
		moved := have && getS(m, "path") != "" && getS(m, "path") != getS(spec, "marketplace")
		// The carrier the ledger owns, if it isn't the declared one (a renamed
		// plugin or marketplace), goes, unless removing its marketplace already
		// takes it.
		if owned != nil {
			oldPlugin, oldMarket := getS(owned, "plugin"), getS(owned, "marketplace")
			if oldPlugin != "" && oldPlugin != plugin && !(moved && strings.HasSuffix(oldPlugin, "@"+name)) {
				actions = append(actions, []string{"plugin", "uninstall", oldPlugin})
			}
			if oldMarket != "" && oldMarket != name {
				actions = append(actions, []string{"plugin", "marketplace", "remove", oldMarket})
			}
		}
		switch {
		case !have:
			actions = append(actions, []string{"plugin", "marketplace", "add", getS(spec, "marketplace")})
		case moved:
			actions = append(actions, []string{"plugin", "marketplace", "remove", name},
				[]string{"plugin", "marketplace", "add", getS(spec, "marketplace")})
		default:
			actions = append(actions, []string{"plugin", "marketplace", "update", name})
		}
		pv, err := claudeJSON("plugin", "list")
		if err != nil {
			return err
		}
		plugins := byField(pv, "id")
		inst, ok := plugins[plugin]
		if moved {
			ok = false
		}
		if !ok {
			actions = append(actions, []string{"plugin", "install", getS(spec, "plugin"), "--scope", "user"})
		} else {
			if !jsonEq(inst.Get("version"), spec.Get("version")) {
				actions = append(actions, []string{"plugin", "update", getS(spec, "plugin")})
			}
			if en, has := inst.Vals["enabled"]; has && !truthy(en) {
				actions = append(actions, []string{"plugin", "enable", getS(spec, "plugin")})
			}
		}
	}
	for _, a := range actions {
		verb := "running"
		if dryRun {
			verb = "would run"
		}
		fmt.Fprintf(out, "piste-agents: %s: claude %s\n", verb, strings.Join(a, " "))
		if !dryRun {
			if _, err := cli(append([]string{ClaudeBin}, a...), CLITimeout); err != nil {
				return err
			}
		}
	}
	if !dryRun {
		if spec != nil {
			c := NewObj()
			c.Set("plugin", spec.Get("plugin"))
			c.Set("marketplace", spec.Get("marketplace_name"))
			ledger.Set("claude", c)
		} else {
			ledger.Del("claude")
		}
	}
	return nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		return string(x) != "0" && string(x) != "0.0"
	case []any:
		return len(x) > 0
	case *Obj:
		return len(x.Keys) > 0
	}
	return true
}

func jsonEq(a, b any) bool { return canonical(a) == canonical(b) }

// byField indexes a JSON list of objects by a field (on a repeated value,
// the last one wins).
func byField(v any, field string) map[string]*Obj {
	out := map[string]*Obj{}
	arr, _ := v.([]any)
	for _, e := range arr {
		o, _ := e.(*Obj)
		if o == nil {
			continue
		}
		out[str(o.Get(field))] = o
	}
	return out
}

func mergeCodexHooks(data *Obj, spec *Obj, ownedIDs map[string]bool) (bool, []string) {
	hooks, _ := data.Get("hooks").(*Obj)
	if hooks == nil {
		if !data.Has("hooks") {
			hooks = NewObj()
			data.Set("hooks", hooks)
		} else {
			hooks = NewObj() // a non-object: treated as empty, like .get fails
			data.Set("hooks", hooks)
		}
	}
	declared := declaredGroups(spec)
	declaredIDs := map[string]bool{}
	for _, dg := range declared {
		declaredIDs[groupID(dg.event, dg.group)] = true
	}
	changed := false
	for _, event := range append([]string(nil), hooks.Keys...) {
		groups, _ := hooks.Vals[event].([]any)
		var keep []any
		for _, g := range groups {
			id := groupID(event, g)
			if !(ownedIDs[id] && !declaredIDs[id]) {
				keep = append(keep, g)
			}
		}
		if len(keep) != len(groups) {
			changed = true
			if len(keep) > 0 {
				hooks.Vals[event] = keep
			} else {
				hooks.Del(event)
			}
		}
	}
	for _, dg := range declared {
		id := groupID(dg.event, dg.group)
		present := false
		gs, _ := hooks.Get(dg.event).([]any)
		for _, x := range gs {
			if groupID(dg.event, x) == id {
				present = true
			}
		}
		if !present {
			hooks.Set(dg.event, append(gs, dg.group))
			changed = true
		}
	}
	ids := make([]string, 0, len(declaredIDs))
	for id := range declaredIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return changed, ids
}

func syncCodex(spec *Obj, ledger *Obj, dryRun bool, out io.Writer, home string) error {
	target := getS(spec, "target")
	if target == "" {
		target = getS(ledger, "codex_target")
	}
	if target == "" {
		return nil
	}
	path := filepath.Join(home, target)
	if err := guardTarget(path, home); err != nil {
		return err
	}
	owned := map[string]bool{}
	if l, ok := ledger.Get("codex_groups").([]any); ok {
		for _, x := range l {
			owned[str(x)] = true
		}
	}
	if spec == nil && len(owned) == 0 {
		return nil
	}
	data, err := readCodexHooks(path)
	if err != nil {
		return err
	}
	changed, ids := mergeCodexHooks(data, spec, owned)
	if changed {
		verb := "updating"
		if dryRun {
			verb = "would update"
		}
		fmt.Fprintf(out, "piste-agents: %s owned hook entries in %s\n", verb, path)
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := writeAtomic(path, encode(data, "  ")+"\n"); err != nil {
				return err
			}
		}
	}
	if !dryRun {
		if spec != nil {
			l := make([]any, len(ids))
			for i, id := range ids {
				l[i] = id
			}
			ledger.Set("codex_groups", l)
			ledger.Set("codex_target", target)
		} else {
			ledger.Del("codex_groups")
			ledger.Del("codex_target")
		}
	}
	return nil
}

// CodexServer is a minimal JSON-RPC client for `codex app-server` (stdio,
// one JSON object per line).
type CodexServer struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	lines   chan string
	id      int
	timeout time.Duration
}

// StartCodexServer launches the app server in cwd and initialises it.
func StartCodexServer(cwd string) (*CodexServer, error) {
	cmd := exec.Command(CodexBin, "app-server")
	cmd.Dir = cwd
	cmd.Env = childEnv()
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, HookError{"cannot start `codex app-server`: " + err.Error()}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, HookError{"cannot start `codex app-server`: " + err.Error()}
	}
	if err := cmd.Start(); err != nil {
		return nil, HookError{"cannot start `codex app-server`: " + err.Error()}
	}
	s := &CodexServer{cmd: cmd, in: in, lines: make(chan string, 64), timeout: 60 * time.Second}
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 1<<20), 64<<20)
		for sc.Scan() {
			s.lines <- sc.Text()
		}
		close(s.lines)
	}()
	params := NewObj()
	ci := NewObj()
	ci.Set("name", "piste-agents")
	ci.Set("version", "1")
	params.Set("clientInfo", ci)
	if _, err := s.Request("initialize", params); err != nil {
		s.Close()
		return nil, err
	}
	n := NewObj()
	n.Set("method", "initialized")
	s.send(n)
	return s, nil
}

func (s *CodexServer) send(msg *Obj) {
	io.WriteString(s.in, encode(msg, "")+"\n")
}

// Request sends a request and waits for its response.
func (s *CodexServer) Request(method string, params any) (any, error) {
	s.id++
	m := NewObj()
	m.Set("id", s.id)
	m.Set("method", method)
	m.Set("params", params)
	s.send(m)
	deadline := time.After(s.timeout)
	for {
		select {
		case line, ok := <-s.lines:
			if !ok {
				return nil, HookError{"codex app-server exited during " + method}
			}
			v, err := ParseOrdered([]byte(line))
			if err != nil {
				return nil, err
			}
			msg, _ := v.(*Obj)
			if id, ok := msg.Get("id").(json.Number); ok && string(id) == fmt.Sprint(s.id) {
				if msg.Has("error") {
					return nil, HookError{fmt.Sprintf("codex app-server %s: %s", method, str(msg.Get("error")))}
				}
				return msg.Get("result"), nil
			}
		case <-deadline:
			return nil, HookError{fmt.Sprintf("codex app-server %s: timed out", method)}
		}
	}
}

// Close kills the server.
func (s *CodexServer) Close() {
	s.in.Close()
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	s.cmd.Wait()
}

func codexHooks(s *CodexServer, home string) ([]*Obj, error) {
	p := NewObj()
	p.Set("cwds", []any{home})
	res, err := s.Request("hooks/list", p)
	if err != nil {
		return nil, err
	}
	ro, _ := res.(*Obj)
	data, _ := ro.Get("data").([]any)
	var out []*Obj
	for _, e := range data {
		eo, _ := e.(*Obj)
		hs, _ := eo.Get("hooks").([]any)
		for _, h := range hs {
			if ho, ok := h.(*Obj); ok {
				out = append(out, ho)
			}
		}
	}
	return out, nil
}

func isOwnedCodexHook(h, spec *Obj, home string) bool {
	ours := str(h.Get("sourcePath")) == filepath.Join(home, getS(spec, "target")) && !truthy(h.Get("pluginId"))
	return ours && declaredCommands(spec)[str(h.Get("command"))]
}

func trustCodex(spec *Obj, dryRun bool, out io.Writer, home string) error {
	if spec == nil {
		return nil
	}
	s, err := StartCodexServer(home)
	if err != nil {
		return err
	}
	defer s.Close()
	hs, err := codexHooks(s, home)
	if err != nil {
		return err
	}
	for _, h := range hs {
		ts := str(h.Get("trustStatus"))
		if !isOwnedCodexHook(h, spec, home) || ts == "trusted" || ts == "managed" {
			continue
		}
		verb := "trusting"
		if dryRun {
			verb = "would trust"
		}
		fmt.Fprintf(out, "piste-agents: %s Codex hook %s\n", verb, str(h.Get("key")))
		if dryRun {
			continue
		}
		key := strings.ReplaceAll(strings.ReplaceAll(str(h.Get("key")), `\`, `\\`), `"`, `\"`)
		p := NewObj()
		p.Set("keyPath", fmt.Sprintf(`hooks.state."%s".trusted_hash`, key))
		p.Set("value", h.Get("currentHash"))
		p.Set("mergeStrategy", "replace")
		if _, err := s.Request("config/value/write", p); err != nil {
			return err
		}
	}
	return nil
}

// SyncHooks applies the runtime part of the hooks; it returns the number
// of failed steps (each reported on stderr).
func SyncHooks(m *Manifest, ledgerPath, home string, dryRun bool, out, errw io.Writer) (int, error) {
	spec := hooksSpec(m)
	ledger, err := loadLedger(ledgerPath, newLedger)
	if err != nil {
		return 0, err
	}
	// An older ledger's Codex keys are re-keyed first; if its hooks file
	// can't be read, the codex step fails and the ledger stays as it was.
	migrated := migrateLedger(ledger, home)
	failures := 0
	steps := []struct {
		name string
		fn   func() error
	}{
		{"claude", func() error { return syncClaude(spec.Claude, ledger, dryRun, out) }},
		{"codex", func() error {
			if migrated != nil {
				return HookError{migrated.Error()}
			}
			return syncCodex(spec.Codex, ledger, dryRun, out, home)
		}},
		{"codex trust", func() error { return trustCodex(spec.Codex, dryRun, out, home) }},
	}
	for _, st := range steps {
		if err := st.fn(); err != nil {
			var he HookError
			var rf refused
			var se *json.SyntaxError
			if errors.As(err, &he) || errors.As(err, &rf) || errors.As(err, &se) || isJSONErr(err) {
				failures++
				fmt.Fprintf(errw, "piste-agents: %s: %s\n", st.name, err)
				continue
			}
			return failures, err
		}
	}
	if !dryRun {
		if err := writeLedger(ledgerPath, ledger); err != nil {
			return failures, err
		}
	}
	return failures, nil
}

func isJSONErr(err error) bool {
	var ue *json.UnmarshalTypeError
	return errors.As(err, &ue) || strings.Contains(err.Error(), "invalid character") || strings.Contains(err.Error(), "unexpected EOF")
}

// short truncates to width code points, with an ellipsis.
func short(command string, width int) string {
	r := []rune(command)
	if len(r) <= width {
		return command
	}
	return string(r[:width-1]) + "…"
}

// readCodexHooks reads the Codex hooks file: missing or empty, it is
// {"hooks": {}}.
func readCodexHooks(path string) (*Obj, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(b) == 0) {
		o := NewObj()
		o.Set("hooks", NewObj())
		return o, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := ParseOrdered(b)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*Obj)
	if !ok {
		return nil, fmt.Errorf("%s: not a JSON object", path)
	}
	return o, nil
}

func loadJSONOr(path string, def *Obj) (*Obj, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return def, nil
	}
	if err != nil {
		return nil, err
	}
	v, err := ParseOrdered(b)
	if err != nil {
		return nil, err
	}
	o, _ := v.(*Obj)
	if o == nil {
		o = NewObj()
	}
	return o, nil
}

// CheckHooks is the check rows for owned, replaced and foreign hooks.
func CheckHooks(m *Manifest, home string) ([]Row, error) {
	spec := hooksSpec(m)
	if spec.Claude == nil && spec.Codex == nil {
		return nil, nil
	}
	var rows []Row
	var replaced []string
	if l, ok := spec.Replaces.Get("hook_commands").([]any); ok {
		for _, x := range l {
			replaced = append(replaced, str(x))
		}
	}
	containsAny := func(cmd string) bool {
		for _, r := range replaced {
			if strings.Contains(cmd, r) {
				return true
			}
		}
		return false
	}
	if c := spec.Claude; c != nil {
		plugin := getS(c, "plugin")
		pv, err := claudeJSON("plugin", "list")
		if err != nil {
			var he HookError
			if !errors.As(err, &he) {
				return nil, err
			}
			rows = append(rows, Row{"unknown", "hooks", "claude:" + plugin, he.Msg})
		} else {
			plugins := byField(pv, "id")
			p, ok := plugins[plugin]
			var state, detail string
			switch {
			case !ok:
				state, detail = "missing", "run `piste agents sync --user`"
			case p.Has("enabled") && !truthy(p.Get("enabled")):
				state, detail = "disabled", "run `piste agents sync --user`"
			case !jsonEq(p.Get("version"), c.Get("version")):
				state, detail = "stale", fmt.Sprintf("installed %s, declared %s; run `piste agents sync --user`", str(p.Get("version")), str(c.Get("version")))
			default:
				state, detail = "ok", str(p.Get("version"))
			}
			rows = append(rows, Row{state, "hooks", "claude:" + plugin, detail})
			if l, ok := spec.Replaces.Get("claude_plugins").([]any); ok {
				for _, x := range l {
					pid := str(x)
					if q, ok := plugins[pid]; ok && truthy(q.Get("enabled")) {
						rows = append(rows, Row{"conflict", "hooks", "claude:" + pid, fmt.Sprintf("replaced by %s; `claude plugin uninstall %s`", plugin, pid)})
					}
				}
			}
		}
		settings, err := loadJSONOr(filepath.Join(home, ".claude", "settings.json"), NewObj())
		if err != nil {
			return nil, err
		}
		if hooks, ok := settings.Get("hooks").(*Obj); ok {
			for _, event := range hooks.Keys {
				gs, _ := hooks.Vals[event].([]any)
				for _, g := range gs {
					for _, cmd := range hookCommandsLoose(g) {
						if containsAny(cmd) {
							rows = append(rows, Row{"conflict", "hooks", "claude:" + event, fmt.Sprintf("%s (replaced by %s; remove from ~/.claude/settings.json)", short(cmd, 90), plugin)})
						} else {
							rows = append(rows, Row{"foreign", "hooks", "claude:" + event, short(cmd, 90)})
						}
					}
				}
			}
		}
	}
	if c := spec.Codex; c != nil {
		path := filepath.Join(home, getS(c, "target"))
		data, err := readCodexHooks(path)
		if err != nil {
			return nil, err
		}
		present := map[string]bool{}
		if hooks, ok := data.Get("hooks").(*Obj); ok {
			for _, e := range hooks.Keys {
				gs, _ := hooks.Vals[e].([]any)
				for _, g := range gs {
					present[groupID(e, g)] = true
				}
			}
		}
		for _, dg := range declaredGroups(c) {
			var cmds []string
			for _, cmd := range hookCommands(dg.group) {
				cmds = append(cmds, short(cmd, 60))
			}
			state := "missing"
			if present[groupID(dg.event, dg.group)] {
				state = "ok"
			}
			go_, _ := dg.group.(*Obj)
			matcher := ""
			if go_.Has("matcher") {
				matcher = str(go_.Get("matcher"))
			}
			rows = append(rows, Row{state, "hooks", fmt.Sprintf("codex:%s[%s]", dg.event, matcher), strings.Join(cmds, ", ")})
		}
		trustRows, err := func() ([]Row, error) {
			s, err := StartCodexServer(home)
			if err != nil {
				return nil, err
			}
			listed, err := codexHooks(s, home)
			s.Close()
			if err != nil {
				return nil, err
			}
			var out []Row
			for _, h := range listed {
				ts := str(h.Get("trustStatus"))
				trusted := ts == "trusted" || ts == "managed"
				label := "codex:" + str(h.Get("eventName"))
				cmd := getS(h, "command")
				switch {
				case isOwnedCodexHook(h, c, home):
					st, d := "ok", short(cmd, 60)
					if !trusted {
						st, d = "untrusted", d+"; run `piste agents sync --user`"
					}
					out = append(out, Row{st, "hooks-trust", label, d})
				case containsAny(cmd):
					out = append(out, Row{"conflict", "hooks", label, short(cmd, 90)})
				default:
					d := short(cmd, 90)
					st := "foreign"
					if !trusted {
						st, d = "untrusted", d+fmt.Sprintf(" (%s: review in codex /hooks)", ts)
					}
					out = append(out, Row{st, "hooks", label, d})
				}
			}
			return out, nil
		}()
		if err != nil {
			var he HookError
			if !errors.As(err, &he) && !isJSONErr(err) {
				return nil, err
			}
			rows = append(rows, Row{"unknown", "hooks-trust", "codex", err.Error()})
		} else {
			rows = append(rows, trustRows...)
		}
	}
	return rows, nil
}

// hookCommandsLoose: h.get("command", "") for every hook of a group.
func hookCommandsLoose(g any) []string {
	o, _ := g.(*Obj)
	hs, _ := o.Get("hooks").([]any)
	var out []string
	for _, h := range hs {
		ho, _ := h.(*Obj)
		if ho.Has("command") {
			out = append(out, str(ho.Get("command")))
		} else {
			out = append(out, "")
		}
	}
	return out
}
