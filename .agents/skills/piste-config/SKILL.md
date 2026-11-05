---
name: piste-config
description: Add packages and settings to a piste machine, look up piste.*, nix-darwin and home-manager options, and say where a package on this machine comes from and which line removes it. Use when asked to install, remove or configure something, what an option does, or "where does X come from".
---

# Configure a piste machine

You're at the root of a piste checkout. The engine is this repo. The person's
overlays are in `overlays/<o>/`, each its own git repo:

- `flake.nix` lists the hosts, e.g. `jane-mac` (a Mac) or `"jane@devbox"`
  (Linux, home-manager).
- `home.nix` holds what's theirs on every machine: a home-manager module that
  each host file imports.
- `hosts/<host>.nix` holds that machine's own settings.

**Edit the overlay.** Don't edit the engine on a release checkout (see
piste-fix, "Where the fix goes").

**Check every change** with `PISTE_AI=none piste fix` (and the `--overlay`,
`--host <hostname>` the person uses), which rebuilds as `piste switch`
would and never activates. Exit 0 means it builds. Always run it with
`PISTE_AI=none`: where your shell has a terminal, plain `piste fix` starts
another AI. Then show `git -C overlays/<o> diff` and tell the person to run
`piste switch`.

**Never** switch, commit, push, or run `piste adopt`, `piste update` or
`piste host add` yourself: the person runs those.

**Secrets.** Never write a secret's value in a `.nix` file: the Nix store is
readable by every user on the machine. Values live encrypted in the overlay's
`secrets/` (`piste secrets edit`, which the person runs). An exported variable
reaches every process the shell starts.

## Add a package

1. **Find its name.**
   - A Nix package: `nix search nixpkgs <name>` gives the attribute, e.g.
     `ripgrep`.
   - Mac apps: `brew search --cask <name>`.
   - Formulae nixpkgs lacks: `brew search <name>`.
2. **Add it to the list that's already there.** What goes on every machine
   goes in `home.nix`; one machine's, in its host file. The starter's files
   have each list below, some empty: add to it, and don't write a second
   `home.packages = …` in the same file (nix refuses an attribute defined
   twice). Look at the file first: on a Mac a host file's top level is
   nix-darwin, and home-manager is under `home-manager.users.${user}`.

   | what | every machine (`home.nix`) | one Mac (`hosts/<host>.nix`, top level) | one Linux machine (`hosts/<host>.nix`) |
   |---|---|---|---|
   | Nix package | `pkgs.<attr>` in `home.packages`, beside piste | `pkgs.<attr>` in `piste.packages.extraPackages` | `pkgs.<attr>` in `home.packages` |
   | GUI app (cask) | — | `"<cask>"` in `piste.packages.casks.extra` | — |
   | Homebrew formula | — | `"<formula>"` in `piste.packages.brews.extra` | — |
   | a tool with a home-manager module (git, tmux, zsh, …) | `programs.<x>.enable = true;` and its settings | the same, under `home-manager.users.${user}` | `programs.<x>.enable = true;` and its settings |

3. **Check it** with `PISTE_AI=none piste fix`. Then `piste switch` installs
   it (the person runs that).

## Look up an option

- **`piste.*`:** `docs/options.md` (where it's missing, the eval below,
  which works for `piste.*` too).
- **Any other option**, from this machine's own config (run from the checkout
  root):
  - Mac, system:
    `nix eval --raw ./overlays/<o>#darwinConfigurations.<host>.options.<path>.description --override-input piste git+file://$PWD --no-write-lock-file`
  - Mac, home-manager: the same, with
    `.options.home-manager.users.valueMeta.attrs.<user>.configuration.options.<path>.description`.
  - Linux:
    `nix eval --raw './overlays/<o>#homeConfigurations."<user>@<host>".options.<path>.description' --override-input piste git+file://$PWD --no-write-lock-file`
  - The current value: `config.<path>` in place of `options.<path>.description`,
    with `--json`.
- **Where to set it:**
  - System settings (Mac: `system.defaults.*`, `homebrew.*`, `environment.*`)
    go at the host file's top level.
  - Per-user ones (`programs.*`, `home.*`) go inside
    `home-manager.users.${user}` on a Mac, and at the top level on Linux.

## Where does X come from, and what removes it

### 1. What is on this machine

Run `readlink -f "$(command -v X)"`:

- **`/nix/store/…`:** from Nix. Go to step 2.
- **`/opt/homebrew/…`, `/usr/local/Cellar/…`, or an app in `/Applications`:**
  a brew or cask. If step 2 finds it in `homebrew.*`, Nix asked for it.
  Otherwise someone installed it by hand.
- **Anything else** (`/usr/bin`, `~/.cargo/bin`, `~/.local/bin`, npm or pnpm
  globals): not from Nix.
  - `piste drift` lists what's installed outside Nix.
  - `piste adopt` moves such installs into the overlay. It switches and
    removes the old install, so name it for the person to run.

### 2. Which module puts it there

