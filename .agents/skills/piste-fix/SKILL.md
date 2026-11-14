---
name: piste-fix
description: Find and fix why a piste build or switch failed. Read nix's error from piste fix's log, trace it to a line in the overlay (or in the engine, on a branch checkout), make the edit with the person, and check it with piste fix until it builds. Use when piste fix started this session, when a build or switch failed, or when asked what a Nix error means.
---

# Fix a failed piste build or switch

You're at the root of a piste checkout. The engine is this repo. The person's
overlays are in `overlays/<name>/`, each its own git repo, with `flake.nix`
(its hosts, e.g. `jane-mac`, `"jane@devbox"`, or `devbox` with `nixos = true`),
`home.nix` (what's theirs on every machine) and `hosts/<host>.nix` (one
machine's own settings; a NixOS host's is `hosts/<host>/default.nix`).

`piste switch` builds an overlay against this checkout
(`--override-input piste git+file://<checkout>`). `piste fix` rebuilds
exactly that, with `--show-trace`, and never activates.

## Never

- Never switch or activate: no `piste switch`, `darwin-rebuild`,
  `nixos-rebuild switch` or `home-manager switch`.
- Never run `piste rollback`, `sudo` or `piste adopt` (adopt switches and
  removes installs).
- Never run `piste update` (it moves the checkout to another release and
  rewrites every overlay's `flake.nix` and `flake.lock`) or `piste host add`
  (it makes an age key and edits `.sops.yaml`).
- Never commit or push.
- Never decrypt secrets: no `sops`, no `piste secrets`.

The person does all of these. You end by telling them to run `piste switch`.

## 1. Get the error

- **piste fix started you.** The prompt has the error line, the log path, the
  build it ran and the command that checks a fix. The log is
  `$XDG_STATE_HOME/piste/fix.log`, by default `~/.local/state/piste/fix.log`.
  Read its last `error:` block (`tail -n 80`).
- **Otherwise** run `PISTE_AI=none piste fix`, with the `--overlay` or `--host`
  the person used. It only rebuilds and reports, and exit 0
  means it builds. Always run it with `PISTE_AI=none`: where your shell has a
  terminal, plain `piste fix` starts another AI.
- **It builds, but the switch failed.** The failure was in activation, or in
  the check after it. Every `piste switch` keeps its whole output in
  `$XDG_STATE_HOME/piste/switch.log` (by default
  `~/.local/state/piste/switch.log`), the next switch overwriting it: a
  header (`# host:`, `# flake:`), the activation's lines and piste's, and a
  footer `# exit: <code>` (none: the switch was killed part-way). When the
  rebuild passes, piste fix shows that log's error lines if it's this
  host's failed switch, and the prompt has them. Read the log around them.
  Ask the person only when there's no such log, or it's another host's or
  a passing switch's.
- **A NixOS switch refused before activating** (piste's lock-out check: ssh,
  sudo, boot or machine): the message usually names the option to set in the host file (the wrong-machine rule names the two roots).
  Make that edit; never suggest `--force`, which is the person's call.
- **The switch undid itself** (`Undone: generation <n> is active again`): the
  new generation failed piste's check that a new login shell starts and runs
  piste. The reason is the `The new generation fails the check: …` line and
  the lines under it, in `switch.log` (above). `piste switch --no-undo` keeps
  the new generation to look at; the person runs it.

## 2. Find the line

- **`at /nix/store/<hash>-source/<path>:<line>:<col>`:** strip
  `/nix/store/<hash>-source/`. Look for `<path>` in the overlay
  (`overlays/<name>/<path>`), then in the checkout.
  - Found in neither: it's in an input (nixpkgs, home-manager, a team layer).
- **``- In `/nix/store/<hash>-source/<path>'``** (piste fix's line says
  `in <path>`): a file that sets the option nix names. Strip and look the
  same way.
- **`at «github:<owner>/<repo>/<rev>»/<path>`:** a locked input, named
  already (with lazy trees, as on Determinate Nix).
- **Which input a plain `/nix/store/<hash>-source` is** (Nix without lazy
  trees): `nix flake archive --option lazy-trees false --json ./overlays/<name> --override-input piste git+file://$PWD --no-write-lock-file`
  prints each input's `path`. With lazy trees on, the overlay's and the
  checkout's store paths in an error are virtual and change between runs,
  so they match none of those: strip and look, as above.
- **The trace** (`… while evaluating`): the first frame in the person's files
  is usually where to look.
- **`builder for '/nix/store/….drv' failed`:** `nix log /nix/store/….drv`
  has the build's own output.
- **Options:** `docs/options.md` lists every `piste.*` option. Where it's
  missing, and for any other option, the piste-config skill shows how to read
  an option's description from this machine's config, `piste.*` too.
- **Commands:** `docs/commands.md`, else `piste --help`.

## 3. Where the fix goes

- **The overlay:** always. It's the person's own repo.
- **The engine** (anything outside `overlays/`): only on a branch checkout,
  where `git -C <checkout> symbolic-ref -q HEAD` succeeds. A release checkout
  is detached at a tag, and an engine edit there gets in the way of the next
  `piste update`.
- **Anything else:** on a release checkout, and for anything in nixpkgs,
  home-manager or a team layer, work around it in the overlay (an `exclude`, a
  `lib.mkForce`, an override). Name the upstream fix for the person to file.

Keep the edit minimal, and show it before you make it.

## Common causes

| nix says | why | fix |
|---|---|---|
| `Path 'hosts/x.nix' in the repository "…" is not tracked by Git` (older nix: `path '…-source/hosts/x.nix' does not exist`) | flakes see only files git tracks | `git -C overlays/<o> add -N <file>` (no commit) |
| ``The option `piste.foo' does not exist`` | a typo, or a renamed option | nix's `Did you mean` line; `docs/options.md`, else piste-config's option eval |
| ``undefined variable 'foo'``, ``attribute 'foo' missing`` on `pkgs` | the package was renamed or removed in nixpkgs | `nix search nixpkgs <name>`; nixpkgs' `pkgs/top-level/aliases.nix` names the replacement |
| `'foo' has been renamed to 'bar'` / `has been removed` | an old alias is gone | use the new name, or drop it |
| `hash mismatch in fixed-output derivation` … `got: sha256-…` | an overlay package's version or URL changed | in `pkgs/overlays/`: the engine's `piste update-overlays` (a maintainer tool, hidden from `--help`); elsewhere put the `got:` hash |
| `has an unfree license … refusing to evaluate` | the overlay made its own `pkgs` (`import nixpkgs { }`, `nixpkgs.legacyPackages`) | use the module's `pkgs`, which allows unfree |
| `collision between '/nix/store/…-a/bin/x' and '/nix/store/…-b/bin/x'` | two packages ship the same file | drop one (`piste.packages.exclude`), or `lib.hiPrio` the one to keep |
| `conflicting definition values` | the overlay sets what an engine module already sets | `lib.mkForce` on the overlay's value |
| `Permission denied (publickey)`, `Cannot access`, `authentication required` while fetching an input | a private input, such as a team layer | the person's git credentials (`ssh -T git@<host>`). On a Mac, `piste switch` fetches inputs as the person before root builds |
| `does not provide attribute 'darwinConfigurations.<host>'` or `'homeConfigurations."<user>@<host>"'`; piste's `Hostname '<x>' doesn't match any darwinConfigurations` | the overlay has no config for this machine | `piste host add` (the person runs it), or the right `--host <hostname>` |
| `Unexpected files in /etc, aborting activation` (Mac) | files nix-darwin manages already exist | the person renames each to `<file>.before-nix-darwin` and switches again |
| `It seems there is already an App at '/Applications/X.app'` (Mac) | the app was installed outside Homebrew | `piste.packages.casks.exclude = [ "x" ];`, or the person removes the app |
| `piste switch` printed `The new generation fails the check: …` and undid the switch | a new login shell doesn't start, or can't find or run piste: its rc files, its `PATH`, the login shell itself | the lines under it, from the person; `piste switch --no-undo` keeps that generation to look at (the person runs it) |

## 4. Check it, and hand it back

1. Run the check command from the prompt (else `PISTE_AI=none piste fix`
   with the person's flags) until it exits 0. It only builds: after a switch
   that undid itself it passes before any edit, because only a switch runs
   that check. Say so: the person's `piste switch` checks the fix, and undoes
   itself again if the check still fails.
2. Show `git -C overlays/<o> diff`, and `git diff` for an engine edit on a
   branch.
3. Tell the person to run `piste switch`. Commit nothing.
