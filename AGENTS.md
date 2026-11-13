# piste

Instructions for coding agents working on piste, this repository. Codex reads this file;
Claude Code reads it through `CLAUDE.md`. Keep it under 20,000 characters (some harnesses
cap a context file there). The repository is public: nothing private goes in it (see
"Nothing private").

piste is a Nix engine and a Go CLI. The engine is a library of composable nix-darwin and
home-manager modules (roles, package categories, a starter overlay) with no host
definitions. A person's machines live in their own overlay, a git repository cloned into
`overlays/<name>/` of their checkout. The CLI, `piste`, builds and switches an overlay
against the checkout around it.

## Helping someone with their machines

If a person asked you to change their machine (add a package, fix a build, change a
setting), this is for you. This checkout is the engine; their settings are in
`overlays/<name>/`.

- Edit the overlay, not the engine. What's on every machine is in `home.nix`; one machine's
  is in `hosts/<host>.nix`. Skill `piste-config` shows where each kind of change goes.
- `git add -N` a new file, then check with `PISTE_AI=none piste fix` (or `piste build`).
- Never run `piste switch`, `update`, `pkgs update`, `host`, `adopt`, `secrets`, `github`
  or `clean`, nor `git push`, `sudo` or `brew install` / `npm install -g`. The person runs those.
- Never put a secret's value in a .nix file: the Nix store is readable by every user on
  the machine. Secrets live encrypted (`piste secrets edit`), and an exported variable
  reaches every process the shell starts.
- Everything below is for changing the engine itself.

## Hostnames

`piste switch` reads the overlay's `darwinConfigurations` (on Linux, `homeConfigurations`;
on NixOS, `nixosConfigurations` first) and refuses to activate when this machine's hostname
isn't one of its hosts, unless `--host <name>` is given. To check a change for another
machine, use `piste build --host <name>`. Never `piste switch --host <name>` for a machine you're not on.

## Quick reference

```bash
# From the checkout or an overlay (before the first switch: nix run .#piste -- <command>)
piste switch            # Build and apply
piste build             # Build only
piste check             # Fast syntax check
piste rollback          # Revert to the previous generation
piste update            # Move the checkout and the overlay's engine pin to the newest release
piste pkgs              # What's out of date, by owner (read-only)
piste pkgs update       # Release, inputs, build, switch, then pnpm and brew upgrades
piste drift             # Packages installed outside Nix
PISTE_AI=none piste fix # Rerun a failed build (the piste-fix skill); plain piste fix starts an AI on it
piste claude            # Claude Code at the checkout root, overlays added
piste setup [<url>]     # Guided onboarding: prerequisites, checkout, overlay, first switch
piste init [name]       # A new overlay in overlays/<name>/ from templates/default
piste add <git url>     # Clone an existing overlay into overlays/ and build it
piste github            # GitHub over SSH: own key (auth + signing), uploaded, in the host file
piste leaks [<dir>]     # Private words, private addresses and secrets in unpushed commits
nix search nixpkgs#<package-name>
```

`docs/commands.md` has every command and flag.

## Workspace layout

```
<checkout>/                 # this repository (piste setup clones it to ~/piste)
├── .piste                  # marks the checkout root
├── AGENTS.md, CLAUDE.md, .agents/skills/, .claude/skills/
└── overlays/               # gitignored: each overlay is its own git repository
    └── <name>/
```

`piste` acts on the overlay it is in; at the checkout root, on the one whose hosts include
this machine (`--overlay <name>` to pick). It builds against this checkout (`--override-input
piste git+file://<checkout>`; see `cli/README.md` "Which flake"). Agent skill links point
into the checkout at `piste.checkout` (default `~/piste`).

Builds see the checkout's *tracked* files only (`git+file`; that keeps `overlays/` out of
the store). Uncommitted edits to tracked files count, but a new engine file (a module, a
`pkgs/overlays/*.nix`) is invisible until `git add -N`.

