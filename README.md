# piste

piste sets up your Mac, your Linux machine or your NixOS machine from one small
repo of your own, and the `piste` command keeps every machine matching it. Say
which tools you want (a shell, git, an editor, your usual command-line
programs) and piste installs and configures them the same way on each machine.

It's for developers who want that without learning Nix. **You don't need to
know Nix:** pick your platform's guide below, copy a few commands, and answer
a few questions.

- **A Mac** (Apple silicon only): [Getting started on a Mac](docs/start-mac.md).
- **A Linux machine** (your user's tools and settings; piste leaves the system
  alone): [Getting started on Linux](docs/start-linux.md).
- **A NixOS machine** (piste sets up the whole machine, system and tools):
  [Getting started on NixOS](docs/start-nixos.md). NixOS support is new and
  experimental.

piste starts once the operating system is installed and you can log in.
Installing NixOS, servers, NixOS desktops and remote deploys aren't covered.

Your own setup (your user, your machines, your packages, your secrets) lives
in a small repo of your own, called your overlay, which sits inside a checkout
of piste. (Here "overlay" means that repo. It isn't a nixpkgs overlay.) Under
the hood piste is an engine of Nix modules with opinions about the tools a
developer wants, plus a CLI that builds, switches, updates and onboards;
[concepts.md](docs/concepts.md) explains the pieces.

Customising is going off-piste: every default below can be turned off, and any
home-manager, nix-darwin or NixOS setting can be set beside it
([customising.md](docs/customising.md)).

## Opinions

What a machine gets from the engine, unless your overlay says otherwise
([customising.md](docs/customising.md) shows how). This is the lean default.
The [extras pack](#the-extras-pack) adds more.

- **Everywhere:** zsh with a starship prompt, autosuggestions, syntax
  highlighting and direnv; git (set up for delta's diffs), vim, tmux and gh.
  The editor (`EDITOR` and `VISUAL`) is nano.
- **Shell aliases:** in a shell you type in (not one an agent started), `cat`
  is `bat`, `ls`, `ll`, `la`, `lt` and `tree` are `eza`, and `rm`, `cp` and
  `mv` ask first. For everyone: `g`, `gs`, `ga`, `gc`, `gp`, `gl`, `gd`,
  `gco`, `gb`, `glog` and `lg` for git, `..`, `...` and `....`, `path`,
  `now`, `week`, `ports`, `zshrc` and `nixconf`, plus a set of git and gh
  aliases (`git aliases` lists them) and zoxide's `z` and `zi`. Nothing
  swaps `grep`, `find`, `ps`, `du`, `df`, `diff`, `top` or `cd`.
- **A package set:** ripgrep, fd, bat, eza, delta, fzf, jq, yq, lazygit, btop,
  dust, zoxide, atuin and friends; just, uv, Node with pnpm, Docker, kubectl,
  mise, and the Claude Code, Codex and Gemini CLIs. A Mac, a Linux and a
  NixOS host all get it ([customising.md](docs/customising.md) trims it).
- **On a Mac:** Ghostty, Alacritty and zellij, Nerd Fonts and Touch ID for
  sudo. The only Homebrew cask is Ghostty, managed by nix-homebrew. macOS
  settings: file extensions always shown, fast key repeat (`KeyRepeat` 2,
  `InitialKeyRepeat` 15), tap to click, no beep, and a Dock that hides until
  you point at it, with 48-point icons at the bottom and no recent apps. All
  of them are defaults: a plain value of yours wins. piste expects Determinate
  Nix, which manages Nix itself.
- **On a NixOS host:** your user in `wheel` with zsh as its login shell, sudo,
  flakes on with your user trusted, OpenSSH with keys only, nix-ld (so
  prebuilt binaries from pip and uv load) and home-manager inside the system.
  The base is headless and sets no firewall rules and no service but OpenSSH.
  These are defaults too: a plain value of yours wins.
- **Secrets** with sops and age: encrypted in your overlay, decrypted on the
  machine when it switches, exported to your shell.
- **Agents:** skills, instructions and hooks for Claude Code and Codex, from
  the engine and from your overlay, kept apart by trust zone. The engine's
  `base` preset is lean: the never-publish-secrets rules and a note that this
  machine is set up by piste. Hooks that block destructive commands (`guard`)
  and the rtk token saver (`rtk`) are presets you opt into.
- **One overlay per trust zone** (say `personal` and `work`): a machine holds
  only its own zone's overlays. The zone limits the agent content a machine
  takes, checked when it builds. It isn't a security boundary for the rest.
- **The checkout is the version.** You sit on a release tag; `piste update`
  moves you to the next one and shows what changed.

## The extras pack

The engine's defaults are lean on purpose. The pack is the rest: more of one
developer's taste, off unless you ask. `piste init` asks "Start minimal or
with everything?". Minimal is the default.

- **Minimal** is everything under Opinions above, and nothing else.
- **Everything** adds the `extras` role to your machines: more aliases (`grep`
  is `rg`, `find` is `fd`, `ps`, `du`, `df`, `diff` and `top` swap too, `cd` is
  `z`, `cc`, `cca` and `ccr` start Claude Code), tmux with prefix `C-x`, saved
  sessions and split keys, Ghostty settings (clipboard reads allowed, tabs
  close without asking, Option as Alt), git's editor set to vim and its
  global ignores. Nothing in it throws work away or saves what is on screen.
  On a Mac it also adds apps and two input settings. The apps are Firefox,
  Chrome, VS Code, Docker Desktop, Cyberduck, Bruno, HTTPie, RapidAPI, cmux,
  Raycast, Ice, Karabiner-Elements and Syncthing, with `.md` and `.mdx` files
  opening in VS Code. The settings turn press-and-hold off and three-finger
  drag on.
- **To turn it on later, or drop a part of it:**
  [Turn on the extras pack](docs/make-it-yours.md#turn-on-the-extras-pack).
  [customising.md](docs/customising.md#the-extras-pack) lists every part and
  what it sets.

## Quickstart

You need Nix first: the guides below say how, and what's different on NixOS.
Then, on a Mac (Apple silicon) or Linux:

```bash
nix run github:elocke/piste/stable -- setup              # a new overlay
nix run github:elocke/piste/stable -- setup <git url>    # or one you already have
```

Step by step: [Mac](docs/start-mac.md) · [Linux](docs/start-linux.md) · [NixOS](docs/start-nixos.md)

## Where next

Docs: [https://elocke.github.io/piste/](https://elocke.github.io/piste/),
with search. The pages, by what you want to do:

- [make-it-yours.md](docs/make-it-yours.md): your first changes, step by
  step. [troubleshooting.md](docs/troubleshooting.md) when something fails,
  and [backing-out.md](docs/backing-out.md) to undo a switch or remove piste.
- [concepts.md](docs/concepts.md), [customising.md](docs/customising.md) and
  [upgrading.md](docs/upgrading.md): how piste works, the reference, and
  moving to a new release. [agents-and-mcp.md](docs/agents-and-mcp.md) and
  [devcontainer.md](docs/devcontainer.md): agents, and container images.
- [commands.md](docs/commands.md) and [options.md](docs/options.md): every
  command and every option, generated. [CHANGELOG.md](CHANGELOG.md): what
  each release changed.

## Provided as-is

piste is shared as it is, with no support: issues are off, and nobody will
answer questions. Everything you need should be in this repo; if it isn't,
fork it and make it yours.

## License and credit

BSD-3-Clause: see [LICENSE](LICENSE). piste began as a fork of
[dustinlyons/nixos-config](https://github.com/dustinlyons/nixos-config), whose
copyright notice LICENSE keeps. `cli/THIRD_PARTY_NOTICES.md` (and
`piste --licenses`) has the notice for the code the CLI ports from another
project (rulesync, MIT). The Go modules the CLI builds with (`cli/go.mod`) keep
their own licences, in their source.
