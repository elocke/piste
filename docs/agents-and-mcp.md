# Agents and MCP servers (`piste agents`, `piste mcp`)

- How do Claude Code and Codex get skills, instructions and hooks from piste?
- How do I add third-party skills, plugins and MCP servers, and keep every machine in step?
- How do I add my own beside a team layer's?
- How does an overlay prove its agent content stays in its zone?
- How can an agent search Nix packages and options?

piste treats agent content as configuration: your overlay declares it, a switch
deploys it, and `piste agents` / `piste mcp` check and reconcile what the
harnesses' own CLIs install. Nothing here writes a harness's own config file
directly; piste drives each harness's CLI, because Nix can't own files those
clients rewrite while they run.

## Skills and instructions (`piste.agents`)

`homeManagerModules.agents` (`modules/programs/agents.nix`) deploys the agent
zones: the engine's `agents/` (the zone `shared`) plus your overlay's own
`agents/`. A host sets `piste.agents = { enable = true; zone = "personal";
presets = [ … ]; }` and adds its zone's folder as a source; the starter does
this for you ([customising.md](customising.md#agents)). A zone is any name of
lowercase letters, digits and dashes (`personal`, `work`, `agent`, `jane`);
`shared` is the engine's own and is never a host's. The resolver
(`lib/agents.nix`) fails the build on a zone mix (a source neither `shared`
nor the host's zone, a work host with a personal source, say), a skill name
defined twice, or a preset naming something that would not deploy.

- **The `base` preset** is what every host gets: the `agent-registry` skill and
  two instructions, the never-publish-secrets rules and a short "this machine
  is set up by piste" note (edit the overlay, don't `brew install` or
  `npm install -g`, the person runs `piste switch`). Everything else is a
  preset you list: `guard` and `rtk` (below), a skill such as `htmx` in a
  preset of your zone.
- **Skills:** links from the checkout into `~/.claude/skills/<name>` and
  `~/.agents/skills/<name>` (Codex reads `.agents`). A `SKILL.md` edit is live;
  adding or removing a skill needs a switch. Nothing is forced: an existing
  copy in the way fails activation.
- **Instructions:** fragments composed at build time and spliced by
  `piste agents apply` into the `<!-- piste-agents:begin/end -->` block of
  `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md`; the rest of each file is
  never touched. `@file` imports are inlined for Codex.
- **Hooks** are declared in a zone's `hooks.toml` (an event, a tool matcher, a
  script in `hooks/`, the harnesses); a host gets one when a preset lists it
  under `hooks = [ … ]`. The engine has two presets of them, both opt-in:
  `guard` (a guard against destructive commands, dotenv reads and commits that
  leak a secret) and `rtk` (the rtk token-saving rewrite, its instruction, and
  the rtk binary on PATH). `piste agents sync --user` delivers them: Claude Code as a
  local plugin, Codex as trusted entries in `~/.codex/hooks.json`. After
selecting `rtk`, run `rtk init -g` once: Claude's rtk instruction imports
`~/.claude/RTK.md`, which only that command creates.
- **Claude profiles:** `piste.agents.claudeProfiles.<name> = "<preset>"`
  renders a `kind = "claude-profile"` preset to `~/.claude/profiles/<name>.json`
  for `claude --settings`.
- **`piste agents check`** compares the manifest
  (`~/.config/piste-agents/manifest.json`) with the disk, plus plugins,
  marketplaces, tools and pins with the zone's `agents.lock`, read-only: each
  item is `ok`, `missing`, `drift`, `undeclared`, `policy`, `tolerated` or
  `floating`. Its hints name `piste agents sync --user`: in a project, a plain
  `piste agents sync` syncs the project only. The Codex app's own plugins (its
  `openai-bundled` and `openai-primary-runtime` marketplaces: browser,
  documents, pdf and the rest) are `tolerated` in every zone, and `sync --user --prune`
  leaves them.

## Third-party skills, plugins and marketplaces

Third-party content is declared in the engine's `agents/agents.base.toml` and
in your zone's `agents/agents.toml`, and pinned in the zone's `agents.lock` by
`piste agents update` (run inside the overlay). For example, a Claude Code
plugin from a marketplace:

```toml
[[claude.plugin]]
id    = "commit-commands@claude-plugins-official"
scope = "user"
```

- `agents.base.toml` declares no plugin: Claude Code's four starter plugins
  (`commit-commands`, `astral`, and the pyright and typescript LSPs) are
  listed in its comments, and you copy the ones you want into your zone's
  `agents.toml`. A declared plugin is installed by `piste agents sync --user`
  for every host of the zone.
- Locked skills are fetched into the store at the locked commit; unlocked ones
  are *deferred*.
