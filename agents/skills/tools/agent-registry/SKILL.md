---
name: agent-registry
description: "Use when asked what agent skills, hooks or MCP servers this project has, whether they are current, or to add or remove one from the registry. Wraps `piste agents audit|drift|search|show --json` and, only on an explicit request, `add|remove|sync` after a `--dry-run` plan."
version: 1.0.0
---

# Agent registry

`piste agents` manages a project's agent content (skills, subagents, instruction
fragments, hooks, MCP servers, plugins) from the machine's **registry**: every
item the machine's trust zones offer, with generated metadata. A project opts
in with `.agents/agents.toml`; piste records what it wrote in `.agents/agents.lock`.

Run everything from inside the repository (the project root is the git root).

## Read first: never mutate to answer a question

| Question | Command |
|---|---|
| What agent content does this project have, and where did each piece come from? | `piste agents audit --json` |
| Is it current? Anything edited, missing, outdated, or refused by policy? | `piste agents drift --json` |
| What could I add? | `piste agents search <words> --json` (`--kind skill\|3p-skill\|agent\|instruction\|hook\|mcp\|plugin`, `--harness claude\|codex\|hermes`, `--in-zone shared\|personal\|work`) |
| Tell me about one item | `piste agents show <id> --json` |
| Which registry is this machine reading? | `piste agents registry info` |

Item ids are `<zone>:<kind>/<name>`, for example `shared:skill/htmx` or
`personal:3p-skill/<name>`.

Reading the results:

- **audit** labels each entry `registry` (piste wrote it, or it is a copy of a
  registry item), `third-party`, `local` (declared `[[local]]` in the
  manifest), `hand-made` (tracked in git, not managed) or `unknown`
  (untracked, or a symlink leaving the repo).
- **drift** states: `ok`, `local`, `edited`, `missing`, `outdated` (the
  registry has a newer version), `gone`, `policy` (the zone rules now refuse
  it), `unsynced`, `unknown`. It exits 1 on drift; `--ci` also fails on
  `unknown`.
- **show** gives trust (first-party, declared, third-party and the policy
  pattern that allows it), harness support, fields a harness drops
  (`lossy`), requirements (commands, environment variables, other items),
  version, commit and content hash.

## Changing the project: only when the user asked in this turn

Never run a mutation because it seems helpful. When the user explicitly asks
to add, remove or reconcile:

1. Show the plan first, with `--dry-run` (`-n`):
   `piste agents add <id>... -n`, `piste agents remove <id>... -n`, `piste agents sync -n`.
2. Report the plan and the diff it prints. Point out hook scripts and MCP
   commands in it: they run code on the user's machine.
3. Apply only after the user confirms, with the same command without `-n`.
4. Show `git status` / `git diff` afterwards for review. Do not commit unless
   asked.

What each writer does:

- `add <id>...`: checks the project's zone (`personal:*` is never allowed in a
  `zone = "work"` project) and the zone's third-party policy, then writes the
  item (skills are copied to `.agents/skills/<n>` with a relative
  `.claude/skills/<n>` link) and records every file and merged key in the
  lock. Items the added ones `requires` come along.
- `remove <id>...`: removes exactly what the lock says piste wrote. It refuses if
  a file was edited by hand since (the diff is shown); `--force` discards the
  edit, so ask first.
- `sync`: re-creates missing files, links and keys from the lock and the
  manifest. A fresh clone or a devcontainer runs it.
- `init --adopt`: writes `.agents/agents.toml` for a project that has none;
  registry copies become `[[use]]`, hand-made content becomes `[[local]]`.

## When it fails

- `not in this machine's registry`: the item is from a zone this machine
  doesn't have, or the registry is stale (`piste agents registry info`).
- `is personal content; this project is zone = "work" (refused)`: by design.
  Say so; don't work around it.
- `not allowed by the ... zone [policy]`: a new third-party source goes
  through the zone repo's `agents.toml`, not a project.
- `hand-edited` / `not ours`: someone changed a file piste owns, or a path is in
  the way. Show the diff and ask.
