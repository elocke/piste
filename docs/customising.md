# Customising

- Where does a setting go: `home.nix` or a host file?
- What does the package set hold on Linux, and how do I trim it?
- Which switches turn off what the engine installs or turns on?
- What's in the extras pack?
- How do I set any other home-manager, nix-darwin or NixOS option?
- When the engine, a layer and I set the same option, who wins?
- How do I take over one of the engine's pieces with my own copy?
- What does `piste.lib.mkFlake` take?
- What does `piste host add` write, and how do I remove a machine?
- How do I make a NixOS machine a host, and what does the switch protect?
- How do I write a `piste` plugin?
- What do the `piste secrets` commands do?
- How do I add agent skills and instructions?

This page is the reference. The first changes, step by step (a package, a
Mac app, a default dropped, the extras pack, a second machine, a secret, any
setting), are recipes in [Make it yours](make-it-yours.md).

Everything here is an edit in your overlay (`~/piste/overlays/<name>/`), then
`piste switch`. A new file must be known to git first (`git add`): builds
only see tracked files ([concepts.md](concepts.md#what-a-build-uses)). Every
`piste.*` option is in [options.md](options.md). When a build or switch
fails, see [Troubleshooting](troubleshooting.md#a-build-or-a-switch-fails).

## Where a setting goes

`piste init` writes a `home.nix` and one file per machine in `hosts/`, and
their comments say what goes where. What's yours on every machine goes in
`home.nix`, which each `hosts/<name>.nix` imports: it's your home-manager
configuration, on Linux and on a Mac alike. A host file holds only that
machine's own settings:

- **On Linux** (`hosts/devbox.nix`), the whole file is that machine's
  home-manager configuration: put settings at its top level.
- **On a Mac** (`hosts/jane-mac.nix`), the file is a nix-darwin module. System
  settings, Homebrew and the package set (`piste.packages.*`) go at its top
  level; that Mac's own home settings go inside
  `home-manager.users.${user} = { … };`.
- **On a NixOS machine** (`hosts/devbox/default.nix`), the file is a NixOS
  module, laid out as a Mac's: system settings (services, users, the firewall)
  and the package set go at its top level; that machine's own home settings go
  inside `home-manager.users.${user} = { … };`. The machine itself (disks, boot
  loader, hardware) is in `hosts/devbox/machine/`, which the file imports
  ([A NixOS machine](#a-nixos-machine)).

Each recipe in [Make it yours](make-it-yours.md) names the file its change
goes in.

## Packages

Adding one, step by step: [Add a package](make-it-yours.md#add-a-package),
and on a Mac [Add a Mac app](make-it-yours.md#add-a-mac-app).

Installed something with Homebrew, npm or pnpm, cargo or pip first?
`piste adopt` lists each install with where it would go (nixpkgs,
`casks.extra` or `brews.extra`, or your `pnpmPackages`), writes the ones you
tick into the lists your overlay already keeps, offers the switch, and then
removes the outside copies Nix now provides. What comes from a third-party
tap, what you've excluded and what nixpkgs doesn't have are listed, not
offered. `piste drift` shows the same report and changes nothing.

### The package set on Linux

A Linux host built with `mkFlake` (the starter is) gets the engine's package
set, the same command-line and dev tools a Mac gets from its platform module:
`mkFlake` adds the packages module, puts its list in `home.packages` and
applies `piste.overlays.shared`. There is nothing to wire. (A NixOS host gets
the set from its platform module, in `environment.systemPackages`, and trims
it the same way at the top level of its host file. On a Mac, the AI tools in
the set, Claude Code, Codex and Gemini CLI, come from `piste.overlays.ai`,
which `mkFlake` applies; the platform module brings the rest.) On a server,
trim it in the host file:

```nix
piste.packages.terminal.enable = false;  # terminal emulators and extra shells: not on a server
piste.packages.exclude = [ "docker" ];
```

The machine's own packages go in its `home.packages` list beside the set, or
in `piste.packages.extraPackages`. The shell runs the init of zoxide, fzf
and atuin only where that tool is installed, so a home without one of them
still gets a working shell. Keep `delta` and `fzf`
if you trim the set: git's pager is delta, so without it `git diff` fails, and
the shell's tab completion (fzf-tab) needs fzf to show its matches.

A standalone home-manager host wired by hand, without `mkFlake`, imports
`piste.homeManagerModules.packages`, sets
`home.packages = config.piste.packages.resolved.homePackages;` and applies
`piste.overlays.shared` to its `pkgs`. A Mac wired by hand
(`piste.darwinModules.platform` without `mkFlake`) adds `piste.overlays.ai`
to its `nixpkgs.overlays`, or its AI tools are nixpkgs' own, not the engine's.

### Newer than the engine: fresh

Your packages come from the engine's nixpkgs, which releases keep about a week
old. When you want a tool newer than that, name it in `fresh`, and give your
overlay a second package source. In `flake.nix` (the starter has both lines,
commented out):

```nix
inputs.nixpkgs-fresh.url = "github:nixos/nixpkgs/nixos-unstable";
```

```nix
piste.lib.mkFlake {
  # ...
  fresh = [ "gh" "uv" ];
}
```

Those packages now come from `nixpkgs-fresh` everywhere, including in
`home.packages` and in piste's own package set. Everything else stays on the
engine's tested version, so a fresh package that breaks affects only that tool.
`piste pkgs update` moves `nixpkgs-fresh` on every run.

Use `fresh` for tools you run, not libraries: a program such as `nodejs` is
built into other packages, and listing it rebuilds everything that uses it. A
name in a package set works written with dots, as `python3Packages.requests`.

`mkFlake` refuses, and says what to change, when `fresh` has names but there
is no `nixpkgs-fresh` input, when nixpkgs-fresh has no package of that name,
and when the engine itself provides the package. The engine provides
`claude-code`, `codex` and `gemini-cli`, and they move with piste releases;
take them out of `fresh`.

### pnpm globals

Tools you install with `pnpm add -g`, the way node tools usually are, can be
listed in `home.nix`:

```nix
piste.packages.pnpm = [ "tsx" ];
```

A switch installs the ones that are missing, and `piste pkgs update` upgrades
them to their latest version. Write `"serve@14.2.0"` to pin a version: that
entry is installed, then left alone. Taking a name out of the list doesn't
uninstall it: `pnpm rm -g <name>` does. A list you pass to
`lib.mkPnpmInstallHook` keeps installing what is missing, but `piste pkgs
update` doesn't upgrade it; move it to `piste.packages.pnpm` to have that.

## Switching defaults off

Step by step: [Drop a default](make-it-yours.md#drop-a-default).

The package set's switches go in the host file (a Mac's or a NixOS machine's
top level, or a Linux host's). `piste.packages.exclude` takes packages by name
(`lib.getName`), a category's switch drops all of it, and on a Mac
`piste.packages.casks.exclude` takes Homebrew casks, the extras pack's too:

```nix
piste.packages.exclude = [ "docker" "gemini-cli" ];
piste.packages.dev.enable = false;           # a whole category: cli, dev, terminal, fonts, …
piste.packages.casks.exclude = [ "google-chrome" "docker-desktop" ];
```

A program a role turns on has a toggle, `piste.roles.<role>.<toggle>.enable = false;`
in your home ([options.md](options.md)). The toggles are base's `shell`
(zsh), `git`, `ssh` and `starship`, dev's `editor` (vim) and `tmux`, and
desktop's `alacritty` and `ghostty` (Ghostty's settings); one switched off
turns its program off. The rest of the engine's piece stays: the editor
piece's nano and `~/.nanorc`, the shell piece's direnv and session variables.
To drop a whole piece, turn its key off ([Take over a
piece](#take-over-a-piece)). How a toggle does it (today `lib.mkForce` on the
program's `enable`) isn't promised, so don't build on it. A program with no
toggle (direnv, gh), force off yourself, with `lib` in the file's arguments:

```nix
programs.direnv.enable = lib.mkForce false;
```

zellij has its own switch, `piste.programs.zellij.enable = false;`.

Either way the program's settings go. A program the package set also installs
(alacritty, zellij) stays installed until you add it to
`piste.packages.exclude` too; on a Mac, Ghostty's app is a cask, dropped with
`piste.packages.casks.exclude`.

## The extras pack

The engine's defaults are lean on purpose. The `extras` role is the rest:
more tools and settings that are one person's taste rather than everyone's,
off unless a host lists it.

- **Minimal** (the default) gets what [the README](../README.md#opinions)
  lists under Opinions: the shell, git, editor and terminal basics, the
  package set, and on a Mac Ghostty and the system settings. No extra apps,
  and none of the pack's aliases or settings.
- **Everything** adds the `extras` role. The table below lists what each part
  brings.
- **Turning it on or off, or dropping one part,** step by step:
  [Turn on the extras pack](make-it-yours.md#turn-on-the-extras-pack).

`piste init` (and `piste init --layer`) asks "Start minimal or with
everything?". Everything adds `"extras"` after the host's other roles in
`flake.nix` (`--extras` answers it in a script, `--extras=false` too).

Everything loosens one of the defaults' safety settings: `ghostty` lets
programs read the clipboard (the table says how). Drop that part if it isn't
for you. Nothing in the pack throws work away (no `git nuke` or `git discard`
alias, no `fetch.pruneTags`) or writes what is on screen to disk.

A machine added later with `piste host add` (or `piste setup` on a
second machine) takes the roles of the overlay's other hosts of its kind (Mac,
NixOS or Linux), so `extras` carries over; it prints `Roles from overlays/<name>'s
other Linux host (<host>): dev, extras; --role sets others`. With no other host
of its kind it gets the default roles, and says so when the overlay's other
hosts take `extras`: `piste host add --extras` (as `init` spells it) adds it.

The pack holds generic, public tools and settings only: nothing tied to a
person, a machine, a service or an account. One part per file in
`modules/extras/`:

| Part | Brings | Where it applies |
|------|--------|------------------|
| `shell` | more aliases, ones that swap classic commands for modern tools among them (`grep` is `rg`, outside an agent's shell), `cd` as zoxide's `z`, `shell()` and `YSU_MODE=ALL` | your home |
| `editor` | git's editor is vim, and git ignores `.idea/`, `.vscode/` and `.envrc` everywhere | your home |
| `tmux` | the prefix is `C-x`, `x` and `v` split, and session save and restore (resurrect, continuum), which keeps the layout and the running programs but not the panes' text | your home |
| `ghostty` | Ghostty settings: any program in the terminal can read your clipboard without asking, a tab closes without asking, and Option is Alt on a Mac | your home |
| `casks` | more Mac apps (Homebrew casks) and the default apps for file types | the Mac's system |
| `macos` | macOS input settings: no accent menu on press-and-hold, three-finger drag | the Mac's system |

Agent content isn't in the pack: the `rtk` and `guard` presets are opted
into on their own, and Claude Code plugins (the starter four among them) go
in your zone's `agents.toml` ([agents-and-mcp.md](agents-and-mcp.md)).

[options.md](options.md) lists every part's toggle,
`piste.roles.extras.<part>.enable`, and the part's file says exactly what it
sets ([where each toggle goes](make-it-yours.md#turn-on-the-extras-pack)). A
part switched off sets nothing. To keep a part but not one thing in it,
override that one option in your overlay (`lib.mkForce` where the part sets
it plainly), or take the part over with a copy of your own ([Take over a
piece](#take-over-a-piece)). On
a team's layer, the layer's `piste.packages.casks.exclude` (the apps your
company installs itself) applies to the pack's casks too.

Two things are yours to add, because they can cost you something. Git's
`nuke` (`reset --hard` and `clean -fd`) and `discard` (`checkout --`) aliases
and `fetch.pruneTags` (fetch deletes local tags the remote lacks, unpushed ones
too) are plain git settings (`programs.git.settings.alias.nuke = …;`). tmux's
resurrect can save each pane's visible text with the session, which leaves
whatever was on screen, a token included, in `~/.cache/tmux/resurrect` in the
clear; if you want it:

```nix
piste.roles.extras.tmux.resurrectExtraConfig = ''
  set -g @resurrect-capture-pane-contents 'on'
'';
```

## Any other option

Step by step: [Change a setting by name](make-it-yours.md#change-a-setting-by-name).

Any [home-manager](https://nix-community.github.io/home-manager/options.xhtml)
option goes in your home, any
[nix-darwin](https://nix-darwin.github.io/nix-darwin/manual/) option at a
Mac's top level, and any [NixOS](https://search.nixos.org/options) option at a
NixOS machine's. Where the engine already sets the value, a plain setting
conflicts:

```
error: The option `home-manager.users.jane.programs.git.settings.push.autoSetupRemote' has conflicting definition values:
- In `…/modules/programs/git.nix': true
- In `…/hosts/jane-mac.nix': false
```

So give yours priority with `lib.mkForce`, with `lib` in the file's
arguments (the `{ … }:` line near its top, such as
`{ config, pkgs, lib, ... }:`):

```nix
programs.git.settings.push.autoSetupRemote = lib.mkForce false;   # in your home
```

The engine's macOS settings (`system.defaults.*`) are defaults, so a plain
value, `system.defaults.dock.autohide = false;`, wins at a Mac's top level. So
are the NixOS base's (`services.openssh.settings.PasswordAuthentication`, say):
a plain value wins at a NixOS machine's.

Lists (packages, casks) add up instead: yours go beside the engine's. And
attribute sets merge key by key, so only a key the engine also sets
conflicts.

### Who wins

Every definition of an option has a priority, and the lowest number wins.
Single values at the same priority conflict (the error above); lists and
attribute sets at the same priority merge. A lower number replaces the rest
outright, lists included: a list you set with `lib.mkForce` is the whole
list. This table is where the engine is heading. Today differs where the
last column says so.

| Who | Priority | Written as | Today |
|-----|----------|------------|-------|
| the option's default | 1500 | the option's `default` | same |
| the engine | 1000 | `lib.mkDefault` | the target. The macOS settings (`system.defaults.*`) and the NixOS base's are there; most of the core program modules (`programs.*`) still set plain values (100), so the same key in your overlay conflicts and needs `lib.mkForce` |
| a pack or a team layer | 950 | `piste.lib.mkPackDefault` | for team layers. The engine's own extras pack still sets `lib.mkDefault` (1000) or plain values, like the core modules |
| you | 100 | a plain value | same |
| force | 50 | `lib.mkForce` | same, and it keeps working |

More exceptions today: a role toggle switched off forces its program off
with `lib.mkForce` ([Switching defaults off](#switching-defaults-off)), and
some values sit at 900, so neither a pack nor the engine's `mkDefault` beats
them. Among them: the container role's `programs.git.settings.commit.gpgsign`,
and on a NixOS machine your user's login shell (`users.users.<you>.shell`,
zsh) and `networking.hostName`, which `mkFlake` sets to the host's name.

If you write a pack or a team layer:

- Set scalars at your tier: `inputs.piste.lib.mkPackDefault value`, from your
  own flake's `piste` input. A person's plain value then wins over yours, and
  yours over the engine's `mkDefault`.
- Leave lists plain, so a person's entries go beside yours (a list at 950
  would lose to theirs whole).
- Never `mkDefault` (or `mkPackDefault`) a whole free-form attribute set (an
  `attrsOf` option such as `programs.starship.settings`): a person who sets
  one key of it plainly replaces the whole set, every key you set included.
  Set each key at your tier instead.

### SSH

piste owns `~/.ssh/config`: a switch replaces it, so an edit to it doesn't
last. Your own hosts go in `~/.ssh/config-external`, which it includes, or in
`programs.ssh.settings."<host>"` in your home. The engine's block for
`github.com` exists only when `piste.github.login` is set (`piste github`
sets it up): until then ssh finds your key the way it always did, whether it is
`~/.ssh/id_rsa` or lives in 1Password, Secretive or a YubiKey.

### The Dock

The engine sets how a Mac's Dock behaves (`system.defaults.dock`: hidden until
you point at it, 48-point icons at the bottom, no recent apps) as defaults, so
a plain value of yours wins. What's on the Dock is yours: the engine lists no
items, so the Dock keeps whatever you put on it. To have every switch build
it, use nix-darwin's own option at the Mac's top level, in order:

```nix
system.defaults.dock.persistent-apps = [
  "/System/Applications/Messages.app"
  "/Applications/Ghostty.app"
];
system.defaults.dock.persistent-others = [ "/Users/jane/Downloads" ];
```

## Take over a piece

When a toggle or a few overrides aren't enough, take one of the engine's
pieces over: turn it off by its module key and import your own copy. Each
piece has a key, and it is promised
([upgrading.md](upgrading.md#what-a-release-promises)); its file's path isn't.

| Pack | Pieces | Key |
|------|--------|-----|
| core | `shell`, `git`, `ssh`, `starship`, `herdr`, `editor`, `tmux`, `uv`, `zellij`, `alacritty`, `ghostty`, `vscode` (in `modules/programs/`) | `pack:core/<piece>` |
| extras | `shell`, `editor`, `tmux`, `ghostty`, `casks`, `macos` (in `modules/extras/`) | `pack:extras/<piece>` |

To take over the prompt, copy the engine's file into your overlay and stage
it:

```bash
mkdir -p ~/piste/overlays/mine/modules
cp ~/piste/modules/programs/starship.nix ~/piste/overlays/mine/modules/
git -C ~/piste/overlays/mine add modules/starship.nix
```

Change the copy's key line from `key = "pack:core/starship";` to
`key = "local:core/starship";`. Then, in your home (`home.nix`):

```nix
disabledModules = [ { key = "pack:core/starship"; } ];
imports = [ ./modules/starship.nix ];
```

If the file already has an `imports` list, add the copy to it: a second
`imports =` in one file is an error. Edit the copy as you like. Mind these:

- **Use the attribute-set form.** `disabledModules = [ "pack:core/starship" ];`
  is read as a path under nixpkgs' modules and silently matches nothing: the
  engine's piece stays on. A path to the engine's file matches nothing either.
- **Change the key.** A copy that keeps `pack:core/starship` is disabled too,
  and you end up with neither.
- **Keep the copy's option declarations.** `herdr` declares
  `piste.programs.herdr.*`, `zellij` declares `piste.programs.zellij.enable`
  and `.timezone`, `vscode` declares `piste.programs.vscode.*`, and each
  extras part declares its `piste.roles.extras.<part>.enable` (`tmux` also
  `resurrectExtraConfig`). Other modules set some of them: the desktop role,
  which every Mac has, and the container role set
  `piste.programs.zellij.enable`. Turn such a piece off without a copy that
  declares them, and any setting of them fails with "option does not exist".
  The other pieces declare no options of their own (the
  `piste.roles.<role>.<toggle>` options live in the role files), so turning
  them off is always safe.
- **Bring the files it imports.** `herdr.nix` imports
  `./herdr-app-settings.nix`: copy it beside your `herdr.nix`.
- **Order can move.** Your copy comes after the engine's modules, so its list
  entries and lines of text land after the engine's other ones of the same
  priority (`nano` later in `home.packages`, extras `shell`'s init lines after
  zellij's). The items are the same.

On Linux it all goes in your home. On a Mac or a NixOS machine, a home piece
(every `core` piece, and extras `shell`, `editor`, `tmux` and `ghostty`) goes
in the home side: in `home.nix`, or inside
`home-manager.users.${user} = { … };` in the machine's host file. A NixOS
machine has no system pieces: the NixOS base (`nixosModules.platform`) is one
module, with no key. Extras `casks` and `macos` are nix-darwin modules, so
they go at the top level of the Mac's host file, beside `piste.packages.*`:

```nix
# hosts/jane-mac.nix, top level
disabledModules = [ { key = "pack:extras/casks"; } ];
imports = [ ../modules/casks.nix ];   # its key changed to local:extras/casks
```

## `mkFlake`'s arguments

Your overlay's `flake.nix` is one call to `piste.lib.mkFlake`. The recipes
that edit it are [Turn on the extras pack](make-it-yours.md#turn-on-the-extras-pack)
and [Add a second machine](make-it-yours.md#add-a-second-machine).

| Argument | What it is | Default |
|----------|------------|---------|
| `inputs`, `self` | the overlay's flake inputs and the overlay itself | (required) |
| `user` | your login name, the same on every host | (required) |
| `zone` | the hosts' trust zone ([concepts.md](concepts.md#zones)) | (required) |
| `hosts` | the machines, by name (below) | (required) |
| `extraSpecialArgs` | module arguments for every host: an attrset, or a function of the system | `{ }` |
| `homebrew` | merged into `piste.homebrew`, e.g. `{ extraTaps = { … }; }` (taps as `flake = false` inputs) | `{ }` |
| `packages` | the overlay's own packages, beside the engine's: an attrset or a function of the system | `{ }` |
| `fresh` | package names taken from the overlay's `nixpkgs-fresh` input ([fresh](#newer-than-the-engine-fresh)) | `[ ]` |
| `agents` | the zone's agents folder (`./agents`), its skills and instructions | `null` |
| `bundle` | `{ presets = [ … ]; }`: the zone's container agent bundle (needs `agents`) | `null` |
| `devcontainer` | `{ name; modules ? [ ]; extraSpecialArgs ? { }; }`: a container image ([devcontainer.md](devcontainer.md)) | `null` |
| `checks` | the overlay's own checks, beside the zone checks | `{ }` |
| `systems` | what packages and checks are made for (every host's system is added) | `[ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ]` |

A host is `{ system; modules ? [ ]; roles ? [ ]; overlays ? [ ]; aliases ? [ ]; nixos ? false; }`:

- `system`: `aarch64-darwin` (a Mac with Apple silicon, named by its
  hostname: `scutil --get LocalHostName`), `x86_64-linux` or `aarch64-linux`
  (a home, named `user@hostname`; with `nixos = true`, a NixOS system).
- `nixos`: `true` on a Linux host makes it a NixOS system with home-manager
  inside it ([A NixOS machine](#a-nixos-machine)). Its name is its hostname:
  letters, digits and hyphens, no `@`, no dots. `mkFlake` throws on `nixos` for a Mac, on `aliases` for a
  NixOS host and on a NixOS name with `@` or a dot.
- `modules`: its own files; `roles`: on Linux or NixOS, the roles it takes (a
  Mac always has `desktop`), and on any, `extras` ([the extras
  pack](#the-extras-pack)); `overlays`: nixpkgs overlays for it; `aliases`: on a
  Mac, other hostnames for the same machine. A role is `base`, `dev`,
  `desktop`, `container` or `extras`.

`mkFlake` checks what it's given and stops with one line per mistake, each
starting `mkFlake:`. Among them:

- a host key it doesn't know: `mkFlake: host jane-mac: unknown key role (a
  host takes system, modules, overlays, roles, aliases, nixos)`;
- a role that isn't one: `mkFlake: host jane@devbox: unknown role vscode
  (roles are base, dev, desktop, container, extras)`. The names of
  `homeManagerModules` that aren't roles (`vscode`, `sops`, `agents`,
  `packages`, `pnpm`, `secrets`) are refused too. A module of yours goes in
  the host's `modules`; one of the engine's goes in your home's imports
  (`imports = [ piste.homeManagerModules.vscode ];`), and `sops`, `agents`
  and `pnpm` need nothing, since `mkFlake` imports them already;
- a key `devcontainer` or `bundle` doesn't take: `mkFlake: devcontainer:
  unknown key overlays (devcontainer takes name, modules, extraSpecialArgs)`,
  `mkFlake: bundle: unknown key zone (bundle takes presets)`.

`mkFlake` wires every host, so its modules needn't: the home gets
`piste.homeManagerModules.sops` (sops-nix's module, re-exported) and
`piste.homeManagerModules.agents`, the machine gets the piste CLI
(`piste.packages.piste`; set it to `null` to install your own), and `user`
and `piste` are module arguments everywhere, the home modules inside a Mac or a
NixOS host included. `mkFlake` returns `darwinConfigurations`,
`homeConfigurations`, `nixosConfigurations`, `packages` (the engine's and
yours), `apps` and `checks` (the zone checks and yours). You don't have to use
it: the modules it wires are `piste.darwinModules.*`, `piste.nixosModules.*`
and `piste.homeManagerModules.*`, and `lib/mk-flake.nix` shows how.

## Adding and removing a machine

Step by step: [Add a second machine](make-it-yours.md#add-a-second-machine).

When the overlay has no host for the machine, setup asks "Add this machine to
it?" and runs `piste host add`. That writes `hosts/<hostname>.nix` and its
entry in `flake.nix` (staged), with the roles of the overlay's other hosts of
its kind (the ones they all have, if they differ), else the ones `init` would
pick (`--role` to choose). On NixOS it writes a NixOS host, which is more
([A NixOS machine](#a-nixos-machine)). If the overlay has secrets, it also
makes this machine's age key and adds it to `.sops.yaml`; the machine reads
the secrets once one that can already has run `piste secrets updatekeys`.

In a checkout, `piste host add [--role r]` does the same and asks nothing.

Removing a machine is by hand: delete its host (its entry in `flake.nix` and
its file, a NixOS host's directory) and its key in `.sops.yaml` (under `keys:`
and in every `age:` list, anchored by its hostname), run
`piste secrets updatekeys`, and change the
secret values, since that machine has read them. updatekeys also puts every
file under a new data key, so the old one the machine had reads nothing new.
If it had a GitHub key (`piste github`), revoke it: `gh ssh-key list`, then
`gh ssh-key delete <id>`.

## A NixOS machine

A NixOS host is the whole machine, system and home, in your overlay. piste
starts once NixOS is installed and booted and you can log in. Installing NixOS,
servers, NixOS desktops and deploying from another machine aren't covered:
`piste switch` runs on the machine it changes, and the engine's base is
headless.

A NixOS machine whose overlay has only a standalone home-manager home for it
(your user's tools and settings, no NixOS host) still works that way: piste
manages your home and never touches the system. Don't run it beside
home-manager's NixOS module on the same machine.

### Making the machine a host

[Getting started on NixOS](start-nixos.md) walks through it, from a fresh
install to the first switch: on the NixOS machine, `piste setup` does it (in a
checkout, `piste host add`; for a new overlay, `piste init`). What follows is
the detail.

`piste host add` on NixOS:

- **Names the host.** `--name <name>`, else the machine's hostname. A name with
  a dot, `nixos` or `localhost` is refused: a fresh machine is called `nixos`,
  which names no machine in particular (two of them would pick the same host),
  so for one host add asks for another name at a terminal, and wants `--name`
  anywhere else. So does a name the overlay already has a NixOS host of, unless
  that host's root filesystem is this machine's by uuid (`fileSystems."/"` by
  `/dev/disk/by-uuid/…` or `/dev/disk/by-partuuid/…`, against what `/` is
  mounted from): a clone or a name typed by mistake isn't taken as that
  machine. Another machine's root is refused, even with `--name` (if this is
  that machine reinstalled, host add says to point the host's `fileSystems` at
  this root's `/dev/disk/by-uuid/…` link instead); a root piste can't tell by (a label, a device name such
  as `/dev/vda1` or `/dev/mapper/…`, a ZFS dataset: every machine made from
  one image has the same; a container's) is taken only with `--name`. A name
  that isn't the hostname becomes it for this session
  (`sudo hostname <name>`), so the first `piste switch` finds its host, and
  the switch makes it permanent: `mkFlake` sets `networking.hostName` to the
  name ahead of a cloud image's default, so EC2, GCE, Azure and DigitalOcean
  images keep it. But an image module that
  forces its own name (Proxmox images, Proxmox LXCs) wins; host add (and init)
  warns when it does, after the build, and names the setting that keeps the
  name. In a Proxmox container, `proxmoxLXC.manageHostName = true;`, or the
  container named the same in Proxmox. In a Proxmox VM with cloud-init (the
  image's default), the VM named the same in Proxmox, which cloud-init sets
  the hostname from, or `proxmox.cloudInit.enable = false;` (then cloud-init
  configures no network either: only for a VM that gets its address by DHCP).
  Else `networking.hostName = lib.mkOverride 49 "<name>";`. Never `lib.mkForce`:
  beside the Proxmox image's own `mkForce ""` the build fails.
- **Carries the machine's own configuration in, whole.** Every `.nix` file
  under `/etc/nixos` is copied into `hosts/<name>/machine/`, so what the
  machine already sets (its boot loader, `system.stateVersion`, network, users'
  groups, sudo rules, a guest agent, the virtualisation module an image-built
  machine imports) comes along unchanged. `<nixpkgs/nixos/modules/…>` imports
  become `modulesPath` ones, which build from the flake's nixpkgs. The files
  are yours to trim over time.
- **Marks what needs you** with a `# piste:` comment in the copied file, and
  lists it under "Needs you": imports from outside `/etc/nixos`, other
  channels than nixpkgs, `nix.nixPath` and the other channel settings, a
  carried `networking.hostName`, which is commented out because the host's
  name is its key in `flake.nix`, and a carried `system.autoUpgrade`, also
  commented out: it would rebuild the machine from `/etc/nixos` every night and
  put back the system your switch replaced. Read the `# piste:` lines before
  you switch. Only the machine's own `networking.hostName`, at a file's top
  level, is taken out: a container's (`containers.<name>.config`) or any other
  nested one stays as it is. One in a form piste can't comment out cleanly
  (an `inherit hostName;`, a `lib.mkMerge` or `lib.mkIf`, a `rec` or `with`
  block) stays too, marked: remove it if the machine's name should be its key
  in `flake.nix`.
- **Leaves `/etc/nixos` where it is.** It stays on the machine, but it is no
  longer what the machine runs: a plain `sudo nixos-rebuild switch` doesn't
  build your overlay. Use `piste switch`.
  ([Remove piste from NixOS](backing-out.md#remove-piste-from-nixos) switches
  back to `/etc/nixos`.)
- **Lets your keys log in.** The keys in `~/.ssh/authorized_keys` go into the
  host file as `users.users.<user>.openssh.authorizedKeys.keys`.
- Writes `hosts/<name>/default.nix`, which imports `./machine/configuration.nix`
  and is where your own system settings go, and the host's entry in `flake.nix`.
  With no `/etc/nixos/configuration.nix` to carry, the host gets the template's
  stand-in machine (a VM, in `machine/configuration.nix`) and a warning to check
  its boot loader and root filesystem before you switch.

### What the base gives a NixOS host

`piste.nixosModules.platform` gives the host your user in `wheel` with zsh as
its login shell, sudo, flakes on with your user a trusted Nix user, OpenSSH with
keys only (no passwords, and no root login unless an image module the host
imports allows it, as EC2's and GCE's do: theirs wins), nix-ld (so prebuilt
binaries from pip and uv load), home-manager inside the system with the same
roles, `home.nix` and `piste.*` options as on a Mac, and the engine's package
set in `environment.systemPackages`. It builds no `/share/doc`
(`documentation.doc.enable = false`; man pages stay): the pinned nixpkgs can't
build Python's docs, and a headless base has no use for them. Each is a default
or a list that adds up, so a plain value of yours wins. It sets no boot loader,
firewall, authorized key or `system.stateVersion`, and no other service: those
are the machine's, from `machine/` and your host file. An overlay that doesn't
use `mkFlake` wires `piste.nixosModules.platform` by hand, with
`nixosModules.packages` and `nixosModules.sops` beside it.

Your own system settings go at the top level of the host's `default.nix`:

```nix
virtualisation.docker.enable = true;
users.users.${user}.extraGroups = [ "docker" ];
networking.firewall.allowedTCPPorts = [ 8080 ];
piste.packages.extraPackages = with pkgs; [ ollama ];
```

### The switch

`piste switch` on a NixOS host is `sudo nixos-rebuild switch`, so it needs a
terminal for sudo's password. It refuses on a machine that isn't NixOS
(`piste build --host <name>` builds a NixOS host on any Linux machine). Before it
activates anything, piste evaluates the new system and refuses (exit 1), usually naming
the option to set (the wrong-machine rule names the two roots instead), a
config that would lock you out:

- **ssh:** you're connected over SSH and the new config turns the SSH server off,
  or turns password logins off and has no key for you. A key counts when it's in
  `users.users.<user>.openssh.authorizedKeys.keys` (or `keyFiles`), or while
  `services.openssh.authorizedKeysInHomedir` is on, in a `~/.ssh/authorized_keys`
  that is a regular file (not a link into the store) with a key in it. Switched
  as root over SSH (an image you reach as root), it also refuses a config that
  turns root's login off, as the base does: set
  `services.openssh.settings.PermitRootLogin = "prohibit-password";`, or switch
  as your overlay's user.
- **sudo:** the new config takes sudo from you (you aren't in `wheel`, or sudo
  is off), or asks a password you don't have: sudo needs none today, and the
  config sets `wheelNeedsPassword` with no `NOPASSWD` rule for you and no
  password for your user. An image-built VM often has this shape; after such a
  switch, `sudo`, `rollback` and the undo would all wait for a password that
  doesn't exist.
- **boot:** no boot loader is enabled (a container is exempt).
- **machine:** the new config's root filesystem isn't this machine's: the host
  looks like another machine's.

`--force` skips these checks. The undo that follows a switch, and `piste
rollback`, use the system profile. When the overlay doesn't evaluate,
`piste status` and `piste log` still read that profile (with a warning);
going back then is in [Undo the last switch](backing-out.md#undo-the-last-switch).

The other commands follow the host: `build`, `check`, `dry`, `gendiff` (`nh os`),
`status`, `log`, `drift` and `clean` (system generations too) work as on a Mac.
`piste adopt` doesn't support a NixOS host yet: add its packages to
`piste.packages.extraPackages` by hand. A NixOS machine whose overlay has only a
`user@hostname` home still works as a home-manager one; with both, the NixOS host
wins.

## Plugins

`piste foo` runs `piste-foo` from your PATH when piste has no command `foo`
(and `piste secrets foo` runs `piste-secrets-foo`). A plugin never shadows a
built-in command. It runs with `PISTE_FLAKE` set to the overlay piste would act
on. `piste --help` lists the plugins it finds:

```
$ piste hello
hello from /home/jane/piste/overlays/personal
```

Ship one from your overlay like any package
([Add a package](make-it-yours.md#add-a-package)), in your home:

```nix
home.packages = [
  (pkgs.writeShellScriptBin "piste-hello" ''echo "hello from $PISTE_FLAKE"'')
];
```

## Secrets

Step by step: [Keep a secret](make-it-yours.md#keep-a-secret).

Secrets live encrypted in your overlay (with
[sops](https://github.com/getsops/sops) and an age key), are decrypted on the
machine when it switches, and reach your shell as environment variables.
`piste init` sets them up when you say yes to secrets, and `piste secrets
init` in an existing overlay. `piste.secrets.envVars` maps each variable to
its path in the file ([options.md](options.md)).

`piste secrets status` shows the key, `.sops.yaml` and the secrets files it finds;
`piste secrets edit` with no file opens the only one, or asks which.
`piste secrets updatekeys` re-encrypts every secrets file for `.sops.yaml`'s
recipients, under a new data key each time; run it on a machine that can
already decrypt.

## Agents

To have an agent change your overlay for you, see
[Let an agent do it](make-it-yours.md#let-an-agent-do-it). This section is
about what agents get on your machines.

Your overlay's `agents/` folder is your zone's agent content: skills,
instructions, presets, and third-party plugins and MCP servers in
`agents/agents.toml`. The starter's `home.nix` turns it on:

```nix
piste.agents = {
  enable = true;
  zone = "personal";
  presets = [ "base" "default" ];   # the engine's base preset and yours (agents/presets/default.toml)
  sources.personal-agents = {
    zone = "personal";
    store = ./agents;
    workdir = "${config.piste.checkout}/overlays/personal/agents";
  };
};
```

The engine's `base` preset is lean. Two more are yours to add to `presets`:
`"guard"` blocks destructive commands, dotenv reads and commits that leak a
secret, and `"rtk"` adds the rtk token-saving rewrite (and installs rtk). Both
need `piste agents sync --user` after the switch. After selecting `"rtk"`, run
`rtk init -g` once: Claude's rtk instruction imports `~/.claude/RTK.md`, which
only that command creates, and until then the import points at nothing.

A skill is a folder `agents/skills/<category>/<name>/` with a `SKILL.md`,
and a host gets it when a preset names it: `skills = [ "<name>" ]` in
`agents/presets/default.toml`.
Skills are linked from the checkout, so editing a `SKILL.md` takes effect
without a switch; adding or removing one needs `piste switch`. After a switch,
`piste agents sync --user` installs the plugins and MCP servers for Claude
Code and Codex. [agents-and-mcp.md](agents-and-mcp.md) has the details.
