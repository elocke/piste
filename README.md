# piste

piste sets up your Macs and Linux machines from one Nix configuration, and the
`piste` command keeps them that way. It's an engine of Nix modules with
opinions about the tools a developer wants, plus a CLI that builds, switches,
updates and onboards. Your own setup (your user, your machines, your packages,
your secrets) lives in a small repo of your own, an overlay, which sits inside
a checkout of piste. (Here "overlay" means that repo. It isn't a nixpkgs
overlay.) On macOS it uses nix-darwin, on Linux standalone home-manager.
Apple silicon Macs only; NixOS machines aren't a target. On NixOS, piste can
manage your home with standalone home-manager (untested) and never touches the
system, but don't run it beside home-manager's NixOS module.

Customising is going off-piste: every default below can be turned off, and any
home-manager or nix-darwin setting can be set beside it.

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
  mise, and the Claude Code, Codex and Gemini CLIs. A Mac and a Linux host
  both get it ([customising.md](docs/customising.md) trims it).
- **On a Mac:** Ghostty, Alacritty and zellij, Nerd Fonts and Touch ID for
  sudo. The only Homebrew cask is Ghostty, managed by nix-homebrew. macOS
  settings: file extensions always shown, fast key repeat (`KeyRepeat` 2,
  `InitialKeyRepeat` 15), tap to click, no beep, and a Dock that hides until
  you point at it, with 48-point icons at the bottom and no recent apps. All
  of them are defaults: a plain value of yours wins. piste expects Determinate
  Nix, which manages Nix itself.
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
  global ignores. Nothing in it throws work away or saves what is on screen:
  there are no `git nuke` or `git discard` aliases, fetch keeps local tags,
  and tmux saves its layout without the panes' text. On a Mac it also adds apps and two input settings. The apps
  are Firefox, Chrome, VS Code, Docker Desktop, Cyberduck, Bruno, HTTPie,
  RapidAPI, cmux, Raycast, Ice, Karabiner-Elements and Syncthing, with
  `.md` and `.mdx` files opening in VS Code. The settings turn press-and-hold
  off and three-finger drag on.
- **To drop a part,** switch its toggle off, for example
  `piste.roles.extras.tmux.enable = false;`. To drop the whole pack, take
  `"extras"` out of the host's `roles`. [customising.md](docs/customising.md#the-extras-pack)
  lists every part and what it sets.

## Quickstart

You need Nix. The first switch downloads a few GB, so allow some time and
disk. On a Mac (Apple silicon), first the Xcode command-line tools,
`xcode-select --install`. Then, on a Mac or Linux, Determinate Nix:
`curl -fsSL https://install.determinate.systems/nix | sh -s -- install`. On
Linux, a multi-user (daemon) install is the one piste is tested on; a
single-user one (`--no-daemon`) works too. Then:

```bash
nix run github:elocke/piste/stable -- setup              # a new overlay
nix run github:elocke/piste/stable -- setup <git url>    # or one you already have
```

`stable` is the branch that follows the newest release, so what you run is a
release, never a half-finished `main`.

If your Nix doesn't have flakes on yet (Determinate Nix has them), add
`experimental-features = nix-command flakes` to `~/.config/nix/nix.conf` first:
setup's own checks need them on, not just for the `nix run`.

`setup` checks what piste needs (Nix with flakes and git, on macOS the Xcode
command-line tools) and says how to fix what's missing; clones
piste at its newest release into `~/piste` (`--dir` to choose); adds your
overlay, or creates one with `piste init`, which asks for your user, this
machine's name, its roles (on Linux), whether to start minimal or with
everything, your git identity and whether to keep secrets;
builds this machine; says what the switch will change (the files of yours it
moves aside, links of yours it would stop on, and on a Mac the Homebrew apps);
and offers the first switch. On a
Mac it looks like this:

