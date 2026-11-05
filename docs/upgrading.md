# Upgrading

- How do I move to the next release of piste?
- What changed, and is there anything I must change in my overlay?
- How do I go back?
- What does a release promise to keep working?
- How often are there releases?
- What does the line about a new release after `piste switch` mean?

## The version you run

Your checkout sits on a release tag (`v0.4.0`), and each overlay pins the same
release in its `flake.nix` (`inputs.piste.url =
"github:elocke/piste/v0.4.0"`). `piste switch` builds your machine from the
checkout, CI and a bare clone of your overlay from the pin
([concepts.md](concepts.md#what-a-build-uses)). Nothing reaches you until you
move both, and `piste update` moves both together.

## `piste update`

Run it anywhere in the checkout:

```
$ piste update
Fetching piste's releases...
You're on v0.4.0. Newest: v0.5.1.

v0.5.0 (2026-11-05)
  - piste host add: a second machine in an overlay.
  Upgrading:
  - piste.foo is now piste.bar: rename it in your hosts/*.nix.
v0.5.1 (2026-11-09)
  - Inputs updated (flake.lock).

✓ piste: v0.4.0 → v0.5.1
✓ overlays/mine: engine v0.4.0 → v0.5.1
╭──────────────────────╮
│ overlays/mine inputs │
╰──────────────────────╯
Input     Before      After       Jump  Status
piste  2026-10-31  2026-11-09  9d    updated
Next: read the Upgrading notes above, then piste switch.
Commit overlays/mine's flake.nix and flake.lock once it works.
```

In order, it:

1. fetches the release tags from the checkout's `origin`;
2. prints the notes of every release between yours and the newest, oldest
   first, each release's `Upgrading` notes set apart;
3. checks the checkout out at the newest release;
4. rewrites each overlay's engine pin to that release (both forms `piste init`
   writes: `github:<owner>/<repo>/<tag>` and `git+…?ref=refs/tags/<tag>`);
5. updates each overlay's other inputs (`nix flake update`: a team layer,
   flakes of your own) and prints each input's date before and after.

Then do what the `Upgrading` notes say, `piste switch`, and once the machine
works, commit and push the overlay's `flake.nix` and `flake.lock`, so your
other machines and CI get the same pin.

What else you may see:

- **Already on the newest:** it says so, and still updates the overlays' other
  inputs.
- **`piste update --dry-run`:** fetches and prints the notes and the planned
  moves, and changes no file.
- **A checkout on a branch** (you track `main`, say): it fast-forwards the
  branch (`git pull --ff-only`) and prints no notes; the overlays' pins still
  move to the newest release tag.
