# piste.lib.mkDevcontainer — build a devcontainer OCI image from a zone's
# home-manager configuration.
#
# The image is Nix-only (no distro base): dockerTools.streamLayeredImage over
# the closure of a home-manager "container" profile (homeManagerModules.
# container) plus whatever modules the zone adds. Zones call this from their
# own flake; piste itself only exports the function and a zone-less base
# image for testing.
#
#   piste.lib.mkDevcontainer {
#     system = "x86_64-linux";
#     name = "work-devcontainer";
#     modules = [ ./modules/git.nix ];
#     agentBundle = null;          # hook for `piste agents bundle` (see below)
#     agentRegistry = null;        # piste.lib.mkAgentRegistry (see below)
#   }
#   => { image; load; home; }
#
#   image  streamLayeredImage script: `$(nix build --print-out-paths .#x) | docker load`
#   load   script that loads the image only when that exact build is missing,
#          then tags it <name>:latest (what .devcontainer/devcontainer.json uses)
#   home   the evaluated home-manager configuration, for inspection
#
# Home-manager activation never runs: there is no Nix inside the image. The
# image gets home.path (on PATH, and as ~/.nix-profile) and a copy of
# home-files in $HOME. Activation-only side effects are therefore absent;
# config files a tool must rewrite go in piste.roles.container.mutableFiles.
#
# Foreign binaries: VS Code Server, uv-managed Pythons and npm prebuilds are
# ordinary glibc binaries that expect /lib64/ld-linux-x86-64.so.2. The image
# puts nix-ld there, with NIX_LD / NIX_LD_LIBRARY_PATH in the image env, so
# they run without a distro base.
#
# Agent bundle: a directory (or derivation) laid out as `piste agents bundle`
# renders it —
#   etc/claude-code/managed-settings.d/*.json, etc/claude-code/managed-mcp.json,
#   etc/codex/skills/*, home/.claude/CLAUDE.md, home/.codex/AGENTS.md,
#   hooks/*.sh, manifest.json
# etc/* is installed into the image's /etc (system scopes, so the image never
# writes a $HOME file the harness rewrites at runtime). home/* is seeded into
# $HOME by `piste-devcontainer-init` on first start, never overwriting.
# The whole bundle stays readable at /etc/piste/agent-bundle. Every JSON
# file under etc/claude-code is validated at build time: an invalid managed
# settings drop-in stops Claude from starting.
#
# Agent registry: a zone's `piste.lib.mkAgentRegistry`
# derivation. It is installed at /etc/piste/agent-bundle/registry, where
# the static `piste` in the image (always there) reads it; it holds only the
# zone's own items (a work registry has no personal item by construction).
# `piste-devcontainer-init` runs `piste agents sync` in each workspace repo
# that has .agents/agents.toml; it writes only to the bind-mounted repo.
#
# Secrets are never baked in. Credentials arrive at runtime through env and
# mounts; see the zone repos' README "Devcontainer" section.
{ inputs, self }:

