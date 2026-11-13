# Getting started on Linux

- What do I need?
- How do I get my Linux machine switched?
- What changed?

This guide takes a Linux machine from nothing to set up by piste. You don't
need to know Nix: you copy a few commands and answer a few questions. Each step
says what you see when it worked.

On Linux, piste sets up your home: your shell, your tools and their settings.
It never changes the system itself, so it works beside your distribution's own
packages. (On NixOS it can set up the whole machine:
[Getting started on NixOS](start-nixos.md).)

## What you need

- **A Linux machine,** x86_64 or ARM, with any distribution.
- **sudo,** to install Nix and to change your login shell.
- **Time and disk space.** The first switch downloads a few GB.
- **A GitHub account**, to keep your settings in at the end (step 6). Any
  private git service will do instead.

## 1. Install Nix

[Nix](https://nixos.org/) is the package manager piste is built on. It
installs software and settings from a written description, so the same
description always gives the same machine. piste writes that description for
you.

```bash
curl -fsSL https://install.determinate.systems/nix | sh -s -- install
```

This installs [Determinate Nix](https://docs.determinate.systems/) for every
user of the machine, with a daemon: the kind of install piste is tested on. The
installer says what it will do and asks before it does it, and it asks for
your password. When it has finished, open a new terminal, so that the `nix`
command is found, and check:

```bash
nix --version
```

You'll see Nix's version.

If you have Nix already, from another installer, it will do: a multi-user
(daemon) install is the one piste is tested on, and a single-user one
(`--no-daemon`) works too. But unless it's Determinate Nix, turn flakes on
first. A [flake](https://nix.dev/concepts/flakes) is how Nix pins a project,
and everything it uses, to exact versions; piste needs them, and without them
the next step fails before setup starts.
[Nix says a feature is disabled](troubleshooting.md#nix-says-a-feature-is-disabled-flakes-are-off)
has the line that turns them on.

## 2. Run setup

Setup keeps your settings in **your overlay**: a small git repository of your
own, with your user, your machines and your packages in it
([The engine and your overlay](concepts.md#the-engine-and-your-overlay)).
piste is the same for everyone; your overlay is what makes the machine yours.
Setup makes a new one on your first machine, and starts from the one you have
on the next:

```bash
nix run github:elocke/piste/stable -- setup              # a new overlay
nix run github:elocke/piste/stable -- setup <git url>    # or one you already have
```

On your first machine, run the first line. `nix run` fetches piste and runs
it, without installing anything yet. `stable` is the branch that follows the
newest release, so what you run is a release, never a half-finished `main`.

Setup first checks what piste needs. You'll see:

```
✓ Nix
✓ flakes
✓ git
Cloned piste vX.Y.Z into /home/jane/piste
```

A row marked ✗ says how to fix it, and setup stops there: fix it, then run the
same command again. Without git, for example, it says
`install git with your package manager` (on Ubuntu or Debian,
`sudo apt install git`).

`~/piste` is now the checkout: a copy of piste at its newest release, where
your own settings live too ([The checkout](concepts.md#the-checkout)).
`setup --dir <path>` puts it somewhere else.

Then setup comes to your overlay, which lives in `~/piste/overlays/`.

If setup stops part of the way, fix what it says and run the same command
again. It finds the checkout in `~/piste` and the overlay that has this
machine, and goes on from there.

<details>
<summary>By hand, without setup</summary>

These are setup's steps without its checks. `init` asks the same questions:

```bash
git clone --branch <newest vX.Y.Z tag> https://github.com/elocke/piste ~/piste && cd ~/piste   # without --branch: the default branch, not a release
nix run .#piste -- init           # or: nix run .#piste -- add <git url>
nix run .#piste -- switch         # afterwards (in a new shell) just `piste switch`
```

</details>

## 3. Answer its questions

At a terminal the questions are forms: the arrow keys pick, Enter goes on and
Shift+Tab goes back. (In a pipe, or with `NO_COLOR`, `TERM=dumb` or
`ACCESSIBLE` set, each one is a line, and Enter takes the default in
brackets.) The defaults are good ones: on a first machine you can take most of
them.

**Your overlay.** Choose "Create a new overlay" on your first machine. On the
next ones, choose "Add one I already have" and give its git URL. Use the https
one (`https://github.com/jane/personal.git`): this machine has no key for
GitHub yet. When you gave a URL on the command line, setup doesn't ask. Each
machine in your overlay is a *host*, with settings of its own. When the overlay
has no host for this machine yet, setup asks "Add this machine to it?"; "Add
it" puts this machine in, and setup goes on to step 4.

A new overlay asks the rest:

- **Your username on the machine:** the account you log in with. Keep it.
- **What kind of machine:** "Linux, x86_64" or "Linux, ARM", chosen already.
- **Hostname:** this machine's name. piste finds its settings by your username
  and this name together, as `jane@devbox`. Keep it.
- **Roles:** what the machine is for. Development, the default, suits most
  machines. Desktop adds settings for zellij and for the Ghostty terminal,
  for a machine with a screen. On Linux it installs no terminal app: Ghostty
  or Alacritty, if you want one, comes from your distribution. Basics only is
  zsh, git, ssh and starship.
- **Agent zone:** what AI agents on this machine may use. Keep `personal`, or
  say `work` on a work machine ([Zones](concepts.md#zones)).
- **Overlay name:** its folder in `~/piste/overlays/`. Left empty, it takes
  the zone's name.
- **Git name** and **Git email:** for your commits. They're filled in from
  your git settings when you have some.
- **Set up encrypted secrets (sops)?** Yes makes a key, so that your overlay
  can hold API keys, encrypted. "Not now" is fine: you can add it later.
- **Start minimal or with everything?** Minimal is piste's lean defaults.
  Everything adds the extras pack: more aliases, and more tmux and git
  settings, each of which you can drop later
  ([The extras pack](customising.md#the-extras-pack)).
- **Ready:** your answers in one place. Create, or Change something to go
  back.

If the GitHub CLI is installed and logged in already, setup also offers a key
for GitHub. On a new machine it usually isn't, and step 6 does this.

## 4. The first switch

Setup builds this machine's configuration first. The first build downloads a
few GB, so it can take a while. Then, before it changes anything, it says what
the switch will change. When files of yours are in the way, it lists them, for
example:

```
The switch moves 1 file of yours aside, to put its own in their place:
  ~/.zshrc → ~/.zshrc.backup-<time>
```

On a fresh machine there's usually nothing in the way, and no such list.
Nothing of yours is deleted: a file piste replaces is kept beside it, renamed
with `.backup-` and the time of the switch. A line such as
`~/.zshrc is a link of yours: the switch stops on it; move it away first` is
about a link of yours (a dotfiles manager makes these), which the switch won't
move for you: move it away before you switch.

Then it asks "Switch this machine to it now?". Choose "Switch now". On Linux,
piste switches with [home-manager](https://github.com/nix-community/home-manager),
which sets up the programs and files in your home from the description Nix
built. It runs as you, without sudo. When the switch is done, setup brings
piste's plugins and MCP servers to Claude Code and Codex
(`$ piste agents sync --user` and `$ piste mcp sync` in the output), and ends
with:

```
Your login shell is still bash, and piste sets up zsh. To make zsh your login shell:
  echo ~/.nix-profile/bin/zsh | sudo tee -a /etc/shells && chsh -s ~/.nix-profile/bin/zsh
Switched. Open a new shell (piste is installed now), then:
  cd /home/jane/piste
  piste switch    # after changing overlays/personal
  gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key && piste github    # a GitHub SSH key, once gh is here
  git -C overlays/personal commit -m 'My overlay' && gh repo create personal --private --source overlays/personal --push    # keep it, in a private repo
```

The first two lines are step 5, the last two step 6. If you chose "Later",
setup stops before the switch and prints the command that does it when you're
ready.

### What changed

- **Your shell's setup:** zsh with a starship prompt, suggestions and colours
  as you type, and a set of aliases and tools: ripgrep, fd, bat, lazygit, the
  Claude Code, Codex and Gemini CLIs and more.
  [piste's README](../README.md#opinions) lists them all, under Opinions.
- **Your git settings,** with the name and email you gave.
- **Your files that were in the way,** moved aside as `.backup-` files.
- **piste itself,** as the `piste` command.

Nothing outside your home has changed: no system packages, no system services,
nothing in `/etc`. Your login shell is still the one you had, until step 5.

## 5. Open a new shell

The switch set zsh up, but your login shell, the one a new terminal or an SSH
login starts, is still the one you had. To make it zsh, run the line setup
printed:

```bash
echo ~/.nix-profile/bin/zsh | sudo tee -a /etc/shells && chsh -s ~/.nix-profile/bin/zsh
```

Then log in again.
[New terminals still open bash](troubleshooting.md#new-terminals-still-open-bash-linux)
says what the line does, and how to check that it worked. When your login
shell is zsh already, setup prints neither line, and a new terminal is all
you need.

Then, in the new shell:

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

1. `gh auth login` logs the GitHub CLI in, through your browser (on a machine
   without one, it gives a code to enter on another). `piste github` then
   makes this machine a key of its own for GitHub, adds it to your account, and
   names it in your overlay. It asks before each change, and ends with the
   two steps after it: `Next: piste switch`, then
   `Then: ssh -T git@github.com`, with a note that GitHub greets you by your
   account's name once the switch is done.
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

On your next machine, setup with the repository's https URL starts from your
overlay ([Add a second machine](make-it-yours.md#add-a-second-machine)):

```bash
nix run github:elocke/piste/stable -- setup https://github.com/jane/personal.git
```

## What next

- [Make it yours](make-it-yours.md): add a package, drop a default, add a
  second machine, keep secrets.
- [Concepts](concepts.md): what the engine, your overlay and the checkout
  are, and how piste picks the overlay you mean.
- [Upgrading](upgrading.md): moving to a new release.

When a build or a switch fails, `piste fix` builds it again and hands the
error to Claude Code or Codex ([Troubleshooting](troubleshooting.md)).
