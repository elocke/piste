// Package mcp is the MCP manifest, definitions and reconcile plan: pure
// logic; the command layer does the I/O.
package mcp

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ValidHarnesses a server may name.
var ValidHarnesses = []string{"chatgpt-desktop", "claude-code", "claude-desktop", "codex"}

var (
	secretShaped = regexp.MustCompile(`(^sk-|^ghp_|^github_pat_|[A-Za-z0-9]{24,})`)
	pinnedGitRef = regexp.MustCompile(`^git\+\S+@[0-9a-fA-F]{40}$`)
)

// ManifestError: the manifest is malformed or unsafe.
type ManifestError struct{ Msg string }

func (e ManifestError) Error() string { return e.Msg }

func merr(format string, a ...any) error { return ManifestError{fmt.Sprintf(format, a...)} }

func checkNotSecret(name, field, value string) error {
	if pinnedGitRef.MatchString(value) {
		return nil
	}
	if secretShaped.MatchString(value) {
		return merr("server '%s': %s looks like a credential value; the manifest is committed to git — use bearerEnv (an env var NAME) for secrets, not a literal value", name, field)
	}
	return nil
}

// KV is one ordered env pair.
type KV struct{ K, V string }

// Server is one [[mcp]] entry.
type Server struct {
	Name, Transport string
	Harnesses       []string
	Hosts           []string // nil = all hosts
	URL, BearerEnv  string
	Command         string
	Args            []string
	Env             []KV
	HarnessEnv      []struct {
		Harness string
		Env     []KV
	}
}

// AppliesTo reports whether s is wanted for harness on host.
func (s Server) AppliesTo(harness, host string) bool {
	if !has(s.Harnesses, harness) {
		return false
	}
	return s.Hosts == nil || has(s.Hosts, host)
}

// EnvFor is env, then this harness's harnessEnv entries (which win), in
// dict order.
func (s Server) EnvFor(harness string) []KV {
	out := append([]KV(nil), s.Env...)
	set := func(k, v string) {
		for i := range out {
			if out[i].K == k {
				out[i].V = v
				return
			}
		}
		out = append(out, KV{k, v})
	}
	for _, he := range s.HarnessEnv {
		if he.Harness == harness {
			for _, kv := range he.Env {
				set(kv.K, kv.V)
			}
		}
	}
	return out
}

// Expanded replaces a leading ~/ in command and args with home.
func (s Server) Expanded(home string) Server {
	exp := func(v string) string {
		if strings.HasPrefix(v, "~/") {
			return strings.TrimRight(home, "/") + v[1:]
		}
		return v
	}
	c := s
	if c.Command != "" {
		c.Command = exp(c.Command)
	}
	c.Args = make([]string, len(s.Args))
	for i, a := range s.Args {
		c.Args[i] = exp(a)
	}
	return c
}

func has(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// tomlType names a TOML value's type, for messages.
func tomlType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case int64:
		return "integer"
	case float64:
		return "float"
	case bool:
		return "boolean"
	case []any, []string:
		return "array"
	case map[string]any:
		return "table"
	}
	return "datetime"
}

