#!/usr/bin/env python3
"""Decide whether a weekly lockfile bump may merge, and write the PR comment.

The gate behind every zone's lock-review workflow, kept out of the workflow so
it can be tested. It re-derives every condition itself rather than trusting the
review step: the triage tier, the verdict when one is required, and the repo's
own checks. Model text arrives through the environment and is only ever written
into a file, never evaluated by a shell.

Two shapes, matching ci/lock-triage.py's two kinds:

    REVIEW_REQUIRED=true   agents.lock. A plain pin move still needs someone to
                           read the upstream diffs, because a skill is
                           instructions the agents follow. Merges only
                           on a clean agent verdict. With no review credential
                           configured, nothing merges — which is a useful
                           triage-only mode for a zone that wants no agent.

    REVIEW_REQUIRED=false  flake.lock. A rev bump is not readable the way a
                           skill diff is, so the mechanical triage is the whole
                           review and the build is what proves it. Merges on
                           tier `review` plus green checks.

Reads from the environment:
    KIND              agents | flake, for the comment wording
    TIER              review | escalate, from lock-triage.py
    REVIEW_REQUIRED   true | false
    HAS_KEY           "true" if a review credential is configured
    NUMBER            the PR number
    HEAD              the PR's head commit; its check runs are the gate
    GITHUB_REPOSITORY owner/repo, for the check-runs API (set by Actions)
    VERDICT           the review step's --json-schema output (may be empty)
    RUNNER_TEMP       holds triage.txt; comment.md is written beside it

Writes `{"merge": bool}` to stdout and the comment body to $RUNNER_TEMP/comment.md.
`--checks-from FILE` reads the status rollup from a file instead of calling
`gh`, for tests.
"""

from __future__ import annotations

import argparse
import json
import os
import pathlib
import subprocess
import sys

# A check that has not concluded is not green. These workflows run well after
# the bump is opened, so "still running" means something is wrong, not "wait".
GREEN = ("SUCCESS", "NEUTRAL", "SKIPPED")

APPLY_HINT = "Not applied to any machine yet: run `piste switch`, then `piste agents sync --user`."


def neutralise(text: str) -> str:
    """Stop model-written text from forging structure in the PR comment.

    The verdict cannot change the merge decision — that is settled before any of
    this is rendered. But a summary containing `### Reviewed and merged` would
    render as a heading and mislead whoever reads the comment, so line-leading
    markdown is escaped. GitHub honours the backslash.
    """
    out = []
    for line in text.splitlines():
        stripped = line.lstrip()
        if stripped[:1] in ("#", ">", "-", "*", "+", "|", "=", "`"):
            indent = line[: len(line) - len(stripped)]
            out.append(f"{indent}\\{stripped}")
        else:
            out.append(line)
    return "\n".join(out)


def fence(text: str) -> str:
    """Fence `text` so a run of backticks inside it cannot close the block."""
    longest = run = 0
    for char in text:
        run = run + 1 if char == "`" else 0
        longest = max(longest, run)
    bars = "`" * max(3, longest + 1)
    return f"{bars}\n{text.strip()}\n{bars}"


