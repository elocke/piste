# Changelog

What each piste release changed, newest first. `piste update` shows you the
notes between your release and the newest one. What a release promises to keep
working, and what it doesn't, is in
[docs/upgrading.md](docs/upgrading.md#what-a-release-promises).

Every pull request that changes what a person sees adds one line under
`## Unreleased`. A breaking change also adds a line under `### Upgrading`,
saying what an overlay must change. Breaking means that an overlay which built
on the last release fails to build, or that a machine changes without its
overlay asking for it. Before 1.0, a breaking change is a minor release.

Releases are cut from this file. A `flake.lock` update becomes a patch release
by itself, and a patch carries any change merged without an Upgrading note. A
minor or a major release is a pull request whose only change is
`go run ./cmd/changelog cut minor` (or `major`), run from `cli/`, and whose
branch is up to date with `main` when it merges: a cut that brings other
changes in with it isn't released.

## Unreleased
- On a Mac with `homebrew.onActivation.cleanup` set, `piste.homebrew.trustedFormulae`, `trustedCasks` and `trustedTaps` now survive the switch: `brew bundle`'s cleanup rebuilds `~/.homebrew/trust.json` from the Brewfile alone, and the activation now puts the declared entries back after it. While the cleanup runs, only the Brewfile's own trust holds, so a third-party formula it may load (such as a tapped dependency) belongs in `homebrew.brews` by its full name.

## v0.1.0 (2026-11-13)
The first public release. piste sets up a Mac, a Linux machine or a NixOS machine from one small repo of your own, and its `piste` command keeps every machine matching it. Start with the [README](https://github.com/elocke/piste#readme); the docs are also a site, https://elocke.github.io/piste/, which redeploys with each release. piste is provided as is, with no support.

### Getting started
- `nix run github:elocke/piste/stable -- setup` on a Mac or a Linux machine (a fresh NixOS has flakes off: [Getting started on NixOS](https://github.com/elocke/piste/blob/main/docs/start-nixos.md) has the command for it). `stable` is a branch that follows the newest release. `piste setup` checks what piste needs (Nix with flakes, git, and on a Mac the Xcode command-line tools), clones piste at its newest release, creates your overlay or adds one you already have, builds this machine, says what the switch will change, and offers the first switch.
- A guide for each platform (a Mac, Linux, NixOS), recipes to make it yours (a package, a Mac app, a default dropped, the extras pack, a second machine, a secret, any setting), troubleshooting and backing out, and generated references for every command and every option.
- Releases attach no binaries: the source tag is the release.

### Your overlay and your machines
- Your own setup (your user, your machines, your packages, your secrets) lives in an overlay: a small git repo of your own, inside the piste checkout. Its `flake.nix` is one call to `piste.lib.mkFlake`, which wires every host.
- Three kinds of machine. A Mac (Apple silicon) is a nix-darwin system, with Homebrew managed by nix-homebrew. A Linux machine is a standalone home-manager configuration: your user's tools and settings, and piste leaves the system alone. A NixOS machine (`nixos = true`, experimental) is the whole system with home-manager inside it; `piste host add` carries the machine's `/etc/nixos` into the overlay.
- `piste host add` (and `piste setup` on a second machine) adds this machine to the overlay with the roles of the overlay's other hosts of its kind, so `extras` carries over: it prints `Roles from overlays/<name>'s other Linux host (<host>): dev, extras; --role sets others`. Hosts that differ give the roles they all have; with no other host of its kind, the default roles. `--role` and `--extras` choose others.
- `mkFlake` checks what it's given and stops with one line per mistake, each starting `mkFlake:`. Among them: a host key, system or role it doesn't know (a role is `base`, `dev`, `desktop`, `container` or `extras`); aliases on a host that isn't a Mac, and an alias that is a host's name or is given twice; `nixos` on a Mac, or not `true` or `false`; a NixOS host named with `@` or a dot; a key `devcontainer` or `bundle` doesn't take; a `fresh` name it can't take; and a check of the overlay's named like one of the zone checks.

### What the engine sets up
- Lean by default: zsh with a starship prompt, git with delta, vim, tmux, gh, and a package set of command-line and dev tools on every kind of machine. On a Mac, also Ghostty (the only Homebrew cask), Alacritty, zellij, fonts, Touch ID for sudo and a few macOS settings; on Linux the `desktop` role adds zellij's and Ghostty's settings, and no terminal app. A NixOS machine gets a headless base: your user, sudo, the Nix settings piste needs, key-only OpenSSH and nix-ld.
- Claude Code, Codex and Gemini CLI come from their own flakes, the same versions on every kind of machine, and move with piste releases.
- `yq` is yq-go (mikefarah's yq), the `yq` piste itself uses: it takes yq-go's expressions, not jq's.
- The extras pack is the rest, opt-in: the `extras` role adds more aliases, tmux and Ghostty settings, git's editor and ignores, and on a Mac more apps (Firefox, Chrome, VS Code, Docker Desktop, Raycast and others) and a few input settings. `piste init` asks "Start minimal or with everything?" (`--extras` in a script). One toggle drops a part: `piste.roles.extras.<part>.enable = false;`. Nothing in it throws work away or writes the screen to disk: no `git nuke` or `git discard`, no `fetch.pruneTags`, and tmux's resurrect saves the layout without the panes' text (`piste.roles.extras.tmux.resurrectExtraConfig` turns that on).

### Making it yours
- Every default can be turned off: a role's toggle (`piste.roles.<role>.<toggle>.enable = false;`) turns its program off, a package set category has its own switch, and `piste.packages.exclude` drops one package. The macOS settings and the NixOS base's are defaults your plain value wins over. [docs/customising.md](https://github.com/elocke/piste/blob/main/docs/customising.md) shows how, and its "Who wins" table gives the priorities.
- Every engine piece has a module key, `pack:core/<piece>` (shell, git, ssh, starship, herdr, editor, tmux, uv, zellij, alacritty, ghostty, vscode) or `pack:extras/<piece>` (shell, editor, tmux, ghostty, casks, macos), and the keys are promised; file paths under `modules/` aren't. To take a piece over, turn it off with `disabledModules = [ { key = "pack:core/starship"; } ];` (the attribute-set form: a bare string matches nothing) and import your own copy with its key changed to `local:core/starship`. [docs/customising.md](https://github.com/elocke/piste/blob/main/docs/customising.md#take-over-a-piece) has the recipe and what a copy must keep.
- `piste.lib.mkPackDefault` (`lib.mkOverride 950`): a team layer's (later a pack's) default, which wins over the engine's `mkDefault` and loses to your plain value.
- `fresh` in `mkFlake`: the packages you name come from your overlay's own `nixpkgs-fresh` input, newer than the engine's tested nixpkgs; everything else stays on the engine's.
- `piste.packages.pnpm`: pnpm globals the engine installs, and `piste pkgs update` upgrades. Every `mkFlake` host has the option, and so does a `lib.mkDevcontainer` image.

### Commands
- `setup`, `init`, `add` and `host add`, `build`, `check`, `dry`, `gendiff`, `switch` and `rollback`, `update`, `where`, `status`, `log` and `clean`. `piste switch` checks that a new login shell starts and runs piste, and puts the previous generation back when it can't. A file of yours in the way of a link is moved aside as `<file>.backup-<epoch>`. After a switch, one line says when a newer release is out (one check a day at most).
- On NixOS, `piste switch` evaluates the new system first and refuses a config that would lock you out: SSH off or without your key while you're connected over SSH, sudo taken from you, no boot loader, or another machine's root filesystem. `--force` skips the checks.
- `piste update` moves your checkout and every overlay's engine pin to the newest release and shows each release's notes on the way. A note under Upgrading is a change you must act on.
- `piste pkgs` shows what is out of date on this machine, grouped by who updates it (your overlay, the engine, a team layer), and changes nothing. `piste pkgs update` takes the newest engine release and your overlay's inputs, builds with the version diff, offers the switch, then upgrades your pnpm globals and Homebrew apps. Both match a Homebrew app declared by an alias or an old name (`python` for `python@3.13`).
- `piste adopt` and `piste drift` find what you installed outside Nix (Homebrew, npm and pnpm globals, cargo, pip) and show where each would go; `adopt` writes the ones you tick into your overlay.
- `piste fix` rebuilds after a failed build or switch, keeps the error, and starts Claude Code (else Codex) at the checkout with it. `PISTE_AI=none` turns that off. The checkout's skills `piste-fix` and `piste-config`, and `piste claude`, help an agent change your overlay. A committed `.claude/settings.json` stops an agent started at the checkout from switching, updating or pushing.
- Plugins: `piste foo` runs `piste-foo` from your PATH when piste has no command `foo`.

### Secrets, GitHub and leaks
- Secrets with sops and age: encrypted in your overlay, decrypted on the machine when it switches, exported to your shell. `piste secrets` makes the key and edits the files; `piste secrets updatekeys` re-encrypts them for a new machine.
- `piste github` gives this machine its own GitHub SSH key, for pushing and for signing commits, uploaded with `gh`. Commits are signed by default only when there is a key to sign with. It finds this machine's host file through the overlay's hosts (a host named differently from the hostname works).
- `piste leaks` checks the commits a push would add, or an exported tree, for your leak-marker words, private (RFC1918) addresses, your home directory and secrets (via gitleaks, which ships with piste). The `.githooks/pre-push` hook runs it and blocks the push on a hit once a clone turns it on (`git config core.hooksPath .githooks`) and has a marker list.

### Agents
- `piste.agents` deploys skills, instructions and hooks to Claude Code and Codex from the engine and from your overlay, and `piste agents` and `piste mcp` pin and reconcile third-party plugins, skills and MCP servers. The `base` preset is lean; `guard` (blocks destructive commands and commits that leak a secret) and `rtk` (the rtk token saver) are presets you choose. Content is kept apart by trust zone, and the build refuses agent content from another zone. A zone's policy can also refuse sources you haven't listed. [docs/agents-and-mcp.md](https://github.com/elocke/piste/blob/main/docs/agents-and-mcp.md) says what is and isn't checked.
- `piste agents check` accepts the Codex app's own plugins (its `openai-bundled` and `openai-primary-runtime` marketplaces: browser, computer-use, documents, pdf, spreadsheets and the rest) in every zone, and `piste agents sync --user --prune` leaves them.
- Container images built from the same modules: `piste.lib.mkDevcontainer`, with the zone's agent bundle.

### Team layers (preview)
- A team shares a flake of modules, agent content and the names of the secrets it expects, and `piste init --layer <url>` (or `piste setup <layer url>`) starts an overlay on it. A layer's scalars use `piste.lib.mkPackDefault`, so your plain value wins over the team's and the team's over the engine's defaults.
- The whole surface is a preview, outside what a release promises: `piste-layer.toml`, the `pisteLayer` flake output, `piste init --layer` and `piste setup <layer url>`. It may change in a minor release; layers are expected to become packs ([docs/design/packs.md](https://github.com/elocke/piste/blob/main/docs/design/packs.md), a design, not shipped).

### What a release promises
- The options, `mkFlake`'s arguments and host keys, the commands and flags, the `lib` functions the docs name, the piece keys and the files piste reads. [docs/upgrading.md](https://github.com/elocke/piste/blob/main/docs/upgrading.md#what-a-release-promises) lists it, and what isn't promised.
- Reserved names: option prefixes `piste.packs.*`, `piste.managed.*` and `piste.internal.*`, the flake outputs `pistePacks` and `pisteGroups`, check prefixes `pack-*`, `managed-*` and `group-*`, the commands and plugins `pack`, `eject`, `managed` and `group`, and the rest listed in [docs/upgrading.md](https://github.com/elocke/piste/blob/main/docs/upgrading.md#reserved-names). Don't use them in an overlay, a layer or a plugin.
- SECURITY.md says how to report a vulnerability privately. The licence is BSD-3-Clause.
