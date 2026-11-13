# pnpm globals: the environment pnpm needs under activation, the activation
# hook that installs a list of globals, and the `piste-pnpm` script that
# `piste pkgs update` reads and drives (and `piste drift` reads). flake.nix's lib.mkPnpmInstallHook
# (the long comment on why activation needs all of this) and
# modules/programs/pnpm.nix both build on it.
#
#   prelude    : str          shell that makes `pnpm -g` work from a minimal PATH
#   installHook: { lib, name, pnpmPackages } -> home-manager dag entry
#   script     : { pkgs, pnpmPackages } -> derivation (bin/piste-pnpm)
rec {
  prelude = ''
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
  '';

  installHook = { lib, name, pnpmPackages }:
    lib.hm.dag.entryAfter [ "writeBoundary" ] ''
      PNPM_PACKAGES=(
        ${lib.concatMapStringsSep "\n        " (p: ''"${p}"'') pnpmPackages}
      )

      if [ ''${#PNPM_PACKAGES[@]} -gt 0 ]; then
        ${prelude}
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

  # `piste-pnpm outdated`: one `name<TAB>current<TAB>latest` line per declared,
  # unpinned entry that is installed and has a newer latest; non-zero, with a
  # line on stderr, when pnpm is missing or `pnpm outdated` gives no JSON.
  # `piste-pnpm upgrade`: `pnpm add -g <name>@latest` per unpinned entry,
  # past failures, non-zero if any failed. A pinned entry ("name@1.2.3") is
  # the person's choice of version and is left alone.
  # `piste-pnpm declared`: every entry, one per line, as declared (piste
  # drift and adopt count them as declared).
  script = { pkgs, pnpmPackages }:
    pkgs.writeShellApplication {
      name = "piste-pnpm";
      runtimeInputs = [ pkgs.jq ];
      text = ''
        ${prelude}
        DECLARED=(
          ${pkgs.lib.concatMapStringsSep "\n    " (p: ''"${p}"'') pnpmPackages}
        )

        # Unpinned entries: "name" or "@scope/name" with no @version.
        UNPINNED=()
        for entry in "''${DECLARED[@]}"; do
          if ! [[ $entry =~ ^@?[^@]+@.+$ ]]; then
            UNPINNED+=("$entry")
          fi
        done

        case "''${1:-}" in
          outdated)
            if ! command -v pnpm >/dev/null 2>&1; then
              echo "piste-pnpm: pnpm isn't on PATH" >&2
              exit 1
            fi
            installed=$(pnpm list -g --depth=0 --json 2>/dev/null || true)
            printf '%s' "$installed" | jq -e . >/dev/null 2>&1 || installed='[]'
            # pnpm outdated exits 1 when something is outdated: its JSON
            # says what. Nothing at all, with exit 0, is nothing outdated.
            rc=0
            latest=$(pnpm outdated -g --format json 2>/dev/null) || rc=$?
            if [ "$rc" -eq 0 ] && [ -z "''${latest//[[:space:]]/}" ]; then
              latest='{}'
            fi
            if ! printf '%s' "$latest" | jq -e 'type == "object"' >/dev/null 2>&1; then
              echo "piste-pnpm: pnpm outdated failed (exit $rc) and gave no JSON" >&2
              exit 1
            fi
            for name in "''${UNPINNED[@]}"; do
              jq -rn --arg n "$name" --argjson i "$installed" --argjson o "$latest" '
                select((($i[0].dependencies // {}) | has($n)))
                | ($o[$n] // empty)
                | select(.latest != null and .current != .latest)
                | [$n, .current, .latest] | @tsv'
            done
            ;;
          declared)
            if [ ''${#DECLARED[@]} -gt 0 ]; then
              printf '%s\n' "''${DECLARED[@]}"
            fi
            ;;
          upgrade)
            status=0
            for name in "''${UNPINNED[@]}"; do
              pnpm add -g "$name@latest" || status=1
            done
            exit "$status"
            ;;
          *)
            echo "usage: piste-pnpm outdated|upgrade|declared" >&2
            exit 2
            ;;
        esac
      '';
    };
}