let
  inherit (inputs) nixpkgs home-manager;

  # Fails the build when the bundle's Claude JSON does not parse, or when it
  # has no recognised layout at all. Exposed so zone CI can run it alone.
  validateAgentBundle =
    pkgs: bundle:
    pkgs.runCommand "agent-bundle-validated"
      {
        nativeBuildInputs = [ pkgs.jq ];
      }
      ''
        cd ${bundle}
        if [ ! -d etc ] && [ ! -d home ] && [ ! -f manifest.json ]; then
          echo "agent bundle ${bundle} has no etc/, home/ or manifest.json" >&2
          exit 1
        fi
        status=0
        for f in manifest.json $(find etc/claude-code -name '*.json' 2>/dev/null); do
          [ -e "$f" ] || continue
          if ! jq empty "$f"; then
            echo "invalid JSON in agent bundle: $f" >&2
            status=1
          fi
        done
        [ "$status" -eq 0 ] || exit 1
        ln -s ${bundle} $out
      '';

  mkDevcontainer =
    {
      system ? "x86_64-linux",
      name,
      tag ? null,
      user ? "dev",
      uid ? 1000,
      gid ? 1000,
      modules ? [ ],
      overlays ? [ ],
      extraSpecialArgs ? { },
      agentBundle ? null,
      agentRegistry ? null,
    }:
    let
      pkgs = import nixpkgs {
        inherit system;
        config.allowUnfree = true;
        overlays = [ self.overlays.shared ] ++ overlays;
      };
      inherit (pkgs) lib;

      homeDir = "/home/${user}";

      home = home-manager.lib.homeManagerConfiguration {
        inherit pkgs;
        extraSpecialArgs = {
          inherit user;
          piste = self;
        } // extraSpecialArgs;
        modules = [
          # The dev role declares piste.secrets, which is built on sops-nix
          # options. Inert in the image: sops-nix renders secrets through a
          # systemd user unit, and there is no systemd here.
          inputs.sops-nix.homeManagerModules.sops
          self.homeManagerModules.container
          {
            home.username = user;
            home.homeDirectory = homeDir;
          }
        ] ++ modules;
      };
      hm = home.config;

      zsh = "${hm.programs.zsh.package}/bin/zsh";

      # Userland the devcontainer tooling (VS Code, devcontainer CLI) shells
      # out to before any user profile is on PATH.
      baseEnv = pkgs.buildEnv {
        name = "devcontainer-base";
        paths = with pkgs; [
          bashInteractive
          coreutils
          diffutils
          findutils
          gawk
          gnugrep
          gnused
          gnutar
          gzip
          procps
          which
          less
          getent
          hm.programs.zsh.package
          initScript
          piste
        ];
        pathsToLink = [ "/bin" "/share/terminfo" ];
      };

      # Libraries foreign binaries most often need (VS Code Server's node,
      # manylinux wheels, uv-managed CPython).
      nixLdLibs = with pkgs; [
        stdenv.cc.cc.lib
        glibc
        zlib
        openssl
        libgcc
        icu
        curl
        krb5
      ];

      validatedBundle =
        if agentBundle == null then null else validateAgentBundle pkgs agentBundle;
      # The static Go piste: project-scope `piste agents` in the image.
      # Its tests run in the engine's checks.piste, not in every image build.
      piste = (import ../cli { inherit pkgs; }).piste-go;

      initScript = pkgs.writeShellApplication {
        name = "piste-devcontainer-init";
        runtimeInputs = [ pkgs.coreutils pkgs.findutils ];
        text = ''
          # Idempotent first-start setup; safe as postCreateCommand.
          mkdir -p "$HOME/.claude" "$HOME/.codex" "$HOME/.config" "$HOME/.cache"
          bundle=/etc/piste/agent-bundle
          if [ -d "$bundle/home" ]; then
            # Seed, never overwrite: the harness owns these once they exist.
            (cd "$bundle/home" && find . -type f -print0) |
              while IFS= read -r -d "" f; do
                if [ ! -e "$HOME/$f" ]; then
                  mkdir -p "$(dirname "$HOME/$f")"
                  cp "$bundle/home/$f" "$HOME/$f"
                  chmod u+w "$HOME/$f"
                  echo "seeded ~/''${f#./}"
                fi
              done
          fi
          # Project agent content: reconcile each workspace
          # repo that opted in, from the image's registry.
          for repo in "$PWD" /workspaces/*; do
            [ -f "$repo/.agents/agents.toml" ] || continue
            case " ''${synced:-} " in *" $repo "*) continue ;; esac
            synced="''${synced:-} $repo"
            echo "piste agents sync in $repo"
            ${piste}/bin/piste agents --root "$repo" sync \
              || echo "piste agents sync failed in $repo; run it by hand" >&2
          done
          echo "piste devcontainer ready (${name})"
        '';
      };

      etcFiles = pkgs.runCommand "devcontainer-etc" { } ''
        mkdir -p $out/etc
        cat > $out/etc/passwd <<EOF
        root:x:0:0:root:/root:/bin/zsh
        ${user}:x:${toString uid}:${toString gid}::${homeDir}:/bin/zsh
        nobody:x:65534:65534:nobody:/var/empty:/bin/false
        EOF
        cat > $out/etc/group <<EOF
        root:x:0:
        ${user}:x:${toString gid}:
        nogroup:x:65534:
        EOF
        cat > $out/etc/shadow <<EOF
        root:!:1::::::
        ${user}:!:1::::::
        EOF
        echo "hosts: files dns" > $out/etc/nsswitch.conf
        printf '/bin/sh\n/bin/bash\n/bin/zsh\n${zsh}\n' > $out/etc/shells
        echo ${name} > $out/etc/hostname
        cat > $out/etc/os-release <<EOF
        NAME="piste devcontainer"
        ID=piste
        PRETTY_NAME="piste devcontainer (${name})"
        EOF
      '';

      image = pkgs.dockerTools.streamLayeredImage {
        inherit name tag;
        contents = [
          baseEnv
          etcFiles
          pkgs.dockerTools.caCertificates
          pkgs.dockerTools.usrBinEnv
          pkgs.dockerTools.binSh
        ];
        # home.path, home-files and nix-ld are not in `contents` (that would
        # splat them over /); they reach the closure through the symlinks the
        # fakeRootCommands below leave in the layer tarball.
        fakeRootCommands = ''
          set -euo pipefail
          mkdir -p tmp root var/empty ${lib.removePrefix "/" homeDir}
          chmod 1777 tmp

          # nix-ld at the standard glibc loader path.
          ld=$(cat ${pkgs.nix-ld}/nix-support/ldpath)
          mkdir -p "$(dirname ".$ld")"
          ln -sf ${pkgs.nix-ld}/libexec/nix-ld ".$ld"

          # $HOME: home-files (symlinks into the store, real dirs), profile link,
          # mutable files as real copies, state dirs for volume mounts.
          h=${lib.removePrefix "/" homeDir}
          cp -a ${hm.home-files}/. "$h"/
          chmod -R u+w "$h"
          ln -sfn ${hm.home.path} "$h/.nix-profile"
          ${lib.concatMapStringsSep "\n" (f: ''
            if [ -L "$h/${f}" ]; then
              cp --remove-destination "$(readlink -f "$h/${f}")" "$h/${f}"
              chmod u+w "$h/${f}"
            fi
          '') hm.piste.roles.container.mutableFiles}
          mkdir -p "$h/.claude" "$h/.codex" "$h/.config" "$h/.cache" "$h/.local/share" "$h/.local/state" "$h/.ssh"
          chmod 700 "$h/.ssh"
          chown -R ${toString uid}:${toString gid} "$h"

          ${lib.optionalString (validatedBundle != null) ''
            mkdir -p etc/piste
            cp -rL ${validatedBundle} etc/piste/agent-bundle
            if [ -d etc/piste/agent-bundle/etc ]; then
              chmod -R u+w etc
              cp -rL etc/piste/agent-bundle/etc/. etc/
            fi
          ''}
          ${lib.optionalString (agentRegistry != null) ''
            # In-tree symlinks stay links: the payload hashes cover them.
            mkdir -p etc/piste/agent-bundle
            chmod u+w etc/piste/agent-bundle
            cp -a --no-preserve=ownership ${agentRegistry} etc/piste/agent-bundle/registry
            chmod -R u+w etc/piste/agent-bundle/registry
          ''}
        '';
        config = {
          User = user;
          WorkingDir = homeDir;
          Cmd = [ "/bin/zsh" "-l" ];
          Env = [
            "HOME=${homeDir}"
            "USER=${user}"
            "SHELL=/bin/zsh"
            "PATH=${homeDir}/.nix-profile/bin:/bin:/usr/bin"
            "LANG=C.UTF-8"
            "SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
            "NIX_SSL_CERT_FILE=/etc/ssl/certs/ca-bundle.crt"
            "NIX_LD=${pkgs.stdenv.cc.bintools.dynamicLinker}"
            "NIX_LD_LIBRARY_PATH=${lib.makeLibraryPath nixLdLibs}"
            "TERMINFO_DIRS=${homeDir}/.nix-profile/share/terminfo:/share/terminfo"
            # Also set by the container role's sessionVariables; repeated here
            # because `docker exec claude` never starts a login shell.
            "CLAUDE_CONFIG_DIR=${homeDir}/.claude"
            "CODEX_HOME=${homeDir}/.codex"
          ];
          Labels = {
            "org.opencontainers.image.title" = name;
            "org.opencontainers.image.source" = "https://github.com/elocke/piste";
          };
        };
      };

      load = pkgs.writeShellApplication {
        name = "${name}-load";
        text = ''
          # Load ${name} into docker (or podman) unless this exact build is
          # already there, then point :latest at it.
          if command -v docker >/dev/null 2>&1; then engine=docker
          elif command -v podman >/dev/null 2>&1; then engine=podman
          else echo "need docker or podman on PATH" >&2; exit 1; fi
          ref="${name}:${image.imageTag}"
          if "$engine" image inspect "$ref" >/dev/null 2>&1; then
            echo "$ref already loaded"
          else
            ${image} | "$engine" load
          fi
          "$engine" tag "$ref" "${name}:latest"
          echo "tagged ${name}:latest -> $ref"
        '';
      };
    in
    {
      inherit image load home;
    };
in
{
  inherit mkDevcontainer validateAgentBundle;
}
