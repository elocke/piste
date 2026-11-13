# Troubleshooting

- What does this error mean, and how do I fix it?
- Why does the first build take so long?
- Why can't my shell find `piste` after the switch?
- What do I do when a build or a switch fails?

Each heading below is something that goes wrong, with what you see and the
fix. To undo a switch, see [Backing out](backing-out.md).

## Nix says a feature is disabled (flakes are off)

You ran `nix run github:elocke/piste/stable -- setup`, and Nix stopped before
setup started:

```
error: experimental Nix feature 'nix-command' is disabled; add '--extra-experimental-features nix-command' to enable it
```

or the same about `'flakes'`. Or setup started, and its checklist says:

```
✗ flakes: add `experimental-features = nix-command flakes` to ~/.config/nix/nix.conf (Determinate Nix has them on)
```

piste needs Nix's [flakes](https://nix.dev/concepts/flakes) on. Determinate
Nix has them on; a Nix from another installer may not. Turn them on, then
run the same command again:

```bash
mkdir -p ~/.config/nix && echo 'experimental-features = nix-command flakes' >> ~/.config/nix/nix.conf
```

On NixOS, don't edit a file: start setup with them on, as
[Getting started on NixOS](start-nixos.md) shows. Your first switch turns
them on for good.

## The first build takes a long time

piste says `Building...` (or `Building and switching...`), and Nix's progress
runs under it for many minutes. The first build fetches every tool your
machine gets, a few GB, and builds what it can't fetch. Later builds fetch
only what changed.

Let it run. If it stops part of the way (a dropped connection, a closed
laptop), run the same command again: Nix keeps what it has fetched already.
Old versions pile up over time; `piste clean` removes the ones you no longer
need.

## A file of yours is in the way

piste puts its own `~/.zshrc` and other settings files in your home. When a
file of yours is already there, it's moved aside, not deleted.
Before the first switch, setup lists them:

```
The switch moves 1 file of yours aside, to put its own in their place:
  ~/.zshrc → ~/.zshrc.backup-<time>
```

During the switch, on Linux, home-manager says, for each one:

```
Existing file '/home/jane/.zshrc' is in the way of '/nix/store/…-home-manager-files/.zshrc', will be moved to '/home/jane/.zshrc.backup-<time>'
```

