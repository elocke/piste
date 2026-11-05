# agents — the shared zone

Agent content every zone gets. A machine takes this plus its overlay's `agents/`, the
machine's own zone. A zone is any lowercase name (letters, digits, dashes); `shared`,
this one, is the engine's.

```
skills/<category>/<name>/SKILL.md     Agent Skills, portable frontmatter only
.agents/skills/<name>  -> ../../skills/<category>/<name>   flat index (Codex, Hermes)
.claude/skills/<name>  -> ../../skills/<category>/<name>   flat index (Claude Code)
instructions/<topic>.md               fragments composed into CLAUDE.md / AGENTS.md
instructions/include/<harness>/<f>    text inlined for `@<f>` where the harness can't import (Codex)
presets/<name>.toml                   named sets of skills + fragments; hosts select presets
hooks.toml + hooks/<script>           owned harness hooks (rtk rewrite, piste-guard), selected by preset (`rtk`, `guard`)
capabilities.toml                     where each harness reads each kind
agents.base.toml                      shared third-party declarations (pinned in each zone's agents.lock)
scripts/lint.sh                       skill lint, run by every zone's CI
```

Rules:

- **Nothing personal.** Every machine gets this zone, so no personal hostnames, domains, LAN
  addresses, services or paths: `piste leaks` checks every push and the published tree. A
  person's own content goes in their overlay's `agents/`.
- **Lint.** `scripts/lint.sh` must pass: `name` == leaf dir, top-level frontmatter limited to
  `name`, `description`, `license`, `version`, `platforms`, `metadata`; `SKILL.md` < 500 lines;
  both link layers complete.
- **Third-party** items are declared in `agents.base.toml` (shared) or a zone's `agents.toml`,
  never copied. `piste agents update` (run in the zone repo) pins them in that zone's
  `agents.lock`: skills by commit + narHash, marketplaces by commit, plugins by version +
  `gitCommitSha`, MCP / hook installers / npm tools by exact version + integrity. A
  modified fork is owned: keep the upstream `LICENSE` and record `metadata.upstream`.
- **Deployed by `piste.agents`** (`modules/programs/agents.nix`). A host picks presets;
  owned skills are linked from the working checkout into `~/.claude/skills/<n>` and
  `~/.agents/skills/<n>` (edits are live, adding/removing a skill needs a switch), and the
  fragments are composed into the `<!-- piste-agents:begin/end -->` block of
  `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md`. `piste agents check` compares the result
  with the disk, plus plugins, marketplaces, tools and pins against `agents.lock`; `piste agents
  sync` installs/pins them (marketplaces via a local mirror at the locked commit) and, with
  `--prune --apply`, removes undeclared ones (a dry run without `--apply`). Locked third-party
  skills are linked from their store fetch; unlocked ones are reported as *deferred*.
  `piste.agents.claudeProfiles` renders `kind = "claude-profile"` presets to
  `~/.claude/profiles/<name>.json` for `claude --settings` (a lean profile).
  `piste agents budget` (a hidden maintainer tool: not in `--help`, not promised) measures the
  start-of-session context of the latest transcript.
- **Registry and projects.** (`piste agents registry` is a hidden maintainer tool: not in
  `--help`, not promised.) `piste agents registry build` turns the zone
  roots (+ each locked third-party skill's fetch) into `registry.json` + `content/`; the
  module links it at `~/.local/share/piste-agents/registry`, zone devcontainers carry it at
  `/etc/piste/agent-bundle/registry`, and `piste agents registry pull` installs one on a
  machine without Nix (the static `piste` from `.#piste-release`). Ids are
  `<zone>:<kind>/<name>` (`shared:skill/htmx`, `personal:3p-skill/<name>`). In any repo:
  `piste agents audit` (works before `init`), `init --adopt` (registry copies become `[[use]]`,
  hand-made content `[[local]]`), `search`, `show`, `add` / `remove` / `sync` (`-n` plans
  first), `drift --ci`. `--workdir` reads the zone checkouts live instead of the built
  registry. The `agent-registry` skill wraps these for agents.
- **Hooks.** `hooks.toml` declares each hook once (canonical event, matcher, script,
  targets); a preset opts a host in (`presets/guard.toml`: the piste-guard hooks; `presets/rtk.toml`: the
  rtk rewrite, its instruction, and rtk on PATH). The module installs each script
  behind a stable wrapper at `~/.local/share/piste-agents/hooks/bin/<name>` and renders a local
  Claude plugin `piste-harness@piste-local` plus owned entries for `~/.codex/hooks.json`.
  `piste agents sync --user` installs/updates the plugin, merges the Codex entries (foreign ones, such
  as another tool's hooks, are never touched) and trusts only the owned ones; `piste agents check`
  lists owned, foreign, untrusted and replaced (`conflict`) hooks.
  - `piste-guard` blocks: reading dotenv files, whole-tree `git add` (`-A`, `.`, wildcards),
    commits whose content gitleaks flags, `rm` of anything git can't restore (use
    `trash-put`; `/tmp` and clean tracked files are fine), and discarding uncommitted work
    (`reset --hard`, `checkout -- <p>`, `restore`, `clean -f`, `push --force`), and
    `git push --no-verify` (the push hook is the leak gate, `piste leaks`). It never
    asks for approval, and fails open on an internal error. `PISTE_GUARD_DISABLE=1` in the
    harness environment turns it off. Cases: `tests/agents/test_piste_guard.py`.