Start agent sessions at the checkout root (`piste claude` does), to reach all instructions
and skills.

## Project structure

```
flake.nix                       # inputs + outputs (modules, overlays, lib, packages, templates)
flake/parts/per-system.nix      # the piste derivation, apps, checks, devShells
modules/
  packages-module.nix           # the package list, options-based (piste.packages);
                                # exported as darwin-, nixos- and homeManagerModules.packages
  programs/                     # one program per file: shell, git (identities), ssh, editor,
                                # tmux, zellij, ghostty, alacritty, starship, vscode, uv, herdr,
                                # secrets (sops-nix), agents (piste.agents), checkout, github
  roles/                        # base, dev, desktop; container for devcontainer images
  platforms/darwin.nix          # macOS: system settings (lib.mkDefault), homebrew, home-manager
  platforms/nixos.nix           # NixOS: user, sudo, nix settings, sshd, nix-ld
  darwin/                       # macOS extras: homebrew-base
  extras/                       # the extras role: opt-in taste (casks, macos, shell, ...)
  shared/                       # nixpkgs config + auto-loaded pkgs/overlays/
pkgs/overlays/NN-name.nix       # nixpkgs overlays, auto-loaded; also flake.overlays.shared
templates/default/              # the starter overlay (piste init; flake.templates.default)
lib/
  mk-flake.nix                  # lib.mkFlake: an overlay's whole outputs from one call
  zone-checks.nix               # lib.zoneChecks: an overlay's trust-zone checks
  agents.nix                    # the piste.agents resolver (presets -> links + blocks)
  agents-registry.nix, agent-bundle.nix
  devcontainer.nix              # lib.mkDevcontainer / validateAgentBundle (docs/devcontainer.md)
agents/                         # the shared agent zone (agents/README.md): skills, instructions,
                                #   presets (base, guard, rtk, subagents), hooks.toml + hooks/
.agents/skills/<name>/SKILL.md  # this project's skills (Codex reads them here)
.claude/skills/<name>           # links into .agents/skills (Claude Code)
.claude/settings.json           # permissions.deny: an agent here can't switch, update, push or sudo
.agents/agents.toml             # piste agents project manifest (zone piste: shared content only)
.githooks/pre-push              # runs piste leaks before a push (core.hooksPath .githooks)
cli/                            # piste, the Go CLI (cli/README.md)
  default.nix                   # buildGoModule (static) + the runtime-PATH wrapper
  cmd/piste, internal/          # cobra + fang; unknown commands run piste-<cmd> plugins
  cmd/changelog/                # cuts CHANGELOG.md (not shipped)
  e2e/                          # testscript harness and its goldens
tests/                          # agents.nix (resolver golden), agents/ (hook tests), fixtures/
docs/                           # the user docs (docs/commands.md is generated)
site/                           # mdBook config, SUMMARY.md and stage.pl for the docs site (checks.docs-site)
.github/workflows/              # piste.yml (Go, Nix, the published tree's leak scan),
                                #   agents.yml (skill lint, hook tests), releases, docs.yml (site redeploy), lock bumps
CHANGELOG.md                    # release notes; piste --version reads the newest
```

No config modules for claude-code, codex or gemini: the read-only Nix store blocks their
runtime config writes. Their binaries come from `packages-module.nix`;
`~/.config/...` is managed by hand or by `piste.agents`.

## Flake exports

The engine flake exports these for overlays (the input is named `piste`):

