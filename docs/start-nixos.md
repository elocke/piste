# Getting started on NixOS (experimental)

- What do I need?
- How do I get my NixOS machine switched?
- What changed?

> **Experimental.** NixOS hosts, the machines piste sets up whole, are new and
> haven't had a real-machine release yet. Try them on a machine you can
> reinstall.

This guide takes a NixOS machine you've just installed to one piste runs. On
NixOS, piste sets up the whole machine, system and home: its users, sudo, SSH
and Nix's own settings, and your shell and tools. You don't need to know Nix:
you copy a few commands and answer a few questions. Each step says what you
see when it worked.

## What you need

- **NixOS, installed and booted,** and you can log in as your own user, with
  sudo. piste starts from there: installing NixOS is
  [the NixOS manual's](https://nixos.org/manual/nixos/stable/#sec-installation)
  job. piste's base is headless, for a machine you work on: servers, NixOS
  desktops and deploying from another machine aren't covered.
- **Your SSH key in `~/.ssh/authorized_keys`,** if you log in over SSH. The
  switch turns SSH password logins off, and lets in the keys it finds there.
- **A name for the machine.** A fresh install is called `nixos`, which names
  no machine in particular, so setup asks for another, such as `devbox`. It
  becomes the machine's hostname.
- **No other tool already managing your home** on this machine. If one does
  (home-manager's NixOS module), read
  [A NixOS machine](customising.md#a-nixos-machine) first.
- **Time and disk space.** The first switch downloads a few GB.
- **A GitHub account**, to keep your settings in at the end (step 6). Any
  private git service will do instead.

## 1. Nix is there already

NixOS is a Linux built on [Nix](https://nixos.org/), the package manager piste
uses: the whole system, from its users to its services, comes from a written
description. piste writes that description for you, starting from the one your
install made, in `/etc/nixos`. Don't install another Nix.

A fresh NixOS has flakes off, though. A
[flake](https://nix.dev/concepts/flakes) is how Nix pins a project, and
everything it uses, to exact versions, and piste needs them. So the commands
below start with `--extra-experimental-features 'nix-command flakes'`, which
turns them on for that one command. Setup then turns them on for its own
commands, and your first switch turns them on for good.

## 2. Run setup

Setup keeps your settings in **your overlay**: a small git repository of your
own, with your user, your machines and your packages in it
([The engine and your overlay](concepts.md#the-engine-and-your-overlay)).
piste is the same for everyone; your overlay is what makes the machine yours.
Setup makes a new one on your first machine, and starts from the one you have
on the next:

```bash
nix --extra-experimental-features 'nix-command flakes' shell nixpkgs#git --command nix --extra-experimental-features 'nix-command flakes' run github:elocke/piste/stable -- setup              # a new overlay
nix --extra-experimental-features 'nix-command flakes' shell nixpkgs#git --command nix --extra-experimental-features 'nix-command flakes' run github:elocke/piste/stable -- setup <git url>    # or one you already have
```

On your first machine, run the first line. It's long because a fresh NixOS
has flakes off and no git: the `--extra-experimental-features` settings turn
flakes on for this one command, and `shell nixpkgs#git` gives it a git for
this run, without installing anything. `nix run` then fetches piste and runs
it. `stable` is the branch that follows the newest release, so what you run
is a release, never a half-finished `main`.

Where git is already there, the shorter form works too:
`nix --extra-experimental-features 'nix-command flakes' run github:elocke/piste/stable -- setup`.
If you start with it and git is missing, setup says so and prints the long
command above as the fix.

Setup first checks what piste needs:

```
✓ Nix
· flakes: Flakes are off in this NixOS's Nix; setup turns them on for itself, and your first switch turns them on for good
✓ git
Cloned piste vX.Y.Z into /home/jane/piste
```

`~/piste` is now the checkout: a copy of piste at its newest release, where
your own settings live too ([The checkout](concepts.md#the-checkout)).
`setup --dir <path>` puts it somewhere else.

Then setup comes to your overlay, which lives in `~/piste/overlays/`. Each
machine in it is a *host*, with settings of its own; this one will be a NixOS
host, the whole machine, system and home.

If setup stops part of the way, fix what it says and run the same command
again (the long one with `nixpkgs#git`, until the first switch). It finds the
checkout in `~/piste` and the overlay that has this machine, and goes on from
there. A command setup prints for later works as printed too. Most start
with `cd ~/piste &&`; until the first switch, piste in them runs as
`NIX_CONFIG='extra-experimental-features = nix-command flakes' nix run .#piste --`,
which turns flakes on for it as setup did for itself, and git as
`nix --extra-experimental-features 'nix-command flakes' shell nixpkgs#git --command git`,
since your shell has no git yet.

## 3. Answer its questions

At a terminal the questions are forms: the arrow keys pick, Enter goes on and
Shift+Tab goes back. (In a pipe, or with `NO_COLOR`, `TERM=dumb` or
`ACCESSIBLE` set, each one is a line, and Enter takes the default in
brackets.) Run setup at a terminal: on a machine still called `nixos`, piste
needs one to ask for its name.

**Your overlay.** Choose "Create a new overlay" on your first machine. On the
next ones, choose "Add one I already have" and give its git URL. Use the https
one (`https://github.com/jane/personal.git`): this machine has no key for
GitHub yet. When you gave a URL on the command line, setup doesn't ask.

**An overlay you have** but without this machine: setup says so, and asks to
add it.

```
overlays/personal has no host for this machine (nixos).
```

Choose "Add it" at "Add this machine to it?". On a machine still called
`nixos`, it then asks "A name for this machine": type one, such as `devbox`,
in letters, digits and dashes. That name is the machine's host in your
overlay, and its hostname from now on.

**A new overlay** asks the questions of [Linux](start-linux.md#3-answer-its-questions),
with two that are different here:

- **What kind of machine:** "NixOS, x86_64" or "NixOS, ARM", chosen already:
  piste makes the whole system, from this machine's `/etc/nixos`. "Linux"
  instead sets up your home only, as on any Linux.
- **Hostname:** the machine's hostname from now on. On a machine still called
  `nixos` it's empty: type a name, such as `devbox`, in letters, digits and
  dashes.

The others are your username, the roles (Development suits most machines),
the agent zone (`personal`), the overlay's name, your git name and email,
whether to keep encrypted secrets, and whether to start minimal or with
everything. Then Create.

Either way, piste makes this machine a NixOS host of your overlay. Adding it
to an overlay you have, you'll see:

```
Added devbox (roles: dev) to overlays/personal: hosts/devbox/ and its entry in flake.nix, staged
This machine's /etc/nixos is in hosts/devbox/machine/
Needs you (marked "# piste:" in the files):
  configuration.nix:20: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
/etc/nixos stays on this machine; piste switch builds this overlay, and a plain nixos-rebuild switch doesn't.
1 SSH key from ~/.ssh/authorized_keys may log in as jane (hosts/devbox/default.nix)
```

For a new overlay, the first line is
`Created overlays/personal (host devbox, zone personal), its files staged`.
What the rest means:

- **`hosts/devbox/machine/`** is a copy of the machine's own configuration,
  from `/etc/nixos`: its disks, its boot loader, its network. It comes along
  unchanged.
- **Needs you** lists what piste marked with a `# piste:` comment in that
  copy. Read those lines before you switch. The one about the host's name is
  expected: the machine's name now comes from your overlay. Your machine may
  have none of these, or others.
- **`/etc/nixos` stays,** but it isn't what the machine runs any more. From now
  on, use `piste switch`, not `nixos-rebuild switch`.
- **Your SSH keys** from `~/.ssh/authorized_keys` may log in. They're listed
  in the host's file, `hosts/devbox/default.nix`, under "Who can log in over
  SSH". With none there, piste warns instead: add yours to that list before
  you switch.

Then piste builds the whole system. The first build downloads a few GB, so it
can take a while. After the build, it gives the machine its name:
`Setting this machine's hostname to devbox for now; the switch makes it permanent.`
sudo may ask for your password here. Some images force a name of their own
(Proxmox's do); then piste warns, and names the setting that keeps yours.
[Making the machine a host](customising.md#making-the-machine-a-host) has
every detail.

## 4. The first switch

With the system built (step 3), setup says `sudo will ask for your password.`
and asks "Switch this machine to it now?". Choose "Switch now". The switch is
`sudo nixos-rebuild switch`, NixOS's own, on your overlay: give your password
when sudo asks.

Before it changes anything, piste checks that the new system won't lock you
out (`Checking that the new system won't lock you out...`). It refuses a
system that would leave you without SSH (when you're connected over it),
without sudo or without a boot loader, or that looks like another machine's.
When it refuses, it says what to change, for example:

```
piste switch: refusing, the new configuration would lock you out:
  you're connected over SSH and the new config gives jane no SSH keys: add users.users.jane.openssh.authorizedKeys.keys to your host
Fix it, or run cd ~/piste && NIX_CONFIG='extra-experimental-features = nix-command flakes' nix run .#piste -- switch --force to switch anyway.
```

Make the change in your host's file,
`~/piste/overlays/personal/hosts/devbox/default.nix`, then run setup again.
For an SSH key, that file has the line already, under "Who can log in over
SSH": put your public key in its list, in quotes (the one line of a `.pub`
file such as `~/.ssh/id_ed25519.pub`, on the computer you connect from).
[The switch](customising.md#the-switch) lists every check. The last line's
`--force` switches without them: keep it for a refusal you're sure is wrong.

Run again on a machine made from an image, setup may warn that
`devbox is already a host in this overlay, and piste can't tell this machine is it`:
its root disk has a name that every copy of the image shares. When it's the
machine you've just added, go on.

When the switch is done, setup brings piste's plugins and MCP servers to
Claude Code and Codex (`$ piste agents sync --user` and `$ piste mcp sync` in
the output), and ends with:

```
Switched. Open a new shell (piste is installed now), then:
  cd /home/jane/piste
  piste switch    # after changing overlays/personal
  gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key && piste github    # a GitHub SSH key, once gh is here
  git -C overlays/personal commit -m 'My overlay' && gh repo create personal --private --source overlays/personal --push    # keep it, in a private repo
```

The last two lines are step 6. If you chose "Later", setup stops before the
switch and prints the command that does it when you're ready.

### What changed

- **The whole machine builds from your overlay.** `hosts/devbox/` holds the
  machine's own configuration and your settings for it; the overlay's
  `home.nix` holds what's yours on every machine.
- **piste's base:** your user in `wheel` (the admin group, whose members may
  use sudo) with zsh as its login shell, sudo,
  flakes on for good, OpenSSH with keys only (SSH password logins are off),
  nix-ld (so prebuilt binaries from pip and uv run) and your home, set up as
  on any machine
  ([What the base gives a NixOS host](customising.md#what-the-base-gives-a-nixos-host)).
- **Your shell:** zsh with a starship prompt, suggestions and colours as you
  type, and a set of aliases and tools: ripgrep, fd, bat, lazygit, the Claude
  Code, Codex and Gemini CLIs and more.
  [piste's README](../README.md#opinions) lists them all, under Opinions.
- **The machine's name** is the one you gave, for good.
- **piste itself,** as the `piste` command.

`/etc/nixos` is still there, but it isn't what the machine runs, and a plain
`sudo nixos-rebuild switch` doesn't build your overlay. Use `piste switch`. To
go back to the system you had on purpose, `piste rollback`.

## 5. Open a new shell

Log out and back in, or open a new SSH session: your login shell is zsh now,
and `piste` is on your PATH. Over SSH, open the new session before you close
the old one, so that you're still in if something is wrong. Then:

```bash
cd ~/piste
piste --version
```

You'll see piste's version. From now on, after a change to your overlay,
`piste switch` in `~/piste` applies it.

## 6. Keep your overlay

Your overlay is only on this machine so far. Push it to a private repository:
it's yours, and setup on your next machine starts from it. These are setup's
last lines, with the switch that `piste github` asks for in between:

```bash
cd ~/piste
gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key && piste github
piste switch
git -C overlays/personal commit -m 'My overlay' && gh repo create personal --private --source overlays/personal --push
```

1. `gh auth login` logs the GitHub CLI in: it gives a code and a web address
   to enter it at, from any browser. `piste github` then makes this machine a
   key of its own for GitHub, adds it to your account, and names it in your
   overlay. It asks before each change, and ends with the two steps after
   it: `Next: piste switch`, then `Then: ssh -T git@github.com`, with a note
   that GitHub greets you by your account's name once the switch is done.
2. `piste switch` applies it. `ssh -T git@github.com` then checks the key
   works: GitHub greets you by name.
3. The last line commits your overlay and pushes it to a new private
   repository, `personal`, on your GitHub account.

If you named your overlay something else, put its name in place of
`personal`. If you added this machine to an overlay you had, setup printed the
line that commits and pushes it instead, after `To keep it:`.

If you said yes to secrets, setup made an age key,
`~/.config/sops/age/keys.txt`, which decrypts them. It's the one thing your
overlay doesn't hold: keep a copy in a password manager.

On your next machine, give setup the repository's https URL
(`https://github.com/jane/personal.git`), and it starts from your overlay
([Add a second machine](make-it-yours.md#add-a-second-machine)).

## What next

- [A NixOS machine](customising.md#a-nixos-machine): your own system settings
  on a NixOS host, and what the switch protects.
- [Make it yours](make-it-yours.md): add a package, drop a default, add a
  second machine, keep secrets.
- [Concepts](concepts.md): what the engine, your overlay and the checkout
  are, and how piste picks the overlay you mean.
- [Upgrading](upgrading.md): moving to a new release.

When a build or a switch fails, `piste fix` builds it again and hands the
error to Claude Code or Codex ([Troubleshooting](troubleshooting.md)).
