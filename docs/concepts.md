# Concepts

- What are the engine and an overlay, and which one do I edit?
- What is the checkout, and how does piste know which overlay I mean?
- What are hosts, roles, zones and layers?
- Why does `piste switch` build my checkout's engine while CI builds the pinned release?

## The engine and your overlay

**The engine** is this repo: Nix modules (shell, git, editor, terminals,
packages, the Mac's settings, agents, secrets), nixpkgs overlays for packages
nixpkgs lacks, the `piste` CLI, and `lib.mkFlake`, which turns a short
description of your machines into a flake. It holds no machines and nobody's
identity.

**An overlay** is a small flake of your own, in its own git repo. It names your
user, your machines (hosts), the trust zone they belong to, and whatever is
yours: packages, settings, secrets, agent skills. Its `flake.nix` takes the
engine as an input named `piste` and calls `piste.lib.mkFlake`:

```nix
{
  inputs.piste.url = "github:elocke/piste/v0.1.0";

  outputs =
    inputs@{ self, piste, ... }:
    piste.lib.mkFlake {
      inherit inputs self;
      user = "jane";
      zone = "personal";

      hosts."jane@devbox" = {
        system = "x86_64-linux";
        roles = [ "dev" ];
        modules = [ ./hosts/devbox.nix ];
      };

      agents = ./agents;
    };
}
```

That's what `piste init` writes (with your answers), along with `home.nix`
for what's yours on every machine (identity, agents, secrets, packages),
`hosts/<name>.nix` for each machine's own settings (its home directory,
stateVersion, checkout path; on a Mac, its system settings), `agents/` for
your skills and instructions, a `README.md` and a `CLAUDE.md`. You edit the
overlay; you don't edit the engine. When the engine does something you don't
want, your overlay turns it off or overrides it
([customising.md](customising.md)).

