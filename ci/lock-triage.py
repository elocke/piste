#!/usr/bin/env python3
"""Classify a lockfile diff as a plain pin move or something needing a human.

Shared by every zone overlay's lock-review workflow, the way
agents/scripts/lint.sh is shared: the overlay clones the engine in CI and
runs this from the checkout.

Two kinds, because the two weekly bumps carry different risk:

  --kind agents   agents/agents.lock, from `piste agents update --all`. A skill is
                  instructions the agents follow, a marketplace commit carries
                  plugin content including hooks, and an [[mcp]] entry is a
                  command. These are reviewed like code.

  --kind flake    flake.lock, from the update-flake-lock action. An input's rev
                  moving is not readable the way a skill diff is — a nixpkgs
                  bump is tens of thousands of commits — so here the mechanical
                  check is the whole review, and the build is what proves it.

No tier means "merge it" on its own. The tiers say what has to happen next:

  `review`   — a plain move of things already trusted. For agents, an agent reads
               the upstream diffs and decides. For flake, the build is the gate.
  `escalate` — no automation merges this; a person looks at it.

Usage:
    lock-triage.py --kind {agents,flake} BASE HEAD [--json]

Exit status is 0 unless the inputs are unreadable; read `tier` from the output,
so the caller stays in charge of what a tier means.
"""

from __future__ import annotations

import argparse
import json
import sys
import tomllib
from pathlib import Path

# agents.lock sections whose movement always needs a person. [[mcp]] is a
# command line, [[tool]] is a binary on PATH and [[hook]] is an installer that
# writes hooks into every harness, so a bump there is not a boring bump.
EXECUTABLE_SECTIONS = ("mcp", "tool", "hook")

# The only fields a mechanical bump is allowed to touch.
AGENTS_MECHANICAL = {"commit", "narHash", "version", "fetched", "gitCommitSha", "integrity"}

# Same idea for a flake.lock node's `locked` block. `lastModified` and
# `revCount` move with the rev; `rev` and `narHash` are the content.
FLAKE_MECHANICAL = {"rev", "narHash", "lastModified", "revCount"}

# A change to any of these means the input points somewhere else, not just
# further along. `ref` covers a branch or tag switch.
FLAKE_IDENTITY = ("type", "owner", "repo", "url", "ref", "dir", "host", "path")


def major(version: object) -> str | None:
    """The leading numeric component of a version, or None if there isn't one."""
    if not isinstance(version, str) or not version:
        return None
    head = version.lstrip("v").split(".", 1)[0].split("-", 1)[0]
    return head if head.isdigit() else None


# --- agents.lock -------------------------------------------------------------


def agents_flatten(doc: dict) -> dict[tuple[str, str], dict]:
    """{(section, name): entry} over every array-of-tables in the lock.

    Harness sections nest ([claude] / [codex] -> marketplace / plugin), so they
    are flattened to "claude.plugin" etc. An entry is keyed by `name` or `id`.
    """
    out: dict[tuple[str, str], dict] = {}

    def add(section: str, rows: object) -> None:
        if not isinstance(rows, list):
            return
        for row in rows:
            if not isinstance(row, dict):
                continue
            key = row.get("name") or row.get("id")
            if key:
                out[(section, str(key))] = row

    for section, value in doc.items():
        if isinstance(value, list):
            add(section, value)
        elif isinstance(value, dict):
            for sub, rows in value.items():
                add(f"{section}.{sub}", rows)
    return out


def triage_agents(base: dict, head: dict) -> tuple[list[str], list[str]]:
    before, after = agents_flatten(base), agents_flatten(head)
    reasons: list[str] = []
    moves: list[str] = []

    for section, name in sorted(after.keys() - before.keys()):
        reasons.append(f"new {section}: {name} — a source to trust, not a bump")
    for section, name in sorted(before.keys() - after.keys()):
        reasons.append(f"removed {section}: {name} — confirm it was retired on purpose")

    for key in sorted(before.keys() & after.keys()):
        section, name = key
        old, new = before[key], after[key]
        if old == new:
            continue

        changed = {f for f in old.keys() | new.keys() if old.get(f) != new.get(f)}

        if section.split(".")[-1] in EXECUTABLE_SECTIONS or section in EXECUTABLE_SECTIONS:
            reasons.append(
                f"{section} {name} moved ({', '.join(sorted(changed))}) — "
                "commands and installers are reviewed by hand"
            )
            continue

        unexpected = changed - AGENTS_MECHANICAL
        if unexpected:
            reasons.append(
                f"{section} {name} changed {', '.join(sorted(unexpected))}, "
                "which a pin bump does not touch"
            )
            continue

        old_major, new_major = major(old.get("version")), major(new.get("version"))
        if old_major is not None and new_major is not None and old_major != new_major:
            reasons.append(
                f"{section} {name}: major {old.get('version')} -> {new.get('version')}"
            )
            continue

        detail = []
        if old.get("version") != new.get("version"):
            detail.append(f"{old.get('version')} -> {new.get('version')}")
        if old.get("commit") != new.get("commit"):
            detail.append(f"{str(old.get('commit'))[:12]} -> {str(new.get('commit'))[:12]}")
        moves.append(f"{section} {name}: {'; '.join(detail) if detail else 'rehashed'}")

    return reasons, moves


