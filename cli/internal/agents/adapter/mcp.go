// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/features/mcp/{claudecode,codexcli,hermesagent}-mcp.ts, rulesync-mcp.ts.
// Copyright (c) 2024 dyoshikawa. MIT License; see cli/THIRD_PARTY_NOTICES.md.

package adapter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/pelletier/go-toml/v2"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/lock"
	"github.com/elocke/piste/cli/internal/agents/safety"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// wholeEnvRef matches a value that is exactly ${VAR}.
var wholeEnvRef = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// checkMCP refuses what can't be rendered safely: literal secrets, bad
// variable names, both or neither of command and url.
func checkMCP(m canon.MCP) error {
	if (m.Command == "") == (m.URL == "") {
		return fmt.Errorf("mcp %s: exactly one of command or url", m.Name)
	}
	for _, v := range append(append([]string{}, m.SecretEnv...), m.BearerEnv) {
		if v != "" && !envName.MatchString(v) {
			return fmt.Errorf("mcp %s: %q is not an environment variable name", m.Name, v)
		}
	}
	for k, v := range m.Env {
		if safety.LooksSecret(k, v) {
			return fmt.Errorf("mcp %s: env %s holds a literal secret; declare it in secretEnv instead", m.Name, k)
		}
	}
	for k, v := range m.Headers {
		if safety.LooksSecret(k, v) {
			return fmt.Errorf("mcp %s: header %s holds a literal secret; use bearerEnv or ${VAR}", m.Name, k)
		}
	}
	if safety.LooksSecret("url", m.URL) {
		return fmt.Errorf("mcp %s: url embeds a secret", m.Name)
	}
	return nil
}

// claudeServer is the .mcp.json / carrier shape. Secrets are ${VAR}
// references, which Claude Code expands from its environment.
func claudeServer(m canon.MCP) map[string]any {
	s := map[string]any{}
	if m.Stdio() {
		s["command"] = m.Command
		if len(m.Args) > 0 {
			s["args"] = m.Args
		}
		env := map[string]string{}
		for k, v := range m.Env {
			env[k] = v
		}
		for _, v := range m.SecretEnv {
			env[v] = "${" + v + "}"
		}
		if len(env) > 0 {
			s["env"] = env
		}
		return s
	}
	s["type"] = "http"
	s["url"] = m.URL
	h := map[string]string{}
	for k, v := range m.Headers {
		h[k] = v
	}
	if m.BearerEnv != "" {
		h["Authorization"] = "Bearer ${" + m.BearerEnv + "}"
	}
	if len(h) > 0 {
		s["headers"] = h
	}
	return s
}

// codexServer is the [mcp_servers.<n>] table. Codex never expands ${VAR}:
// stdio secrets go in env_vars (passed through from the environment), a
// bearer in bearer_token_env_var, whole-value ${VAR} headers in
// env_http_headers (convertEnvVarRefsForCodex). rulesync wrote Codex `env`
// literally; that is the bug the canonical secretEnv fixes.
func codexServer(m canon.MCP) (map[string]any, error) {
	s := map[string]any{}
	if m.Stdio() {
		s["command"] = m.Command
		if len(m.Args) > 0 {
			s["args"] = m.Args
		}
		env := map[string]string{}
		for k, v := range m.Env {
			if strings.Contains(v, "${") {
				return nil, fmt.Errorf("mcp %s: env %s uses ${…}, which Codex does not expand; use secretEnv", m.Name, k)
			}
			env[k] = v
		}
		if len(env) > 0 {
			s["env"] = env
		}
		if len(m.SecretEnv) > 0 {
			vars := append([]string(nil), m.SecretEnv...)
			sort.Strings(vars)
			s["env_vars"] = vars
		}
	} else {
		s["url"] = m.URL
		static, envHeaders := map[string]string{}, map[string]string{}
		for k, v := range m.Headers {
			if mm := wholeEnvRef.FindStringSubmatch(v); mm != nil {
				envHeaders[k] = mm[1]
				continue
			}
			if strings.Contains(v, "${") {
				return nil, fmt.Errorf("mcp %s: header %s: Codex only sources a whole-value ${VAR}", m.Name, k)
			}
			static[k] = v
		}
		if len(static) > 0 {
			s["http_headers"] = static
		}
		if len(envHeaders) > 0 {
			s["env_http_headers"] = envHeaders
		}
		if m.BearerEnv != "" {
			s["bearer_token_env_var"] = m.BearerEnv
		}
	}
	if m.StartupTimeout > 0 {
		s["startup_timeout_sec"] = m.StartupTimeout
	}
	return s, nil
}