You can have more than one overlay, one per trust zone: a `personal` one and a
`work` one, say. A machine holds only the overlays of its own zone. What piste
enforces is narrow: agent content (skills, instructions, hooks, plugins and
MCP servers) from another zone fails the build ([Zones](#zones)). Keeping the
rest apart, such as your personal packages and secrets, is your doing:
clone only that zone's overlays onto a machine.

## The checkout

You work in a clone of the engine, the checkout (`~/piste` unless you chose
another place), with your overlays cloned inside it:

```
~/piste/                    a clone of piste, at a release tag
├── .piste                  marks the checkout's root
├── README.md, docs/        these docs
├── modules/ lib/ cli/ …    the engine
└── overlays/               ignored by piste's git: each one its own repo
    └── personal/           your overlay (piste init or piste add <url>)
        ├── flake.nix
        ├── home.nix
        ├── hosts/devbox.nix
        └── agents/
```

piste works out what you mean from where you run it:

- **Inside an overlay,** that overlay.
- **At the checkout's root,** the overlay `--overlay <name>` names; else the
  only one; else the one whose hosts include this machine. Two overlays that
  both have this machine are an error.
- **Outside any flake,** `$PISTE_FLAKE`, else the checkout this machine was
  last switched from, else `~/piste`.

`piste where` says what it picked, and why:

```
$ cd ~/piste && piste where
flake: /home/jane/piste/overlays/personal (checkout /home/jane/piste: overlay personal (the only overlay))
engine override: /home/jane/piste (the checkout around the overlay)
```

Start an AI agent at the checkout's root (`piste claude` does): from there it
sees the engine's instructions and skills and your overlay's.

## Hosts

A host is one machine in an overlay's `hosts`, keyed the way piste matches it
to the machine it's running on:

- **A Mac** (`system = "aarch64-darwin"`) is a nix-darwin system named by its
  hostname, `jane-mac`. It can have `aliases`, other names for the same
  machine. Its user's home is home-manager inside it.
- **A Linux machine** (`system = "x86_64-linux"` or `"aarch64-linux"`) is a
  standalone home-manager configuration named `user@hostname`,
  `jane@devbox`. piste manages your home there, not the system.

Each host has its own file in `hosts/` for settings that belong to that
machine, and that file imports the overlay's `home.nix`, what's yours on every
machine. `piste host add` makes the machine you're on a host of the overlay.
`piste switch` builds and activates the host that is this machine, and stops
when the overlay has no host for it.

## Roles

A role is a bundle of home-manager modules. They stack: each one includes the
one before it.

| Role | Brings |
|------|--------|
| `base` | zsh with a starship prompt, git, ssh |
| `dev` | `base`, plus vim, tmux, sops secrets support and uv's settings |
| `desktop` | `dev`, plus Ghostty, Alacritty and zellij |
| `container` | `dev` without what only a real machine needs; what [devcontainer images](devcontainer.md) are built from |
| `extras` | beside any of the others, opt-in: more tools, aliases and settings, and on a Mac more apps and input settings ([the extras pack](customising.md#the-extras-pack)) |

A Linux host takes `roles = [ "dev" ]` (or another) in `flake.nix`. A Mac
always gets `desktop`, from the engine's platform module, plus the package
set, Homebrew and the system settings. Either
takes `"extras"` in its `roles` too when you started with everything. To turn off one thing a role
brings, switch its toggle off in your home, e.g.
`piste.roles.base.starship.enable = false;`
([customising.md](customising.md#drop-a-default) has the rest).

## Zones

A zone is a trust boundary for agent content: the skills, instructions, hooks
and MCP servers that Claude Code and Codex get. Every overlay has one
(`zone = "personal"` in `flake.nix`): any name of lowercase letters, digits
and dashes. The engine's own content is the zone `shared`, which every machine
may use.

A machine takes agent content only from `shared` and its own zone. That's
checked when it's built, and only for agent content: it doesn't stop a
secret, a package or a setting from reaching a machine, and it isn't a control
over what an agent does. Your employer's device management (MDM) is the real
control on a work laptop. An overlay made with `mkFlake` gets checks that its
hosts link only allowed content (`zone-clean`) and that content of another
zone fails to build (`zone-foreign-rejected`). They run with `nix flake check`
in the overlay. [agents-and-mcp.md](agents-and-mcp.md) says what the policy
checks and what it doesn't.

## Layers

Team layers are a preview in v0.1.0. A team layer is a flake a team shares:
its modules, its agent content (in the team's zone), its packages and plugins, and the names of the secrets it
expects. It holds no machines, no identity and no secret values. An overlay on
a layer is an ordinary overlay with one more input, `layer`, whose modules its
hosts import. Values the layer sets are defaults your overlay can override;
lists the layer sets (packages, casks, agent presets) add up with yours.

To start your own overlay on a team's layer:

```bash
nix run github:elocke/piste/stable -- setup <the layer's git url>    # or, in a checkout:
piste init --layer <the layer's git url>
```

`piste update` moves the layer's pin along with the engine's. The preview
label means a layer is outside [what a release
promises](upgrading.md#what-a-release-promises): it may change in a minor
release, and layers are expected to become packs
([design/packs.md](design/packs.md)). piste reads only this of a layer:

- `piste-layer.toml` at its root: its zone, and the secrets it expects (env
  var = sops key path).
- `template/`, the starter of each overlay on it: `hosts/darwin.nix` and
  `hosts/linux.nix` become the machine's host file, and these placeholders are
  filled in: `@user@`, `@hostname@`, `@hostKey@`, `@system@`, `@roles@`,
  `@zone@`, `@name@`, `@checkout@`, `@gitName@`, `@gitEmail@`, `@home@`,
  `@engine@`, `@layer@` and `@secrets@`.
- Its engine input, named `piste` like the overlay's, which the starter makes
  follow the overlay's.

The rest (its outputs, `pisteLayer` among them, and its modules) is between
the layer and its starter. In a layer's modules, scalars are
`piste.lib.mkPackDefault` (priority 950, from the layer's own `piste` input)
and lists plain. A person's plain value then wins over the team's, the team's
wins over the engine's `lib.mkDefault`, and the person's list entries go
beside the team's ([who wins](customising.md#who-wins)).
`tests/fixtures/layer` in this repo is a complete small example.

## What a build uses

Your overlay pins a release of the engine (`inputs.piste.url =
"github:elocke/piste/v0.1.0"`), and its `flake.lock` records it. But
`piste switch` builds the overlay against **the checkout around it**, passing
`--override-input piste git+file://<checkout>`. So what you run is what you
have checked out, and moving the checkout (`piste update`) moves your machines.
`piste where` shows that override.

Two consequences:

- **Only tracked files reach a build.** The checkout is read as a git
  repository, so `overlays/` stays out, and so does a file you've just created
  and not yet told git about. After adding a file to the engine or to an
  overlay, `git add` it (`git add -N` is enough) before `piste switch`.
- **Inside a checkout, every build is against the checkout.** `piste build`,
  `piste check`, `piste dry`, `piste fix`, `piste gendiff` and `piste switch`
  build the engine in the checkout around your overlay, not the release your
  overlay pins; CI and a bare clone of your overlay build the pin. To try a
  change to the engine before you switch, `piste build` it, or `piste gendiff`
  to see what would change. Keep the pin and the checkout on the same
  release: `piste update` moves both ([upgrading.md](upgrading.md)).
