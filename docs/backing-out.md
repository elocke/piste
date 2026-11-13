# Backing out

- How do I undo the last switch?
- How do I go back further than that?
- How do I remove piste from a Mac, a Linux machine or a NixOS machine?
- What does a removal leave behind?
- How do I remove Nix itself?

Every switch makes a new *generation*: a complete, numbered copy of your
setup, kept beside the ones before it
([Profiles](https://nix.dev/manual/nix/latest/package-management/profiles)
in Nix's manual). Going back is switching to an older one, so nothing has to
be rebuilt.

Going back changes the machine, not your overlay. The next `piste switch`
applies what your overlay says again, so undo the change there too:
`git -C ~/piste/overlays/personal diff` shows an edit you haven't committed,
and `git -C ~/piste/overlays/personal checkout -- .` drops it;
`git -C ~/piste/overlays/personal revert HEAD` undoes your last commit.

## Undo the last switch

```bash
piste rollback
```

On Linux it shows the two generations and asks first (`--yes` doesn't ask):

```
Rolling back...
  Current:  id 42 (2026-09-30 10:30)
  Rollback: id 41 (2026-09-29 09:00)
  Path:     /nix/store/…-home-manager-generation
Activate previous generation? [y/N]: y
Rolled back to generation 41!
```

On a Mac and on NixOS it runs `sudo darwin-rebuild switch --rollback` or
`sudo nixos-rebuild switch --rollback`, asks for your password, and ends with
`Rollback complete!`. A machine with only one generation has nothing to go
back to: on Linux, piste says `Cannot rollback: fewer than 2 generations found.`

On NixOS, when your overlay has an error, piste can't tell which host this
machine is, but the system can still go back. piste prints the command:

```
To roll back the system by hand: sudo nixos-rebuild switch --rollback
```

### When a switch undoes itself

After a switch, piste checks that a new login shell starts and runs piste
within 30 seconds. If it doesn't, piste puts back the generation that was
active before, and you see:

```
Checking that a new login shell (/home/jane/.nix-profile/bin/zsh) starts and runs piste...
The new generation fails the check: the shell exited 127 after 0.4s.
  zsh: command not found: piste
Undoing the switch: back to generation 41...
Undone: generation 41 is active again. 42 stays in the store.
  To see what it changed:  piste gendiff
  To keep it anyway:       piste switch --no-undo
```

Your machine is as it was before the switch. The lines under
`The new generation fails the check:` say what went wrong, and
`piste gendiff` shows what the switch would have changed. Fix it in your
overlay and switch again ([A build or a switch fails](troubleshooting.md#a-build-or-a-switch-fails)).
`piste switch --no-undo` keeps the new generation anyway, to look at it; only
do that when you can still log in another way. A machine's first switch
isn't checked, as there's nothing to go back to.

## Go back further

`piste log` lists the recent generations. To switch to one by its number,
41 here:

- **On Linux,** each line of `piste log` gives a generation's date, number and
  path: `2026-09-29 09:00 : id 41 -> /nix/store/…-home-manager-generation`.
  Run that path's `activate`:
  `/nix/store/…-home-manager-generation/activate`, with the full path from
  your list. (`piste rollback` does the same with the one before.)
- **On a Mac:** `sudo darwin-rebuild --switch-generation 41`.
- **On NixOS:** choose it in the boot menu when the machine starts, or:

  ```bash
  sudo nix-env --profile /nix/var/nix/profiles/system --switch-generation 41
  sudo /nix/var/nix/profiles/system/bin/switch-to-configuration switch
  ```

Old generations stay until `piste clean` removes them: by default, it keeps
the newest five and any from the last 30 days.

## Before you remove piste

On any machine, do these first, while piste is still installed:

1. **Push your overlay.** It holds your settings, and your next machine
   starts from it. `git -C ~/piste/overlays/personal status` shows anything
   not committed yet; commit it, then
   `git -C ~/piste/overlays/personal push`.
2. **Keep your age key,** if you'll want those secrets again:
   `~/.config/sops/age/keys.txt`, copied into your password manager.
3. **Take piste's additions out of Claude Code and Codex.** Setup's
   `piste agents sync --user` and `piste mcp sync` added plugins, their
   marketplaces and MCP servers through each tool's own command, and hooks
   when a preset (`guard`, `rtk`) has them. They're the tools' own settings,
   not home-manager's files, so none of the removals below touches them, and
   the `claude` and `codex` commands piste installed go with it.
   `piste agents check` names the plugins, marketplaces and pinned tools; the
   MCP servers are the `[[mcp]]` entries in your overlay's
   `agents/agents.toml`. Remove each with the command piste uses itself:

   ```bash
   claude plugin marketplace remove <name>   # with the plugins installed from it
   codex plugin remove <id>
   claude mcp remove --scope user <name>
   codex mcp remove <name>
   pnpm remove -g <name>                     # a tool pinned with pnpm
   ```

   piste's own hooks are the `piste-local` marketplace in Claude Code, and the
   entries in `~/.codex/hooks.json` whose command is in
   `~/.local/share/piste-agents/hooks/bin/`: delete those entries. Then
   delete piste's block in `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md`,
   from `<!-- piste-agents:begin -->` to `<!-- piste-agents:end -->` (the rest
   of each file is yours), and piste's record of what it added,
   `~/.local/state/piste-agents/ledger.json`.

## Remove piste from a Mac

piste's Mac is a [nix-darwin](https://github.com/nix-darwin/nix-darwin)
system, and nix-darwin's uninstaller takes it away. It's on the Mac already:

```bash
sudo darwin-uninstaller
```

It says what it will do, and asks `Proceed? [y/n]`:

```
Uninstalling nix-darwin, this will:

    - remove /Applications/Nix Apps symlink
    - cleanup static /etc files
    - disable and remove all launchd services managed by nix-darwin
```

If `darwin-uninstaller` isn't found, nix-darwin's README gives its newest
version instead:
`sudo nix --extra-experimental-features "nix-command flakes" run nix-darwin#darwin-uninstaller`.

What stays: [what any removal leaves](#what-any-removal-leaves) (your
overlay, your age key, your `.backup-` files and the checkout), and:

- **piste's files in your home,** such as `~/.zshrc`: links into the Nix
  store, which stay as they are. Remove the ones you don't want before you
  put your own back.
- **Homebrew and the apps it installed.** piste's Homebrew runs from the Nix
  store, so it stops working if you remove Nix too; to keep it, reinstall it
  from [brew.sh](https://brew.sh).
- **The macOS settings piste made,** such as the Dock's: change them back in
  System Settings.
- **The old generations.** The uninstaller ends with a note that they're
  still there, and they go with Nix itself.

## Remove piste from Linux

On Linux, piste is a home-manager setup in your home, and home-manager's own
`uninstall` removes it.

1. If you made zsh your login shell, change it back first:
   `chsh -s /bin/bash`. The uninstall removes piste's zsh, and a login shell
   that doesn't exist can stop you logging in. (You can also take its line
   out of `/etc/shells`.)
2. Run home-manager's uninstall:

   ```bash
   nix run github:nix-community/home-manager -- uninstall
   ```

   It asks `Really uninstall Home Manager? [y/n]`, then removes every file and
   package piste put in your home, piste itself among them, and every
   generation. It ends with
   `Home Manager is uninstalled but your home.nix is left untouched.` (its
   own `home.nix`, not your overlay's). A Nix from another installer than
   Determinate's may stop it with
   `error: file 'nixpkgs' was not found in the Nix search path (add it using $NIX_PATH or -I)`;
   then run it as
   `nix run github:nix-community/home-manager -- -I nixpkgs=flake:nixpkgs uninstall`.

What stays: [what any removal leaves](#what-any-removal-leaves) (your
overlay, your age key, your `.backup-` files and the checkout), and Nix.

## Remove piste from NixOS

piste's NixOS host started from your machine's own configuration, in
`/etc/nixos`, and left it there. Switching to it again takes piste away:

```bash
sudo nixos-rebuild switch -I nixpkgs=/nix/var/nix/profiles/per-user/root/channels/nixos -I nixos-config=/etc/nixos/configuration.nix
```

The two `-I` settings tell it where `/etc/nixos` and your NixOS release's
packages are (NixOS's own defaults). piste's switch replaced them, so without
them `nixos-rebuild` can't find `/etc/nixos`. After this switch, a plain
`sudo nixos-rebuild switch` works as before.

The packages come from root's `nixos` channel (the list of packages for your
NixOS release, which the machine downloads from nixos.org). `sudo -i nix-channel --list`
shows root's channels (a plain `sudo` shows yours instead). A machine made
from an image may have none. Then add the channel of the release the machine
had, before running the switch above:

```bash
sudo -i nix-channel --add https://channels.nixos.org/nixos-<release> nixos
sudo -i nix-channel --update
```

`<release>` is the machine's NixOS release from before piste, such as
`25.11`. `nixos-version` now prints piste's, so read it from a generation
from before your first piste switch: `ls /nix/var/nix/profiles` lists them as
`system-<n>-link`, and `cat /nix/var/nix/profiles/system-<n>-link/nixos-version`,
for the lowest `<n>`, prints the version it ran, whose first two numbers are
the release. Check it against the `system.stateVersion` in
`/etc/nixos/configuration.nix`, the release the machine was installed with. A
garbage collection may have deleted the generations from before piste, and
then the lowest one left is piste's own: it has piste in it
(`ls /nix/var/nix/profiles/system-<n>-link/sw/bin/piste` finds it). Then take
`system.stateVersion`'s release instead.

piste's checks don't run for this switch, so first make sure `/etc/nixos`
lets you back in: your SSH keys, your sudo rules and your login shell come
from it again, not from your host file. Do it at the machine's console, or
keep a second session open until you've logged in again. Flakes are off
again too, unless `/etc/nixos` turns them on.

What stays: [what any removal leaves](#what-any-removal-leaves) (your
overlay, your age key, your `.backup-` files and the checkout), and:

- **piste's files in your home:** links into the Nix store, which you can
  remove before you move your own files back.
- **The generations piste made,** in the boot menu as well:
  `sudo nix-collect-garbage -d` deletes every old one, and the next switch
  takes them off the menu.

## What any removal leaves

- **Your overlay,** in `~/piste/overlays/personal` and in your private
  repository.
- **Your age key,** `~/.config/sops/age/keys.txt`, which you need to read
  those secrets again.
- **Your own files the first switch moved aside,** as `<file>.backup-<time>`.
  Once piste's file is gone, move each back, for example
  `mv ~/.zshrc.backup-<time> ~/.zshrc`.
- **The checkout,** `~/piste` (or wherever `setup --dir` put it), with your
  overlay inside. Once your overlay is pushed, delete it: `rm -rf ~/piste`.
- **Your GitHub key,** if you ran `piste github`: on this machine,
  `~/.ssh/id_ed25519_github_<login>`, and on your GitHub account, where
  `gh ssh-key list`, then `gh ssh-key delete <id>`, removes it.

## Remove Nix itself

Remove piste first (above): uninstalling Nix removes everything in `/nix`,
and Determinate's README lists network requests failing on a Mac whose Nix
was removed while nix-darwin was still installed.

With Determinate Nix, on a Mac or on Linux:

```bash
/nix/nix-installer uninstall
```

It says what it will remove, and asks before it does it. A Nix from another
installer has its own steps:
[Uninstalling Nix](https://nix.dev/manual/nix/latest/installation/uninstall).
On NixOS, Nix is the system itself: there's nothing to remove.

What stays: [what any removal leaves](#what-any-removal-leaves).
