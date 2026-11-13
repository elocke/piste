# piste

`piste` is one Go binary:

- **Built-in commands** run in Go (`internal/cli`, one file per command).
- **Unknown commands** run a plugin from `PATH`: `piste foo` → `piste-foo`,
  `piste secrets foo` → `piste-secrets-foo`. A plugin never shadows a built-in. It runs with `PISTE_FLAKE` set to the flake piste resolves (below), unless `PISTE_FLAKE` is already set.
  An overlay's own commands ship this way (a package in the overlay that
  installs `piste-<name>`), so this binary carries no overlay's code.

```sh
piste completion zsh|bash|fish  # also installed by the package
piste man                       # man page
```

## Which flake

You work in a piste checkout: a clone of the engine, marked by a `.piste`
file at its root, with your overlays cloned into `overlays/<name>/`.

piste walks up from the cwd to the first `flake.nix`, skipping git submodules
(a `.git` file pointing into `modules/`). A git worktree (a `.git` file
pointing at `<repo>/.git/worktrees/<name>`) resolves to itself.

- **Inside an overlay,** that overlay.
- **At the checkout root,** the overlay is, in order:
  - the one `--overlay <name>` names;
  - the only one;
  - the one whose hosts include this machine (`darwinConfigurations.<hostname>`, or `homeConfigurations."$USER@<hostname>"`). Two overlays claiming the machine is an error.
- **A checkout with no overlays** is the engine itself.
- **Outside any flake,** `$PISTE_FLAKE`; else the checkout the agents manifest records (`~/.config/piste-agents/manifest.json`); else `~/piste`. These last two are reported as `FALLBACK`.

`piste switch` and `piste gendiff` build an overlay against the engine of its
checkout, as `--override-input piste git+file://<checkout>`. It is
`git+file`, not `path:`, so only the engine's tracked files reach the store,
never `overlays/`. Overrides:

- `$PISTE_ENGINE` gives a path, or `none` for the locked input.
- A worktree of an overlay outside the checkout uses the checkout around its main repo (reported as a `FALLBACK`).
- A flake without a `piste` input (the engine) gets no override.
- A flake with no checkout around it builds its locked engine.

`piste build`, `check`, `dry`, `fix`, `gendiff` and `switch` all override alike.

**Environment.** `PISTE_ENGINE` (above), `PISTE_CONFIG` (the config file),
`PISTE_AGENTS_HOME` / `PISTE_AGENTS_ROOTS` (with `--allow-env` only) and
`PISTE_AGENTS_TOOL` (set by the `piste-agents` wrapper).

```sh
piste where    # flake: <dir> (<source>) / engine override: <path|none> (<reason>)
```

## Moving Claude Code jobs to a remote box

`piste remote move` is a hidden maintainer tool: it isn't in `piste --help`
and isn't promised ([upgrading.md](../docs/upgrading.md#what-a-release-promises)).

```sh
piste remote move <host> <job-id|session-id>...   # or --all-blocked; -n/--dry-run plans only
```

Per job, piste clones the repo the job last worked in from its origin at the
same path under the remote's `$HOME`, pushes the branch from here straight
into that clone (as `refs/remotes/piste-move/<branch>`; origin is never pushed
to, and a remote branch with its own commits is never overwritten), checks it
out in the same worktree layout, imports the transcript with a pinned
`session-migrate` (via `uvx`) and prints the `ssh -t … claude --resume …`
line. It refuses uncommitted changes, a detached HEAD, and transcripts that
match a secret pattern (names and counts only, never the match;
`--allow-secrets` overrides). Re-running is safe. The remote needs `git` and
`uvx`. `<host>` resolves through `[remote.hosts]` like the other `piste remote`
commands.

## Layout

