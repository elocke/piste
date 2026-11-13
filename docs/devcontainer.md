# Devcontainers

- How do I build a devcontainer image from the same modules as my machines?
- What is in the image, and what isn't?
- How do agent content and credentials get into a container?
- How do I build and try one?

piste builds devcontainer images (OCI images for VS Code's Dev Containers and
the `devcontainer` CLI) from the same home-manager modules your machines use.
The engine owns the shared base; your overlay adds its own modules, gets an
image output, and keeps the `.devcontainer/devcontainer.json` that uses it.

| Piece | Where | What it is |
|-------|-------|------------|
| `homeManagerModules.container` | `modules/roles/container.nix` | The `dev` role minus what only a real machine needs: headless package categories, no GUI vim, trimmed locales, commit signing off, and `CLAUDE_CONFIG_DIR` / `CODEX_HOME` pinned so one volume holds each harness's state. |
| `lib.mkDevcontainer` | `lib/devcontainer.nix` | Evaluates the container role plus your modules, then builds a Nix-only OCI image with `dockerTools.streamLayeredImage`. Returns `{ image, load, home }`. |
| `lib.validateAgentBundle` | `lib/devcontainer.nix` | A build-time check of an agent bundle. Runs inside `mkDevcontainer`, and as the engine's `checks.<system>.devcontainer-agent-bundle`. |
| `packages.<linux>.devcontainer` | `flake/parts/per-system.nix` | The engine's base image with no zone content, for testing the shared layer on its own. |
| your image | your overlay's `flake.nix` → `packages.<linux>.devcontainer{,-load}` | `mkFlake`'s `devcontainer` argument, or `mkDevcontainer` called by hand. |

## From your overlay

With `mkFlake`, one argument (and, for agent content, `agents` and `bundle`):

```nix
piste.lib.mkFlake {
  # … user, zone, hosts …
  agents = ./agents;
  bundle.presets = [ "personal:bundle/base" ];      # optional: the agent bundle (below), presets as
                                                    # <zone>:<name> (agents/presets/bundle/base.toml)
  devcontainer = {
    name = "personal-devcontainer";                 # image name; the tag is a content hash
    modules = [ ./modules/devcontainer.nix ];       # your additions to the container role
  };
}
```

Or by hand:

