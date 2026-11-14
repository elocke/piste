# piste, for an AI agent

- What do I, an AI coding agent, need to help a person set up a machine with piste?
- Which commands are mine to run, and which are the person's?
- Where does a change go, how is it checked, and what do I do when it breaks?

This page is for you, the agent (Claude Code, Codex, …), not for the person.
Follow it step by step with the person; link them to the human pages (listed
last) when they want the long version. Every command here is from those pages.

## Rules (read first)

- **The person runs anything that changes the machine.** You explain and
  propose; they type it, or approve it when you run it. That is: `sudo`, the
  Nix installer, `nix run github:elocke/piste/stable -- setup`, `piste switch`,
  `piste rollback`, `piste update`, `piste pkgs update`, `piste adopt`,
  `piste host add`, `piste secrets`, `piste github`, `piste clean`, `chsh`,
  `brew install`, `npm install -g`, `git push`.
- **Yours to run freely** (they only read or build): `piste build`,
  `piste check`, `PISTE_AI=none piste fix`, `piste pkgs`, `piste drift`,
  `piste status`, `piste where`, `piste log`, `nix search nixpkgs <name>`,
  `git diff` / `git status` in the overlay.
- **Never put a secret's value in a `.nix` file**, nor on a command line or in
  a placeholder you write: the Nix store is readable by every user. Secrets go
  through `piste secrets edit <file>`, which the person runs.
- **Edit the overlay, not the engine.** The engine is the checkout (`~/piste`);
  the person's settings are in `~/piste/overlays/<name>/`, their own git repo.
- **`git add -N` every new file before a build.** Builds see git-tracked files
  only; an untracked file is invisible (Nix says `is not tracked by Git`).
- **Setup is interactive** (forms at a terminal). Have the person run it in
  their own terminal and read you its output; don't drive it yourself.
- **Never `piste switch --host <name>`** for a machine you're not on.

## What piste is

- **The engine:** this repo, `github.com/elocke/piste`. Nix modules (shell,
  git, editor, packages, agents, secrets), the `piste` CLI and `lib.mkFlake`.
  Setup clones it to `~/piste` (the *checkout*), at the newest release tag.
- **The overlay:** the person's own small git repo (a flake), cloned into
  `<checkout>/overlays/<name>/`. Names their user, their machines (*hosts*),
  their zone, packages, settings, secrets. Not a nixpkgs overlay.
- **Team layer** (preview): a team's shared flake; `setup <layer url>` starts
  the person's own overlay on it.
- **Roles:** `base` → `dev` → `desktop` stack; `extras` is opt-in taste beside
  them. A Mac always gets `desktop`.
- **Platforms:** Mac (Apple silicon only) = nix-darwin, whole system.
  Linux = standalone home-manager, the home only, no sudo for a switch.
  NixOS = a host with `nixos = true`, whole system (experimental).

## Setup

### Prerequisites

Mac (Apple silicon; setup stops on Intel), with an admin account:

```bash
xcode-select --install
curl -fsSL https://install.determinate.systems/nix | sh -s -- install
```

Linux (any distribution, x86_64 or ARM), with sudo:

```bash
curl -fsSL https://install.determinate.systems/nix | sh -s -- install
```

A Nix from another installer works, but unless it's Determinate Nix, flakes
must be on first (troubleshooting page, "Nix says a feature is disabled").
Setup needs git: on Ubuntu or Debian, `sudo apt install git`.

NixOS: Nix is there; **don't install another**. Flakes are off and git is
missing on a fresh install, so the setup command is the long one below. If they
log in over SSH, their key must be in `~/.ssh/authorized_keys` (the switch
turns SSH password logins off). Try it on a machine they can reinstall.

After the installer: a new terminal, then `nix --version`.

### Private overlay or layer repo

Before setup, log GitHub's CLI in (HTTPS; a new machine has no SSH key):

```bash
nix run nixpkgs#gh -- auth login --web --git-protocol https
```

Use the repo's https URL (`https://github.com/jane/personal.git`).

### Run setup

```bash
nix run github:elocke/piste/stable -- setup              # a new overlay
nix run github:elocke/piste/stable -- setup <git url>    # their overlay, or a team layer
```

NixOS:

```bash
nix --extra-experimental-features 'nix-command flakes' shell nixpkgs#git --command nix --extra-experimental-features 'nix-command flakes' run github:elocke/piste/stable -- setup [<git url>]
```

`stable` follows the newest release. `--dir <path>` puts the checkout
elsewhere than `~/piste`.

What it does, in order:

1. Checks prerequisites (✓/✗ rows). A ✗ says how to fix it and stops: fix,
   then run the same command again. Re-running resumes.
2. Clones the checkout to `~/piste`.
3. Asks (new overlay): username, kind of machine, hostname (on NixOS still
   called `nixos`: a new name, letters, digits, dashes), roles (Linux/NixOS;
   Development suits most), agent zone (`personal`, or `work`), overlay name,
   git name and email, encrypted secrets (sops) now or not, minimal or
   everything (the extras pack), Ready. With a URL, it clones it; if the
   overlay has no host for this machine, it asks "Add this machine to it?".