```nix
# Darwin modules
piste.darwinModules.platform       # Full macOS platform config
piste.darwinModules.homebrew-base  # Shared nix-homebrew tap config
piste.darwinModules.packages       # Package category options
piste.darwinModules.extras         # The extras role's Mac side (casks, macOS settings)

# NixOS modules
piste.nixosModules.platform        # The NixOS base
piste.nixosModules.packages        # Package category options
piste.nixosModules.sops            # sops-nix's own NixOS module, re-exported

# Home-manager modules (role hierarchy)
piste.homeManagerModules.base      # shell, git, ssh
piste.homeManagerModules.dev       # base + editor, tmux, secrets
piste.homeManagerModules.desktop   # dev + alacritty
piste.homeManagerModules.container # dev minus host-only bits (devcontainer images)
piste.homeManagerModules.extras    # opt-in taste, beside any role (roles = [ "dev" "extras" ])
piste.homeManagerModules.packages  # Package category options
piste.homeManagerModules.pnpm      # piste.packages.pnpm (mkFlake imports it)
piste.homeManagerModules.secrets   # sops-nix env var injection (opt-in)
piste.homeManagerModules.sops      # sops-nix's own home module, re-exported
piste.homeManagerModules.vscode    # VS Code config

# Overlays
piste.overlays.ai                  # claude-code, codex, gemini-cli
piste.overlays.shared              # composed: overlays.ai + every overlays/*.nix
piste.overlays.vscode              # nix-vscode-extensions

# An overlay's whole flake outputs from one call (lib/mk-flake.nix has the details)
piste.lib.mkFlake {
  inherit inputs self; user; zone;
  hosts = { <name> = { system; modules ? [ ]; overlays ? [ ]; roles ? [ ]; aliases ? [ ]; nixos ? false; }; };
  extraSpecialArgs ? (system: { }); homebrew ? { }; packages ? (system: { });
  fresh ? [ ]; agents ? null; bundle ? null; devcontainer ? null; checks ? (system: { });
  systems ? [ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ];
}
  # => { darwinConfigurations; homeConfigurations; nixosConfigurations; packages; apps; checks; }
  #    *-darwin hosts are nix-darwin systems (+ aliases), *-linux standalone home-manager,
  #      *-linux with nixos = true NixOS systems (key = hostname: no @, no dots);
  #    extraSpecialArgs, packages, checks, devcontainer.extraSpecialArgs: a function of
  #      the system, or one attrset for all;
  #    packages.<s> and checks.<s> for each of systems plus every host's system;
  #    checks.<s> = zoneChecks over every host's home + the overlay's own (no shared names);
  #    fresh = names from inputs.nixpkgs-fresh (lib/fresh.nix); throws if it's missing,
  #      lacks the name, or the engine's overlays have it;
  #    throws "mkFlake: …" on another host key or system, aliases on a *-linux host,
  #      nixos on a *-darwin host, a NixOS name with @ or a dot, a role not in
  #      lib/roles.nix, an alias that is a host's name or is given twice, an unknown
  #      devcontainer or bundle key.
  #    *-linux homes get the package set: homeManagerModules.packages, its resolved
  #      list in home.packages and overlays.shared ahead of the host's overlays
  #      (NixOS: nixosModules.platform's, in environment.systemPackages); a Mac,
  #      overlays.ai ahead of its overlays.
  #    Every host gets its wiring from mkFlake (its modules bring none of it):
  #      homeManagerModules.sops (sops-nix's), .agents and .pnpm in the home,
  #      piste.packages.piste = the engine's piste (mkDefault; null leaves it out; on a
  #        Mac or NixOS host a system option, so piste is in the system packages), and
  #        there home-manager.extraSpecialArgs.piste (else infinite recursion).
  #    extraSpecialArgs reaches darwin and NixOS system modules and *-linux home modules,
  #      not the home modules inside those (they get home-manager.extraSpecialArgs).

# An overlay's trust-zone checks (any zone name; docs/agents-and-mcp.md)
piste.lib.zoneChecks { inherit pkgs; zone; homes; withSource ? null; bundle ? null; registry ? null; }
  # => { zone-clean; zone-foreign-rejected; zone-unapproved-rejected; zone-bundle; zone-registry; }
  #    zone-foreign-rejected with withSource, zone-bundle / zone-registry with bundle / registry;
  #    zone-unapproved-rejected with withSource, when the zone's own policy is deny-unlisted
  #      and doesn't allow the fixture source (lib/fixtures/unapproved).

# Devcontainer OCI image from a zone's HM modules (docs/devcontainer.md)
piste.lib.mkDevcontainer { system; name; modules; agentBundle ? null; ... }
  # => { image; load; home; }  — zones expose packages.<linux>.devcontainer{,-load}
```