- **git refuses to move the checkout** (changes of yours in the way, a branch
  with no upstream, a detached HEAD that isn't a release): it stops before
  touching any overlay, with git's reason and the command that fixes it.
- **A pin that isn't a release tag** (a `?rev=`, a branch): it says so and
  leaves that pin as it is.
- **An overlay fails** (its pin, or `nix flake update`): it reports that one,
  carries on with the rest, and exits 1.
- **No checkout** (you're outside one, and `~/piste` isn't one): it says so.

`piste update <input>` updates one input of this machine's overlay, a team
layer for instance. An input that comes from the engine (`nixpkgs`,
`home-manager`, …) isn't yours to move: it moves with releases.

```
$ piste update nixpkgs
nixpkgs comes from the engine: it moves with piste releases (piste update)
```

## Release notes and Upgrading notes

Every release's notes are in the checkout's
[CHANGELOG.md](../CHANGELOG.md), newest first, and `piste update` prints the
ones between your release and the newest. A note under **Upgrading** is a
change you must act on: something an overlay that built on the release before
will fail on, or something that changes your machines without your overlay
asking. Read those before you switch.

Before 1.0, a release with Upgrading notes is a minor release (`v0.5.0`); a
patch release (`v0.5.1`) never has any.

## What a release promises

A patch release (`v0.1.1`) never breaks what's listed here. A minor release
(`v0.2.0`) may, and says so under **Upgrading**. Before 1.0 that is the whole
rule. A patch carries the `flake.lock` update and anything merged without an
Upgrading note.

Promised:

- every option in [options.md](options.md), `mkFlake`'s arguments and host keys
  ([customising.md](customising.md#mkflakes-arguments)), and every command and
  flag in [commands.md](commands.md);
- the `PISTE_*` variables these docs name (`PISTE_AI`, `PISTE_FLAKE`,
  `PISTE_AGENTS_HOME` and `PISTE_AGENTS_ROOTS`);
- the files piste reads: `agents.toml`, `agents.lock` (version 1),
  `piste-layer.toml` and the leak-marker lists;
- the plugin contract (`piste-<name>` on your PATH, run with `PISTE_FLAKE`);
- the names of the engine inputs an overlay can follow: `nixpkgs`,
  `home-manager`, `darwin` and `sops-nix`;
- the `zone-*` prefix of the checks `mkFlake` adds, reserved for the engine.

Not promised:

- hidden commands (they're not in `piste --help`) and undocumented `lib`
  functions;
- the package attributes in `overlays.shared`, beyond what nixpkgs has;
- team layers, which are a preview in v0.1.0 and may change in a minor release;
- what upstream does: nixpkgs, Homebrew, Claude Code and the other tools move
  with each release, and so does what they install.

After v0.1.0, a renamed option keeps working for a release or more
(`mkRenamedOptionModule`), and a renamed command keeps its old name with a
deprecation notice.

## Going back

Each `piste switch` checks the new generation: a clean login shell must run
`piste --version` within 30 seconds. If it fails, the switch puts the
generation that was active before back by itself and exits 1.
`piste switch --no-undo` skips that check.

`piste update` asks nothing; going back is git. To return to `v0.4.0` before
you've committed the overlay:

```bash
git -C ~/piste checkout v0.4.0
git -C ~/piste/overlays/mine checkout flake.nix flake.lock
piste switch
```

After you've committed it, revert that commit in the overlay instead of the
second line. If a switch already happened and the machine misbehaves,
`piste rollback` puts its previous generation back at once; move the checkout
and the pin back too, or the next switch brings the new release again.

## How often

Releases come regularly. The engine's inputs (nixpkgs, home-manager,
nix-darwin and the rest) move in a release as a patch with the note "Inputs
updated (flake.lock)." (or, when a minor release is pending, they ship with
that one), so `piste update` from time to time keeps your packages current.
New features and changes to your overlay's options come in minor releases.
Your overlay never updates nixpkgs on its own: it follows the engine's.

## The line after `piste switch`

After a successful `piste switch`, when a newer release is out, piste says so
in one line:

```
piste v0.5.1 is out (you're on v0.5.0): piste update shows what changed
```

"You're on" is the checkout's release, or, on a branch, the switched
overlay's pin. piste checks at most once a day, by asking your checkout's
`origin` which tags it has (a `git ls-remote`, three seconds at most). It never
prompts, for an ssh passphrase or anything else, and it's silent when you're
offline, when the check fails, and when the `CI` variable is set.

## Several machines

After updating on one machine and pushing the overlay, on each of the
others: pull the overlay, put the checkout on the release its pin now names,
and switch.

```bash
git -C ~/piste/overlays/mine pull
git -C ~/piste fetch --tags
git -C ~/piste checkout v0.5.1    # the tag in the overlay's inputs.piste.url
piste switch
```

Don't run `piste update` there. It would move that checkout to the newest
release, which may be newer than the one you tested, and update the overlay's
other inputs (a team layer, say) past the `flake.lock` you committed. The line
after a switch (above) points at `piste update` because it suits the machine
you update first. On the others, `piste update --dry-run` shows the notes
and changes nothing. The engine's zsh has `pisteup`, which pulls the overlays
and switches (`pisteup -n` skips the switch); it leaves a checkout on a
release tag where it is, so run the two `git -C ~/piste` lines before it.

## Homebrew apps

`piste switch` installs the casks and formulae the Mac's lists name. It never
uninstalls one, so an app you drop from the lists stays on the Mac until
`brew uninstall --cask <name>`. It doesn't upgrade installed apps either:
nix-darwin's `homebrew.onActivation.upgrade` is off by default, and piste
leaves it that way. To have every switch upgrade them, set
`homebrew.onActivation.upgrade = true;` at the Mac's top level, or run
`brew upgrade --cask` yourself.
