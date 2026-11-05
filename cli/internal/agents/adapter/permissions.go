// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/features/permissions/codexcli-permissions.ts
// (buildCodexBashRulesContent, toCommandPatternTokens, mapBashActionToDecision),
// src/features/permissions/claudecode-permissions.ts. Copyright (c) 2024 dyoshikawa.
// MIT License; see cli/THIRD_PARTY_NOTICES.md.

package adapter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/lock"
)

// CommandPattern is a normalised shell-command rule.
type CommandPattern struct {
	Tokens []string
	Prefix bool // matches any command starting with Tokens
}

var claudeBash = regexp.MustCompile(`^Bash\((.*)\)$`)

// ParseCommandPattern normalises canonical (`rm -rf *`) and Claude
// (`Bash(rm -rf:*)`, `Bash(git status)`) spellings.
//
// This is where rulesync's fail-open bug is fixed: it turned
// `rm -rf *` into prefix_rule(["rm","-rf","*"]), and a literal "*" token
// matches only a literal "*" argument, so `rm -rf /` was not blocked. Here a
// trailing `*` (or Claude's `:*`) means prefix semantics and is stripped;
// any other wildcard is a hard error rather than a rule that silently
// matches nothing.
func ParseCommandPattern(p string) (CommandPattern, error) {
	s := strings.TrimSpace(p)
	if m := claudeBash.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	prefix := false
	if strings.HasSuffix(s, ":*") {
		s, prefix = strings.TrimSuffix(s, ":*"), true
	}
	toks := strings.Fields(s)
	if n := len(toks); n > 0 && toks[n-1] == "*" {
		toks, prefix = toks[:n-1], true
	}
	if len(toks) == 0 {
		return CommandPattern{}, fmt.Errorf("command pattern %q matches everything; name the command", p)
	}
	for _, t := range toks {
		if strings.ContainsAny(t, "*?[") {
			return CommandPattern{}, fmt.Errorf("command pattern %q: wildcard %q is only allowed as the last token (prefix match); "+
				"Codex rules cannot express it, and emitting it would fail open", p, t)
		}
		if strings.ContainsAny(t, "\n\r\x00") {
			return CommandPattern{}, fmt.Errorf("command pattern %q: control characters", p)
		}
	}
	return CommandPattern{Tokens: toks, Prefix: prefix}, nil
}

func codexDecision(d canon.Decision) (string, error) {
	switch d {
	case canon.Allow:
		return "allow", nil
	case canon.Ask:
		return "prompt", nil
	case canon.Deny:
		return "forbidden", nil
	}
	return "", fmt.Errorf("unknown decision %q", d)
}

// CodexRules renders .codex/rules/piste.rules (Codex execpolicy, Starlark
// prefix_rule). Any rule that can't be expressed fails the whole render
// (fail-closed); nothing is written.
func CodexRules(perms []canon.Permission) ([]byte, error) {
	var b strings.Builder
	b.WriteString("# Rendered by piste agents from the zone's permissions; do not edit.\n")
	b.WriteString("# https://developers.openai.com/codex/rules\n")
	for _, p := range perms {
		cp, err := ParseCommandPattern(p.Pattern)
		if err != nil {
			return nil, err
		}
		dec, err := codexDecision(p.Decision)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Pattern, err)
		}
		quoted := make([]string, len(cp.Tokens))
		for i, t := range cp.Tokens {
			q, _ := json.Marshal(t)
			quoted[i] = string(q)
		}
		just, _ := json.Marshal("piste agents: " + p.Pattern)
		fmt.Fprintf(&b, "\n# %s\nprefix_rule(\n    pattern = [%s],\n    decision = %q,\n    justification = %s,\n)\n",
			strings.ReplaceAll(p.Pattern, "\n", " "), strings.Join(quoted, ", "), dec, just)
	}
	return []byte(b.String()), nil
}

// ClaudeRule spells a pattern as a Claude permission rule.
func ClaudeRule(cp CommandPattern) string {
	s := strings.Join(cp.Tokens, " ")
	if cp.Prefix {
		return "Bash(" + s + ":*)"
	}
	return "Bash(" + s + ")"
}

// Permissions renders shell rules:
//   - codex: .codex/rules/piste.rules, whole (ours)
//   - claude: one owned entry per rule in permissions.allow / ask / deny of
//     .claude/settings.json; the user's own entries are never touched
func Permissions(harness string, perms []canon.Permission) (Result, error) {
	var r Result
	if len(perms) == 0 {
		return r, nil
	}
	switch harness {
	case canon.Codex:
		b, err := CodexRules(perms)
		if err != nil {
			return r, err
		}
		r.add(Artifact{Harness: harness, Kind: lock.KindHook, Item: "permissions", File: ".codex/rules/piste.rules", Whole: b})
		return r, nil
	case canon.Claude:
		for _, p := range perms {
			cp, err := ParseCommandPattern(p.Pattern)
			if err != nil {
				return r, err
			}
			list := map[canon.Decision]string{canon.Allow: "allow", canon.Ask: "ask", canon.Deny: "deny"}[p.Decision]
			if list == "" {
				return r, fmt.Errorf("%s: unknown decision %q", p.Pattern, p.Decision)
			}
			v, _ := json.Marshal(ClaudeRule(cp))
			r.add(Artifact{Harness: harness, Kind: lock.KindHook, Item: "permission:" + p.Pattern, File: ".claude/settings.json",
				JSONArray: edit.Pointer("permissions", list), JSONEntry: v})
		}
		return r, nil
	case canon.Hermes, canon.ClaudePlugin, canon.AgentsMD:
		return r, fmt.Errorf("%s: shell permission rules can't be expressed; refusing to render a deny that would not apply (fail-closed)", harness)
	}
	return r, fmt.Errorf("permissions: unknown harness %q", harness)
}