| Path | What |
|---|---|
| `cmd/piste` | `main` |
| `internal/cli` | the cobra tree, the dispatcher (`Resolve`) |
| `internal/execx` | every external call: explicit argv, timeouts, a `Recorder` for tests / dry runs |
| `internal/platform` | system, hostname, flake-dir search |
| `internal/nixcli` | `nix eval --json` / `nix build --print-out-paths` |
| `internal/envvar` | `PISTE_*` lookups |
| `internal/config` | `~/.config/piste/config.toml` via koanf (machine data only) |
| `internal/plugins` | `piste-*` lookup |
| `internal/ui` | lipgloss styles and tables; plain for `NO_COLOR` / non-TTY |
| `internal/remotemove` | `piste remote move`: hand Claude Code background jobs to a remote box |
| `internal/machine` | `Carry`: a NixOS machine's `/etc/nixos` as flake-buildable files for a host (`<nixpkgs/nixos/modules/…>` becomes `modulesPath`, what can't be carried gets a `# piste:` marker and a note); goldens under `testdata/`, `-update` rewrites them |
| `internal/agents/fsx` | the only filesystem writer: symlink / Nix-store refusal, atomic temp+fsync+rename, staged writes |
| `internal/agents/edit` | span-preserving edits: JSON/JSONC (hujson, JSON pointers), TOML managed blocks validated by go-toml, Markdown managed blocks |
| `internal/agents/lock` | the all-kinds lock + `[[owned]]` records; re-hash on every check |
| `internal/agents/plan` | plan-then-apply: every edit computed and guarded first, all-or-nothing staging, journal |
| `internal/agents/zone` | explicit zone roots, the zone boundary, no ambient config |
| `internal/agents/canon` | the canonical model: skills, subagents, instruction fragments, hooks, MCP, permissions |
| `internal/agents/adapter` | per-harness renders (claude, claude-plugin, codex, hermes, agentsmd) → artifacts; ported from rulesync |
| `internal/agents/carrier` | the piste-harness Claude plugin + one-plugin marketplace |
| `internal/agents/safety` | control / hidden characters, confusable names, literal-secret detection |
| `internal/agents/registry` | the registry: items of explicit zone roots + locked third-party skills, generated metadata, `search`; the built artifact (`registry.json` + `content/`) and its verified `Load` |
| `THIRD_PARTY_NOTICES.md` | rulesync's MIT notice + the ported-file map; `piste --licenses`; `tools/check-notices`, `tools/upstream-watch` |
| `e2e/` | testscript harness: shims for nix, sops, claude, codex, … record argv; outputs are compared with frozen goldens (`e2e/testdata/golden`) |
| `internal/scripttest` | the golden commands (`cmpwork`, `cmpworkdir`, `$WORK` normalised, `<name>@darwin` variants, `-update`) and `PinMachine` / `PinEnv`: the test binaries pin hostname, arch, nix-darwin profile, date, TZ, git config and locale, so goldens match on any machine |

## Develop

```sh
nix develop            # go + gopls
cd cli && go test ./...
nix build .#piste     # static Go binary + wrapper (no tests: doCheck = false)
nix build .#checks.$(nix eval --raw --impure --expr builtins.currentSystem).piste   # runs the tests and the end-to-end harness
PISTE_TINT=1 go test ./internal/agents/...   # + the integration tier: real `codex execpolicy`, `claude plugin validate`
tools/upstream-watch   # rulesync commits to ported paths since v22.0.0
```

Porting more from rulesync: add the `// Portions ported from rulesync v22.0.0 …`
header as the file's first line and a row in `THIRD_PARTY_NOTICES.md`;
`tools/check-notices` (and `go test`) fail otherwise.

After changing `go.mod`, update `vendorHash` in `default.nix`:
`ci/go-vendor-hash.sh`, from the checkout root, builds the vendored modules
with a fake hash and writes the `got:` value. On Renovate's Go PRs the
`go-vendor-hash` workflow runs it and pushes the result.

A deliberate change to a command's output fails its golden. Rewrite the
goldens on Linux first (canonical), then on the Mac (writes `<name>@darwin`
only where macOS differs), and review the diff:

```sh
go test ./e2e/ -update
PISTE_AGENTS_FAKES=$PWD/../tests/agents/fakes go test ./internal/agents/ -update
```
