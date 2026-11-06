# Packs (design)

> **Status: a design for v0.2 and later. Not shipped.** Nothing here is
> promised until it appears in [options.md](../options.md),
> [commands.md](../commands.md) or [upgrading.md](../upgrading.md). Names,
> keys and numbers may still change before then. What v0.1.0 already ships
> is listed in [What v0.1.0 already has](#what-v010-already-has).

- What is a pack, and how is it different from an overlay?
- How will I use someone else's pack, and change what it does?
- Who wins when the engine, a pack and my overlay set the same option?
- What changes in my overlay when packs land?
- What is planned for organisations, and what isn't?

Part 1 is for anyone with an overlay. Part 2 is for organisations that roll
piste out to many people; you can stop reading before it.

## Why

The engine has opinions: a prompt, git settings, aliases, terminal settings,
Mac apps. Today they come in roles, and to change one you often need
`lib.mkForce`, or you copy a file and hope your copy and the engine's don't
both apply. Teams share settings through layers, which are a preview with
their own rules.

Packs make all of that one thing. The engine's own opinions become two packs,
`core` and `extras`. A team's or a stranger's shared settings are packs too,
and they compose with the engine's the same way. Whatever a pack sets, a plain
value in your overlay wins. You can turn off any piece of any pack, replace
it with another pack's, or take it over with your own copy, all without
`mkForce`.

There is no registry, resolver or installer. A pack is a flake output, you
add it as a flake input, and `flake.lock` pins it. Who wins is the module
system's priorities, and two packs that disagree get the module system's own
error.

# Part 1: packs

## Five words

| Word | What it is |
|------|------------|
| **pack** | A flake output, `pistePacks.<name>`: a small descriptor listing the pack's pieces, plus a few optional extras. The engine's own are `core` and `extras`. |
| **piece** | One part of a pack you can turn on and off: a prompt, git settings, extra Mac apps. A module for each side it has, with one switch. (Today's docs call the extras ones parts.) |
| **tweak** | Set an option plainly in your overlay. Yours wins. |
| **swap** | Turn a piece off and use another pack's piece instead. |
| **take over** | Turn a piece off by its key and import your own copy of it. |

Two more words are part of the contract a pack author needs:

- **side**: which evaluation a piece's module goes into. The *home side* is
  home-manager (a Linux home, a Mac's home, a container image). The *darwin
  side* is the Mac's system (nix-darwin). A piece can have both.
- **key**: a piece's public identity, the `key` attribute `mkFlake` gives its
  modules, `pack:<pack>/<piece>` (`pack:core/starship`). Turning a piece off by
  key and taking it over use it. File paths inside a pack are not its API.

Zones don't change ([concepts.md](../concepts.md#zones)), and roles stay as
short names for selections of `core` ([Roles](#roles-become-short-names)).

## What v0.1.0 already has

v0.1.0, the first public release, is the baseline phase 1 builds on. It ships
four small changes made for packs, so that phase 1 breaks nothing beyond what
its [Upgrading notes](#the-upgrading-notes-v02-will-need) name:

- **Piece keys.** Every engine opinion file has a key:
  `pack:core/<piece>` for `modules/programs/{shell,git,ssh,starship,herdr,editor,tmux,uv,zellij,alacritty,ghostty,vscode}.nix`,
  and `pack:extras/<piece>` for the six extras files. The
  [take-over recipe](../customising.md#take-over-a-piece) uses them. Mechanism
  modules (packages, agents, secrets, checkout, github, homebrew, platforms)
  keep their path identity: they aren't pieces.
- **`piste.lib.mkPackDefault`**, `lib.mkOverride 950`: the priority a pack
  sets its values at ([Who wins](#who-wins)).
- **`mkFlake` refuses** a role that isn't `base`, `dev`, `desktop`,
  `container` or `extras`, and an unknown key in its `devcontainer` and
  `bundle` arguments. Phase 1 lowers roles to selections and adds `packs` to
  `devcontainer`, so nothing may be passing silently today.
- **Reserved names**: among them the option prefix `piste.packs.*`, the flake
  output `pistePacks`, the `packs` argument and host key, the lib name
  `mkPackChecks`, the key prefixes `pack:` and `local:`, the check prefix
  `pack-*`, the command `pack` and the pack names `core` and `extras`. The full
  list is in [upgrading.md](../upgrading.md#reserved-names), which also says
  that paths under `modules/`, the modules inside a role, and what
  `darwinModules.platform` imports are not API.

Nothing in that list changes a machine. v0.1.0 also has the team layers
preview, which phase 1 turns into packs: a layer's `piste-layer.toml` (its
`zone` and the names of its `[secrets]`), its `pisteLayer` output (its
`agents` root), its `template/`, and `piste init --layer`.

## Phase 1: packs (v0.2)

Phase 1 is committed. It ships in a minor release with the Upgrading notes
listed at the end of this part.

### The descriptor

A pack is an attribute of a flake's `pistePacks` output:

```nix
# flake.nix of a pack repo
{
  inputs.piste.url = "github:elocke/piste/v0.2.0";

  outputs = { self, piste, ... }: {
    pistePacks.acme = {
      name = "acme";                                   # = the attribute name
      source = self.outPath;                           # with name, the pack's identity
      description = "Acme's prompt, git settings and Mac apps";
      pieces = {
        prompt = { description = "Acme's starship prompt";
                   home = import ./home/prompt.nix { inherit piste; }; };  # closes over the pack's piste
        git = { description = "Acme's git settings"; home = ./home/git.nix; };
        apps = { description = "Acme's Mac apps"; darwin = ./darwin/apps.nix; };
      };
      overlays = [ (import ./pkgs/overlay.nix) ];      # nixpkgs overlays for the host's pkgs
      secrets = { ACME_TOKEN = "acme/token"; };        # names only: env var = sops key path
      template = ./template;                           # a starter for an overlay on this pack
      zone = "work";                                   # a team's pack: hosts in zone work only
      agents = ./agents;                               # the team's agent content, in that zone
    };

    checks.x86_64-linux = piste.lib.mkPackChecks {
      pack = self.pistePacks.acme;
      system = "x86_64-linux";
    };
  };
}
```

| Key | Required | What it is |
|-----|----------|------------|
| `name` | yes | The pack's name: lowercase letters, digits and dashes, equal to its attribute under `pistePacks`. It owns `piste.packs.<name>.*` and the keys `pack:<name>/*`. |
| `source` | yes | `self.outPath` of the flake that exports the pack. With `name`, it is the pack's identity (see [Choosing packs](#choosing-packs)). |
| `description` | yes | One line, shown by `piste pack list` and `show`. |
| `pieces` | yes | `<piece> = { description; home ? null; darwin ? null; }`, with at least one side. A side is a module or a path to one: `home` a home-manager module, `darwin` a nix-darwin module (a Linux host ignores it). |
| `overlays` | no | nixpkgs overlays, for the selecting hosts' `pkgs` and an image's. |
| `secrets` | no | The secrets the pack's pieces read, by name: env var = sops key path (what `piste-layer.toml`'s `[secrets]` holds today). `mkFlake` adds them to `piste.secrets.envVars` on every host that selects the pack. Values stay in the overlay's encrypted files. |
| `template` | no | A starter overlay, as a layer's `template/` is today. `piste init --pack` reads it. |
| `zone` | no | The pack's zone (what `piste-layer.toml`'s `zone` is today). Only hosts of that zone may select it; with none, any host may. |
| `agents` | no | The pack's agent root (what `pisteLayer.agents` is today). It becomes one of the host zone's sources, as a layer's does today. In phase 1 it needs `zone`. |

`mkFlake` wraps each side of each piece in a module with the key
`pack:<pack>/<piece>`, and declares the piece's switch,
`piste.packs.<pack>.<piece>.enable` (default on), on each side the piece has.
The switch is declared beside the keyed module, not inside it, so a piece
turned off by key keeps its switch. A small `piste.lib` helper does the
wrapping, so an overlay that wires its hosts by hand can do the same. The
`pieces` table is also the list `mkPackChecks`, `piste pack show` and
`piste pack explain` work from. A pack with no pieces is an error.

Phase 2 adds one key, `managed`. A descriptor with any key the engine doesn't
know makes `mkFlake` throw, so a pack written for a newer engine fails loudly
on an older one. Keys are only ever added; an existing key never changes
shape. That is the versioning story: there is no `api` or `engine.min` field,
and a pack repo tests itself against the engine releases it claims to support
with `mkPackChecks`.

### A piece

A piece's side is one module file. It reads the switch `mkFlake` declares,
wraps everything it sets in `lib.mkIf`, sets scalars at the pack tier and
leaves lists plain. A side that needs the pack's own inputs is a function of
them, applied in the `pieces` table (`import ./home/prompt.nix { inherit piste; }`
above); one that doesn't is a plain path (`./home/git.nix`).

```nix
# home/prompt.nix in the acme pack: pieces.prompt.home
{ piste }:                                   # the pack's own engine input
{ config, lib, pkgs, ... }:
{
  _file = ./prompt.nix;                      # a side given as a value: errors name this file

  config = lib.mkIf config.piste.packs.acme.prompt.enable {    # the switch mkFlake declares
    programs.starship.enable = piste.lib.mkPackDefault true;
    programs.starship.settings.palette = piste.lib.mkPackDefault "gruvbox";    # a scalar, at 950
    programs.starship.settings.palettes.gruvbox = { fg = piste.lib.mkPackDefault "#ebdbb2"; };
    programs.zsh.initContent = lib.mkOrder 1100 ''eval "$(acme completion zsh)"'';
    home.packages = [ pkgs.acme-cli ];                            # lists plain (acme-cli from the pack's overlay)
  };
}
```

The rules, which `mkPackChecks` checks where it can:

- **No key of its own.** `mkFlake` gives the key. A side given as a value (as
  above) sets `_file`, so conflict errors and `piste pack explain` name it.
- **The switch is `mkFlake`'s.** A piece reads
  `config.piste.packs.<pack>.<piece>.enable` and doesn't declare it. Its other
  options, if any, go under `piste.packs.<pack>.<piece>.*`, declared by the
  piece outside `mkIf`.
- **Everything it sets under `lib.mkIf` on its switch**, so a switch turned
  off drops every definition the piece makes.
- **Scalars at the pack tier, on the leaf**: `piste.lib.mkPackDefault "x"` on
  `settings.palette`, never on a whole attribute set. A default on a whole set
  is replaced whole by any plain definition of the set, so a person who sets
  one key would lose all the others.
- **Lists and attribute sets plain**: lists add up with everyone else's, and
  sets merge key by key.
- **Text in a documented band** with `lib.mkOrder` (below), never plain, so
  where it lands in the file doesn't depend on module order.
- **Static imports, no `../`**: a side imports only files inside its own
  directory, and imports nothing conditionally.
- **No `lib.mkForce`** on an option another module may set.
- **No reliance on `extraSpecialArgs`.** Those don't reach the home modules
  inside a Mac, and they are the consumer's, not the pack's. A pack closes over
  its own inputs (the `{ piste }:` above). A piece takes only the standard
  module arguments (`config`, `lib`, `pkgs`, `options`, and on the home side
  `osConfig`, read as `args.osConfig or null`).
- **To read another pack's switch**, use
  `config.piste.packs.<other>.<piece>.enable or false`: that pack may not be
  selected, and then its options don't exist.

The text bands for `programs.zsh.initContent`, tmux's `extraConfig` and the
like:

| Order | Who |
|-------|-----|
| 500 (`lib.mkBefore`) | the engine's early init |
| 1000 (a plain value) | the engine's body, and your own lines |
| 1100 to 1400 | packs |
| 1500 (`lib.mkAfter`) | what must come last: the extras pack's command shadows, zellij's autostart |

### Sides

A piece's home side and darwin side are two modules in two evaluations, under
the same key. The rules:

- **One switch, and on a Mac the system's decides.** The home side's switch
  defaults to the darwin side's through `osConfig`
  (`default = osConfig.piste.packs.<pack>.<piece>.enable or true`), so
  turning a piece off at the Mac's top level turns off both sides. On a Linux
  home `osConfig` is null, and the switch defaults on.
- **A darwin side never writes into `home-manager.users.<user>`.** If a Mac
  system setting needs a home effect, the piece has a home side too, and that
  home side reads the system through `osConfig` (null on a Linux home). The
  extras pack's `casks` piece does this in phase 1: today its darwin module
  writes VS Code's file associations into the home.
- **Switch off and take over by side.** A home piece is switched off or taken
  over in your home (`home.nix`, or inside `home-manager.users.<user>` on a
  Mac). A piece with a darwin side is switched off at the Mac's top level,
  which turns its home side off too; taking over both sides takes a
  `disabledModules` entry in each.
- **Images run no activation.** A container image built by
  `mkDevcontainer` gets `home.packages` and `home.file` and nothing from
  `home.activation`. A piece that relies on activation says so in its
  description, and `mkPackChecks`' `pack-activation` reports it, so an image
  author knows before building one.

### Choosing packs

`mkFlake` takes `packs` as a top-level argument (every host) and as a host key
(that host):

```nix
piste.lib.mkFlake {
  inherit inputs self;
  user = "jane";
  zone = "work";
  packs = [ inputs.acme.pistePacks.acme ];          # every host

  hosts."jane@devbox" = {
    system = "x86_64-linux";
    roles = [ "dev" ];
    packs = [ "extras" ];                           # this host too
    modules = [ ./hosts/devbox.nix ];
  };

  devcontainer = { name = "acme-dev"; packs = [ inputs.acme.pistePacks.acme ]; };
};
```

An entry is a descriptor, or the string `"extras"` for the engine's extras
pack. `core` isn't listed: a host's role says how much of it the host gets.
A host may select a pack whose `zone` is unset or equal to the overlay's
`zone`; any other makes `mkFlake` throw.

`mkFlake` de-duplicates the selection by name and source: a pack listed at
the top and on a host, or reached twice through `follows`, has the same name
and the same `source`, and is imported once. Two packs with the same name and
different sources make `mkFlake` throw, naming both sources. Without that
check one would silently replace the other: their pieces share keys
(`pack:<name>/<piece>`), and the module system imports one module per key.
`mkFlake` never compares descriptors, which hold functions; it compares the
two strings.

Only selected packs are imported. A pack's options exist on a host only when
the host selected it, so `config.piste.packs ? acme` means "this host uses
acme", the same way option existence works for roles today.

Where `mkFlake` puts each side:

- **Home side**: in the slot roles use today, after the engine's role modules
  and before the host's own modules. On a Mac that is
  `home-manager.users.<user>.imports`; on Linux, the home configuration's
  module list.
- **Darwin side**: in the slot `roleDarwinModules` uses today, before the
  host's own modules.
- **Overlays**: into that host's `pkgs`, after `overlays.shared` and before
  the host's own `overlays`, each wrapped in `once "pack-<name>"`. Overlays
  must be known before any module is evaluated; this is why packs are chosen
  in `mkFlake` and not with an option.
- **Secrets and agents**: the pack's `secrets` into the home's
  `piste.secrets.envVars`, and its `agents` root into the zone's sources.
- **An image**: `devcontainer.packs` gives the image the home sides of those
  packs (an image has no darwin side), and `mkDevcontainer` gets their
  overlays. A team's image takes the team pack this way, in place of
  `devcontainer.modules`.

The order of the `packs` list decides only where list entries and same-band
text land. It never decides which value wins.

### Roles become short names

`roles` stays, and stays the way a host says how much of `core` it gets:

| Role | Selects |
|------|---------|
| `base` | `core`'s shell, git, ssh, starship and herdr (off until switched on) |
| `dev` | `base`'s pieces, plus editor, tmux, uv and zellij (off) |
| `desktop` | `dev`'s pieces, plus alacritty and ghostty, with zellij on |
| `container` | `dev`'s pieces, plus the image trims, with zellij on and commit signing off |
| `extras` | the `extras` pack: the same as `packs = [ "extras" ]` |

A Mac always gets the `desktop` selection and `core`'s darwin pieces, as it
gets `desktop` from its platform module today (whether a Mac may skip the
desktop pieces is an [open question](#open-questions)). Roles are never
extended to third-party packs: `roles = [ "acme" ]` throws, as it does since
v0.1.0.

### Who wins

Every definition carries a priority; the lower number wins. Each publisher
has one tier:

| Tier | Priority | Written as | Who |
|------|----------|------------|-----|
| option default | 1500 | the option's `default` | home-manager, nix-darwin |
| engine | 1000 | `lib.mkDefault` | `core` and `extras` alike |
| pack | 950 | `piste.lib.mkPackDefault` | any other pack |
| you | 100 | a plain value | your overlay |
| force | 50 | `lib.mkForce` | still works, and is promised to keep working |

Today (v0.1) is not there yet: most of `core`'s program settings are plain
values (100), a role's switch turned off uses `mkForce`, and the container
role sets commit signing at 900. The extras pack and the Mac's
`system.defaults` are already `mkDefault`. Phase 1 moves the rest
([The engine as packs](#the-engine-as-packs)).

Three cases `mkPackChecks` holds every pack to, because people change them
first:

```nix
# all plain, in your home; each wins over core (1000) and any pack (950)
programs.starship.settings.palette = "nord";
programs.starship.settings.format = "$directory$git_branch$character";
programs.git.settings.push.autoSetupRemote = false;   # no lib.mkForce any more
programs.zsh.history.size = 10000;
```

**Two packs that disagree** on a scalar at the same tier get the module
system's error, which names both files:

```
error: The option `programs.starship.settings.palette' has conflicting definition values:
- In `/nix/store/…-source/home/prompt.nix': "gruvbox"
- In `/nix/store/…-source/home/theme.nix': "dracula"
```

You settle it with a plain value of your own, or by switching one of the two
pieces off. `piste pack explain` shows every definition of an option, with
its pack, piece and priority, finding the piece from the definition's file
through the packs' `pieces` tables:

```
$ piste pack explain programs.starship.settings.palette
programs.starship.settings.palette = "nord"
   100  you          overlays/personal/home.nix
   950  acme         pack:acme/prompt   home/prompt.nix
  1000  core         pack:core/starship modules/programs/starship.nix
```

`core` and `extras` never set the same scalar differently: that is the
engine's own check, not something you meet.

**What you can and can't remove.** A scalar you override. An attribute set
merges key by key, so you override one key and the rest stay. A list adds
up: you can't take one entry out of a list another module adds, except where
the engine has an exclude option (`piste.packages.exclude`,
`piste.packages.casks.exclude`) or by switching off the piece that adds it. A
text option (`initContent`) concatenates: to drop a piece's lines, switch the
piece off or take it over.

### Tweak, swap, take over

**Tweak**: set it plainly, anywhere you'd set any option.

```nix
programs.starship.settings.palette = "nord";
```

**Swap**: switch the engine's piece off and select another pack's.

```nix
# flake.nix: hosts."jane@devbox".packs = [ inputs.acme.pistePacks.acme ];
piste.packs.core.starship.enable = false;           # in your home
```

In v0.2 a switch turned off drops every definition the piece makes. It no
longer forces the program off: acme's prompt piece turns starship on itself,
and so can you, with your own settings.

**Take over**: turn the piece off by its key and import your own copy.

```nix
# in your home (on a Mac, inside home-manager.users.<user> for a home piece)
disabledModules = [ { key = "pack:core/starship"; } ];
imports = [ ./modules/starship.nix ];
```

- Copy the piece's file (and any file it imports, such as herdr's
  `herdr-app-settings.nix`). In v0.2 the file has no key line, since `mkFlake`
  gives the key, so your copy needs none; a copy made in v0.1.0, keyed
  `local:core/starship`, works as it is.
- The switch stays: `mkFlake` declares it outside the keyed module, so your
  copy reads `piste.packs.core.starship.enable` as the original did. Keep the
  piece's other option declarations in your copy; other modules may set them.
- `disabledModules` takes the attribute-set form. A bare string is read as a
  path under nixpkgs' modules and silently matches nothing.

The recipe already works in v0.1.0 with the v0.1 file layout
([customising.md](../customising.md#take-over-a-piece)). `piste eject`
(phase 4) would do the copy for you.

### The engine as packs

Phase 1 re-expresses the engine's opinions as `core` and `extras`:

- **Two descriptors**, with `pieces` tables like any pack's. The `key` lines
  in the 18 files go, since `mkFlake`'s wrapping gives the same keys.
- **Every piece gates itself** with `lib.mkIf` on
  `piste.packs.<pack>.<piece>.enable`, and the roles' `mkForce` blocks go.
- **The leaf `mkDefault` sweep**: every scalar a `core` piece sets moves to
  `lib.mkDefault` on its leaf (starship, git, shell, tmux, editor, alacritty,
  ssh; `enable = true` included). Lists stay plain.
- **New pieces** where a program has no switch today: `direnv` and `gh` (now
  inside shell and git), and `macos`, the Mac's `system.defaults` moved out of
  the platform module into a darwin piece of `core`.
- **Ghostty's config becomes settings**: key/value options in place of one
  text file, so you can change one setting without replacing the file.
- **Text moves to the bands** above: zsh's init, tmux's config.
- **Container signing**: the container selection's commit-signing override
  (900 today) becomes an option of the git piece that the container selection
  sets, so `core` has one definition of it.
- **The extras casks piece gets a home side** reading `osConfig`, in place of
  its darwin module writing into the home. Its switch is two-sided
  ([Sides](#sides)).

The toggles move to the packs' namespaces with `mkRenamedOptionModule`, each
on the side it is set today: `piste.roles.extras.casks.enable` and
`.macos.enable`, set at a Mac's top level, rename to the darwin side's
switches. The old names keep working, with a warning, for a release or more,
as [upgrading.md](../upgrading.md#what-a-release-promises) promises:

| v0.1 | v0.2 |
|------|------|
| `piste.roles.base.{shell,git,ssh,starship}.enable` | `piste.packs.core.{shell,git,ssh,starship}.enable` |
| `piste.roles.dev.{editor,tmux}.enable` | `piste.packs.core.{editor,tmux}.enable` |
| `piste.roles.desktop.{alacritty,ghostty}.enable` | `piste.packs.core.{alacritty,ghostty}.enable` |
| `piste.roles.extras.<piece>.enable` | `piste.packs.extras.<piece>.enable` |
| `piste.roles.extras.tmux.resurrectExtraConfig` | `piste.packs.extras.tmux.resurrectExtraConfig` |

Phase 1 also decides where `piste.programs.{zellij,herdr}.*`,
`piste.roles.container.mutableFiles` and the VS Code module
(`homeManagerModules.vscode`, in no role today) go. The rename modules of a
role are imported with that role's selection, so `config.piste.roles ? dev`
keeps meaning "this host has dev".

### `mkPackChecks`

```nix
checks.x86_64-linux = piste.lib.mkPackChecks { pack = self.pistePacks.acme; system = "x86_64-linux"; };
```

It returns checks named `pack-*`:

- **`pack-eval`**: the pack evaluates on a synthetic Linux home and a
  synthetic Mac (evaluated, not built), alone and beside `core` and `extras`.
- **`pack-yields`**: a person's plain value wins on the cases above
  (starship's palette and format, git's `push.autoSetupRemote`, zsh's history
  size).
- **`pack-hygiene`**: the descriptor's `name` matches its attribute and it
  lists at least one piece, each with a description and a side; no side sets a
  key of its own, imports through `../` or uses `mkForce`; and turning each
  switch off removes the piece's definitions.
- **`pack-activation`**: reports the pieces whose home side uses
  `home.activation`, which doesn't run in an image (`devcontainer.packs`). It
  reports; it doesn't fail.

A pack repo runs them against each engine release it claims, by overriding its
`piste` input in CI. The engine runs them on `core` and `extras`.

### The CLI

- `piste pack list`: the packs each host of the overlay selects, with where
  each comes from and the revision `flake.lock` pins.
- `piste pack show <name>`: its description, and its pieces from the
  `pieces` table, each with its description, sides and switch.
- `piste pack explain <option>`: every definition of the option on this host,
  with pack, piece, priority and file, and the value that wins. It maps each
  definition's file back to a piece through the `pieces` tables.
- `piste init --pack <flake-ref>#<name>` starts an overlay on
  `pistePacks.<name>` of a flake that exports several. `--layer <url>`, the
  preview's spelling, stays through v0.2 and takes the flake's only pack; with
  several, it stops and names them. Either reads the descriptor with
  `nix eval` (the CLI already runs nix), not `piste-layer.toml`: the overlay's
  zone from `zone`, the starter from `template`, and from `secrets` the names
  it writes into the overlay's empty sops file, the same names `mkFlake`
  exports.
- `piste init` and `piste host add` write `packs` beside `roles` in
  `flake.nix` (the same text patching they use for `roles` today), and
  `host add` copies the other hosts' `packs` as it copies their roles.
- The CLI reads a host's selection from what `mkFlake` records of it, not
  from which options exist, since renamed options exist on every host that
  has the role.

### Sharing a pack, and trusting one

You publish a pack by pushing its flake. You use one by adding it as an input
that follows your engine, and selecting it:

```nix
inputs.acme.url = "github:acme/piste-pack";
inputs.acme.inputs.piste.follows = "piste";
```

`flake.lock` pins it, and `piste update` (or `piste update acme`) moves it.
Read a pack before you add it. A pack can do what any module can: run
activation scripts as you, change the Mac's system settings (applied as
root), add Homebrew taps and casks, and replace any package through its
overlays. Run `piste leaks` on a pack repo before you push it, as on the
engine.

Where things belong:

| Where | What |
|-------|------|
| the engine (public) | the contract, `core`, `extras`, `mkPackDefault`, `mkPackChecks` |
| a public pack repo | generic pieces several overlays would otherwise copy |
| an organisation's overlay | the organisation's packs (today's team layer) |
| your overlay | what is bound to you or your machines: identity, hosts, secrets |

### Done when

- A team layer exports `pistePacks.<team>` (with `zone`, `agents`, `secrets`
  and `template`), its members' hosts select it with `packs`, and the team's
  image with `devcontainer.packs`, instead of importing its modules by hand.
  `piste-layer.toml` is no longer read.
- Generic pieces that two overlays both carry today live in one public pack
  that both select.
- Nothing else moves. Every host of the maintainer's reference overlays (Mac
  systems and Linux homes) and of the engine's fixtures (the template, the
  layer fixture, the fixture Mac), evaluated before and after with
  `--override-input piste git+file://<checkout>`, gives the same drvPath
  (`system.drvPath`, `activationPackage.drvPath`), or a `nix-diff` that shows
  only the engine's source path. The exceptions are the changes the Upgrading
  notes name: Ghostty's config rendered from settings, and zsh's and tmux's
  text moved into the bands (the same lines; their order may change).
  `tests/flake-drvs.sh` lists an overlay's drvPaths for the comparison.
- `mkPackChecks` passes on `core`, `extras` and the team pack, and the team
  pack's own `nix flake check` passes for a member's overlay.

### The Upgrading notes v0.2 will need

- **A switch turned off drops the piece's settings**, and no longer forces the
  program off. `programs.<x>.enable = true` of yours now takes effect beside a
  switched-off piece.
- **`piste.roles.*` is now `piste.packs.core.*` and `piste.packs.extras.*`.**
  The old names warn; rename them. `piste.packs.extras.casks.enable` stays at
  a Mac's top level, and turns off the piece's new home side too.
- **The engine's values are now defaults.** A plain value of yours wins
  without `lib.mkForce` (which still works). A `lib.mkDefault` of yours on an
  option the engine sets now conflicts with the engine's, where before it lost
  silently: make it plain.
- **A layer or pack that sets scalars with `lib.mkDefault`** now conflicts
  with the engine's on every key both set. Use `piste.lib.mkPackDefault`
  (950), in every release since v0.1.0. A layer wired by hand keeps working
  otherwise.
- **A pack's or layer's value at 950** that lost to an engine plain value now
  wins.
- **Ghostty's config is now settings.** An override of
  `xdg.configFile."ghostty/config"` conflicts: move it to the settings.
- **zsh's and tmux's text is in bands.** The same lines, but their order may
  change. A line of yours that must come first or last takes `lib.mkBefore`
  or `lib.mkAfter`.
- **An extras part taken over in v0.1.0** declares `piste.roles.extras.<part>.*`,
  which the rename modules now declare too: take those declarations out of
  your copy.
- **Layers are packs.** A layer exports `pistePacks.<name>` (`piste-layer.toml`
  and `pisteLayer` move into the descriptor), and overlays on it select it
  with `packs`.

## Later, when people ask: phase 4

These wait until people ask for them. Taking over already works with the
recipe above.

- `piste eject <pack>/<piece>`: copies the piece's files into the overlay,
  keys the copy `local:<pack>/<piece>`, adds the `disabledModules` entry and the
  import, and records the upstream revision in a header; `--diff` shows what
  upstream changed since, `--undo` goes back.
- `piste pack show` lists what a pack touches: activation scripts, darwin
  system options, casks and taps, overlays, agent content.
- `piste update` prints each pack's changelog between the locked revisions.
- `piste init` asks "Minimal, everything, or pick packs?".
- Maybe: a `trustedPacks` allow list, and exclusive slots (one prompt, one
  `~/.ssh/config`) if conflicts between packs turn out to be common.

# Part 2: for organisations

This part is direction, not a plan. Phases 2 and 3 are built when an
organisation's pilot needs them, and they start small: one group, one
managed file, no merging of managed settings across packs.

## Four more words

| Word | What it is |
|------|------------|
| **group** | A named audience in the organisation's repo: which packs and pieces, which managed settings, and where they go. Assigning people to it (Intune, Entra, Jamf) is the organisation's pipeline, not the engine's. |
| **target** | Where a group lands: a host's starter overlay, a Coder image, WSL, Windows policy through Intune, a Mac MDM profile, the claude.ai console. |
| **render** | Turning a group's managed settings into the files a target delivers. |
| **managed** | Settings an admin delivers, as opposed to defaults. In piste, `piste.managed.*`. (`agents.toml`'s `[policy]` keeps its name and meaning.) |

## Defaults and managed settings

A default is anything a pack's home or darwin side sets. You own your overlay,
so you can always change it, whatever its priority: `mkForce` and assertions
are not locks, and piste never calls them that.

Managed settings are the other kind: Claude Code's `managed-settings.json` and
its relatives, which Claude Code applies above your own settings. They are a
lock only where you don't control their delivery: Intune or MDM, an image's
`/etc`, or the claude.ai console. On a machine you administer yourself,
managed output is advisory, and the docs will say so wherever it appears.

## Phase 2: one group, through Coder and WSL

- **A pack may carry one managed module**, `managed = ./managed.nix` in its
  descriptor. `piste.lib.evalManaged` evaluates it on its own, per group and
  without a host, in its own module class, so a host's home never sees it.
  Its options are `piste.managed.claudeCode.settings` and
  `piste.managed.claudeCode.mcpServers`, freeform so new Claude Code keys pass
  through; `env`, `versions` and `claudeDesktop` may follow.
- **A group** is plain Nix in the organisation's repo (`mkFlake`'s reserved
  `groups` argument): packs, pieces, one managed module, targets. In the first
  version a group has exactly one managed module; two packs in a group that
  both carry one is an error.
- **The first render** is the files for `/etc/claude-code`:
  `managed-settings.d/90-managed.json`, and `managed-mcp.json` only when the
  group opts in (it takes over MCP entirely, as `--managed-mcp` does today).
  The slot is 90 because drop-ins merge in name order and the later file's
  value wins for a single value; the container bundle already writes
  `50-agents.json`.
- **Coder**: an image from `mkDevcontainer`, built from the `container`
  selection plus the group's packs (as `devcontainer.packs` does in phase 1),
  with the render baked into `/etc`. The Coder template and the image's CI
  stay in the organisation's overlay.
- **WSL**: a rootfs built from the same closure, or a full home-manager host
  inside WSL. Which one first depends on what the tenant allows
  ([open questions](#open-questions)).
- **Agent content from any pack**: phase 1 takes `agents` only from a pack
  with a `zone`, as a layer's are today. Phase 2 adopts a public pack's (no
  `zone`) into the host's zone too; a pack's root becomes a source named
  `pack-<name>`, and a zone with `unlisted = "deny"` must allow
  `pack:<name>`.
- **`piste managed render|check`**: render writes a group's files; check
  compares the live files with the render. Checks `mkFlake` adds for groups are
  named `managed-*` and `group-*`.

## Phase 3: Windows, Mac MDM and the console

- **Renderers** for the files Intune, MDM and the console deliver: on Windows,
  an HKLM `.reg` (`HKLM\SOFTWARE\Policies\ClaudeCode`, the settings as one
  JSON string) and a file installer for `C:\Program Files\ClaudeCode`, plus
  Claude Desktop's HKLM values as its ADMX defines them; on a Mac, a
  `.mobileconfig` for Claude Desktop and the managed-settings file; for the
  claude.ai console, org-wide JSON, never with MCP servers. HKCU is never
  rendered: you can write it yourself, so it is no lock.
- **Sources don't stack by default.** Claude Code takes the first managed
  source it finds (server-managed from the console, then MDM or HKLM, then
  the files) unless `managedSourcesBehavior` says otherwise. Rendering the
  same settings to `/etc` and to the console needs that setting, or one
  source per machine.
- **The tenant pipeline** (assigning groups to Entra or Jamf groups, signing
  scripts, packaging) is a plugin in the organisation's overlay, run as
  `piste managed <name>` (`piste-managed-<name>`).
- **Windows is never a host.** `mkFlake` keeps throwing on `*-windows`. The
  roughly 80% of Windows users who need only what Intune already does are out
  of scope: piste reaches the people who do development or AI work, through
  Coder or WSL, and renders files for Intune to deliver.

A possible later step, once a pilot needs packs handed out together to
combine their managed settings: merge rules that can only tighten. Deny lists
add up, a restricting boolean is on if any pack turns it on, the allowed
version window is the overlap of all of them (an empty window fails), and an
`env` key set twice is a conflict. A group would loosen a rule only in its
own definition. None of this is in the first version.

# Part 3: what was decided against, and what's open

## Rejected ideas

- **Importing every pack on every host, switched off, through a generated
  index.** Option existence would stop meaning "in use", which the CLI and
  overlays rely on; every host would pay to evaluate every pack; and a darwin
  switch reading the home could recurse. Switches that `mkFlake` declares only
  for the packs a host selects do the same job.
- **Bundles: named sets of pieces inside a pack (`acme@minimal`).** Roles
  already name `core`'s selections, groups cover organisations, and "bundle"
  is taken by the agent bundle.
- **Exclusive slots, requires and provides in the contract.** The module
  system's conflict error already names both files, and `piste pack explain`
  diagnoses it. Slots can come in phase 4 without changing the contract.
- **The order of `packs` deciding who wins.** A pack's effect would depend on
  where someone listed it, and it would hide the module system's priorities
  and errors. Two packs disagreeing stays an error.
- **A registry, installer, lockfile or marketplace for packs; `api` or
  `engine.min` fields.** Flake inputs and `flake.lock` already fetch and pin.
  Additive keys plus `mkPackChecks` handle compatibility.
- **A tier per pack, or `extras` on a tier of its own.** One tier per
  publisher is enough; overlap between `core` and `extras` is the engine's own
  check.
- **`mkForce` or assertions as locks.** Whoever owns the overlay can remove
  them. Locks exist only where an admin owns delivery.
- **Managed settings evaluated inside each host's home.** They target groups,
  and Windows users have no host; a home-evaluated setting also looks like a
  lock when it isn't one.
- **Accepting `packs` in `mkFlake`, or renaming `piste.roles.*`, before v0.2.**
  That would freeze a shape nobody has used. `mkFlake`'s arguments and host
  keys are closed, so adding them later breaks no one, and renames go through
  `mkRenamedOptionModule`.
- **The `mkDefault` sweep before v0.2.** It changes who wins for values at 950
  and for an overlay's `mkDefault`, so it isn't a pure shim; it ships with the
  `mkIf` gating under one Upgrading note.
- **No shims at all.** Keys can't be added later without silently breaking a
  path-based `disabledModules`, and the pack tier had to be named before
  packs were written at `mkDefault`.
- **Windows or WSL as `mkFlake` systems, or empty Windows outputs now.**
  Windows is a target of a group, not a host, and stubs would promise
  semantics not yet designed.
- **HKCU output, or `managed-mcp.json` by default.** HKCU is the user's to
  write. `managed-mcp.json` takes MCP over entirely, so it stays opt-in per
  group.
- **Rewriting option names when ejecting.** Not needed: the original is
  disabled by key, so the copy keeps the same declarations.

## Open questions

- **WSL for people who aren't admins.** Can a standard user get WSL and import
  a piste-built rootfs through Intune, and will the organisation's security
  team accept it? If not, Coder is the only path and phase 2 drops the rootfs
  builder.
- **Claude Code defaults on hosts.** Should piste write the keys it owns into
  `~/.claude/settings.json` (a merge that tracks which keys are piste's,
  crossing a line the agents module keeps today), or reach hosts only as
  `claude --settings` profiles (`piste.agents.claudeProfiles`, which exist)?
  Images seed `home/.claude/settings.json` once either way.
- **The claude.ai console.** Will an organisation keep an org-wide floor in
  the console, so piste renders console JSON and checks that no group weakens
  it, or will every lock be a file or registry value delivered by Intune and
  MDM? This decides whether phase 3 builds the console renderer.
- **A Mac without the desktop pieces.** Should a Mac be able to take only
  `base` or `dev`, where today every Mac gets `desktop`? It would be one more
  Upgrading note in v0.2, and changes how `piste init` and `host add` treat
  Macs.