// hermesServer is a Hermes mcp_servers entry (bundle deltas only; the engine
// never edits live Hermes config). Secrets come from the profile env.
func hermesServer(m canon.MCP) map[string]any {
	s := map[string]any{}
	if m.Stdio() {
		s["command"] = m.Command
		if len(m.Args) > 0 {
			s["args"] = m.Args
		}
		env := map[string]string{}
		for k, v := range m.Env {
			env[k] = v
		}
		for _, v := range m.SecretEnv {
			env[v] = "${" + v + "}"
		}
		if len(env) > 0 {
			s["env"] = env
		}
		return s
	}
	s["url"] = m.URL
	h := map[string]string{}
	for k, v := range m.Headers {
		h[k] = v
	}
	if m.BearerEnv != "" {
		h["Authorization"] = "Bearer ${" + m.BearerEnv + "}"
	}
	if len(h) > 0 {
		s["headers"] = h
	}
	return s
}

// MCPServers renders MCP servers for one harness and scope:
//   - claude, project: one owned key per server in .mcp.json
//   - claude-plugin: the carrier's .mcp.json, whole
//   - codex: one managed block "mcp" of [mcp_servers.*] tables in
//     .codex/config.toml (whole owned tables)
//   - hermes, bundle: mcp_servers.yaml for the profile delta
//
// Claude user scope is not a file render: it goes through the carrier or
// `claude mcp add` (never a mcpServers replace in ~/.claude.json).
func MCPServers(harness string, scope canon.Scope, servers []canon.MCP) (Result, error) {
	var r Result
	var mine []canon.MCP
	for _, m := range servers {
		if len(m.Targets) > 0 && !contains(m.Targets, targetFamily(harness)) {
			continue
		}
		if err := checkMCP(m); err != nil {
			return r, err
		}
		mine = append(mine, m)
	}
	if len(mine) == 0 {
		return r, nil
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].Name < mine[j].Name })
	switch harness {
	case canon.Claude:
		if scope == canon.User {
			return r, fmt.Errorf("claude user-scope MCP is delivered by the piste-harness carrier or `claude mcp add`, not a file edit")
		}
		for _, m := range mine {
			b, err := json.Marshal(claudeServer(m))
			if err != nil {
				return r, err
			}
			r.add(Artifact{Harness: harness, Kind: lock.KindMCP, Item: m.Name, File: ".mcp.json",
				JSONPointer: edit.Pointer("mcpServers", m.Name), JSONValue: b})
		}
		return r, nil
	case canon.ClaudePlugin:
		all := map[string]any{}
		for _, m := range mine {
			all[m.Name] = claudeServer(m)
		}
		b, err := json.MarshalIndent(map[string]any{"mcpServers": all}, "", "  ")
		if err != nil {
			return r, err
		}
		r.add(Artifact{Harness: harness, Kind: lock.KindMCP, Item: "mcp", File: ".mcp.json", Whole: append(b, '\n')})
		return r, nil
	case canon.Codex:
		var body strings.Builder
		for i, m := range mine {
			s, err := codexServer(m)
			if err != nil {
				return r, err
			}
			if i > 0 {
				body.WriteString("\n")
			}
			tb, err := tomlTable([]string{"mcp_servers", m.Name}, s)
			if err != nil {
				return r, err
			}
			body.WriteString(tb)
		}
		r.add(Artifact{Harness: harness, Kind: lock.KindMCP, Item: "mcp", File: ".codex/config.toml",
			TOMLBlock: "mcp", TOMLBody: body.String()})
		return r, nil
	case canon.Hermes:
		if scope != canon.Bundle {
			return r, fmt.Errorf("hermes MCP is rendered only into the bundle (profile deltas)")
		}
		all := yaml.MapSlice{}
		for _, m := range mine {
			all = append(all, yaml.MapItem{Key: m.Name, Value: hermesServer(m)})
		}
		b, err := yaml.Marshal(yaml.MapSlice{{Key: "mcp_servers", Value: all}})
		if err != nil {
			return r, err
		}
		r.add(Artifact{Harness: harness, Kind: lock.KindMCP, Item: "mcp", File: "mcp_servers.yaml", Whole: b})
		return r, nil
	}
	return r, fmt.Errorf("mcp: unknown harness %q", harness)
}

// tomlTable renders one table with only its own header ([a.b], never the
// parent [a], which a user's file may already define) and nested maps as
// inline tables, keys sorted.
func tomlTable(path []string, v map[string]any) (string, error) {
	var b strings.Builder
	keys := make([]string, 0, len(path))
	for _, p := range path {
		keys = append(keys, tomlKey(p))
	}
	b.WriteString("[" + strings.Join(keys, ".") + "]\n")
	names := make([]string, 0, len(v))
	for k := range v {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		val, err := tomlValue(v[k])
		if err != nil {
			return "", err
		}
		b.WriteString(tomlKey(k) + " = " + val + "\n")
	}
	return b.String(), nil
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	q, _ := tomlValue(k)
	return q
}

// tomlValue encodes a scalar, array or string map (as an inline table).
func tomlValue(v any) (string, error) {
	switch t := v.(type) {
	case map[string]string:
		ks := make([]string, 0, len(t))
		for k := range t {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		parts := make([]string, 0, len(ks))
		for _, k := range ks {
			val, err := tomlValue(t[k])
			if err != nil {
				return "", err
			}
			parts = append(parts, tomlKey(k)+" = "+val)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	}
	out, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.TrimPrefix(string(out), "v = ")), nil
}
