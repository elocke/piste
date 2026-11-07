{
  description = "Shared Nix configuration for macOS and Linux hosts";
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    darwin = {
      url = "github:nix-darwin/nix-darwin/master";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    nix-homebrew = {
      url = "github:zhaofengli-wip/nix-homebrew";
    };
    homebrew-core = {
      url = "github:homebrew/homebrew-core";
      flake = false;
    };
    homebrew-cask = {
      url = "github:homebrew/homebrew-cask";
      flake = false;
    };
    claude-code-nix = {
      url = "github:sadjow/claude-code-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    codex-cli-nix = {
      url = "github:sadjow/codex-cli-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    gemini-cli-nix = {
      url = "github:nklmilojevic/gemini-cli-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    flake-parts = {
      url = "github:hercules-ci/flake-parts";
      inputs.nixpkgs-lib.follows = "nixpkgs";
    };
    sops-nix = {
      url = "github:Mic92/sops-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    nix-vscode-extensions = {
      url = "github:nix-community/nix-vscode-extensions";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    # Nix-packaged MCP servers: a server
    # pinned by flake.lock instead of `npx pkg@<v>` on every start.
    mcp-servers-nix = {
      url = "github:natsukium/mcp-servers-nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };
  outputs = inputs@{ self, nixpkgs, claude-code-nix, codex-cli-nix, gemini-cli-nix, flake-parts, nix-vscode-extensions, mcp-servers-nix, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      imports = [ ./flake/parts/per-system.nix ];

      flake = let
        # An overlay that applies once: a host may list it beside mkFlake
        # (which adds overlays.shared to a Linux host), or list overlays.ai
        # and overlays.shared, and a second application would redo every
        # override on top of the first (gemini-cli's --replace-fail fails).
        # Each marks pkgs.pisteOverlays.<name>; a later copy sees it and adds
        # nothing.
        once = name: overlay: final: prev:
          let
            marks = prev.pisteOverlays or { };
            applied = overlay final prev;
          in
          if marks ? ${name} then
            { }
          else
            applied // { pisteOverlays = marks // (applied.pisteOverlays or { }) // { ${name} = true; }; };
      in {
        overlays.ai = once "ai" (final: prev:
          let
            base =
              (claude-code-nix.overlays.default final prev)
              // (codex-cli-nix.overlays.default final prev)
              // (gemini-cli-nix.overlays.default final prev);
          in
          base // {
            # MCP servers from mcp-servers-nix, picked one by one: its overlay
            # also defines generic names (serena, mcp-server-git, …) that
            # should not land in every pkgs. Installing one is the zone's call.
            # playwright-mcp replaces nixpkgs' own, which trails upstream.
            inherit (mcp-servers-nix.overlays.default final prev) playwright-mcp;

            # gemini-cli-nix wrapper points at dist/index.js, but @google/gemini-cli
            # has shipped its entry at bundle/gemini.js since v0.38. Rewrite the
            # wrapper post-install. Drop this once upstream nklmilojevic/gemini-cli-nix
            # fixes its installPhase.
            gemini-cli = base.gemini-cli.overrideAttrs (old: {
              postFixup = (old.postFixup or "") + ''
                substituteInPlace $out/bin/gemini \
                  --replace-fail \
                    "node_modules/@google/gemini-cli/dist/index.js" \
                    "node_modules/@google/gemini-cli/bundle/gemini.js"
              '';
            });
          });

        overlays.vscode = nix-vscode-extensions.overlays.default;

        # Aggregate of everything piste ships: the AI overlay + every file
        # in ./pkgs/overlays/. mkFlake applies it to every *-linux host (the
        # package set needs it); a standalone home-manager host wired by hand
        # applies it itself. Listing it again does nothing (`once`, above).
        overlays.shared = once "shared" (nixpkgs.lib.composeManyExtensions ([
          self.overlays.ai
        ] ++ (
          let path = ./pkgs/overlays; in with builtins;
            map (n: import (path + ("/" + n)))
                (filter (n: match ".*\\.nix" n != null ||
                            pathExists (path + ("/" + n + "/default.nix")))
                        (attrNames (readDir path)))
        )));

        darwinModules = {
          platform = ./modules/platforms/darwin.nix;
          homebrew-base = ./modules/darwin/homebrew-base.nix;
          packages = ./modules/packages-module.nix;
          # The extras role's Mac side (casks, macOS settings): mkFlake adds
          # it to a Mac whose roles list extras.
          extras = ./modules/extras/darwin.nix;
        };

        homeManagerModules = {
          base = ./modules/roles/base.nix;
          dev = ./modules/roles/dev.nix;
          desktop = ./modules/roles/desktop.nix;
          # dev minus host-only bits; what lib.mkDevcontainer bakes in.
          container = ./modules/roles/container.nix;
          # The opt-in "everything cool" pack: one part per file, each with
          # its piste.roles.extras.<part>.enable. Its Mac side is
          # darwinModules.extras.
          extras = ./modules/extras;
          packages = ./modules/packages-module.nix;
          secrets = ./modules/programs/secrets.nix;
          vscode = ./modules/programs/vscode.nix;
          # Skills + instructions from the agent zones.
          agents = ./modules/programs/agents.nix;
          # sops-nix's home module (the secrets module and the dev role set
          # its options): a host imports it from here, not piste.inputs.
          # A path, so importing it beside sops-nix's own counts once.
          sops = inputs.sops-nix.homeManagerModules.sops;
        };

        # The starter overlay. `piste init` renders it, filling its
        # @placeholders@; `nix flake init -t` copies it as is.
        templates.default = {
          path = ./templates/default;
          description = "A piste overlay: one host, built with piste.lib.mkFlake (render it with `piste init`)";
          welcomeText = ''
            This is piste's overlay template: its files still have @placeholders@
            (@user@, @hostname@, @engine@, ...) and won't evaluate as they are.
            `piste init` fills them in: run it in a piste checkout instead
            (`nix run github:elocke/piste/stable -- setup` makes one), or replace each
            placeholder here by hand.
          '';
        };

        lib = {
          # Build a devcontainer OCI image from a zone's home-manager modules.
          # See lib/devcontainer.nix for the interface and the agent-bundle hook.
          inherit (import ./lib/devcontainer.nix { inherit inputs self; })
            mkDevcontainer validateAgentBundle;
          # A zone's container agent bundle, rendered by `piste agents bundle`.
          mkAgentBundle = import ./lib/agent-bundle.nix { inherit self; };
          # The agent registry of a zone as a derivation.
          inherit (import ./lib/agents-registry.nix { inherit (nixpkgs) lib; }) mkAgentRegistry;
          # The generic trust-zone checks of an overlay.
          zoneChecks = import ./lib/zone-checks.nix { inherit (nixpkgs) lib; };
          # An overlay's flake outputs from one call.
          mkFlake = import ./lib/mk-flake.nix { piste = self; };
          # A pack's (or a team layer's) default, at priority 950: it wins
          # over the engine's mkDefault (1000) and loses to a person's plain
          # value (100), with no mkForce either way. A pack reaches it through
          # its own flake input: inputs.piste.lib.mkPackDefault.
          mkPackDefault = nixpkgs.lib.mkOverride 950;

          # home-manager activation that installs the given pnpm packages
          # globally, skipping any already on the global list. An overlay
          # module that lists pnpm globals calls this.
          #
          # Activation runs with home-manager's own minimal PATH (bash,
          # coreutils, diffutils, findutils, gettext, grep, sed, jq, ncurses,
          # nix) — it does NOT include the user profile. Four things are
          # therefore missing and all four must be supplied explicitly:
          #   1. pnpm itself, from both profile roots: a Linux home gets pnpm
          #      through home.packages (~/.nix-profile/bin); darwin.nix routes
          #      the same resolved set into environment.systemPackages, so on
          #      a Mac it is only in /run/current-system/sw/bin.
          #   2. PNPM_HOME — set via home.sessionVariables, which activation
          #      never sources.
          #   3. $PNPM_HOME/bin on PATH — pnpm hard-refuses any `-g` operation
          #      when its global bin dir isn't on PATH ("Run pnpm setup").
          #   4. NIX_SSL_CERT_FILE — nixpkgs' node uses OpenSSL's CA store,
          #      not node's bundled one, so every fetch fails TLS validation
          #      without it. Also a sessionVariable activation never sources.
          #
          # pnpm is resolved through the profile, NOT ${pkgs.pnpm}: on
          # aarch64-darwin that store path can't be interpolated without the
          # dynamic-derivations experimental feature, so referencing it breaks
          # a Mac's eval. The profile copy is the one
          # packages-module.nix already installs, keeping one source of truth.
          # This hook is the deliberate impurity escape hatch anyway; the
          # overlay module that calls it holds the list.
          mkPnpmInstallHook = { lib, name, pnpmPackages }:
            lib.hm.dag.entryAfter [ "writeBoundary" ] ''
              PNPM_PACKAGES=(
                ${lib.concatMapStringsSep "\n                " (p: ''"${p}"'') pnpmPackages}
              )

              if [ ''${#PNPM_PACKAGES[@]} -gt 0 ]; then
                export PNPM_HOME="''${PNPM_HOME:-$HOME/.local/share/pnpm}"
                # /usr/bin and /bin are appended (not prepended, so nix
                # binaries still win) for packages with native addons: node-gyp
                # shells out to `make` and `clang++`, which on macOS only exist
                # in the Apple toolchain under /usr/bin. Without them an install
                # of e.g. @tobilu/qmd dies at better-sqlite3 with
                # "gyp ERR! stack Error: not found: make".
                export PATH="$HOME/.nix-profile/bin:/etc/profiles/per-user/''${USER:-$(id -un)}/bin:/run/current-system/sw/bin:$PNPM_HOME/bin:$PATH:/usr/bin:/bin"

                # nixpkgs' node is linked against OpenSSL's CA store rather
                # than node's bundled Mozilla list, so without this every
                # registry fetch dies with UNABLE_TO_GET_ISSUER_CERT_LOCALLY.
                if [ -z "''${NIX_SSL_CERT_FILE:-}" ] && [ -r /etc/ssl/certs/ca-certificates.crt ]; then
                  export NIX_SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
                fi

                if ! command -v pnpm &>/dev/null; then
                  echo "WARNING: pnpm not on activation PATH — skipped ${name} JS globals; install pnpm, then run 'piste switch' again" >&2
                else
                  # Exact whole-line match against the installed set, so a
                  # declared "typescript" doesn't count as present when only
                  # typescript-language-server is. A pinned entry
                  # ("name@1.2.3") must match name@version.
                  installed=$(pnpm list -g --depth=0 --json 2>/dev/null \
                    | jq -r '.[0].dependencies // {} | to_entries[] | .key, "\(.key)@\(.value.version)"' 2>/dev/null || true)

                  for pkg in "''${PNPM_PACKAGES[@]}"; do
                    if ! printf '%s\n' "$installed" | grep -qxF "$pkg"; then
                      echo "Installing ${name} pnpm global: $pkg"
                      # A failed install must not abort the whole activation.
                      $DRY_RUN_CMD pnpm add -g "$pkg" \
                        || echo "WARNING: $pkg failed; retry with 'pnpm add -g $pkg', or run 'piste switch' again" >&2
                    fi
                  done
                fi
              fi
            '';

          # home-manager activation that warns about expected bins missing
          # from PATH (or ~/.local/bin). Used by the JS-globals drift check.
          mkBinVerifyHook = { lib, name, expectedBins }:
            lib.hm.dag.entryAfter [ "writeBoundary" ] ''
              EXPECTED_BINS=(
                ${lib.concatMapStringsSep "\n                " (b: ''"${b}"'') expectedBins}
              )

              # Activation PATH excludes the per-user install roots, so check
              # them explicitly. ~/.cargo/bin matters because two of the tools
              # in this list (conduit, rtk) are documented as `cargo install`
              # — without it an installed rtk is reported missing every switch.
              BIN_DIRS=("$HOME/.local/bin" "$HOME/.cargo/bin" "$HOME/.bun/bin")

              MISSING=()
              for bin in "''${EXPECTED_BINS[@]}"; do
                found=""
                command -v "$bin" &>/dev/null && found=1
                if [ -z "$found" ]; then
                  for d in "''${BIN_DIRS[@]}"; do
                    if [ -x "$d/$bin" ]; then found=1; break; fi
                  done
                fi
                [ -z "$found" ] && MISSING+=("$bin")
              done

              if [ ''${#MISSING[@]} -gt 0 ]; then
                echo "WARNING: Missing expected local bins (${name}): ''${MISSING[*]}"
                echo "Install them manually or run 'piste drift' for instructions."
              fi
            '';
        };
      };
    };
}