def rollup(number: str, source: pathlib.Path | None) -> list[dict]:
    """The checks on the bump's head commit.

    The bump PR is opened with GITHUB_TOKEN, so its checks come from runs
    dispatched onto the branch (workflow_dispatch), and GitHub does not list
    those in the PR's statusCheckRollup: it stays empty however green they
    are. So, given HEAD, read the check runs on that commit instead, the
    newest run of each name. Without HEAD, the PR's rollup.
    """
    if source is not None:
        return json.loads(source.read_text())["statusCheckRollup"]
    head = os.environ.get("HEAD", "")
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    if head and repo:
        out = subprocess.run(
            [
                "gh", "api", "--paginate",
                f"repos/{repo}/commits/{head}/check-runs",
                "--jq", ".check_runs[] | {name, status, conclusion, started_at}",
            ],
            capture_output=True,
            text=True,
            check=True,
        ).stdout
        return newest_by_name(json.loads(line) for line in out.splitlines() if line)
    out = subprocess.run(
        ["gh", "pr", "view", number, "--json", "statusCheckRollup"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    return json.loads(out)["statusCheckRollup"]


def newest_by_name(runs) -> list[dict]:
    """One check per name, the newest run's, conclusion upper-cased as the
    rollup spells it. A run that has not completed has no conclusion."""
    newest: dict[str, dict] = {}
    for run in runs:
        name = run.get("name") or "?"
        if name not in newest or (run.get("started_at") or "") > (
            newest[name].get("started_at") or ""
        ):
            newest[name] = run
    return [
        {"name": name, "conclusion": (run.get("conclusion") or "").upper()}
        for name, run in sorted(newest.items())
    ]


def not_green(checks: list[dict]) -> list[str]:
    return sorted(
        check.get("name") or check.get("context") or "?"
        for check in checks
        if (check.get("conclusion") or "") not in GREEN
    )


def check_concerns(number: str, checks_file: pathlib.Path | None) -> list[str]:
    """Why the repo's own checks do not clear this PR, if they don't."""
    checks = rollup(number, checks_file)
    if not checks:
        # These bump PRs are opened with GITHUB_TOKEN, which starts no
        # workflows, so their checks are dispatched onto the branch. No check
        # on the head means that dispatch did not happen — no CI ran at all,
        # not a pass.
        return ["no checks reported on this PR at all"]
    stuck = not_green(checks)
    return ["checks not green: " + ", ".join(stuck)] if stuck else []


def decide(env: dict[str, str], checks_file: pathlib.Path | None) -> tuple[bool, str]:
    kind = env.get("KIND", "agents")
    tier = env["TIER"]
    review_required = env.get("REVIEW_REQUIRED", "true") == "true"
    has_key = env.get("HAS_KEY") == "true"
    triage = pathlib.Path(env["RUNNER_TEMP"], "triage.txt").read_text()
    what = "pin" if kind == "agents" else "input"

    merge = False
    concerns: list[str] = []

    if tier != "review":
        heading = "Triage: needs a person"
        body = (
            f"This bump is not a plain {what} move, so nothing merged it. The "
            "reasons are below."
        )
    elif review_required and not has_key:
        heading = "Triage: clean, but unreviewed"
        body = (
            f"Every change is a plain {what} move, so this was eligible for "
            "agent review — but no review credential is set in this repo "
            "(`CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY`), so no one read "
            "the upstream diffs and nothing was merged."
        )
    elif not review_required:
        # flake.lock: the triage plus the build is the whole gate.
        concerns = check_concerns(env["NUMBER"], checks_file)
        merge = not concerns
        body = (
            f"Every input moved forward only, with no change to where any of "
            f"them points. The build is the gate here, not a reading of the diff."
        )
        heading = "Mechanical bump, merged" if merge else "Mechanical bump, not merged"
    else:
        try:
            verdict = json.loads(env.get("VERDICT") or "{}")
        except json.JSONDecodeError:
            verdict = {}
        if not isinstance(verdict, dict):
            verdict = {}

        body = neutralise(str(verdict.get("summary") or "(no summary returned)"))
        concerns = [neutralise(str(c)) for c in (verdict.get("concerns") or [])]

        # A verdict with concerns is not a merge, whatever the boolean says.
        merge = verdict.get("merge") is True and not concerns

        gate = check_concerns(env["NUMBER"], checks_file)
        if gate:
            merge = False
            concerns += gate

        heading = "Reviewed and merged" if merge else "Reviewed, not merged"

    parts = [f"### {heading}", "", body]
    if concerns:
        parts += ["", "**Concerns**", ""] + [f"- {c}" for c in concerns]
    parts += ["", "<details><summary>Triage</summary>", "", fence(triage), "", "</details>"]
    if merge:
        parts += ["", APPLY_HINT]

    return merge, "\n".join(parts) + "\n"


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--checks-from", type=pathlib.Path, default=None)
    args = ap.parse_args()

    merge, comment = decide(dict(os.environ), args.checks_from)
    pathlib.Path(os.environ["RUNNER_TEMP"], "comment.md").write_text(comment)
    json.dump({"merge": merge}, sys.stdout)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