# --- flake.lock --------------------------------------------------------------


def flake_nodes(doc: dict) -> dict[str, dict]:
    """{node name: node} for every node except the synthetic root."""
    nodes = doc.get("nodes")
    if not isinstance(nodes, dict):
        return {}
    root = doc.get("root")
    return {
        name: node
        for name, node in nodes.items()
        if name != root and isinstance(node, dict)
    }


def triage_flake(base: dict, head: dict) -> tuple[list[str], list[str]]:
    before, after = flake_nodes(base), flake_nodes(head)
    reasons: list[str] = []
    moves: list[str] = []

    for name in sorted(after.keys() - before.keys()):
        reasons.append(f"new input node: {name} — a new source, not a bump")
    for name in sorted(before.keys() - after.keys()):
        reasons.append(f"input node gone: {name} — confirm that is intended")

    for name in sorted(before.keys() & after.keys()):
        old, new = before[name], after[name]
        if old == new:
            continue

        old_locked = old.get("locked") or {}
        new_locked = new.get("locked") or {}

        # Where the input points, as opposed to how far along it is.
        moved_identity = [
            field
            for field in FLAKE_IDENTITY
            if old_locked.get(field) != new_locked.get(field)
        ]
        if moved_identity:
            detail = ", ".join(
                f"{f}: {old_locked.get(f)!r} -> {new_locked.get(f)!r}" for f in moved_identity
            )
            reasons.append(f"{name} now points elsewhere ({detail})")
            continue

        # `original` is the flake.nix declaration; CI must not see it change in
        # a lockfile-only bump.
        if (old.get("original") or {}) != (new.get("original") or {}):
            reasons.append(
                f"{name}: the declared input changed, which a flake.lock bump does not do"
            )
            continue

        if (old.get("inputs") or {}) != (new.get("inputs") or {}):
            reasons.append(f"{name}: its own input wiring changed (follows)")
            continue

        changed_locked = {
            f for f in old_locked.keys() | new_locked.keys()
            if old_locked.get(f) != new_locked.get(f)
        }
        unexpected = changed_locked - FLAKE_MECHANICAL
        if unexpected:
            reasons.append(
                f"{name} changed {', '.join(sorted(unexpected))}, "
                "which a rev bump does not touch"
            )
            continue

        # A rev moving *backwards* is mechanical by every rule above and still
        # wrong: it is what a stale bump branch does once main has moved past
        # it. lastModified is upstream commit time, so it is the honest signal.
        old_when, new_when = old_locked.get("lastModified"), new_locked.get("lastModified")
        if isinstance(old_when, int) and isinstance(new_when, int) and new_when < old_when:
            reasons.append(
                f"{name} would move backwards: lastModified {old_when} -> {new_when} "
                "(stale branch; rebase it rather than merging)"
            )
            continue

        old_rev = str(old_locked.get("rev") or "")[:12]
        new_rev = str(new_locked.get("rev") or "")[:12]
        moves.append(f"{name}: {old_rev or '?'} -> {new_rev or '?'}")

    return reasons, moves


# --- entry point -------------------------------------------------------------


def load(path: Path, kind: str) -> dict:
    if kind == "agents":
        with path.open("rb") as fh:
            return tomllib.load(fh)
    return json.loads(path.read_text())


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--kind", choices=("agents", "flake"), required=True)
    ap.add_argument("base", type=Path, help="the lockfile as the base branch has it")
    ap.add_argument("head", type=Path, help="the lockfile as the PR proposes it")
    ap.add_argument("--json", action="store_true", help="machine-readable output")
    args = ap.parse_args()

    base, head = load(args.base, args.kind), load(args.head, args.kind)
    triage = triage_agents if args.kind == "agents" else triage_flake
    reasons, moves = triage(base, head)
    tier = "escalate" if reasons else "review"

    if args.json:
        json.dump({"tier": tier, "reasons": reasons, "moves": moves}, sys.stdout, indent=2)
        sys.stdout.write("\n")
        return 0

    print(f"tier: {tier}")
    if reasons:
        print("\nneeds a person:")
        for reason in reasons:
            print(f"  - {reason}")
    if moves:
        print(f"\nmechanical moves ({len(moves)}):")
        for move in moves:
            print(f"  - {move}")
    if not reasons and not moves:
        print("\nnothing moved.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