An overlay pins the engine by release:

```nix
inputs.piste.url = "github:elocke/piste/v0.1.0";   # piste update moves it
```

`piste build`, `check`, `dry`, `fix`, `gendiff` and `switch` build against the checkout, not the pin. By hand,
from `overlays/<name>/`:

```bash
nix build --override-input piste git+file://$PWD/../.. .#darwinConfigurations.<host>.system
```

A standalone home-manager host wired by hand (not with `mkFlake`) must apply
`overlays.shared` so the dev package category resolves (twice is harmless):

```nix
pkgs = import nixpkgs {
  system = "x86_64-linux";
  overlays = [ piste.overlays.shared ];
};
```

## Where to add things

For a person's own machines, the overlay (`piste-config` has the details):

| Want to add...                  | Edit, in the overlay                                   |
|---------------------------------|--------------------------------------------------------|
| CLI tool (Nix package)          | `home.packages` in `home.nix`, or `piste.packages.extraPackages` in a Mac's or NixOS host's file |
| GUI app (Homebrew cask)         | `piste.packages.casks.extra` in the Mac's host file    |
| CLI tool (Homebrew)             | `piste.packages.brews.extra` in the Mac's host file    |
| Shell alias, git or SSH setting | `programs.zsh.shellAliases`, `programs.git.settings`, `programs.ssh.settings` in `home.nix` |
| macOS system setting            | `system.defaults.*` in the Mac's host file             |

In the engine, for something every piste user should get (a tool only one person wants
goes in their overlay):

| Want to add...              | Edit this file                              |
|-----------------------------|---------------------------------------------|
| CLI tool (Nix package)      | `modules/packages-module.nix` → cliPkgs/devPkgs |
| CLI tool (Homebrew)         | `modules/packages-module.nix` → allBrews    |
| GUI app (Homebrew cask)     | `modules/packages-module.nix` → allCasks, if the engine configures it; else a person's `casks.extra`, or `modules/extras/casks.nix` for the extras pack |
| Binary from GitHub releases | `pkgs/overlays/NN-name.nix` + `packages-module.nix` |
| Shell aliases               | `modules/programs/shell.nix` (taste: `modules/extras/shell.nix`) |
| Git config                  | `modules/programs/git.nix`                  |
| SSH hosts                   | `modules/programs/ssh.nix`                  |
| Vim config                  | `modules/programs/editor.nix`               |
| macOS system settings       | `modules/platforms/darwin.nix`              |
| NixOS system settings       | `modules/platforms/nixos.nix`               |
| Devcontainer base           | `modules/roles/container.nix`, `lib/devcontainer.nix` |

## Role hierarchy

```
base.nix    → shell, git, ssh
    ↓
dev.nix     → base + editor, tmux, secrets
    ↓
desktop.nix → dev + alacritty

extras (modules/extras/) is beside them, not on top: opt-in, a toggle per part.
```

## Overlay packages (GitHub release binaries)

For tools that ship pre-built binaries, use an overlay, not nixpkgs:

1. Create `pkgs/overlays/NN-name.nix` (and `git add -N` it: builds see tracked files only)
   with `# @overlay-update github:owner/repo` metadata.
2. Add the package to `modules/packages-module.nix`.
3. Run `piste update-overlays` to check for new versions and update hashes
   (`--dry-run` to only check): a hidden maintainer command (not in
   `piste --help`, not promised) that edits the engine's checkout.

Claude Code, Codex and Gemini CLI come from the `claude-code-nix`, `codex-cli-nix` and
`gemini-cli-nix` flake inputs (`nix flake update`), exported as `piste.overlays.ai`.

## Key patterns

