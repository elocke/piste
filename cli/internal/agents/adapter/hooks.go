// Portions ported from rulesync v22.0.0 (https://github.com/dyoshikawa/rulesync,
// commit 81b97709), src/types/hooks.ts (event tables), src/features/hooks/tool-hooks-converter.ts,
// src/features/hooks/{claudecode,claudecode-plugin,codexcli,hermesagent}-hooks.ts.
// Copyright (c) 2024 dyoshikawa. MIT License; see cli/THIRD_PARTY_NOTICES.md.

package adapter

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/elocke/piste/cli/internal/agents/canon"
	"github.com/elocke/piste/cli/internal/agents/edit"
	"github.com/elocke/piste/cli/internal/agents/lock"
)

// eventAliases: spellings accepted in hooks.toml for the canonical names.
var eventAliases = map[string]string{
	"userPromptSubmit": "beforeSubmitPrompt",
	"interrupt":        "stopCancelled",
}

// CanonicalToClaude is CANONICAL_TO_CLAUDE_EVENT_NAMES.
var CanonicalToClaude = map[string]string{
	"sessionStart": "SessionStart", "sessionEnd": "SessionEnd",
	"preToolUse": "PreToolUse", "postToolUse": "PostToolUse",
	"beforeSubmitPrompt": "UserPromptSubmit", "stop": "Stop",
	"subagentStop": "SubagentStop", "preCompact": "PreCompact",
	"permissionRequest": "PermissionRequest", "notification": "Notification",
	"setup": "Setup", "worktreeCreate": "WorktreeCreate", "worktreeRemove": "WorktreeRemove",
	"messageDisplay": "MessageDisplay", "instructionsLoaded": "InstructionsLoaded",
	"userPromptExpansion": "UserPromptExpansion", "postToolUseFailure": "PostToolUseFailure",
	"postToolBatch": "PostToolBatch", "permissionDenied": "PermissionDenied",
	"subagentStart": "SubagentStart", "taskCreated": "TaskCreated", "taskCompleted": "TaskCompleted",
	"stopFailure": "StopFailure", "teammateIdle": "TeammateIdle", "configChange": "ConfigChange",
	"cwdChanged": "CwdChanged", "fileChanged": "FileChanged", "directoryAdded": "DirectoryAdded",
	"postCompact": "PostCompact", "elicitation": "Elicitation", "elicitationResult": "ElicitationResult",
}

// CanonicalToCodex is CANONICAL_TO_CODEXCLI_EVENT_NAMES.
var CanonicalToCodex = map[string]string{
	"sessionStart": "SessionStart", "sessionEnd": "SessionEnd",
	"preToolUse": "PreToolUse", "postToolUse": "PostToolUse",
	"beforeSubmitPrompt": "UserPromptSubmit", "stop": "Stop",
	"stopCancelled": "Interrupt", "permissionRequest": "PermissionRequest",
	"subagentStart": "SubagentStart", "subagentStop": "SubagentStop",
	"preCompact": "PreCompact", "postCompact": "PostCompact",
}

// CanonicalToHermes is CANONICAL_TO_HERMESAGENT_EVENT_NAMES.
var CanonicalToHermes = map[string]string{
	"sessionStart": "on_session_start", "sessionEnd": "on_session_end",
	"preToolUse": "pre_tool_call", "postToolUse": "post_tool_call",
	"preModelInvocation": "pre_llm_call", "postModelInvocation": "post_llm_call",
	"subagentStart": "subagent_start", "subagentStop": "subagent_stop",
}

// claudeNoMatcher: events that fire without a tool matcher (CLAUDE_NO_MATCHER_EVENTS).
var claudeNoMatcher = map[string]bool{
	"worktreeCreate": true, "worktreeRemove": true, "messageDisplay": true, "postToolBatch": true,
	"taskCreated": true, "taskCompleted": true, "teammateIdle": true, "cwdChanged": true,
	"beforeSubmitPrompt": true, "stop": true,
}

// codexNoMatcher: Codex ignores a matcher on these.
var codexNoMatcher = map[string]bool{"beforeSubmitPrompt": true, "stop": true, "stopCancelled": true}

// CanonicalEvent normalises an event name.
func CanonicalEvent(e string) string {
	if a, ok := eventAliases[e]; ok {
		return a
	}
	return e
}

// HookCommand is how a hook's command is spelled for a harness. Scripts
// (a file in the zone's hooks/) are resolved by the caller to a stable
// installed path or, for the carrier, to "./hooks/<script>".
type HookCommand func(h canon.Hook) (string, error)

