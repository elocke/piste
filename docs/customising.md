# Customising

- How do I add a package, a Homebrew cask or a font?
- How do I drop something the engine installs or turns on?
- What's in the extras pack, and how do I drop part of it?
- How do I set any other home-manager or nix-darwin option?
- When the engine, a layer and I set the same option, who wins?
- How do I take over one of the engine's pieces with my own copy?
- What does `piste.lib.mkFlake` take?
- How do I add a second machine?
- How do I write a `piste` plugin?
- How do I keep secrets?
- How do I add agent skills and instructions?

Everything here is an edit in your overlay (`~/piste/overlays/<name>/`), then
`piste switch`. A new file must be known to git first (`git add`): builds
only see tracked files ([concepts.md](concepts.md#what-a-build-uses)). Every
`piste.*` option is in [options.md](options.md).

When a build or switch fails, `piste fix` rebuilds what the switch builds,
without activating it, and opens an AI session at the checkout that already
has the error. The agent edits under its CLI's own approval and checks its fix
with `piste fix`. With no AI CLI, or with `PISTE_AI=none`, it only reports.

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

## Add a package

Find its name: `nix search nixpkgs <name>` (on a Mac, `brew search <name>` for
Homebrew). Then add it to a list in your overlay. On every machine: `home.nix`'s `home.packages`, beside
piste. On one Linux machine, its host file's list (the starter's is empty):

```nix
home.packages = with pkgs; [ ollama ];
```

On a Mac, for every user, at the top level of its host file (the starter has
this list too):

```nix
piste.packages.extraPackages = with pkgs; [ ollama ];
```

A Mac app from Homebrew, at the top level of the Mac's file (both lists are in
the starter, empty):

```nix
piste.packages.casks.extra = [ "obsidian" ];
piste.packages.brews.extra = [ "mas" ];   # a formula, when nixpkgs lacks it
```

Add `pkgs` to the file's arguments (`{ user, piste, pkgs, ... }:`) when it
isn't there.

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
applies `piste.overlays.shared`. There is nothing to wire. On a server, trim
it in the host file:

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
`piste.overlays.shared` to its `pkgs`.

## Drop a default