```nix
# packages-module.nix (inside the let bindings)
allCasks = [ "ghostty" ];                  # Homebrew GUI apps (the engine's own; more: extras/casks.nix)
allBrews = [ "ffmpeg" ];                   # Homebrew CLI
cliPkgs = with pkgs; [ ripgrep fd ];       # Nix packages

# An SSH host (in the overlay's home; programs/ssh.nix owns ~/.ssh/config)
programs.ssh.settings."myserver" = {
  HostName = "192.0.2.50";
  User = "admin";
  IdentityFile = [ "~/.ssh/id_ed25519" ];
};
```

## Skills

This project's skills (`.agents/skills/`, linked from `.claude/skills/`):

- `piste-fix`: a failed build or switch. Read the error, find the cause in the overlay or
  the engine, fix it, and check with `PISTE_AI=none piste fix` until it builds.
- `piste-config`: add a package, find an option (`docs/options.md`), or find where
  something on the machine comes from and the line that removes it.

## Secrets (API keys)

sops-nix injects encrypted API keys as shell environment variables. The module is in the
engine; the secrets and their configuration live in an overlay:

1. The sops-nix HM module: `mkFlake` imports it on every host (by hand:
   `inputs.piste.homeManagerModules.sops`).
2. Create the encrypted file: `piste secrets edit secrets/env/api-keys.sops.yaml`.
3. Enable and configure:

```nix
piste.secrets = {
  enable = true;
  sopsFile = ../secrets/env/api-keys.sops.yaml;
  envVars = {
    ANTHROPIC_API_KEY = "anthropic/api_key";
    OPENROUTER_API_KEY = "openrouter/api_key";
  };
};
```

## MCP tools and the agent harness

`piste mcp` (MCP servers from the zone manifests) and `piste agents` (skills, instructions,
hooks, third-party pins, the registry and project `.agents/`): see
[`docs/agents-and-mcp.md`](docs/agents-and-mcp.md).

## Contributing

- **Tests.** `cd cli && go test ./...` runs the unit tests and the end-to-end harness
  (`cli/e2e`, testscript against goldens; `go test ./e2e/ -update` rewrites
  them, `@darwin` ones on a Mac). `nix build .#checks.<system>.piste` runs
  them again in the Nix sandbox (not `nix build .#piste`: doCheck = false). Also: `nix flake check --no-build --option lazy-trees true`, and the
  agent hook tests, `python3 -m unittest discover -s tests/agents`, and
  `nix build -L .#checks.x86_64-linux.nixos-vm`, which boots the template's NixOS host (CI
  only evaluates it). CI (`.github/workflows/piste.yml`) runs these on Linux, and on macOS
  builds the template and runs `checks.piste` (template-darwin).
- **Release notes.** Every user-visible change adds a line under `## Unreleased` in
  `CHANGELOG.md`, in the PR that makes it. A change an overlay must act on also gets a line
  under `### Upgrading`: breaking means an overlay that built on the last release fails to
  build, or a machine changes without its overlay asking (before 1.0, a minor release).
- **Releases.** Patch releases cut themselves after a `flake.lock` bump. A minor (or
  major) is a PR whose only change is `cd cli && go run ./cmd/changelog cut minor`;
  merging it lets `piste-release.yml` tag the release (it builds and smoke-tests, attaches no binaries).

## Nothing private

- No hostnames, private addresses, home paths, employer or personal names, account names or
  secrets in code, tests, fixtures, docs or commit messages.
- Fixtures use `192.0.2.0/24`, `/home/user`, `example.com` and `jane`. A secret-shaped
  fixture is built at run time or ends its line with `gitleaks:allow`.
- `piste leaks` checks this: CI runs it over `git archive HEAD`, and a maintainer's pre-push hook (`.githooks/pre-push`) adds their own
  marker lists (`overlays/*/leak-markers*.txt`).
- Nothing under `overlays/` is ever tracked here; `piste leaks` refuses such a path.