- `piste agents sync --user` adds marketplaces from a local mirror at the
  locked commit (`pin = "source"` for ones Claude only loads from their own
  source), installs the plugins, Codex plugins and pinned npm tools, then runs
  `piste mcp sync`. `--prune` lists undeclared items and shadowing binaries,
  and removes them only with `--apply`. `piste setup` runs it after the first
  switch.
- `agents.base.toml` is a catalogue. A marketplace counts for a zone only when
  a `[[claude.plugin]]` (Codex: `[[codex.plugin]]`) in the zone's manifests
  names it (`x@<name>`), and a skill only when a preset selects it (for `piste
  build` and `piste agents check`, the host's presets; for `piste agents
  update`, any preset of the zone's roots, since the lock is per zone). An
  unused one is not policy-checked, locked, checked, installed or put in a
  bundle for that zone. What the zone declares in its own `agents.toml` always
  counts.
- A marketplace can come from a raw GitHub URL:
  `source = "url:https://raw.githubusercontent.com/OWNER/REPO/REF/PATH/marketplace.json"`.
  The lock records the commit `REF` resolves to and the `sha256` of the file at
  that commit (canonical JSON: Claude re-serialises it). `check` compares that
  hash with the installed copy; a different one is `floating`, like `pin =
  "source"`, and `sync` adds it from the URL and never mirrors it. Allow it in
  `[policy]` by its `url:` string. (Claude Code refuses to install a url
  marketplace's relative plugin paths, so `update` reads plugin versions from a
  mirror of the repo at the locked commit instead.)
- `enabled = false` on a `[[claude.plugin]]` declares it installed but
  disabled: `check` treats a disabled install as `ok` (and an enabled one as
  `drift`), and `sync` installs it if missing and disables it.
- A zone whose `agents.toml` has `[policy] unlisted = "deny"` refuses
  third-party sources it doesn't list.

### What the policy checks, and what it doesn't

With `[policy] unlisted = "deny"`, the build fails when a zone declares a
third-party skill, marketplace, plugin, pinned tool or installer whose source
isn't in `allow`, or a skill that isn't locked. It also checks an MCP server
that runs a package through `npx`, `pnpx`, `bunx` or `uvx`, by the package's
source.

It doesn't check:

- an MCP server with any other command (a script, a binary on `PATH`);
- an `http` MCP server: its URL is not looked at;
- what an allowed plugin or skill does once it runs, or what an agent does
  with its tools;
- anything outside agent content: packages, settings and secrets are not part
  of it.

It keeps a zone's declared content to a list you wrote. It isn't a sandbox.
On a work machine, your employer's device management (MDM) is the real
control.

### Your own items beside a team layer's

Team layers are a preview in v0.1.0 ([what a release promises](upgrading.md#what-a-release-promises)). An overlay on a team layer takes the layer's `agents/` as its zone's source,
with the layer's `[policy]`, allow list and `agents.lock`. For third-party
plugins, marketplaces, skills or MCP servers of your own, which the team
doesn't get, give your overlay an `agents/agents.toml` whose policy extends the
zone's:

```toml
# agents/agents.toml in your overlay
[policy]
extends = true                              # the zone's policy is the layer's
allow   = ["github:example/own-plugins"]    # what this file adds to its allow list

[[claude.marketplace]]
name   = "own-plugins"
source = "github:example/own-plugins"

[[claude.plugin]]
id    = "mine@own-plugins"
scope = "user"
```

and add that folder as a second source of the zone, in a module every host of
yours imports. `store` is a path relative to that module's file: `./agents`
from your overlay's root (`home.nix`), `../agents` from a module in `modules/`:

```nix
# modules/agents.nix in your overlay
piste.agents.sources.work-own = {
  zone = "work";                            # the layer's zone
  store = ../agents;
  workdir = "${config.piste.checkout}/overlays/<your overlay>/agents";
};
```

- `extends = true` adds the `allow` entries to the zone's policy and may set
  nothing else: `unlisted`, `require`, `installers` and `tolerate` stay the
  layer's, so its `unlisted = "deny"` refuses anything neither allow list
  names. Two sources of a zone that each set a policy are refused.
- `piste agents update`, run in your overlay, writes its `agents/agents.lock`
  with what your `agents.toml` declares, plus any marketplace of the shared
  catalog (`agents.base.toml`) that your plugins use and the layer's lock
  doesn't pin. Update reads the layer's lock from the installed manifest
  when that manifest is of this zone and names this folder as a source's
  workdir; elsewhere (on a host of another zone, or in another checkout) it
  keeps the catalog marketplaces your lock already has. The rest of the
  shared catalog's items stay in the layer's lock, and an item locked in
  both is an error.
- `piste build` checks your items against the zone's policy, and `piste agents
  check` and `piste agents sync --user` read both sources and both locks.
- `mkFlake`'s `agents` stays the layer's folder: the registry, the bundle and
  the zone checks are the team's.

## MCP servers (`piste mcp`)

Which MCP servers each host should have is declared per zone, in `[[mcp]]`
entries of `agents/agents.toml`:

```toml
[[mcp]]
name = "playwright"
transport = "stdio"
command = "npx"
args = ["-y", "@playwright/mcp"]
harnesses = ["claude-code", "codex"]

[[mcp]]
name = "gateway"
transport = "http"
url = "https://mcp.example.com/mcp"
bearerEnv = "GATEWAY_TOKEN"            # the token comes from this env var, never the file
harnesses = ["claude-code"]
hosts = ["jane-mac"]                   # only on these machines
```

```bash
piste mcp check                # report missing, drifted and prunable servers; non-zero if any
piste mcp sync                 # add missing servers, re-add drifted ones (remove + add)
piste mcp sync --prune         # also show what would be removed: a dry run
piste mcp sync --prune --apply # ...and remove it
```

`piste mcp` drives each harness's own CLI and never writes Claude's global
config or `~/.codex/config.toml` itself. It reads them to compare definitions
(Claude's user scope; Codex through `codex mcp list --json`), and reports only
the names of drifted fields, never their values.

- Claude's global config is found the way Claude Code finds it: a legacy
  `<config home>/.config.json` wins if present (`$CLAUDE_CONFIG_DIR` or
  `~/.claude`), else `<$CLAUDE_CONFIG_DIR or ~>/.claude.json`. A leftover
  `~/.claude.json` next to a live `.config.json` is reported with its server
  names and never touched. If an add still answers "already exists", `sync`
  treats it as drift (remove + add). Codex's own built-ins (`node_repl`,
  `cua_repl`, `codex_app(s)`, `computer-use`, `mcp-search`) are foreign, like
  claude.ai connectors.
- `--prune` removes only servers the manifest owns: names in its top-level
  `retired` list, and servers it declares for another host or harness. A
  server declared nowhere is listed as *undeclared* and left alone; plugin and
  claude.ai connector servers are never touched. To retire a server, move its
  name from `[[mcp]]` to `[retired] mcp`.
- Per-harness environment goes in `[mcp.harnessEnv.<harness>]`; a leading `~/`
  in `command` or `args` expands to the home directory.
- Nix-packaged servers come from the engine's `mcp-servers-nix` input, exposed
  one by one through `overlays.ai` (today: `playwright-mcp`).

## Zone checks

An overlay proves its isolation with `piste.lib.zoneChecks`
(`lib/zone-checks.nix`). An overlay built with `piste.lib.mkFlake` gets them
for every host system, over every host's home, and `nix flake check` in the
overlay runs them. One wired by hand merges them into its `checks.<system>`:

```nix
piste.lib.zoneChecks {
  inherit pkgs;
  zone = "work";                                   # the overlay's host zone
  homes = { devbox = devboxHome.config; mac = macHome; };  # each host's home-manager config
  withSource = m: (mkDevbox [ m ]).config;         # optional: one host plus a module
  bundle = agentBundle;                            # optional: has manifest.json
  registry = agentRegistry;                        # optional: has registry.json
}
# => zone-clean               every home links something, from shared + zone sources only
#    zone-foreign-rejected    a source of another zone fails evaluation (with withSource)
#    zone-unapproved-rejected an unlisted marketplace fails evaluation, when the
#                             zone's own [policy] is unlisted = "deny" and doesn't
#                             allow lib/fixtures/unapproved's source (with withSource)
#    zone-bundle              manifest.json names only shared + zone (with bundle)
#    zone-registry            registry.json zone, roots and item ids likewise (with registry)
```

An overlay's own policy (approved third-party sources, words that must never
appear in its agent content) goes in its own checks (`mkFlake`'s `checks`).

## Searching Nix from an agent

An agent that has the NixOS MCP server ([mcp-nixos](https://github.com/utensils/mcp-nixos),
declared in `[[mcp]]` like any other) can search packages and options instead
of guessing:

```
# Search packages
mcp__nixos__nix action:search source:nixos type:packages query:<name>

# Search nix-darwin options
mcp__nixos__nix action:options source:darwin query:<topic>

# Search home-manager options/programs
mcp__nixos__nix action:options source:home-manager query:<topic>

# Get package version history
mcp__nixos__nix_versions package:<name>
```

The tool names assume the server is declared as `name = "nixos"`. For the
engine's own options, [options.md](options.md) is the reference.
