# Getting started on a Mac

- What do I need?
- How do I get my Mac switched?
- What changed?

This guide takes a Mac from nothing to set up by piste. You don't need to know
Nix: you copy a few commands and answer a few questions. Each step says what
you see when it worked.

## What you need

- **A Mac with Apple silicon** (M1 or newer). piste doesn't support Intel
  Macs, and setup stops on one.
- **An administrator account.** Installing Nix and the first switch ask for
  your password.
- **Time and disk space.** The first switch downloads a few GB.
- **A GitHub account**, to keep your settings in at the end (step 7). Any
  private git service will do instead.

## 1. Install the Xcode command-line tools

```bash
xcode-select --install
```

A window asks whether to install them: click Install, and wait until it's
done. They bring git, which piste needs. If they're installed already, the
command says so, and you can go on.

## 2. Install Nix

[Nix](https://nixos.org/) is the package manager piste is built on. It
installs software and settings from a written description, so the same
description always gives the same machine. piste writes that description for
you.

```bash
curl -fsSL https://install.determinate.systems/nix | sh -s -- install
```

This installs [Determinate Nix](https://docs.determinate.systems/), the Nix
piste is tested with on a Mac. The installer says what it will do and asks
before it does it, and it asks for your password. When it has finished, open a
new terminal window, so that the `nix` command is found, and check:

```bash
nix --version
```

You'll see Nix's version.

## 3. Run setup

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
✓ Xcode command-line tools
✓ Determinate Nix
Cloned piste vX.Y.Z into /Users/jane/piste
```

A [flake](https://nix.dev/concepts/flakes) is how Nix pins a project, and
everything it uses, to exact versions; Determinate Nix has flakes on. A row
marked ✗ says how to fix it, and setup stops there: fix it, then run the same
command again.

`~/piste` is now the checkout: a copy of piste at its newest release, where
your own settings live too ([The checkout](concepts.md#the-checkout)).
`setup --dir <path>` puts it somewhere else.

Then setup comes to your overlay, which lives in `~/piste/overlays/`.

If setup stops part of the way, fix what it says and run the same command
again. It finds the checkout in `~/piste` and the overlay that has this Mac,
and goes on from there.

<details>
<summary>By hand, without setup</summary>

These are setup's steps without its checks. `init` asks the same questions:

```bash
git clone --branch <newest vX.Y.Z tag> https://github.com/elocke/piste ~/piste && cd ~/piste   # without --branch: the default branch, not a release
nix run .#piste -- init           # or: nix run .#piste -- add <git url>
nix run .#piste -- switch         # afterwards (in a new shell) just `piste switch`
```

</details>

## 4. Answer its questions

At a terminal the questions are forms: the arrow keys pick, Enter goes on and
Shift+Tab goes back. (In a pipe, or with `NO_COLOR`, `TERM=dumb` or
`ACCESSIBLE` set, each one is a line, and Enter takes the default in
brackets.) The defaults are good ones: on a first Mac you can take most of
them.

**Your overlay.** Choose "Create a new overlay" on your first machine. On the
next ones, choose "Add one I already have" and give its git URL. Use the https
one (`https://github.com/jane/personal.git`): this Mac has no key for GitHub
yet. When you gave a URL on the command line, setup doesn't ask. Each machine
in your overlay is a *host*, with settings of its own. When the overlay has no
host for this Mac yet, setup asks "Add this machine to it?"; "Add it" puts this
Mac in, and setup goes on to step 5.

A new overlay asks the rest:

- **Your username on the machine:** the account you log in with. Keep it.
- **What kind of machine:** "Mac, Apple silicon", chosen already.
- **Hostname:** this Mac's name. piste finds the Mac's settings by it. Keep it.
- **Agent zone:** what AI agents on this Mac may use. Keep `personal`, or say
  `work` on a work machine ([Zones](concepts.md#zones)).
- **Overlay name:** its folder in `~/piste/overlays/`. Left empty, it takes
  the zone's name.
- **Git name** and **Git email:** for your commits. They're filled in from
  your git settings when you have some.
- **Set up encrypted secrets (sops)?** Yes makes a key, so that your overlay
  can hold API keys, encrypted. "Not now" is fine: you can add it later.
- **Start minimal or with everything?** Minimal is piste's lean defaults.
  Everything adds the extras pack: more aliases and settings, and more apps
  (browsers, VS Code, Raycast and others), each of which you can drop later
  ([The extras pack](customising.md#the-extras-pack)).
- **Ready:** your answers in one place. Create, or Change something to go
  back.

If the GitHub CLI is installed and logged in already, setup also offers a key
for GitHub. On a new Mac it isn't, and step 7 does this.

## 5. The first switch

Setup builds your Mac's configuration first. The first build downloads a few
GB, so it can take a while. Then, before it changes anything, it says what the
switch will change. When files of yours are in the way, it lists them, for
example:

```
The switch moves 1 file of yours aside, to put its own in their place:
  ~/.zshrc → ~/.zshrc.backup-<time>
```

Nothing of yours is deleted: a file piste replaces is kept beside it, renamed
with `.backup-` and the time of the switch. With nothing in the way, there's no
such list. On a Mac, a line about Homebrew and sudo follows; starting minimal,
the one Homebrew app is Ghostty:

```
It installs 1 Homebrew app and changes some macOS settings (the README lists them); sudo will ask for your password.
```

Then it asks "Switch this machine to it now?". Choose "Switch now", and give
your password when sudo asks. On a Mac, piste switches with
[nix-darwin](https://github.com/nix-darwin/nix-darwin), which applies the
description Nix built to macOS. When the switch is done, setup brings piste's
plugins and MCP servers to Claude Code and Codex (`$ piste agents sync --user`
and `$ piste mcp sync` in the output), and ends with:

```
Switched. Open Ghostty (piste is installed now), then:
  cd /Users/jane/piste
  piste switch    # after changing overlays/personal
  gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key && piste github    # a GitHub SSH key, once gh is here
  git -C overlays/personal commit -m 'My overlay' && gh repo create personal --private --source overlays/personal --push    # keep it, in a private repo
```

The last two lines are step 7. If you chose "Later", setup stops before the
switch and prints the command that does it when you're ready.

### What changed

- **Your shell:** zsh with a starship prompt, suggestions and colours as you
  type, and a set of aliases and tools: ripgrep, fd, bat, lazygit, the Claude
  Code, Codex and Gemini CLIs and more.
  [piste's README](../README.md#opinions) lists them all, under Opinions.
- **Ghostty**, a terminal app, in Applications: the Homebrew app the switch
  installed. Homebrew apps you had already stay; piste uninstalls none.
- **Some macOS settings,** such as file extensions always shown, a faster key
  repeat and a Dock that hides until you point at it, and Touch ID for sudo.
- **Your files that were in the way,** moved aside as `.backup-` files.
- **piste itself,** as the `piste` command.

## 6. Open Ghostty

Open Ghostty, from Applications or with Spotlight. `piste` is on your PATH
there, and not yet in the terminal you ran setup in. Ghostty also shows the
prompt's icons, which Terminal's font shows as boxes. In Ghostty:

```bash
cd ~/piste
piste --version
```

You'll see piste's version. From now on, after a change to your overlay,
`piste switch` in `~/piste` applies it.

## 7. Keep your overlay

Your overlay is only on this Mac so far. Push it to a private repository: it's
yours, and setup on your next machine starts from it. These are setup's last
lines, with the switch that `piste github` asks for in between. In Ghostty:

```bash
cd ~/piste
gh auth login --hostname github.com --git-protocol ssh --web --skip-ssh-key && piste github
piste switch
git -C overlays/personal commit -m 'My overlay' && gh repo create personal --private --source overlays/personal --push
```

1. `gh auth login` logs the GitHub CLI in, through your browser. `piste github`
   then makes this Mac a key of its own for GitHub, adds it to your account,
   and names it in your overlay. It asks before each change, and ends with
   the two steps after it: `Next: piste switch`, then
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

- [Make it yours](make-it-yours.md): add a package or a Mac app, drop a
  default, add a second machine, keep secrets.
- [Concepts](concepts.md): what the engine, your overlay and the checkout
  are, and how piste picks the overlay you mean.
- [Upgrading](upgrading.md): moving to a new release.

When a build or a switch fails, `piste fix` builds it again and hands the
error to Claude Code or Codex ([Troubleshooting](troubleshooting.md)).