Ask the config which files define it, built the way switch builds it. Run this
from the checkout root. Lazy trees stay off, so the file paths are the
store's real ones, which step 3 can name.

```sh
# Mac:   ./overlays/<o>#darwinConfigurations.<host>.options
# Linux: './overlays/<o>#homeConfigurations."<user>@<host>".options'
nix eval --option lazy-trees false --json <ref> \
  --override-input piste git+file://$PWD --no-write-lock-file \
  --apply "$(cat <<'EOF'
o:
let
  want = "tmux"; # X: the package's name (pname), cask or formula
  user = "jane"; # the overlay's user (Mac only)
  name = p: if builtins.isAttrs p then p.pname or (builtins.parseDrvName (p.name or "")).name else toString p;
  files = defs: map (d: d.file) defs;
  find = opt: files (builtins.filter
    (d: builtins.elem want (map name (if builtins.isList d.value then d.value else [ d.value ])))
    (opt.definitionsWithLocations or [ ]));
  mac = o ? home-manager;
  hm = if mac then o.home-manager.users.valueMeta.attrs.${user}.configuration.options else o;
in
builtins.mapAttrs (_: find) {
  "home.packages" = hm.home.packages;
  "environment.systemPackages" = if mac then o.environment.systemPackages else { };
  "homebrew.casks" = if mac then o.homebrew.casks else { };
  "homebrew.brews" = if mac then o.homebrew.brews else { };
  "piste.packages.extraPackages" = o.piste.packages.extraPackages or { };
  "piste.packages.casks.extra" = o.piste.packages.casks.extra or { };
  "piste.packages.brews.extra" = o.piste.packages.brews.extra or { };
}
// { "programs.${want}.enable" = files (hm.programs.${want}.enable.definitionsWithLocations or [ ]); }
EOF
)"
```

Each option maps to the files that put X in it, as
`/nix/store/<hash>-source/<path>`. To see which source each is, run
`nix flake archive --option lazy-trees false --json ./overlays/<o> --override-input piste git+file://$PWD --no-write-lock-file`.
It prints each input's `path`:

- the top level is the overlay itself;
- `piste` is this checkout;
- the rest are its inputs, such as `piste/home-manager` and
  `piste/nixpkgs`.

**If the eval fails** (a nixpkgs bump can move `valueMeta`), grep:
`grep -rn 'X' overlays/<o> modules/packages-module.nix modules/programs`.

### 3. The line that removes it

| X is defined in | remove it with |
|---|---|
| the overlay's or team layer's own list (`extraPackages`, `casks.extra`, `home.packages` in `home.nix` or `hosts/…`) | delete that entry |
| a module that sets it from the engine's default list, `piste.packages.resolved.*` (`modules/packages-module.nix`): the engine's `modules/platforms/darwin.nix` (`environment.systemPackages`, `homebrew.casks` or `homebrew.brews`), its `modules/roles/container.nix` (`home.packages`, Linux), or a host file that does the same | `piste.packages.exclude = [ "X" ];` (Nix packages, by name), `piste.packages.casks.exclude` or `brews.exclude` |
| a program module (home-manager's `modules/programs/<x>.nix` in `home.packages`) | see who sets `programs.<x>.enable`. If it's the engine's `modules/programs/<x>.nix`, switch its role's toggle off in the home (`home.nix`, a Linux host file, or a Mac's `home-manager.users.${user}`): `piste.roles.<role>.<toggle>.enable = false;`. The toggles (`docs/options.md`) are base's `shell` (zsh), `git`, `ssh` and `starship`, dev's `editor` (vim) and `tmux`, and desktop's `alacritty` and `ghostty`; zellij has its own switch, `piste.programs.zellij.enable = false;`. A program with none (direnv, gh, …): `programs.<x>.enable = lib.mkForce false;`, with `lib` in the file's arguments (a plain `false` conflicts with the engine's `true`) |

X can come from more than one place: remove it from each. Then offer the edit
and check it with `PISTE_AI=none piste fix`.

**Examples** (the starter overlays):

- **tmux on `jane@devbox`** (Linux):
  - `home.packages` comes from `piste/home-manager`'s
    `modules/programs/tmux.nix`;
  - `programs.tmux.enable` comes from `piste`'s `modules/programs/tmux.nix`;
  - the fix is dev's toggle, `piste.roles.dev.tmux.enable = false;`, in
    `overlays/<o>/hosts/devbox.nix` (or in `home.nix`, for every machine).
- **tmux on `jane-mac`:**
  - the same two;
  - `environment.systemPackages` from `piste`'s
    `modules/platforms/darwin.nix`;
  - so both `piste.packages.exclude = [ "tmux" ];` at the top level of
    `hosts/jane-mac.nix` and `piste.roles.dev.tmux.enable = false;` under
    `home-manager.users.${user}`.
- **The `ngrok` cask on `jane-mac`:** `homebrew.casks` from
  `modules/platforms/darwin.nix`, removed with
  `piste.packages.casks.exclude = [ "ngrok" ];`.