// applyProjectDir prefixes a ./-relative command with the harness's root
// variable, quoting only the variable (applyCommandPrefix with
// prefixDotRelativeCommandsOnly): "$CLAUDE_PLUGIN_ROOT"/hooks/x.sh args.
func applyProjectDir(cmd, variable string) string {
	t := strings.TrimLeft(cmd, " \t")
	unq := strings.TrimLeft(t, `"'`)
	if variable == "" || strings.HasPrefix(t, "$") || !strings.HasPrefix(unq, ".") ||
		path.IsAbs(unq) || strings.HasPrefix(unq, "~/") {
		return cmd
	}
	rel := t
	if strings.HasPrefix(rel, `"./`) || strings.HasPrefix(rel, `'./`) {
		rel = rel[:1] + rel[3:]
	} else {
		rel = strings.TrimPrefix(rel, "./")
	}
	return `"` + variable + `"/` + rel
}

// hookEntry is one matcher group with one handler.
func hookEntry(h canon.Hook, cmd string, noMatcher map[string]bool, event string) ([]byte, error) {
	handler := map[string]any{"type": "command", "command": cmd}
	if h.Timeout > 0 {
		handler["timeout"] = h.Timeout
	}
	group := map[string]any{"hooks": []any{handler}}
	if h.Matcher != "" && !noMatcher[event] {
		group["matcher"] = h.Matcher
	}
	return json.Marshal(group)
}

func checkHook(h canon.Hook) error {
	for _, s := range []string{h.Command, h.Matcher, h.Script} {
		if strings.ContainsAny(s, "\n\r\x00") {
			return fmt.Errorf("hook %s: command/matcher must not contain newline, CR or NUL", h.Name)
		}
	}
	if h.Command == "" && h.Script == "" {
		return fmt.Errorf("hook %s: needs a script or a command", h.Name)
	}
	return nil
}

// Hooks renders owned hooks for one harness.
//   - claude (settings.json) and codex (.codex/hooks.json): one matcher group
//     per hook, each an owned array entry, so foreign hooks in the same
//     event (another tool's installer) are never touched.
//   - claude-plugin: the carrier's hooks/hooks.json, whole (the carrier is
//     entirely ours), with ./-relative commands under "$CLAUDE_PLUGIN_ROOT".
//   - hermes: not rendered; warned.
func Hooks(harness string, scope canon.Scope, hooks []canon.Hook, command HookCommand) (Result, error) {
	var r Result
	var mine []canon.Hook
	for _, h := range hooks {
		if len(h.Targets) > 0 && !contains(h.Targets, targetFamily(harness)) {
			continue
		}
		if err := checkHook(h); err != nil {
			return r, err
		}
		mine = append(mine, h)
	}
	if len(mine) == 0 {
		return r, nil
	}
	switch harness {
	case canon.Claude, canon.Codex:
		table, noMatch, file := CanonicalToClaude, claudeNoMatcher, ".claude/settings.json"
		if harness == canon.Codex {
			table, noMatch, file = CanonicalToCodex, codexNoMatcher, ".codex/hooks.json"
		}
		for _, h := range mine {
			ev := CanonicalEvent(h.Event)
			name, ok := table[ev]
			if !ok {
				return r, fmt.Errorf("%s: hook %s: event %q is not supported", harness, h.Name, h.Event)
			}
			cmd, err := command(h)
			if err != nil {
				return r, err
			}
			if harness == canon.Claude && scope == canon.Project {
				cmd = applyProjectDir(cmd, "$CLAUDE_PROJECT_DIR")
			}
			entry, err := hookEntry(h, cmd, noMatch, ev)
			if err != nil {
				return r, err
			}
			r.add(Artifact{Harness: harness, Kind: lock.KindHook, Item: h.Name, File: file,
				JSONArray: edit.Pointer("hooks", name), JSONEntry: entry})
		}
		return r, nil
	case canon.ClaudePlugin:
		events := map[string][]json.RawMessage{}
		for _, h := range mine {
			ev := CanonicalEvent(h.Event)
			name, ok := CanonicalToClaude[ev]
			if !ok {
				return r, fmt.Errorf("claude-plugin: hook %s: event %q is not supported", h.Name, h.Event)
			}
			cmd, err := command(h)
			if err != nil {
				return r, err
			}
			entry, err := hookEntry(h, applyProjectDir(cmd, "$CLAUDE_PLUGIN_ROOT"), claudeNoMatcher, ev)
			if err != nil {
				return r, err
			}
			events[name] = append(events[name], entry)
		}
		b, err := json.MarshalIndent(map[string]any{"hooks": events}, "", "  ")
		if err != nil {
			return r, err
		}
		r.add(Artifact{Harness: harness, Kind: lock.KindHook, Item: "hooks", File: "hooks/hooks.json", Whole: append(b, '\n')})
		return r, nil
	case canon.Hermes:
		names := make([]string, 0, len(mine))
		for _, h := range mine {
			names = append(names, h.Name)
		}
		sort.Strings(names)
		r.warn("hermes: hooks not rendered (" + strings.Join(names, ", ") + "); Hermes gets no workstation hooks")
		return r, nil
	}
	return r, fmt.Errorf("hooks: unknown harness %q", harness)
}

func targetFamily(h string) string {
	if h == canon.ClaudePlugin {
		return canon.Claude
	}
	return h
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