4. Builds. **The first build downloads a few GB and takes many minutes**;
   interrupted, the same command resumes.
5. Shows what the switch changes (files of theirs moved aside as
   `.backup-<time>`; nothing deleted; on a Mac, Homebrew apps and macOS
   settings with sudo) and asks "Switch this machine to it now?". It waits for
   their yes. "Later" prints the command to run when ready.
6. After the switch: `piste agents sync --user` and `piste mcp sync`, then
   printed next steps.

On NixOS, before switching, have them read the "Needs you" lines (marked
`# piste:` in `hosts/<name>/machine/`) and check their SSH key is listed in
`hosts/<name>/default.nix`. The switch refuses one that would lock them out.

### After the first switch

- Mac: open Ghostty (piste is on PATH there). Linux: a new shell; to make zsh
  the login shell, the line setup printed:
  `echo ~/.nix-profile/bin/zsh | sudo tee -a /etc/shells && chsh -s ~/.nix-profile/bin/zsh`.
  NixOS: log in again (over SSH, open the new session before closing the old).
- Check: `cd ~/piste && piste --version`.
- Keep the overlay (setup's last lines; the person runs them):

  ```bash
  gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key && piste github
  piste switch
  git -C overlays/personal commit -m 'My overlay' && gh repo create personal --private --source overlays/personal --push
  ```

- If they chose secrets: the age key `~/.config/sops/age/keys.txt` is the one
  thing the overlay doesn't hold. Tell them to copy it to a password manager.
- A second machine with secrets can't read them until a machine that can runs
  `piste secrets updatekeys`; setup prints the three steps.

## Changing the machine

Once in a checkout, **read `~/piste/AGENTS.md`** and the overlay's
`CLAUDE.md`, and use the checkout's skills: `piste-config` (where a change
goes, finding an option, where something comes from) and `piste-fix` (a
failed build). They take over from this page. `piste claude` starts Claude
Code at the checkout with the overlays added; for Codex, run `codex` in
`~/piste`.

Where things go, in the overlay:

| Want to add...                  | Edit, in the overlay                                   |
|---------------------------------|--------------------------------------------------------|
| CLI tool (Nix package)          | `home.packages` in `home.nix`, or `piste.packages.extraPackages` in a Mac's or NixOS host's file |
| GUI app (Homebrew cask)         | `piste.packages.casks.extra` in the Mac's host file    |
| CLI tool (Homebrew)             | `piste.packages.brews.extra` in the Mac's host file    |
| Shell alias, git or SSH setting | `programs.zsh.shellAliases`, `programs.git.settings`, `programs.ssh.settings` in `home.nix` |
| macOS system setting            | `system.defaults.*` in the Mac's host file             |
| Drop a package from piste's set | `piste.packages.exclude` in the host file              |

- `home.nix` is every machine; `hosts/<host>.nix` (NixOS:
  `hosts/<host>/default.nix`) is one machine.
- Add to the list that's there; a second `home.packages` line is an eval
  error.
- Find a package: `nix search nixpkgs <name>`.

The loop:

1. Edit; `git add -N` any new file.
2. Check: `piste build` (or `PISTE_AI=none piste fix`, which rebuilds and
   reports with no AI).
3. Show the diff (`git -C ~/piste/overlays/<name> diff`); the person runs
   `piste switch` and commits.

Up to date: `piste pkgs` (read-only: what's behind, who updates it);
`piste pkgs update` (the person's: moves the release and overlay inputs,
builds, asks before switching). Installed outside Nix: `piste drift` lists it
(read-only); `piste adopt` writes it into the overlay and offers a switch (the
person's; not on NixOS).

## When it breaks

1. Read Nix's error above `Build failed` / `Switch failed`: its last `error:`
   line names the problem and the file. Common: a misspelt option (look for
   `Did you mean`), or a new file git doesn't track (`git add -N` it).
2. `PISTE_AI=none piste fix`: rebuilds what the switch builds, without
   activating, output in `~/.local/state/piste/fix.log`. Follow the
   `piste-fix` skill.
3. Hostname doesn't match a host: a new machine needs `piste host add` (the
   person's); to check another host, `piste build --host <name>`.
4. A switch that went wrong: `piste rollback` (the person's). A switch whose
   new shell can't start or find piste undoes itself.
5. Going back further, or removing piste: the backing-out page.

## The human pages

- Start: [Mac](https://elocke.github.io/piste/start-mac.html),
  [Linux](https://elocke.github.io/piste/start-linux.html),
  [NixOS](https://elocke.github.io/piste/start-nixos.html)
- [Make it yours](https://elocke.github.io/piste/make-it-yours.html): first
  changes, a second machine, secrets
- [Troubleshooting](https://elocke.github.io/piste/troubleshooting.html)
- [Backing out](https://elocke.github.io/piste/backing-out.html): undo a
  switch, remove piste
- [Concepts](https://elocke.github.io/piste/concepts.html),
  [Customising](https://elocke.github.io/piste/customising.html),
  [Upgrading](https://elocke.github.io/piste/upgrading.html)
- [Commands](https://elocke.github.io/piste/commands.html),
  [Options](https://elocke.github.io/piste/options.html)