and on a Mac or NixOS, `piste: moved /Users/jane/.zshrc aside to /Users/jane/.zshrc.backup-<time>`.
`<time>` is the time of the switch, in seconds. That's all: the switch goes
on. Your old file is beside the new one. To keep something from it, such as
an alias, put it in your overlay
([Change a setting by name](make-it-yours.md#change-a-setting-by-name)).

A **link** of yours (a dotfiles manager, such as stow, makes these) is
different: the switch won't move it. Setup warns:

```
~/.zshrc is a link of yours: the switch stops on it; move it away first
```

and the switch stops with home-manager's
`Existing file '/home/jane/.zshrc' would be clobbered`. Move the link away
(`mv ~/.zshrc ~/.zshrc.mine`), then switch again.

## The shell can't find `piste`

The switch is done, but your shell says there's no `piste` command. Setup's
last lines said why:

```
Switched. Open a new shell (piste is installed now), then:
```

(on a Mac, `Switched. Open Ghostty (piste is installed now), then:`). The
shell you ran setup in started before piste was installed, so it doesn't
know about it. Open a new terminal, or a new SSH session, and run
`piste --version`.

Before the first switch, piste isn't installed at all. Run it from the
checkout through Nix instead: `cd ~/piste && nix run .#piste -- switch`. On a
fresh NixOS, whose flakes are off until that switch, start it with
`NIX_CONFIG='extra-experimental-features = nix-command flakes'`, as the
commands setup prints there do.

## New terminals still open bash (Linux)

On Linux, piste sets up zsh but leaves your login shell alone: changing it
needs your password. At the end, setup said:

```
Your login shell is still bash, and piste sets up zsh. To make zsh your login shell:
  echo ~/.nix-profile/bin/zsh | sudo tee -a /etc/shells && chsh -s ~/.nix-profile/bin/zsh
```

Run that line as setup printed it (setup prints another path when your Nix
keeps its profile elsewhere). The first half adds piste's zsh to the shells
the system allows; the second makes it yours. Each asks for your password.
The change takes effect the next time you log in: log out and back in, or
open a new SSH session. Then `echo $SHELL` shows piste's zsh. A NixOS host's
switch sets zsh as your login shell itself.

## A build or a switch fails

A failed build ends:

```
Build failed (exit code 1)
Rerun with: piste --verbose build
To have Claude Code fix it: piste fix
```

and a failed switch:

```
Switch failed (exit code 1)
To rollback: piste rollback
To debug:    piste --verbose switch
To have Claude Code fix it: piste fix
```

Nix's own error is above these lines. Its last `error:` line usually names the
problem, and the file:

```
error: The option `piste.pakages' does not exist. Definition values:
- In `/nix/store/…-source/hosts/devbox.nix':
```

Here, a typo in `hosts/devbox.nix`. Two causes are common after an edit:

- **A misspelt name.** Nix often adds a `Did you mean …?` line. piste's own
  options are in [options.md](options.md).
- **A new file git doesn't know yet.** A build sees only the files git
  tracks. Determinate Nix says
  `error: Path 'secrets/env/api-keys.sops.yaml' in the repository "/home/jane/piste/overlays/personal" is not tracked by Git.`,
  and gives the `git -C … add` command that fixes it: run it.

For anything else, `piste fix` rebuilds what the switch builds, without
activating it, and keeps the output in `~/.local/state/piste/fix.log`. Then
it starts Claude Code (else Codex) at the checkout, with the error and the
`piste-fix` skill. The agent finds the line, edits under its CLI's own
approval and checks its fix with `piste fix`; you switch when it's done.
`PISTE_AI=codex` picks Codex. `PISTE_AI=none piste fix` rebuilds and reports
only, with no AI. With neither installed, piste fix says
`piste fix hands this to Claude Code or Codex, and neither is on PATH`, and
names the skill's file, `~/piste/.agents/skills/piste-fix/SKILL.md`, whose
table of common errors is readable on its own.

A switch can also fail after it has built, while it applies the change. Then
`piste rollback` puts back what you had before
([Backing out](backing-out.md)). A switch that leaves a new shell unable to
start or to find piste undoes itself
([When a switch undoes itself](backing-out.md#when-a-switch-undoes-itself)).

## The hostname doesn't match any host

```
Hostname 'jane-mbp' doesn't match any darwinConfigurations.
  Declared: jane-mac

  To build another host's config without activating it:
      piste build --host <name>
  To force activation on this hostname (dangerous — only if you
  really mean to activate a different host's config here):
      piste switch --host <name>
```

piste finds this machine's settings in your overlay by its hostname: a Mac
by its name (`scutil --get LocalHostName`), a Linux home as
`jane@<hostname>`, a NixOS host by its hostname. On NixOS the message says
`nixosConfigurations`, and adds `  Add this machine: piste host add`. At the
checkout, with more than one overlay, it reads
`no overlay in /home/jane/piste/overlays has this host (devbox); pass --overlay <name>`.

- **A new machine,** not in the overlay yet: add it with `piste host add`
  ([Add a second machine](make-it-yours.md#add-a-second-machine)).
- **The machine was renamed:** on a Mac, add its new name to the host's
  `aliases` in `flake.nix` (`aliases = [ "jane-mbp" ];`, other names for the
  same Mac), or give the machine its old name back.
- **A Linux machine** with no `jane@<hostname>` home uses your only home in
  the overlay. With more than one, piste can't tell which is this machine's,
  and the build fails: add the machine, or name its home with
  `--host <hostname>`.

`piste switch --host <name>` puts that host's settings on this machine: use it
only when this machine is that host under another name.

## The NixOS switch refuses: it would lock you out

Before it changes anything, a NixOS switch checks that the new system still
lets you in, and refuses one that doesn't:

```
piste switch: refusing, the new configuration would lock you out:
  you're connected over SSH and the new config gives jane no SSH keys: add users.users.jane.openssh.authorizedKeys.keys to your host
Fix it, or run piste switch --force to switch anyway.
```

Each line names what's wrong and, usually, the setting that fixes it: here,
your SSH public keys in the host file, `hosts/<name>/default.nix`. Set it,
and switch again. The other checks are about sudo, the boot loader, and
whether the new system is this machine's at all;
[The switch](customising.md#the-switch) lists them all.

`--force` switches without the checks. Use it only when you know the line is
wrong for this machine, for example at its own console, where SSH doesn't
matter.

When the new system doesn't build, the check can't run, and says:

```
piste switch: can't evaluate the new system to check it: …
To switch without the check: piste switch --force
```

Fix the build first ([A build or a switch fails](#a-build-or-a-switch-fails)):
a system that doesn't evaluate won't switch with `--force` either.

## sudo asks for a password with nobody to type it

On a Mac and on NixOS, `piste switch` and `piste rollback` run sudo
(`sudo darwin-rebuild switch`, `sudo nixos-rebuild switch`), which asks for
your password. `piste switch --help` says it on NixOS: "the switch is sudo
nixos-rebuild switch, which needs a terminal for sudo's password". Started
without one, from a script, a service or an AI agent, sudo can't ask, and the
switch ends with `Switch failed (exit code …)`.

Run `piste switch` yourself, in a terminal. Over SSH, log in first and run it
there, or give `ssh -t` for a single command:
`ssh -t devbox 'cd ~/piste && piste switch'`. An agent started at the checkout
doesn't switch anyway: it asks you to. On Linux, a home's switch needs no
sudo.
