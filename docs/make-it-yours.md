# Make it yours

- How do I add a package, or an app on a Mac?
- How do I drop something piste installs or turns on?
- How do I turn on the extras pack?
- How do I set up my next machine?
- How do I keep my packages up to date?
- How do I keep an API key secret?
- How do I change a setting I know the name of?
- Can an AI agent make the change for me?

Your machine is switched. These recipes are the changes most people make
first. Each one edits a file in your overlay: the repository setup made in
`~/piste/overlays/personal/`
([The engine and your overlay](concepts.md#the-engine-and-your-overlay)). If
you named your overlay something else, put its name in place of `personal`.

The files are written in [Nix's language](https://nix.dev/tutorials/nix-language),
a small language for describing a configuration. You don't need to learn it:
each recipe gives the line to add and where it goes. Open the file in any
editor, such as `nano`.

Every recipe ends by checking the change:

```bash
cd ~/piste
piste build     # builds it; the machine stays as it is
piste switch    # applies it
```

`piste build` finds a mistake before anything on the machine changes. When it
fails, see [A build or a switch fails](troubleshooting.md#a-build-or-a-switch-fails).
Then commit the change in your overlay and push it, so your other machines
get it too:

```bash
git -C ~/piste/overlays/personal commit -am 'Describe your change'
git -C ~/piste/overlays/personal push
```

## Add a package

A command-line tool, on every machine. Here, `ollama`:

1. Find its name: search [search.nixos.org/packages](https://search.nixos.org/packages),
   or run `nix search nixpkgs ollama`. nixpkgs is the collection of packages
   Nix installs from.
2. Open `~/piste/overlays/personal/home.nix`. It has an empty list for
   packages:

   ```nix
   home.packages = with pkgs; [ ];
   ```

3. Put the name between the brackets (several are separated by spaces):

   ```nix
   home.packages = with pkgs; [ ollama ];
   ```

   Add it to that line; don't write a second `home.packages` line, because Nix
   refuses a setting that's given twice in one file.
4. Check it: `piste build`, then `piste switch`. In a new shell, `ollama` is
   there.
5. Commit it: `git -C ~/piste/overlays/personal commit -am 'Add ollama'`.

**On one machine only,** use that machine's file in `hosts/` instead (on a
NixOS machine, `hosts/<name>/default.nix`). On
Linux, the host file has a `home.packages` list of its own. On a Mac or a
NixOS machine, the list is `piste.packages.extraPackages = with pkgs; [ ];`,
near the top of the file. (If the line near the top that starts with `{`
doesn't name `pkgs`, add it: `{ user, pkgs, ... }:`.)

Installed something with Homebrew, npm, cargo or pip already? `piste adopt`
brings it in ([Packages](customising.md#packages)).

## Add a Mac app

An app from [Homebrew](https://brew.sh), the Mac's package manager. Here,
Obsidian:

1. Find its Homebrew name: `brew search obsidian`, or search
   [formulae.brew.sh](https://formulae.brew.sh). An app is a *cask*.
2. Open your Mac's host file, `~/piste/overlays/personal/hosts/<your Mac's name>.nix`
   (`ls ~/piste/overlays/personal/hosts` lists them).
3. Put the name, in quotes, in the list that's there:

   ```nix
   piste.packages.casks.extra = [ "obsidian" ];
   ```

   A command-line tool that only Homebrew has (a *formula*) goes in the list
   under it: `piste.packages.brews.extra = [ "mas" ];`.
4. Check it: `piste build`, then `piste switch`. The app is in Applications.
5. Commit it.

To remove an app again, see [Drop a default](#drop-a-default): piste never
uninstalls one by itself.

## Drop a default

Something piste installs or turns on that you don't want:

1. Find where it comes from. An agent at the checkout
   ([Let an agent do it](#let-an-agent-do-it)) answers "where does docker come
   from?" with the file and the line that removes it. Or look it up in
   [Switching defaults off](customising.md#switching-defaults-off).
2. **A package from piste's set:** add its name to `piste.packages.exclude`
   in the machine's host file. On a Mac or a NixOS machine, put it at the top
   level of the file, not inside `home-manager.users.${user} = { … };`:

   ```nix
   piste.packages.exclude = [ "docker" ];
   ```

3. **A program piste sets up,** such as the starship prompt: switch it off in
   `home.nix`:

   ```nix
   piste.roles.base.starship.enable = false;
   ```

4. **A Mac app:** add it to `piste.packages.casks.exclude` at the top level of
   the Mac's host file, `piste.packages.casks.exclude = [ "ghostty" ];`. piste
   then stops installing it, but leaves the copy that's there: Homebrew's
   cleanup is off, so piste never uninstalls an app. Remove it yourself with
   `brew uninstall --cask ghostty`.
5. Check it: `piste build`, then `piste switch`. Commit it.

## Turn on the extras pack

The extras pack is more of one developer's taste: more aliases, more tmux and
git settings, and on a Mac more apps
([what's in it](customising.md#the-extras-pack)). If you chose "Everything"
at setup, it's on already.

1. Open `~/piste/overlays/personal/flake.nix`.
2. Find your machine under `hosts`, and add `"extras"` to its `roles`:

   ```nix
   hosts."jane@devbox" = {
     system = "x86_64-linux";
     roles = [ "dev" "extras" ];
   ```

   On a Mac, `roles = [ ];` becomes `roles = [ "extras" ];` (a Mac has the
   desktop role already).
3. Check it: `piste build`, then `piste switch`.
4. Don't want one part of it? Switch that part off. A part for your home, such
   as tmux's, goes in `home.nix`: `piste.roles.extras.tmux.enable = false;`.
   On a Mac, the `casks` and `macos` parts go at the top level of the Mac's
   host file instead: `piste.roles.extras.macos.enable = false;`.
5. Commit it.

To turn the pack off, take `"extras"` out of `roles` again and switch. Apps it
installed stay until `brew uninstall --cask <name>`.

## Add a second machine

Your overlay has everything you set up, and a new machine starts from it. On
the new machine:

1. Install Nix, as your first machine's guide says:
   [Mac](start-mac.md), [Linux](start-linux.md). On NixOS, Nix is there
   already.
2. Your repository is private, so log GitHub's CLI in first:
   `nix run nixpkgs#gh -- auth login --web --git-protocol https`.
3. Run setup with your overlay's https URL:

   ```bash
   nix run github:elocke/piste/stable -- setup https://github.com/jane/personal.git
   ```

   Use the https URL: a new machine has no SSH key for GitHub yet
   (`piste github` makes one after its first switch). When a github.com clone
   fails, setup says so.
4. Setup finds no host for this machine in your overlay and asks "Add this
   machine to it?". Choose "Add it". It writes the machine's host file and
   its entry in `flake.nix`, with the roles of your other machines of its
   kind ([what it writes](customising.md#adding-and-removing-a-machine)).
5. Setup builds the machine and offers the first switch, as on your first
   machine. It ends with a line after `To keep it:` that commits and pushes
   the new host: run it.

On a fresh NixOS, whose Nix has flakes off, put
`--extra-experimental-features 'nix-command flakes'` after `nix` in both
commands. Setup's is then
`nix --extra-experimental-features 'nix-command flakes' run github:elocke/piste/stable -- setup <your overlay's git url>`
([Getting started on NixOS](start-nixos.md)).

If your overlay keeps secrets, the new machine gets an age key of its own,
but it can't read the secrets until a machine that can re-encrypts them for
it. Setup stops before the switch and prints the steps:

```
This machine can't read overlays/personal's secrets yet. To finish:
  1. here:   git -C ~/piste/overlays/personal commit -m 'Add jane@laptop' -- flake.nix hosts/laptop.nix .sops.yaml && git -C ~/piste/overlays/personal push
  2. on jane@devbox, in its overlays/personal:
             git pull && piste secrets updatekeys && git commit -m 'Secrets for jane@laptop' -- '*.sops.yaml' && git push
  3. here:   cd ~/piste && git -C overlays/personal pull && nix run .#piste -- setup
```

Run them in that order, and the third goes on to the first switch.

## Keep it up to date

One command brings the machine's packages up to date:

```bash
piste pkgs            # what is behind, and who updates it; changes nothing
piste pkgs update     # moves what is yours, shows the changes, offers the switch
```

`piste pkgs update` does these, in order. Steps 1 to 4 stop the run at the
first that fails:

1. Moves the checkout to the newest piste release, as `piste update` does, and
   your overlay's pin with it. Your other overlays keep their pins; it tells
   you so, and `piste update` moves them.
2. Updates the inputs your overlay declares, such as a team layer or
   `nixpkgs-fresh` ([Newer than the engine](customising.md#newer-than-the-engine-fresh)).
3. Builds the machine and shows which versions change.
4. Asks before it switches, with all of `piste switch`'s checks.
5. Only after a successful switch, upgrades your pnpm globals
   ([pnpm globals](customising.md#pnpm-globals)) and the Homebrew apps on a Mac.
   If one of those fails, it names it, carries on with the other, and exits
   with an error; the switch stays.

If it stops before the switch (a failed build, "no" at the question, or
`--no-switch`), nothing on the machine has changed. Only the checkout (now on
the newest release) and your overlay's `flake.nix` and `flake.lock` have;
commit the overlay's two files once the machine works.

What it leaves alone: the pins inside the engine's and a team layer's own
repositories. It moves your overlay to their newest release or commit, but
never edits them. Homebrew upgrades go only as far as the pinned Homebrew
catalogue goes, which is the engine's; an overlay that
pins its own `homebrew-core` and `homebrew-cask` and makes the engine follow
them moves them itself:

```nix
inputs.homebrew-core = { url = "github:homebrew/homebrew-core"; flake = false; };
inputs.homebrew-cask = { url = "github:homebrew/homebrew-cask"; flake = false; };
inputs.piste.inputs.homebrew-core.follows = "homebrew-core";
inputs.piste.inputs.homebrew-cask.follows = "homebrew-cask";
```

Useful flags:

- `--dry-run` prints the moves and changes nothing.
- `--no-switch` stops after the build and the list of changes.
- `--yes` switches without asking.
- `--only nix`, `--only pnpm` or `--only brew` (repeat it for two) runs just
  that part. `pnpm` and `brew` upgrade what is switched now.
- `--only brew` is an error on a machine that isn't a Mac, and on a Mac whose
  config turns Homebrew off (`homebrew.enable = false`).
- `--greedy` also upgrades Mac apps that update themselves.

To change the engine's or a layer's pins, that's a pull request to that
repository; `piste pkgs` names it.

## Keep a secret

An API key your shell needs, kept encrypted in your overlay, and decrypted on
each machine when it switches. Never put the value itself in a `.nix` file:
everything Nix builds goes into the [Nix store](https://nix.dev/manual/nix/latest/store/),
which is readable by every user on the machine. Here, an Anthropic API key:

1. If you said "Not now" to secrets at setup, set them up once, in your
   overlay:

   ```
   $ cd ~/piste/overlays/personal
   $ piste secrets init
   Created age key: /home/jane/.config/sops/age/keys.txt
     Keep a copy of it in a password manager: a reinstalled machine needs it to read your secrets.
     Public key: age1…
   Created /home/jane/piste/overlays/personal/.sops.yaml

   Secrets infrastructure ready.
     Next: add secrets with piste secrets edit
   ```

   The age key is what decrypts your secrets, and it stays out of git.
   Without it nothing can decrypt them, which is why piste says to keep a
   copy.
2. Start the file with a placeholder (never a real value on disk), then let
   piste encrypt it and open it in your editor, where you replace `change-me`
   with the key:

   ```bash
   cd ~/piste/overlays/personal
   printf 'anthropic:\n  api_key: change-me\n' > secrets/env/api-keys.sops.yaml
   piste secrets edit secrets/env/api-keys.sops.yaml
   git add secrets/env/api-keys.sops.yaml .sops.yaml
   ```

   The `git add` matters: a build sees only the files git knows about
   ([What a build uses](concepts.md#what-a-build-uses)).
3. Turn it on in `home.nix`. The starter has this block, commented out:
   remove the `#` in front of its five lines.

   ```nix
   piste.secrets = {
     enable = true;
     sopsFile = ./secrets/env/api-keys.sops.yaml;
     envVars.ANTHROPIC_API_KEY = "anthropic/api_key";   # variable = path in the file
   };
   ```

4. Check it: `piste build`, then `piste switch`. A new shell has
   `ANTHROPIC_API_KEY`.
5. Commit it. The file in git is encrypted.

An exported variable reaches every program you start from the shell, so
export only what you need. [Secrets](customising.md#secrets) has the other
`piste secrets` commands.

## Change a setting by name

Everything piste sets up is made of *options*: named settings, such as
`programs.git.settings.pull.rebase`. You can set any of them yourself.

1. Find the option:
   - piste's own (`piste.*`): [options.md](options.md);
   - your home's, on every machine:
     [home-manager's options](https://nix-community.github.io/home-manager/options.xhtml);
   - a Mac's system settings:
     [nix-darwin's options](https://nix-darwin.github.io/nix-darwin/manual/);
   - a NixOS machine's: [NixOS's options](https://search.nixos.org/options).
2. Put it where it belongs
   ([Where a setting goes](customising.md#where-a-setting-goes)): a home
   option in `home.nix`, for example a shell alias,

   ```nix
   programs.zsh.shellAliases.k = "kubectl";
   ```

   and a system option at the top level of a Mac's or a NixOS machine's host
   file, for example `system.defaults.dock.autohide = false;` on a Mac.
3. Check it: `piste build`, then `piste switch`. If the build says the option
   "has conflicting definition values", piste sets it too, and yours needs
   priority. Wrap your value in `lib.mkForce`:

   ```nix
   programs.git.settings.init.defaultBranch = lib.mkForce "trunk";
   ```

   and add `lib` to the line near the top of the file that starts with `{`,
   so that `home.nix`'s reads `{ config, pkgs, lib, ... }:` (a Mac's host
   file: `{ user, pkgs, lib, ... }:`). Then build again
   ([Any other option](customising.md#any-other-option)).
4. Commit it.

## Let an agent do it

Claude Code or Codex can make these changes for you, and check them.

1. Start Claude Code at the checkout, with your overlay in reach:

   ```bash
   cd ~/piste
   piste claude
   ```

   It reads the engine's instructions and your overlay's. For Codex, run
   `codex` in `~/piste`.
2. Ask in plain words: "add ollama to every machine", or "where does docker
   come from, and how do I drop it?". The checkout's skills, `piste-config`
   and `piste-fix`, teach it to add a package, look up an option and read a
   Nix error.
3. It edits your overlay and checks the change with a build. Switching,
   updating and pushing are yours: the checkout's instructions tell an agent
   not to, and its `.claude/settings.json` stops Claude Code from doing it.
4. Read what it changed, `git -C ~/piste/overlays/personal diff`, then
   `piste switch` and commit it.

When a build or a switch fails, `piste fix` starts the agent with the error
already in hand ([A build or a switch fails](troubleshooting.md#a-build-or-a-switch-fails)).
