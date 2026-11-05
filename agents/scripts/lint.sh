#!/usr/bin/env bash
# lint.sh — validate every skill: agentskills reference validator + skillscheck --strict,
# then structural checks piste adds (name == dir, symlink layers complete, no Claude-only
# keys in shared skills, SKILL.md < 500 lines). Read-only. Exit 1 on any failure.
#
# Shared by every zone (the engine's and each overlay's). A zone dir holds
#   skills/<category>/<name>/SKILL.md
#   .agents/skills/<name> -> ../../skills/<category>/<name>
#   .claude/skills/<name> -> ../../skills/<category>/<name>
#
# Usage: lint.sh [--allow-empty] [ZONE_DIR]   (default ZONE_DIR: this piste/agents)
#   the engine:    agents/scripts/lint.sh
#   your overlay:  <engine checkout>/agents/scripts/lint.sh agents
set -euo pipefail
export PYTHONDONTWRITEBYTECODE=1
allow_empty=0
if [ "${1:-}" = "--allow-empty" ]; then allow_empty=1; shift; fi
ROOT="$(cd "${1:-$(dirname "$0")/..}" && pwd)"
cd "$ROOT"
echo "== zone $ROOT"
fail=0
skills=$( { find skills -mindepth 2 -maxdepth 2 -type d 2>/dev/null || true; } | sort)
if [ -z "$skills" ]; then
  if [ "$allow_empty" -eq 1 ]; then
    for l in .agents/skills/* .claude/skills/*; do
      [ -e "$l" ] || [ ! -L "$l" ] || { echo "FAIL dangling symlink $l"; exit 1; }
    done
    echo "lint clean (no skills in this zone yet)"; exit 0
  fi
  echo "no skills found under skills/"; exit 1
fi

# Top-level frontmatter = the agentskills set (name, description, license,
# metadata, compatibility, allowed-tools) PLUS the two keys Hermes itself
# honors and `hermes skills check` compares: `version` and `platforms`. The
# reference validator has no allowlist, so its ONLY tolerated error is
# "Unexpected fields in frontmatter: …" where every listed field is
# version|platforms.
echo "== agentskills validate (skills-ref) — tolerating only version/platforms"
for d in $skills; do
  if uvx -q --from skills-ref==0.1.1 agentskills validate "$d" >/tmp/lint-$$.out 2>&1; then
    echo "ok   $d"
  else
    unexpected=$(sed -nE 's/.*Unexpected fields in frontmatter: *([^.]*)\..*/\1/p' /tmp/lint-$$.out | tr -d ' ' | tr ',' '\n' | grep -vE '^(version|platforms)$' || true)
    other=$(grep -vE 'Unexpected fields in frontmatter|Validation failed for|^\s*$|error\(s\)|Invalid skill' /tmp/lint-$$.out || true)
    if [ -z "$unexpected" ] && [ -z "$other" ] && grep -q 'Unexpected fields in frontmatter' /tmp/lint-$$.out; then
      echo "ok   $d (top-level version/platforms tolerated)"
    else
      echo "FAIL $d"; cat /tmp/lint-$$.out; fail=1
    fi
  fi
done

# skillscheck's `spec` category insists every metadata value is a string, which
# forbids the nested metadata.hermes.* block Hermes itself reads.
# agentskills validate (the reference validator) is the spec authority here;
# skillscheck runs its agents/disclosure/quality checks only.
echo "== skillscheck --strict (agents,disclosure,quality)"
for d in $skills; do
  if ! uvx -q skillscheck==0.9.7 --strict --check agents,disclosure,quality "$d" >/tmp/lint-$$.out 2>&1; then
    echo "FAIL $d"; cat /tmp/lint-$$.out; fail=1
  else echo "ok   $d"; fi
done

echo "== structural"
for d in $skills; do
  name=$(basename "$d")
  fm_name=$(sed -n '/^---$/,/^---$/p' "$d/SKILL.md" | sed -nE 's/^name:[[:space:]]*"?([^"]+)"?$/\1/p' | head -1)
  [ "$fm_name" = "$name" ] || { echo "FAIL $d: frontmatter name '$fm_name' != dir '$name'"; fail=1; }
  for layer in .agents/skills .claude/skills; do
    [ -L "$layer/$name" ] || { echo "FAIL $d: missing symlink $layer/$name"; fail=1; }
    [ "$(readlink -f "$layer/$name")" = "$(readlink -f "$d")" ] || { echo "FAIL $layer/$name points elsewhere"; fail=1; }
  done
  if sed -n '/^---$/,/^---$/p' "$d/SKILL.md" | grep -qE '^(context|hooks|allowed-tools|paths):'; then
    echo "FAIL $d: Claude-only frontmatter key in a shared skill"; fail=1
  fi
  # agentskills allows only name/description/license/metadata/compatibility/allowed-tools at
  # top level; version/author/platforms live under metadata (metadata.version, metadata.hermes.*).
  if sed -n '/^---$/,/^---$/p' "$d/SKILL.md" | grep -qE '^(author|prerequisites|tags|category):'; then
    echo "FAIL $d: top-level author/prerequisites/tags/category — move under metadata (metadata.author, metadata.hermes.*)"; fail=1
  fi
  sed -n '/^---$/,/^---$/p' "$d/SKILL.md" | grep -qE '^version:' || { echo "FAIL $d: missing top-level version (hermes skills check needs it)"; fail=1; }
  lines=$(wc -l < "$d/SKILL.md"); [ "$lines" -lt 500 ] || { echo "FAIL $d: SKILL.md has $lines lines (>=500)"; fail=1; }
  for s in "$d"/scripts/*; do
    [ -f "$s" ] || continue
    [ -x "$s" ] || { echo "FAIL $s not executable"; fail=1; }
    case "$s" in
      *.sh) bash -n "$s" || fail=1 ;;
      *.py) PYTHONDONTWRITEBYTECODE=1 python3 -c "import ast,sys; ast.parse(open(sys.argv[1]).read(), sys.argv[1])" "$s" || { echo "FAIL $s: syntax"; fail=1; } ;;
    esac
  done
done
# stray bytecode/caches inside a skill dir break skillscheck on the next run
for d in $skills; do
  if find "$d" \( -name __pycache__ -o -name '*.pyc' \) | grep -q .; then
    echo "FAIL $d: __pycache__/*.pyc inside the skill dir"; fail=1
  fi
done
# every symlink must resolve to a real skill
for l in .agents/skills/* .claude/skills/*; do
  [ -e "$l" ] || { echo "FAIL dangling symlink $l"; fail=1; }
done
[ "$fail" -eq 0 ] && echo "lint clean" || echo "lint FAILED"
exit $fail