// tomlRepr is a TOML value written as TOML (an inline table's keys sorted),
// for messages.
func tomlRepr(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case bool:
		return strconv.FormatBool(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		s := strconv.FormatFloat(x, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eIN") {
			s += ".0"
		}
		return s
	case []any:
		p := make([]string, len(x))
		for i, e := range x {
			p[i] = tomlRepr(e)
		}
		return "[" + strings.Join(p, ", ") + "]"
	case []string:
		p := make([]string, len(x))
		for i, e := range x {
			p[i] = strconv.Quote(e)
		}
		return "[" + strings.Join(p, ", ") + "]"
	case map[string]any:
		ks := make([]string, 0, len(x))
		for k := range x {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		p := make([]string, len(ks))
		for i, k := range ks {
			key := k
			if !bareKey.MatchString(k) {
				key = strconv.Quote(k)
			}
			p[i] = key + " = " + tomlRepr(x[k])
		}
		return "{" + strings.Join(p, ", ") + "}"
	}
	return fmt.Sprint(v)
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tomlOrdered decodes TOML keeping table key order where it matters (env
// tables): go-toml maps are unordered, so key order is recovered from a
// second, ordered pass over the document.
func tomlOrdered(text string) (map[string]any, error) {
	var m map[string]any
	if err := toml.Unmarshal([]byte(text), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// keyOrder returns the keys of an inline or standard table in source
// order, found by scanning for `key =` within the server's section.
func keyOrder(m map[string]any, order []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range order {
		if _, ok := m[k]; ok && !seen[k] {
			out = append(out, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

var keyRe = regexp.MustCompile(`(?m)(?:^|[{,])\s*("?)([A-Za-z0-9_-]+)("?)\s*=`)

// sourceKeys lists every `key =` in the text, in order (a coarse but stable
// way to keep TOML table order for env / harnessEnv, like tomllib's dicts).
func sourceKeys(text string) []string {
	var out []string
	for _, m := range keyRe.FindAllStringSubmatch(text, -1) {
		out = append(out, m[2])
	}
	return out
}

// LoadManifest parses [[mcp]] (agents.toml).
func LoadManifest(text string) ([]Server, error) {
	data, err := tomlOrdered(text)
	if err != nil {
		return nil, err
	}
	order := sourceKeys(text)
	list, _ := data["mcp"].([]any)
	var servers []Server
	seen := map[string]bool{}
	for _, x := range list {
		r, _ := x.(map[string]any)
		name, _ := r["name"].(string)
		if name == "" {
			return nil, merr("every [[mcp]] entry needs a name")
		}
		if seen[name] {
			return nil, merr("duplicate server name: %s", name)
		}
		seen[name] = true
		transport, _ := r["transport"].(string)
		if rt, ok := r["transport"]; !ok {
			return nil, merr("server '%s' has no transport; expected \"http\" or \"stdio\"", name)
		} else if transport != "http" && transport != "stdio" {
			return nil, merr("server '%s' has transport %s; expected \"http\" or \"stdio\"", name, tomlRepr(rt))
		}
		rh, present := r["harnesses"]
		if !present {
			rh = []any{}
		}
		hl, ok := rh.([]any)
		if !ok {
			return nil, merr("server '%s': harnesses must be a list of strings, not %s (%s)", name, tomlType(rh), tomlRepr(rh))
		}
		if len(hl) == 0 {
			return nil, merr("server '%s' lists no harnesses", name)
		}
		var harnesses []string
		for _, h := range hl {
			hs, _ := h.(string)
			if !has(ValidHarnesses, hs) {
				return nil, merr("server '%s' names unknown harness %s; expected one of %s", name, tomlRepr(h), tomlRepr(ValidHarnesses))
			}
			harnesses = append(harnesses, hs)
		}
		bearer, _ := r["bearerEnv"].(string)
		if bearer != "" && secretShaped.MatchString(bearer) {
			return nil, merr("server '%s': bearerEnv must be an env var NAME, not a value", name)
		}
		url, _ := r["url"].(string)
		command, _ := r["command"].(string)
		if transport == "http" {
			if url == "" {
				return nil, merr("http server '%s' needs a url", name)
			}
			if err := checkNotSecret(name, "url", url); err != nil {
				return nil, err
			}
		} else if command == "" {
			return nil, merr("stdio server '%s' needs a command", name)
		}
		var hosts []string
		if hv, ok := r["hosts"]; ok {
			hl, ok := hv.([]any)
			if !ok {
				return nil, merr("server '%s': hosts must be a list of host names, not %s (%s)", name, tomlType(hv), tomlRepr(hv))
			}
			if len(hl) == 0 {
				return nil, merr("server '%s': hosts must be a non-empty list of host names, or omitted entirely to mean 'all hosts'", name)
			}
			hosts = []string{}
			for _, h := range hl {
				hosts = append(hosts, fmt.Sprint(h))
			}
		}
		rawArgs, present := r["args"]
		if !present {
			rawArgs = []any{}
		}
		al, ok := rawArgs.([]any)
		if !ok {
			return nil, merr("server '%s': args must be a list of strings, not %s (%s)", name, tomlType(rawArgs), tomlRepr(rawArgs))
		}
		var args []string
		for i, a := range al {
			as, ok := a.(string)
			if !ok {
				return nil, merr("server '%s': args[%d] must be a string, not %s (%s)", name, i, tomlType(a), tomlRepr(a))
			}
			if err := checkNotSecret(name, fmt.Sprintf("args[%d]", i), as); err != nil {
				return nil, err
			}
			args = append(args, as)
		}
		rawEnv, present := r["env"]
		if !present {
			rawEnv = map[string]any{}
		}
		em, ok := rawEnv.(map[string]any)
		if !ok {
			return nil, merr("server '%s': env must be a table of KEY = \"value\" pairs, not %s (%s)", name, tomlType(rawEnv), tomlRepr(rawEnv))
		}
		var env []KV
		for _, k := range keyOrder(em, order) {
			v, ok := em[k].(string)
			if !ok {
				return nil, merr("server '%s': env.%s must be a string, not %s (%s)", name, k, tomlType(em[k]), tomlRepr(em[k]))
			}
			if err := checkNotSecret(name, "env."+k, v); err != nil {
				return nil, err
			}
			env = append(env, KV{k, v})
		}
		s := Server{Name: name, Transport: transport, Harnesses: harnesses, Hosts: hosts, URL: url, BearerEnv: bearer,
			Command: command, Args: args, Env: env}
		rawHE, present := r["harnessEnv"]
		if !present {
			rawHE = map[string]any{}
		}
		hm, ok := rawHE.(map[string]any)
		if !ok {
			return nil, merr("server '%s': harnessEnv must be a table of per-harness env tables, not %s", name, tomlType(rawHE))
		}
		for _, h := range keyOrder(hm, order) {
			if !has(harnesses, h) {
				return nil, merr("server '%s': harnessEnv.%s names a harness the server is not declared for", name, h)
			}
			tm, ok := hm[h].(map[string]any)
			if !ok {
				return nil, merr("server '%s': harnessEnv.%s must be a table of KEY = \"value\" pairs", name, h)
			}
			var pairs []KV
			for _, k := range keyOrder(tm, order) {
				v, ok := tm[k].(string)
				if !ok {
					return nil, merr("server '%s': harnessEnv.%s.%s must be a string, not %s (%s)", name, h, k, tomlType(tm[k]), tomlRepr(tm[k]))
				}
				if err := checkNotSecret(name, "harnessEnv."+h+"."+k, v); err != nil {
					return nil, err
				}
				pairs = append(pairs, KV{k, v})
			}
			s.HarnessEnv = append(s.HarnessEnv, struct {
				Harness string
				Env     []KV
			}{h, pairs})
		}
		servers = append(servers, s)
	}
	return servers, nil
}

// LoadRetired is the manifest's retired list (top-level, or [retired] mcp).
func LoadRetired(text string) (map[string]bool, error) {
	data, err := tomlOrdered(text)
	if err != nil {
		return nil, err
	}
	raw, ok := data["retired"]
	if !ok {
		raw = []any{}
	}
	if m, ok := raw.(map[string]any); ok {
		raw, ok = m["mcp"]
		if !ok {
			raw = []any{}
		}
	}
	l, ok := raw.([]any)
	out := map[string]bool{}
	if !ok {
		return nil, merr("retired must be a list of server names")
	}
	for _, n := range l {
		s, ok := n.(string)
		if !ok || s == "" {
			return nil, merr("retired must be a list of server names")
		}
		out[s] = true
	}
	decl, _ := data["mcp"].([]any)
	var both []string
	for _, x := range decl {
		if m, ok := x.(map[string]any); ok {
			if n, _ := m["name"].(string); out[n] {
				both = append(both, n)
			}
		}
	}
	sort.Strings(both)
	if len(both) > 0 {
		return nil, merr("server(s) both declared and retired: %s", strings.Join(both, ", "))
	}
	return out, nil
}

var uvxValueOptions = map[string]bool{"--from": true, "--with": true, "--with-requirements": true, "--python": true,
	"-p": true, "--index": true, "--index-url": true, "--extra-index-url": true}

// UnpinnedReason: why a stdio server's package floats (nil when pinned).
func UnpinnedReason(s Server) string {
	if s.Transport != "stdio" || s.Command == "" {
		return ""
	}
	for _, a := range s.Args {
		if strings.HasSuffix(a, "@latest") {
			return strconv.Quote(a) + " floats on @latest"
		}
	}
	runner := s.Command
	if i := strings.LastIndex(runner, "/"); i >= 0 {
		runner = runner[i+1:]
	}
	switch runner {
	case "npx", "pnpx", "bunx":
		pkg := ""
		for _, a := range s.Args {
			if !strings.HasPrefix(a, "-") {
				pkg = a
				break
			}
		}
		if pkg == "" {
			return runner + " has no package argument"
		}
		i := strings.LastIndex(pkg, "@")
		name, version := "", pkg
		if i >= 0 {
			name, version = pkg[:i], pkg[i+1:]
		} else {
			version = ""
		}
		if name == "" || version == "" || version[0] < '0' || version[0] > '9' {
			return fmt.Sprintf("npm package %s has no exact version (use %s@<version>)", strconv.Quote(pkg), pkg)
		}
		return ""
	case "uvx":
		spec, found := "", false
		for i := 0; i < len(s.Args); i++ {
			a := s.Args[i]
			if a == "--from" {
				if i+1 < len(s.Args) {
					spec = s.Args[i+1]
				}
				found = true
				break
			}
			if uvxValueOptions[a] {
				i++
				continue
			}
			if strings.HasPrefix(a, "-") {
				continue
			}
			spec, found = a, true
			break
		}
		_ = found
		if spec == "" {
			return "uvx has no package argument"
		}
		if strings.Contains(spec, "==") || pinnedGitRef.MatchString(spec) {
			return ""
		}
		return fmt.Sprintf("python package %s has no exact version or pinned commit", strconv.Quote(spec))
	}
	return ""
}

// ServersFor filters for a harness on a host.
func ServersFor(servers []Server, harness, host string) []Server {
	var out []Server
	for _, s := range servers {
		if s.AppliesTo(harness, host) {
			out = append(out, s)
		}
	}
	return out
}

// Def is a normalised definition: ordered keys, JSON-ish values.
type Def struct {
	Keys []string
	Vals map[string]any
}

func def(kv ...any) Def {
	d := Def{Vals: map[string]any{}}
	for i := 0; i < len(kv); i += 2 {
		k := kv[i].(string)
		d.Keys = append(d.Keys, k)
		d.Vals[k] = kv[i+1]
	}
	return d
}

func envMap(env []KV) map[string]any {
	m := map[string]any{}
	for _, kv := range env {
		m[kv.K] = kv.V
	}
	return m
}

func strList(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

func orNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Desired is what the add command makes the harness store (expanded server).
func Desired(s Server, harness string) Def {
	switch harness {
	case "claude-code":
		if s.Transport == "http" {
			h := map[string]any{}
			if s.BearerEnv != "" {
				h["Authorization"] = "Bearer ${" + s.BearerEnv + "}"
			}
			return def("type", "http", "url", orNil(s.URL), "headers", h)
		}
		return def("type", "stdio", "command", orNil(s.Command), "args", strList(s.Args), "env", envMap(s.EnvFor(harness)))
	case "codex":
		if s.Transport == "http" {
			return def("type", "http", "url", orNil(s.URL), "bearer_token_env_var", orNil(s.BearerEnv), "http_headers", map[string]any{})
		}
		return def("type", "stdio", "command", orNil(s.Command), "args", strList(s.Args), "env", envMap(s.EnvFor(harness)))
	}
	panic("no definition for harness " + harness)
}

func mapOr(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && len(m) > 0 {
		return m
	}
	return map[string]any{}
}

func listOr(v any) []any {
	if l, ok := v.([]any); ok && len(l) > 0 {
		return l
	}
	return []any{}
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case bool:
		return x
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// ClaudeDef normalises an mcpServers entry of Claude's global config.
func ClaudeDef(e map[string]any) Def {
	kind := e["type"]
	if !truthy(kind) {
		if truthy(e["url"]) {
			kind = "http"
		} else {
			kind = "stdio"
		}
	}
	if kind == "http" || kind == "sse" {
		return def("type", kind, "url", e["url"], "headers", mapOr(e["headers"]))
	}
	return def("type", "stdio", "command", e["command"], "args", listOr(e["args"]), "env", mapOr(e["env"]))
}

// CodexDef normalises a `codex mcp list --json` entry or a config.toml table.
func CodexDef(e map[string]any) Def {
	t := e
	if tr, ok := e["transport"]; ok {
		t, _ = tr.(map[string]any)
		if t == nil {
			t = map[string]any{}
		}
	}
	kind := t["type"]
	if !truthy(kind) {
		if truthy(t["url"]) {
			kind = "http"
		} else {
			kind = "stdio"
		}
	}
	if kind == "streamable_http" || kind == "http" || truthy(t["url"]) {
		return def("type", "http", "url", t["url"], "bearer_token_env_var", t["bearer_token_env_var"], "http_headers", mapOr(t["http_headers"]))
	}
	return def("type", "stdio", "command", t["command"], "args", listOr(t["args"]), "env", mapOr(t["env"]))
}

func normalize(v any) any {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case []string:
		return strList(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = normalize(e)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = normalize(e)
		}
		return out
	}
	return v
}

// DriftedFields names the fields whose values differ (never the values).
func DriftedFields(desired, observed Def) []string {
	if !reflect.DeepEqual(normalize(desired.Vals["type"]), normalize(observed.Vals["type"])) {
		return []string{"type"}
	}
	var out []string
	for _, k := range desired.Keys {
		if !reflect.DeepEqual(normalize(desired.Vals[k]), normalize(observed.Vals[k])) {
			out = append(out, k)
		}
	}
	return out
}

// Plan is what sync would do for one harness.
type Plan struct {
	Add      []string
	Redefine []struct {
		Name   string
		Fields []string
	}
	Prune                      []struct{ Name, Why string }
	Unknown, Foreign, Matching []string
}

// Clean reports nothing to add, redefine or prune.
func (p Plan) Clean() bool { return len(p.Add) == 0 && len(p.Redefine) == 0 && len(p.Prune) == 0 }

// PlanHarness reconciles one harness (plan_harness).
func PlanHarness(harness string, desired []Server, owned map[string]Def, listed []string, declared, retired map[string]bool) Plan {
	var p Plan
	want := map[string]Server{}
	var names []string
	for _, s := range desired {
		if _, dup := want[s.Name]; !dup {
			names = append(names, s.Name)
		}
		want[s.Name] = s
	}
	sort.Strings(names)
	for _, n := range names {
		od, ok := owned[n]
		if !ok {
			p.Add = append(p.Add, n)
			continue
		}
		if f := DriftedFields(Desired(want[n], harness), od); len(f) > 0 {
			p.Redefine = append(p.Redefine, struct {
				Name   string
				Fields []string
			}{n, f})
		} else {
			p.Matching = append(p.Matching, n)
		}
	}
	var ownedNames []string
	for n := range owned {
		if _, ok := want[n]; !ok {
			ownedNames = append(ownedNames, n)
		}
	}
	sort.Strings(ownedNames)
	for _, n := range ownedNames {
		switch {
		case retired[n]:
			p.Prune = append(p.Prune, struct{ Name, Why string }{n, "retired in the manifest"})
		case declared[n]:
			p.Prune = append(p.Prune, struct{ Name, Why string }{n, "declared, but not for " + harness + " on this host"})
		default:
			p.Unknown = append(p.Unknown, n)
		}
	}
	seen := map[string]bool{}
	for _, n := range listed {
		if _, o := owned[n]; o {
			continue
		}
		if _, w := want[n]; w || seen[n] {
			continue
		}
		seen[n] = true
		p.Foreign = append(p.Foreign, n)
	}
	sort.Strings(p.Foreign)
	return p
}