```nix
dc = piste.lib.mkDevcontainer {
  system = "x86_64-linux";              # or aarch64-linux
  name = "personal-devcontainer";
  modules = [ ./modules/devcontainer.nix ];
  extraSpecialArgs = { };
  agentBundle = null;                   # or a bundle directory or derivation (below)
  agentRegistry = null;                 # or piste.lib.mkAgentRegistry { … } (below)
  # user ? "dev", uid ? 1000, gid ? 1000, overlays ? [ ]
};
# dc.image: `$(nix build --print-out-paths .#devcontainer) | docker load`
# dc.load:  loads only if that exact build is missing, then tags :latest
```

## What the image contains

- **No distro base.** The image holds the closure of `home.path` and a small
  `/bin` userland: bash, zsh, coreutils, gawk, sed, grep, tar, gzip, procps,
  getent and which. It also has `/etc/passwd` with the user `dev` (uid 1000),
  CA certificates and `/usr/bin/env`.
- **Foreign binaries work.** `nix-ld` sits at `/lib64/ld-linux-x86-64.so.2`,
  and `NIX_LD` / `NIX_LD_LIBRARY_PATH` are set in the image's environment. VS
  Code Server, uv-managed Pythons and npm prebuilds all run.
- **No home-manager activation.** There is no Nix inside the image. `$HOME`
  gets a copy of `home-files`, and `~/.nix-profile` links to `home.path`.
  Anything a module does in `home.activation` is skipped, including pnpm
  globals and `mise install`. Files a tool must rewrite at runtime go in
  `piste.roles.container.mutableFiles`, which the image copies as real,
  writable files. `gh`'s `config.yml` is one of them by default.
- **Left out of the package set** (with `piste.packages.exclude`):
  `proto`, `gemini-cli`, `zrok`, `doctl`, `cmake`, `portaudio`, `mosh`,
  `eternal-terminal`, `bandwhich`, `iftop`, `hunspell`, `unrar`, `fontconfig`
  and `yazi`. proto pulls in gcc, and yazi's previewers pull in ffmpeg. Your
  modules can add any of them back with `extraPackages`, which `exclude`
  never filters. Docker is the client only (the daemon can't run
  unprivileged), and an `ssh` client is added, since the image has no distro
  to supply one.

## Agent bundle

`agentBundle` takes the layout `piste agents bundle --target claude,codex
<presets>` renders, which is what `mkFlake`'s `bundle.presets` builds for you
(`piste.lib.mkAgentBundle`):

```
etc/claude-code/managed-settings.d/*.json   → /etc/claude-code/...  (system scope)
etc/claude-code/managed-mcp.json            → /etc/claude-code/...
etc/claude-code/CLAUDE.md                   → managed policy memory
etc/codex/skills/*                          → /etc/codex/skills/...
home/.claude/CLAUDE.md, home/.codex/AGENTS.md → seeded into $HOME once
hooks/*.sh, manifest.json                   → /etc/piste/agent-bundle/
```

- `etc/*` is installed into the image's `/etc`, so the image never writes a
  `$HOME` file the harness rewrites.
- `home/*` is copied into `$HOME` by `piste-devcontainer-init`, which your
  `devcontainer.json` runs as its `postCreateCommand`. It runs after the
  volumes are mounted and never overwrites an existing file.
- Every JSON file under `etc/claude-code`, and `manifest.json`, must parse, or
  the image build fails: an invalid managed-settings file would stop Claude
  Code from starting.
- `piste agents bundle --check <dir>` verifies a bundle tree: no extra,
  missing or changed skill.

## Agent registry and the static piste

`agentRegistry` takes a zone's `piste.lib.mkAgentRegistry { pkgs; zone;
sources; }` (`mkFlake` passes your zone's when you give it `agents`). The image
always has the static `piste` on `PATH`; with a registry,
`/etc/piste/agent-bundle/registry` holds it and `piste agents search`,
`show` and `add` read it by default. `piste-devcontainer-init` runs `piste
agents sync` in `$PWD` and in each `/workspaces/*` repo that has
`.agents/agents.toml`; it writes only to that bind-mounted repo. A registry
holds only its own zone's items plus `shared:*`: the builder refuses an item
of another zone, and an overlay can re-check the built tree in its own checks
(`mkFlake`'s `checks`).

## Credentials: at runtime only

No secret is ever baked into an image. Each credential comes in when the
container runs:

| Credential | How it comes in |
|------------|-----------------|
| Claude login | Run `claude` and `/login` once inside the container. It persists in a volume mounted at `~/.claude` (`CLAUDE_CONFIG_DIR`). Or set `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY` in `remoteEnv`. |
| Codex login | Run `codex login --device-auth` once. It persists in a volume mounted at `~/.codex` (`CODEX_HOME`). Or set `OPENAI_API_KEY`. |
| GitHub (`gh`) | Forward `GH_TOKEN` from the host (`remoteEnv` → `${localEnv:GH_TOKEN}`). Or run `gh auth login` inside, since `config.yml` is writable. |
| git push over SSH | VS Code forwards the host's SSH agent. With the devcontainer CLI, bind-mount `$SSH_AUTH_SOCK` and set `SSH_AUTH_SOCK` in `remoteEnv`. |
| Other tokens (an MCP server's, say) | Forward them from the host the same way, `${localEnv:NAME}` in `remoteEnv`. |
| sops age key | Not mounted by default. If you need it, add a read-only bind of `~/.config/sops/age` in a local override; better, forward only the decrypted variable you need. sops-nix itself is inert in the image, as there's no systemd. |

## Build and try one

```bash
nix build .#devcontainer --print-out-paths   # in your overlay (on Linux)
$(nix build --print-out-paths .#devcontainer) | docker load
docker run --rm -it <name>:<tag> zsh -lc 'claude --version; codex --version'
nix run .#devcontainer-load                   # load only if missing, and tag :latest
```

Uncompressed, an image is about 3 GB. `devcontainer up` (from
`@devcontainers/cli`) with a `devcontainer.json` that names `<name>:latest`
and runs `piste-devcontainer-init` as `postCreateCommand` starts it and
seeds the bundle's files.