From the package set, by name (`lib.getName`), in the host file (a Mac's top
level, or a Linux host's):

```nix
piste.packages.exclude = [ "docker" "gemini-cli" ];
piste.packages.dev.enable = false;           # a whole category: cli, dev, terminal, fonts, …
piste.packages.casks.exclude = [ "google-chrome" "docker-desktop" ];
```

A program a role turns on, in your home: switch its toggle off.

```nix
piste.roles.base.starship.enable = false;
piste.roles.desktop.alacritty.enable = false;
piste.roles.desktop.ghostty.enable = false;   # Ghostty's settings
```

The toggles ([options.md](options.md)) are base's `shell` (zsh), `git`, `ssh`
and `starship`, dev's `editor` (vim) and `tmux`, and desktop's `alacritty`
and `ghostty`; one switched off turns the engine's piece off. (Today it also
forces the program off with `lib.mkForce`, which a later release may drop, so
don't build on it.) A program with no toggle (direnv, gh), force off
yourself, with `lib` in the file's arguments:

```nix
programs.direnv.enable = lib.mkForce false;
```

zellij has its own switch, `piste.programs.zellij.enable = false;`.

Either way the program's settings go. A program the package set also installs
(alacritty, zellij) stays installed until you add it to
`piste.packages.exclude` too; on a Mac, Ghostty itself is dropped with
`piste.packages.casks.exclude = [ "ghostty" ]`.

A cask you exclude is no longer installed by piste, but one that is already on
the Mac stays there. Homebrew's cleanup is off, so piste never uninstalls an
app. Remove it yourself: `brew uninstall --cask <name>`.

Not sure where something comes from? An agent at the checkout (the
piste-config skill) answers "where does this package come from" with the file
and the line that removes it.

## The extras pack

The engine's defaults are lean on purpose. The `extras` role is the rest:
more tools and settings that are one person's taste rather than everyone's,
off unless a host lists it.

- **Minimal** (the default) gets what the README lists under Opinions: the
  shell, git, editor and terminal basics, the package set, and on a Mac
  Ghostty and the system settings. No extra apps, and none of the pack's
  aliases or settings.
- **Everything** adds the `extras` role. The table below lists what each part
  brings.
- **To drop a part,** switch its toggle off (below). To drop the pack, take
  `"extras"` out of the host's `roles`.

`piste init` (and `piste init --layer`) asks "Start minimal or with
everything?". Everything adds the role after the host's others in `flake.nix`
(`--extras` answers it in a script, `--extras=false` too):

```nix
hosts."jane@devbox" = { system = "x86_64-linux"; roles = [ "dev" "extras" ]; … };
hosts.jane-mac = { system = "aarch64-darwin"; roles = [ "extras" ]; … };  # a Mac has desktop already
```

Everything loosens one of the defaults' safety settings: `ghostty` lets
programs read the clipboard (the table says how). Drop that part if it isn't
for you. Nothing in the pack throws work away (no `git nuke` or `git discard`
alias, no `fetch.pruneTags`) or writes what is on screen to disk.

Changed your mind? Add `"extras"` to a host's `roles`, or take it out, and
switch (apps already installed stay until `brew uninstall --cask <name>`). A machine added later with `piste host add` (or `piste setup` on a
second machine) takes the roles of the overlay's other hosts of its kind (Mac
or Linux), so `extras` carries over; it prints `Roles from overlays/<name>'s
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

[options.md](options.md) lists every part's toggle, and the part's file says
exactly what it sets. To drop a part, switch its toggle off. A home part goes
where your home settings go (`home.nix`, or a Linux host's file); on a Mac,
`casks` and `macos` are system settings, so they go at the top level of the
host file, not inside `home-manager.users`:

```nix
piste.roles.extras.tmux.enable = false;      # home.nix: no tmux extras anywhere
piste.roles.extras.macos.enable = false;     # hosts/jane-mac.nix, top level
```

A part switched off sets nothing. To keep a part but not one thing in it,
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

Any [home-manager](https://nix-community.github.io/home-manager/options.xhtml)
option goes in your home, any
[nix-darwin](https://nix-darwin.github.io/nix-darwin/manual/) option at a
Mac's top level. Where the engine already sets the value, a plain setting
conflicts:

```
error: The option `home-manager.users.jane.programs.git.settings.push.autoSetupRemote' has conflicting definition values:
- In `…/modules/programs/git.nix': true
- In `…/hosts/jane-mac.nix': false
```

So give yours priority with `lib.mkForce`:

```nix
programs.git.settings.push.autoSetupRemote = lib.mkForce false;   # in your home
```

The engine's macOS settings (`system.defaults.*`) are defaults, so a plain
value, `system.defaults.dock.autohide = false;`, wins at a Mac's top level.

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
| the engine | 1000 | `lib.mkDefault` | the target. The macOS settings (`system.defaults.*`) are there; most of the core program modules (`programs.*`) still set plain values (100), so the same key in your overlay conflicts and needs `lib.mkForce` |
| a pack or a team layer | 950 | `piste.lib.mkPackDefault` | same |
| you | 100 | a plain value | same |
| force | 50 | `lib.mkForce` | same, and it keeps working |