```
✓ Nix
✓ flakes
✓ git
✓ Xcode command-line tools
✓ Determinate Nix
Cloned piste v0.1.0 into /Users/jane/piste
  … the questions, the build, what the switch changes, the switch …
Switched. Open Ghostty (piste is installed now), then:
  cd /Users/jane/piste
  piste switch    # after changing overlays/personal
  gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key && piste github    # a GitHub SSH key, once gh is here
  git -C overlays/personal commit -m 'My overlay' && gh repo create personal --private --source overlays/personal --push    # keep it, in a private repo
```

Open a new shell (on a Mac, Ghostty): that's where `piste` is on your PATH.
Push your overlay to a private repo: it's yours, and `piste setup <its https
url>` on your next machine starts from it. The age key setup made
(`~/.config/sops/age/keys.txt`, if you keep secrets) is the one thing not in
it: keep a copy in a password manager.

On Linux the switch doesn't change your login shell; setup prints the two
commands that make it zsh:

```bash
echo ~/.nix-profile/bin/zsh | sudo tee -a /etc/shells && chsh -s ~/.nix-profile/bin/zsh
```

At a terminal, piste's questions are forms: arrow keys to pick, Shift+Tab to go
back, and for `init` a summary with Create or Change something. In a pipe or CI,
or with `NO_COLOR`, `TERM=dumb` or `ACCESSIBLE` set, they're one-line prompts
where Enter takes the default in brackets.

Run `setup` again any time: inside the checkout, or from anywhere when the
checkout is `~/piste` (else pass the same `--dir`), it reuses the checkout and
the overlay that has this machine.

By hand, the same steps:

```bash
git clone --branch <newest vX.Y.Z tag> https://github.com/elocke/piste ~/piste && cd ~/piste   # without --branch: the default branch, not a release
nix run .#piste -- init           # or: nix run .#piste -- add <git url>
nix run .#piste -- switch         # afterwards (in a new shell) just `piste switch`
```

## Your first change

1. Open `~/piste/overlays/<name>/home.nix` and add a package to `home.packages`,
   for example `pkgs.ollama`.
2. `piste build` checks it. `piste switch` applies it.
3. Commit it in the overlay: `git -C ~/piste/overlays/<name> commit -am 'Add ollama'`.

[customising.md](docs/customising.md) covers a Mac app, a setting and a
second machine.

## With an AI agent

- `piste claude` starts Claude Code at the checkout, with your overlay in
  reach. It can read the engine's instructions and your overlay's.
- When a build or switch fails, `piste fix` rebuilds, keeps the error, and
  starts Claude Code (or Codex) with it. `PISTE_AI=none` turns that off.
- The checkout's skills, `piste-config` and `piste-fix`, teach an agent to add
  a package, look up an option and read a Nix error. The checkout's
  `.claude/settings.json` stops an agent started there from switching,
  updating or pushing: you run those.

## Where next

- [concepts.md](docs/concepts.md): the engine, your overlay, the checkout,
  hosts, roles, zones and layers, and what a build uses.
- [customising.md](docs/customising.md): add a package, drop a default, set
  any option, a second machine, plugins, secrets and agents.
- [upgrading.md](docs/upgrading.md): moving to a new release, and back.
- [CHANGELOG.md](CHANGELOG.md): what each release changed.
- [commands.md](docs/commands.md): every command and flag (generated from
  `piste --help`).
- [options.md](docs/options.md): every option the engine declares (generated
  from the modules).
- [agents-and-mcp.md](docs/agents-and-mcp.md): agent skills, instructions,
  hooks and MCP servers.
- [devcontainer.md](docs/devcontainer.md): a container image from the same
  modules.

## Provided as-is

piste is shared as it is, with no support: issues are off, and nobody will
answer questions. Everything you need should be in this repo; if it isn't,
fork it and make it yours.

## License and credit

BSD-3-Clause: see [LICENSE](LICENSE). piste began as a fork of
[dustinlyons/nixos-config](https://github.com/dustinlyons/nixos-config), whose
copyright notice LICENSE keeps. The Go dependencies' notices are in
`cli/THIRD_PARTY_NOTICES.md` (and `piste --licenses`).
