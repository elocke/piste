# Command reference

- What can piste do, and which command does it?
- Which arguments and flags does a command take?

Generated from piste's own help by `cli/internal/cli/docs_test.go`; don't
edit it by hand. After changing a command, run this in `cli/`:
`PISTE_UPDATE_DOCS=1 go test ./internal/cli -run TestCommandsDoc`.
Plugins (`piste-<name>` on your PATH, see [customising.md](customising.md))
aren't listed: `piste --help` shows yours.

- [piste add](#piste-add): Clone an overlay into this checkout and build it
- [piste adopt](#piste-adopt): Adopt what you installed outside Nix: write it into your overlay, switch, remove the outside copies
- [piste agents](#piste-agents): Agent skills, instructions, hooks and MCP (piste.agents)
- [piste build](#piste-build): Build configuration without applying
- [piste check](#piste-check): Fast syntax/eval check (no build)
- [piste claude](#piste-claude): Launch Claude Code at the piste checkout, with its overlays added
- [piste clean](#piste-clean): Clean up old generations (wraps nh clean: all on a Mac or NixOS host, else user)
- [piste drift](#piste-drift): Detect package drift: installs outside nix that should be tracked
- [piste dry](#piste-dry): Dry run: show what would change
- [piste fix](#piste-fix): Rebuild after a failed build or switch, and hand the error to Claude Code or Codex
- [piste gendiff](#piste-gendiff): Preview diff vs current generation (wraps `nh darwin|os|home diff`)
- [piste github](#piste-github): Set up a GitHub account over SSH: its own key for auth and signing, uploaded, and in your overlay
- [piste host](#piste-host): Add this machine to an overlay
- [piste init](#piste-init): Create an overlay in this checkout from the engine's template
- [piste leaks](#piste-leaks): Check unpushed commits, or a tree, for private words, addresses and secrets
- [piste log](#piste-log): Show recent configuration generations
- [piste mcp](#piste-mcp): Reconcile MCP servers against the declarative manifest
- [piste pkgs](#piste-pkgs): Show what's out of date, and who updates it
- [piste rollback](#piste-rollback): Revert to previous generation
- [piste secrets](#piste-secrets): Manage sops-encrypted secrets
- [piste setup](#piste-setup): Set this machine up: prerequisites, the checkout, an overlay, the first switch
- [piste status](#piste-status): Health dashboard: host, nixpkgs age, git state, drift summary
- [piste switch](#piste-switch): Build and apply configuration
- [piste update](#piste-update): Move to the newest piste release and update the overlays' inputs
- [piste where](#piste-where): Show the flake dir and engine override piste targets from the cwd

## Global flags

```text
piste: Nix configuration management CLI.

Unknown commands run `piste-<command>` from PATH (plugins).
`piste --licenses` prints the third-party notices.
```

These go with every command:

```text
      --overlay string   the overlay of the piste checkout to act on (default: the one you are in, else the one with this host)
  -v, --verbose          verbose output (echo external commands)
```

`piste --version` prints piste's version. `piste completion bash|zsh|fish`
prints a shell's completion script (piste's package installs them).

## piste add

`piste add <git url> [flags]`: Clone an overlay into this checkout and build it

```text
Clone an existing overlay (a flake built with piste.lib.mkFlake) into
overlays/<name>/ of this piste checkout, then build it if its hosts include
this machine. To add a package, use piste pkg add.
```

Flags:

```text
      --name string   its directory in overlays/ (default: the repo's name)
```

## piste adopt

`piste adopt`: Adopt what you installed outside Nix: write it into your overlay, switch, remove the outside copies

```text
Finds what is installed outside Nix (Homebrew formulae and casks, npm and pnpm
globals, cargo install, pip --user) and shows where each would go in this host's
overlay: nixpkgs, Homebrew's lists or pnpmPackages. The ticked ones are written
into the lists the overlay already keeps; then it offers the switch, and after a
switch that worked, offers to remove the copies outside Nix that Nix now
provides. Nothing is removed without that switch. Taps' items, excluded names and
names nixpkgs doesn't have are listed, not offered. A NixOS host isn't supported
yet: add its packages to its piste.packages.extraPackages by hand.
```

## piste agents

`piste agents`: Agent skills, instructions, hooks and MCP (piste.agents)

```text
Agent content for the harnesses (Claude Code, Codex, Hermes).

User scope ($HOME):  apply, check — what the piste.agents module declared.
Project scope (git root, .agents/agents.toml):  init, add, remove, sync, drift, audit.
Registry:  list, show.

Zone content is read only from explicit roots: --zone-root name=zone:/dir, or the
sources of the Nix-written manifest (~/.config/piste-agents/manifest.json). Nothing
else in $HOME, parent directories or the environment is consulted;
PISTE_AGENTS_* variables apply only with --allow-env.
```

Flags:

```text
      --allow-env               honour PISTE_AGENTS_HOME / PISTE_AGENTS_ROOTS
      --home string             home directory (default $HOME)
      --ledger string           ledger of the user scope (default ~/.local/state/piste-agents/ledger.json)
      --manifest string         manifest of the user scope (default ~/.config/piste-agents/manifest.json)
      --registry string         read this built registry (default: ~/.local/share/piste-agents/registry, then /etc/piste/agent-bundle/registry)
      --root string             project root (default: the git root of the current directory)
      --workdir                 read the zone checkouts live instead of the built registry (authoring)
      --zone-root stringArray   zone root name=zone:/dir (repeatable)
```

### piste agents add

`piste agents add <id>... [flags]`: Use registry items in this project (zone + policy checked)

Flags:

```text
  -n, --dry-run       show the plan and diff; write nothing
      --force         overwrite or remove entries edited by hand since piste wrote them
      --zone string   this machine's zone (default: from the manifest, else the one non-shared --zone-root)
```

Also takes the flags of `piste agents`.

### piste agents apply

`piste agents apply [flags]`: Splice the composed instruction blocks (home-manager activation)

Flags:

```text
      --force   overwrite a hand-edited managed block
```

Also takes the flags of `piste agents`.

### piste agents audit

`piste agents audit [flags]`: Inventory of the project's agent content, attributed (works before init)

Flags:

```text
      --json          output as JSON
      --zone string   this machine's zone (default: from the manifest, else the one non-shared --zone-root)
```

Also takes the flags of `piste agents`.

### piste agents check

`piste agents check [flags]`: Compare links, instruction blocks, hooks, plugins, marketplaces, tools and pins with the disk

```text
Compare links, instruction blocks, hooks, plugins, marketplaces, tools and pins with the disk.

States: ok | missing | drift | undeclared | policy | tolerated (+ deferred / unmanaged for
skills). Exits non-zero on anything but ok/tolerated/deferred/unmanaged, and on
undeclared only when the zone policy says unlisted = "deny".
```

Flags:

```text
      --json          output as JSON
      --skills-only   links and instruction blocks only; skip the harness CLIs
```

Also takes the flags of `piste agents`.

### piste agents drift

`piste agents drift [flags]`: Lock ↔ disk ↔ registry comparison; exits 1 on drift

Flags:

```text
      --ci            also fail on unknown entries (a branch-protection check)
      --json          output as JSON
      --zone string   this machine's zone (default: from the manifest, else the one non-shared --zone-root)
```

Also takes the flags of `piste agents`.

### piste agents init

`piste agents init [flags]`: Write .agents/agents.toml; --adopt imports .mcp.json (secrets redacted)

Flags:

```text
      --adopt             import existing .mcp.json servers as project-local entries
  -n, --dry-run           print the manifest; write nothing
      --force             rewrite an existing manifest
      --harness strings   harnesses (claude, codex, agentsmd) (default [claude,codex])
      --zone string       this machine's zone (default: from the manifest, else the one non-shared --zone-root)
```

Also takes the flags of `piste agents`.

### piste agents list

`piste agents list [flags]`: List registry items

Flags:

```text
      --json          output as JSON (the items' metadata)
      --kind string   only this kind (skill, 3p-skill, agent, instruction, hook, mcp, …)
      --zone string   this machine's zone (default: from the manifest, else the one non-shared --zone-root)
```

Also takes the flags of `piste agents`.

### piste agents remove

`piste agents remove <id>... [flags]`: Stop using items; removes exactly what the lock says piste wrote

Flags:

```text
  -n, --dry-run       show the plan and diff; write nothing
      --force         overwrite or remove entries edited by hand since piste wrote them
      --zone string   this machine's zone (default: from the manifest, else the one non-shared --zone-root)
```

Also takes the flags of `piste agents`.

### piste agents search

`piste agents search [<text>...] [flags]`: Search the registry (every word must match the id or description)

Flags:

```text
      --harness string   only items this harness supports (claude, codex, hermes)
      --in-zone string   only items of this zone (shared, personal, work)
      --json             output as JSON (the items' metadata)
      --kind string      only this kind (skill, 3p-skill, agent, instruction, hook, mcp, plugin, …)
```

Also takes the flags of `piste agents`.

### piste agents show

`piste agents show <id> [flags]`: Show one registry item: metadata, harness support, requirements, version

Flags:

```text
      --json          output as JSON
      --zone string   this machine's zone (default: from the manifest, else the one non-shared --zone-root)
```

Also takes the flags of `piste agents`.

### piste agents sync

`piste agents sync [--user] [flags]`: Reconcile the project with .agents/agents.toml (never pulls newer versions)

```text
In a project, sync reconciles the project with .agents/agents.toml.
--user, or outside a project, runs the user-scope sync: the third-party lock,
the carrier and hooks.
```

Flags:

```text
      --apply         user scope, with --prune: really remove them
  -n, --dry-run       show the plan and diff; write nothing
      --force         overwrite or remove entries edited by hand since piste wrote them
      --no-mcp        user scope: don't run piste mcp sync
      --prune         user scope: also list undeclared items for removal (dry run unless --apply)
      --repin         user scope: switch drifted github marketplaces to a pinned local mirror
      --user          sync the user scope (third-party lock, carrier, hooks)
      --zone string   this machine's zone (default: from the manifest, else the one non-shared --zone-root)
```

Also takes the flags of `piste agents`.

### piste agents update

`piste agents update [NAME...] [flags]`: Resolve pins and rewrite agents/agents.lock (run inside a zone repo)

```text
Resolve pins and rewrite agents/agents.lock (run inside a zone repo).

`piste agents update` fills in missing entries; `update NAME…` moves those;
`update --all` moves everything. `--from-installed`
pins plugins and marketplaces to what this machine has.
```

Flags:

```text
      --all              move every item
      --base string      piste agents/agents.base.toml (default: this host's switched copy)
      --from-installed   pin plugins/marketplaces to what this machine has
      --zone string      the zone's agents/ dir (default "agents")
```

Also takes the flags of `piste agents`.

## piste build

`piste build [flags]`: Build configuration without applying

Flags:

```text
      --host string   Act on another host of the overlay, by hostname (default: this machine's)
```

## piste check

`piste check`: Fast syntax/eval check (no build)

## piste claude

`piste claude [claude args...]`: Launch Claude Code at the piste checkout, with its overlays added

## piste clean

`piste clean [flags]`: Clean up old generations (wraps nh clean: all on a Mac or NixOS host, else user)

```text
Delete old generations and garbage-collect the store, with nh clean. On a Mac,
and on a NixOS machine the overlay has as a NixOS host, nh clean all, as the
system's generations are piste's. Elsewhere it is nh clean user: only your own
profiles, no sudo, and other users' rollback points stay.
```

Flags:

```text
      --dry-run             Preview what would be removed without deleting.
      --keep int            Keep at least this many recent generations regardless of age. Default: 5. (default 5)
      --keep-since string   Keep generations newer than this age (e.g. 7d, 30d, 2w). Default: 30d. (default "30d")
```

## piste drift

`piste drift`: Detect package drift: installs outside nix that should be tracked

```text
Lists what is installed outside Nix (Homebrew, npm and pnpm globals, cargo,
pip --user) against what this host declares, read from one eval: what Nix
provides too, and what isn't declared. Read-only; piste adopt acts on it.
```

## piste dry

`piste dry [flags]`: Dry run: show what would change

Flags:

```text
      --host string   Act on another host of the overlay, by hostname (default: this machine's)
```

## piste fix

`piste fix [flags]`: Rebuild after a failed build or switch, and hand the error to Claude Code or Codex

```text
Rebuild what `piste switch` builds (the checkout's engine, --show-trace), without
activating, and keep the output in ${XDG_STATE_HOME:-~/.local/state}/piste/fix.log.
At a terminal, then start Claude Code (else Codex) at the piste checkout with the
error and the piste-fix skill. PISTE_AI=claude|codex picks one; PISTE_AI=none, no AI.
```

Flags:

```text
      --host string   Act on another host of the overlay, by hostname (default: this machine's)
```

## piste gendiff

`piste gendiff [flags]`: Preview diff vs current generation (wraps `nh darwin|os|home diff`)

Flags:

```text
      --host string   Act on another host of the overlay, by hostname (default: this machine's)
```

## piste github

`piste github [flags]`: Set up a GitHub account over SSH: its own key for auth and signing, uploaded, and in your overlay

```text
Set this machine up for a GitHub account over SSH, with gh logged in:

  1. The account: gh's active one, or --login <name>.
  2. A key of its own, ~/.ssh/id_ed25519_github_<login> (--key), made if missing
     (no passphrase; add one with ssh-keygen -p -f <key>).
  3. The key on the account, for authentication and for signing (gh ssh-key add);
     what GitHub already has is skipped. --no-upload leaves this to you.
  4. piste.github = { login; id; } in the overlay's host file (the one flake.nix lists
     for this machine's host or --host's, else hosts/<host>.nix), staged, never
     committed. --no-overlay leaves this to you.

After the next switch, github.com uses the key, commits are signed with it, and the
git email is the account's noreply address unless your overlay sets its own.
Each step already done is skipped, so run again whenever. Each change asks first
(--yes: don't). --dry-run says what would change and changes nothing.

gh needs the admin:public_key and admin:ssh_signing_key scopes to upload:
  gh auth refresh -h github.com -s admin:public_key,admin:ssh_signing_key
```

Flags:

```text
      --dry-run        say what would change, change nothing
      --host string    the overlay host whose file to edit, by hostname (default: this machine's)
      --key string     the private key (default ~/.ssh/id_ed25519_github_<login>)
      --login string   the GitHub account (default: gh's active one)
      --no-overlay     don't edit the overlay
      --no-upload      don't upload the key to GitHub
      --yes            don't ask before changing anything
```

## piste host

`piste host`: Add this machine to an overlay

### piste host add

`piste host add [flags]`: Make this machine a host of an overlay, with its own age key if the overlay keeps secrets

```text
Make the machine you're on a host of an overlay: the one you're in, the one
--overlay names, or the only one. Its host file (hosts/<hostname>.nix, from the
engine's template) and its entry in flake.nix are staged, and it is built. What's
yours on every machine comes from the overlay's home.nix.

On NixOS the host is the whole system, hosts/<name>/: its machine/ is a copy of
this machine's /etc/nixos, rewritten to build from the flake (what can't be is
marked "# piste:" and listed), and the keys in ~/.ssh/authorized_keys may log
in. Its name is --name, else the hostname; a fresh machine's (nixos) is asked for
at a terminal. So is a name the overlay has a NixOS host of already, unless that
host's root filesystem is this machine's, by uuid: another machine's is refused,
even with --name, and one piste can't tell (a root by label or device name,
which every machine made from one image shares) needs --name. A name that isn't
the hostname becomes it now (sudo hostname), and for good at the switch, but an
image module that forces its own name (Proxmox images, Proxmox LXCs) wins; host
add warns when it does, with the setting that keeps the name.

Its roles are the ones the overlay's other hosts of its kind (Mac, NixOS or
Linux) have, extras included (the ones they all have, if they differ); with no
other host of its kind, init's defaults. --role gives it others.

If the overlay keeps secrets (.sops.yaml), this machine gets an age key, made if
missing, and its public key goes into .sops.yaml. This machine can't read the
secrets until a machine that can runs piste secrets updatekeys; the steps are
printed. piste stages; it never commits or pushes. Run again, it skips each
step already done.

To remove a machine, by hand: delete its entry in flake.nix and its host file
(a NixOS host's directory), remove its key from .sops.yaml (keys: and every age
list), run piste secrets updatekeys (it also makes a new data key), and change
the secrets' values: that machine has read them. If it had a GitHub key (piste
github), revoke it: gh ssh-key list, then gh ssh-key delete <id>.
```

Flags:

```text
      --extras         add the extras role to the roles, as piste init --extras does (--extras=false leaves it out)
      --name string    on NixOS, the host's name, which becomes this machine's hostname (default: its hostname)
      --role strings   a role: base, dev, desktop or extras (repeatable; default: the roles of the overlay's other hosts of this kind, else dev on Linux, none on macOS)
```

## piste init

`piste init [name] [flags]`: Create an overlay in this checkout from the engine's template

```text
Create an overlay in overlays/<name>/ of this piste checkout from the engine's
templates/default, then build it. Each answer has a flag; a missing one is asked
for (Enter takes the default shown). The overlay is git-initialised with its
files staged; pushing it anywhere is up to you.

The last question, "Start minimal or with everything?", is --extras: everything
adds the extras role to the host (docs/customising.md, "The extras pack");
minimal, the default, is --extras=false.

On a NixOS machine (or with --nixos) the host is the whole system, as piste host
add makes it: hosts/<host>/, its machine/ a copy of this machine's /etc/nixos and
the keys in ~/.ssh/authorized_keys allowed in; a host name that isn't the hostname
becomes it (sudo hostname). Made elsewhere, its machine/ is the template's VM.
--nixos=false makes a home-manager home instead.
```

Flags:

```text
      --engine string      the flake ref the overlay pins the engine at (default: the checkout's origin at its release)
      --extras             start with everything: the extras role beside the others (default: minimal)
      --git-email string   your git email (default: git config --global user.email)
      --git-name string    your git name (default: git config --global user.name)
      --host string        the host's name (default: this machine's)
      --layer string       a team layer's git URL: start the overlay from the layer's starter, in its zone
      --nixos              a NixOS host, the whole system (default on a NixOS machine; --nixos=false: a home-manager home)
      --no-build           don't build it
      --role strings       a role: base, dev or desktop (repeatable; default dev on Linux, none on macOS; the extras role is --extras)
      --sops               set up sops secrets: an age key (kept if there is one) and .sops.yaml
      --system string      one of aarch64-darwin, aarch64-linux or x86_64-linux (default: this machine's)
      --user string        your user on this machine (default $USER)
      --zone string        the overlay's agent zone (default personal)
```

## piste leaks

`piste leaks [<dir>...] [flags]`: Check unpushed commits, or a tree, for private words, addresses and secrets

```text
Check what you are about to publish for private words, private addresses and
secrets: with no arguments, the commits on HEAD that no remote has (what a
push would add: files, file names, messages, authors); with directories,
every file under them (an exported tree, not a checkout).

The words come from every overlays/*/leak-markers*.txt of this piste
checkout (in a worktree, of its main checkout) and from --markers. A list
has one entry per line: a term of 1 to 4 words (any case, any separator)
or re:<regex>; # starts a comment.

Private (RFC1918) addresses and your home directory are always checked,
and gitleaks runs over the same commits or files with its default rules:
a .gitleaks.toml is not read, and a .gitleaksignore is refused (mark the
line gitleaks:allow instead).

Exit status: 0 clean, 1 something found, 2 could not check.
```

Flags:

```text
      --markers stringArray   also check the words of this marker list (repeatable)
```

## piste log

`piste log`: Show recent configuration generations

## piste mcp

`piste mcp`: Reconcile MCP servers against the declarative manifest

### piste mcp check

`piste mcp check`: Report unpinned manifest entries, and drift between the manifest and each harness

### piste mcp sync

`piste mcp sync [flags]`: Add missing servers and re-add drifted ones; with --prune, remove ours that are no longer wanted

Flags:

```text
      --apply     With --prune: actually make the changes.
      --dry-run   Print commands without running them.
      --prune     Also remove retired servers and servers declared for another host or harness. A dry run unless --apply is given.
```

## piste pkgs

`piste pkgs`: Show what's out of date, and who updates it

```text
Show what is out of date on this machine, grouped by who updates it. What your
overlay owns, piste pkgs update moves; the engine's moves with piste releases
(piste update), and a team layer's is a PR to that layer. This only reads.
```

### piste pkgs update

`piste pkgs update [flags]`: Bring this machine's packages up to date, then switch

```text
Bring this machine's packages up to date, within what its overlay owns:

  1. the checkout to the newest piste release (a checkout on a branch pulls it),
     and this overlay's engine pin to that release;
  2. the overlay's inputs (nix flake update), nixpkgs-fresh included;
  3. a build, with the version changes against the current generation (nh's
     dry switch; without nh, a plain build);
  4. the switch, once you say yes, with every check piste switch makes;
  5. then the pnpm globals of piste.packages.pnpm and, on a Mac, the declared
     Homebrew formulae and casks that brew lists as outdated.

Nothing on this machine changes before the switch: after a failed build, a no or
--no-switch, only the checkout and the overlay's flake.nix and flake.lock have
moved, the overlay's for you to commit once they work. Other overlays keep their
engine pins (piste update moves them). A pnpm or Homebrew upgrade that fails is
named, the run goes on, and it exits 1. --only pnpm or brew upgrades against
what is switched now, with no build.
```

Flags:

```text
      --dry-run        print the moves and the upgrades, and change nothing
      --greedy         also upgrade the casks that update themselves (brew upgrade --greedy)
      --no-switch      stop after the build: nothing on this machine changes
      --only strings   only these steps: nix (the engine, inputs, build and switch), pnpm or brew (repeatable)
  -y, --yes            switch without asking
```

## piste rollback

`piste rollback [flags]`: Revert to previous generation

Flags:

```text
  -y, --yes   Skip confirmation prompt
```

## piste secrets

`piste secrets`: Manage sops-encrypted secrets

### piste secrets edit

`piste secrets edit [file]`: Open a sops-encrypted file for editing

### piste secrets init

`piste secrets init`: Bootstrap age key and .sops.yaml for this machine

### piste secrets status

`piste secrets status`: Show secrets infrastructure health

### piste secrets updatekeys

`piste secrets updatekeys`: Rekey every secrets file for .sops.yaml's recipients (after piste host add)

```text
Re-encrypt every *.sops.yaml file of the overlay for the recipients its
.sops.yaml now lists (sops updatekeys -y, one file at a time), then under a
new data key (sops rotate -i), so a machine whose key was removed can't read
what comes next with the data key it had. Run it on a machine that can read
the secrets, after another machine's key was added (piste host add) or
removed; then commit and push. No secret value is written out in plaintext.
```

## piste setup

`piste setup [<overlay or team layer url>] [flags]`: Set this machine up: prerequisites, the checkout, an overlay, the first switch

```text
Guided onboarding. Checks what piste needs (and says how to fix what's
missing), clones the piste checkout at its newest release (unless run in one),
then the overlay: <url> is your overlay to add, or a team layer to start your
own overlay on (piste init --layer); with no url, the overlay that has this
machine, or a new one (piste init). It builds this machine and offers the
first switch; after it, piste agents sync --user and piste mcp sync bring the
plugins, hooks and MCP servers to Claude Code and Codex. When gh is logged in,
it first offers a GitHub SSH key for that account (see piste github).
```

Flags:

```text
      --dir string    where to clone piste (default ~/piste)
      --from string   the git URL to clone piste from (default "https://github.com/elocke/piste")
      --github        set up a GitHub SSH key for gh's account without asking (see piste github)
      --no-github     don't offer a GitHub SSH key
```

## piste status

`piste status`: Health dashboard: host, nixpkgs age, git state, drift summary

## piste switch

`piste switch [flags]`: Build and apply configuration

```text
Build the configuration and activate it.

When the switch changes the active generation, piste then checks it: a new
login shell (the user database's shell, in a clean environment, with no
terminal, as a new terminal or ssh login starts it) must run
"piste --version" within 30 seconds. If it doesn't, piste puts back the
generation that was active before, with nix-darwin's, NixOS's or
home-manager's own tools, and exits 1; the new generation stays in the
store. It checks and undoes only when the generation before passed the same
check, run just before the switch, so a first switch isn't checked.

On NixOS the switch is sudo nixos-rebuild switch, which needs a terminal for
sudo's password. Before it, piste evaluates the new system and refuses
(exit 1) one that would lock you out: an SSH session it leaves with no SSH
server or no key for you (or, switched as root, root's login turned off),
sudo taken away (no "wheel", or a password asked that you don't have), no
boot loader, or another machine's root filesystem.
--force switches without these lock-out checks.

On Linux, inside a systemd user service (an agent's runner, which the switch
may restart), the switch runs as its own unit, piste-switch-<time>.service:
its output is followed here and kept in
${XDG_STATE_HOME:-~/.local/state}/piste/switch.log. Ctrl-C leaves it running
(systemctl --user stop <unit> stops it). This needs systemd 250 or newer, for
systemd-run to pass your environment on by name; with an older one, run
piste switch outside the service: from a terminal or ssh, or under
systemd-run --user --scope.

--no-undo skips the checks and the undo. To undo a switch that passed:
piste rollback.
```

Flags:

```text
      --force         skip the NixOS lock-out checks
      --host string   Act on another host of the overlay, by hostname (default: this machine's)
      --no-undo       don't check that a new login shell runs piste, and don't undo the switch
```

## piste update

`piste update [input] [flags]`: Move to the newest piste release and update the overlays' inputs

```text
With no input: fetches piste's releases, prints the notes of each release between
yours and the newest (Upgrading notes set apart), moves the checkout to the newest
release (a checkout on a branch pulls it instead), moves every overlay's engine pin
to that release, and updates the overlays' other inputs. --dry-run prints the notes
and the moves and changes nothing. Going back: git -C <checkout> checkout <release>,
and git checkout flake.nix flake.lock in each overlay.

piste update <input> updates one input of this machine's overlay; an input the
engine owns (nixpkgs, home-manager when it follows the engine's) moves with piste
releases instead. piste update overlays is piste update-overlays.
```

Flags:

```text
      --dry-run   Print the notes and the moves without changing anything
```

## piste where

`piste where`: Show the flake dir and engine override piste targets from the cwd