Two more exceptions today: a role toggle switched off forces its program off
with `lib.mkForce` ([Drop a default](#drop-a-default)), and the container role
sets `programs.git.settings.commit.gpgsign` at 900, so neither a pack nor the
engine's `mkDefault` beats it there.

If you write a pack or a team layer:

- Set scalars at your tier: `inputs.piste.lib.mkPackDefault value`, from your
  own flake's `piste` input. A person's plain value then wins over yours, and
  yours over the engine's `mkDefault`.
- Leave lists plain, so a person's entries go beside yours (a list at 950
  would lose to theirs whole).
- Never `mkDefault` (or `mkPackDefault`) a whole attribute set: a person who
  sets one key of it plainly would drop every other key you set.

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

On Linux it all goes in your home. On a Mac, a home piece (every `core` piece,
and extras `shell`, `editor`, `tmux` and `ghostty`) goes in the home side: in
`home.nix`, or inside `home-manager.users.${user} = { … };` in the Mac's host
file. Extras `casks` and `macos` are nix-darwin modules, so they go at the top
level of the Mac's host file, beside `piste.packages.*`:

```nix
# hosts/jane-mac.nix, top level
disabledModules = [ { key = "pack:extras/casks"; } ];
imports = [ ../modules/casks.nix ];   # its key changed to local:extras/casks
```

## `mkFlake`'s arguments

| Argument | What it is | Default |
|----------|------------|---------|
| `inputs`, `self` | the overlay's flake inputs and the overlay itself | (required) |
| `user` | your login name, the same on every host | (required) |
| `zone` | the hosts' trust zone ([concepts.md](concepts.md#zones)) | (required) |
| `hosts` | the machines, by name (below) | (required) |
| `extraSpecialArgs` | module arguments for every host: an attrset, or a function of the system | `{ }` |
| `homebrew` | merged into `piste.homebrew`, e.g. `{ extraTaps = { … }; }` (taps as `flake = false` inputs) | `{ }` |
| `packages` | the overlay's own packages, beside the engine's: an attrset or a function of the system | `{ }` |
| `agents` | the zone's agents folder (`./agents`), its skills and instructions | `null` |
| `bundle` | `{ presets = [ … ]; }`: the zone's container agent bundle (needs `agents`) | `null` |
| `devcontainer` | `{ name; modules ? [ ]; extraSpecialArgs ? { }; }`: a container image ([devcontainer.md](devcontainer.md)) | `null` |
| `checks` | the overlay's own checks, beside the zone checks | `{ }` |
| `systems` | what packages and checks are made for (every host's system is added) | `[ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ]` |

A host is `{ system; modules ? [ ]; roles ? [ ]; overlays ? [ ]; aliases ? [ ]; }`:

- `system`: `aarch64-darwin` (a Mac with Apple silicon, named by its
  hostname: `scutil --get LocalHostName`), `x86_64-linux` or `aarch64-linux`
  (a home, named `user@hostname`).
- `modules`: its own files; `roles`: on Linux, the roles it takes (a Mac
  always has `desktop`), and on either, `extras` ([the extras
  pack](#the-extras-pack)); `overlays`: nixpkgs overlays for it; `aliases`: on a
  Mac, other hostnames for the same machine.

`mkFlake` checks what it's given and stops with one line per mistake, each
starting `mkFlake:`. Among them:

- a host key it doesn't know: `mkFlake: host jane-mac: unknown key role (a
  host takes system, modules, overlays, roles, aliases)`;
- a role that isn't one: `mkFlake: host jane@devbox: unknown role vscode (a
  role is base, dev, desktop, container or extras)`. The names of
  `homeManagerModules` that aren't roles (`vscode`, `sops`, `agents`,
  `packages`, `secrets`) are refused; import such a module in your home
  instead (`imports = [ piste.homeManagerModules.vscode ];`);
- a key `devcontainer` or `bundle` doesn't take: `mkFlake: devcontainer:
  unknown key overlays (devcontainer takes name, modules, extraSpecialArgs)`,
  `mkFlake: bundle: unknown key zone (bundle takes presets)`.

`mkFlake` wires every host, so its modules needn't: the home gets
`piste.homeManagerModules.sops` (sops-nix's module, re-exported) and
`piste.homeManagerModules.agents`, the machine gets the piste CLI
(`piste.packages.piste`; set it to `null` to install your own), and `user`
and `piste` are module arguments everywhere, a Mac's home modules
included. `mkFlake` returns `darwinConfigurations`,
`homeConfigurations`, `packages` (the engine's and yours), `apps` and
`checks` (the zone checks and yours). You don't have to use it: the
modules it wires are `piste.darwinModules.*` and
`piste.homeManagerModules.*`, and `lib/mk-flake.nix` shows how.

## A second machine

On the new machine:

```bash
nix run github:elocke/piste/stable -- setup <your overlay's git url>
```

Use the overlay's https URL (`https://github.com/<you>/<overlay>.git`): a new
machine has no SSH key for GitHub yet (`piste github` makes one after the first
switch). For a private repo, log in first with
`nix run nixpkgs#gh -- auth login --web --git-protocol https`; when a github.com
clone fails, setup says so.

When the overlay has no host for it, setup asks "Add this machine to it?" and
runs `piste host add`. That writes `hosts/<hostname>.nix` and its entry in
`flake.nix` (staged), with the roles of the overlay's other hosts of its kind
(the ones they all have, if they differ), else the ones `init` would pick
(`--role` to choose).
If the overlay has secrets, it also makes this machine's age key and adds it to
`.sops.yaml`. It then prints what's left:

1. Commit and push here (the commit takes only host add's files).
2. On a machine that can read the secrets:
   `git pull && piste secrets updatekeys && git commit -m 'Secrets for <host>' -- '*.sops.yaml' && git push`.
3. Back here, pull and run setup again.

In a checkout, `piste host add [--role r]` does the same and asks nothing.

Removing a machine is by hand: delete its host (its entry in `flake.nix` and
its file) and its key in `.sops.yaml` (under `keys:` and in every `age:`
list, anchored by its hostname), run `piste secrets updatekeys`, and change the
secret values, since that machine has read them. updatekeys also puts every
file under a new data key, so the old one the machine had reads nothing new.
If it had a GitHub key (`piste github`), revoke it: `gh ssh-key list`, then
`gh ssh-key delete <id>`.

## Plugins

`piste foo` runs `piste-foo` from your PATH when piste has no command `foo`
(and `piste secrets foo` runs `piste-secrets-foo`). A plugin never shadows a
built-in command. It runs with `PISTE_FLAKE` set to the overlay piste would act
on. `piste --help` lists the plugins it finds:

```
$ piste hello
hello from /home/jane/piste/overlays/personal
```

Ship one from your overlay like any package, in your home:

```nix
home.packages = [
  (pkgs.writeShellScriptBin "piste-hello" ''echo "hello from $PISTE_FLAKE"'')
];
```

## Secrets

Secrets live encrypted in your overlay (with
[sops](https://github.com/getsops/sops) and an age key), are decrypted on the
machine when it switches, and reach your shell as environment variables.
`piste init` sets them up when you say yes to secrets; in an existing overlay:

```
$ piste secrets init
Created age key: /home/jane/.config/sops/age/keys.txt
  Public key: age1…
Created /home/jane/piste/overlays/personal/.sops.yaml

Secrets infrastructure ready.
  Next: add secrets with piste secrets edit
```

The age key (`~/.config/sops/age/keys.txt`) stays out of git. Keep a copy
somewhere safe, such as a password manager: without a key the secrets were
encrypted for, nothing can decrypt them. Never put a secret's value in a
`.nix` file: the Nix store is readable by every user on the machine. Secrets
belong in the encrypted file. Also know that an exported variable reaches
every process you start from the shell, so export only what you need. Start a file
with a placeholder (never a real value on disk), then let piste encrypt it and
open it in your editor:

```bash
printf 'anthropic:\n  api_key: change-me\n' > secrets/env/api-keys.sops.yaml
piste secrets edit secrets/env/api-keys.sops.yaml   # encrypts it, then opens it: replace change-me
```

Then turn them on in `home.nix` (the starter has this block, commented out):

```nix
piste.secrets = {
  enable = true;
  sopsFile = ./secrets/env/api-keys.sops.yaml;
  envVars.ANTHROPIC_API_KEY = "anthropic/api_key";   # variable = path in the file
};
```

After `piste switch`, a new shell has `ANTHROPIC_API_KEY`. `piste secrets
status` shows the key, `.sops.yaml` and the secrets files it finds;
`piste secrets edit` with no file opens the only one, or asks which.
`piste secrets updatekeys` re-encrypts every secrets file for `.sops.yaml`'s
recipients, under a new data key each time; run it on a machine that can
already decrypt.

## Agents

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
