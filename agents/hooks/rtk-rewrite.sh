#!/usr/bin/env bash
# rtk-rewrite: PreToolUse hook for Claude Code and Codex that rewrites a Bash
# command to its rtk equivalent (token-optimised output). Declared in
# agents/hooks.toml; replaces the installer copy in ~/.claude/hooks.
#
# All rewrite logic lives in `rtk rewrite`. Its exit codes:
#   0 + stdout  rewritten, no ask/deny rule matched -> rewrite + allow
#   1           no rtk equivalent                    -> no opinion
#   2           a deny rule matched                  -> no opinion (native deny)
#   3 + stdout  an ask rule matched                  -> rewrite, let the harness ask
# Never blocks: any failure means "no opinion".
set -u

command -v jq >/dev/null 2>&1 || exit 0
command -v rtk >/dev/null 2>&1 || exit 0

input=$(cat)
cmd=$(printf '%s' "$input" | jq -r '.tool_input.command // empty' 2>/dev/null) || exit 0
[ -n "$cmd" ] || exit 0

rewritten=$(rtk rewrite "$cmd" 2>/dev/null)
code=$?
case $code in
  0) [ "$rewritten" = "$cmd" ] && exit 0 ;;
  3) ;;
  *) exit 0 ;;
esac
[ -n "$rewritten" ] || exit 0

updated=$(printf '%s' "$input" | jq -c --arg cmd "$rewritten" '.tool_input | .command = $cmd') || exit 0
if [ "$code" -eq 3 ]; then
  jq -n --argjson u "$updated" \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", updatedInput: $u}}'
else
  jq -n --argjson u "$updated" \
    '{hookSpecificOutput: {hookEventName: "PreToolUse", permissionDecision: "allow",
      permissionDecisionReason: "rtk auto-rewrite", updatedInput: $u}}'
fi
