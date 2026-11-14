# Shared homebrew base configuration
# Consumed by overlay repos via piste.darwinModules.homebrew-base
#
# Usage in overlay flake:
#   piste.darwinModules.homebrew-base
#   { piste.homebrew.taps = { inherit homebrew-core homebrew-cask ...; }; }
#
# Or to add extra taps:
#   { piste.homebrew.extraTaps = { "example-org/homebrew-tools" = example-tap; }; }

{ config, lib, pkgs, ... }:

let
  cfg = config.piste.homebrew;
  declaresTrust = cfg.trustedFormulae != [ ] || cfg.trustedCasks != [ ] || cfg.trustedTaps != [ ];
  trustJson = builtins.toJSON {
    trustedformulae = cfg.trustedFormulae;
    trustedcasks = cfg.trustedCasks;
    trustedtaps = cfg.trustedTaps;
  };
  home = "/Users/${cfg.user}";
  trustFile = "${home}/.homebrew/trust.json";
  # The file stays the user's alone: Homebrew refuses a trust store others can write.
  ownTrustFile = ''
    chown ${cfg.user}:staff ${trustFile}
    chmod 600 ${trustFile}
  '';
in
{
  options.piste.homebrew = {
    enable = (lib.mkEnableOption "shared piste homebrew configuration") // { internal = true; };

    user = lib.mkOption {
      internal = true;
      type = lib.types.str;
      description = "The user to configure homebrew for";
    };

    homebrew-core = lib.mkOption {
      internal = true;
      type = lib.types.path;
      description = "Path to homebrew-core tap source";
    };

    homebrew-cask = lib.mkOption {
      internal = true;
      type = lib.types.path;
      description = "Path to homebrew-cask tap source";
    };

    extraTaps = lib.mkOption {
      type = lib.types.attrsOf lib.types.path;
      default = {};
      description = "Additional homebrew taps to merge with the base set";
    };

    trustedFormulae = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = ''
        Formulae from non-official taps to trust (Homebrew 6 tap-trust).
        Rendered declaratively into ~/.homebrew/trust.json instead of relying on
        interactive `brew trust`. Trust dependencies explicitly too — a trusted
        formula's tapped deps are NOT covered transitively (e.g. krunkit's
        libkrun-efi must be listed on its own).

        With `homebrew.onActivation.cleanup` set, `brew bundle`'s cleanup
        rebuilds trust.json from the Brewfile's own `trusted: true` entries
        (fully-qualified names only), dropping these; the activation puts them
        back after `brew bundle`. While the cleanup runs, only the Brewfile's
        trust holds: a formula or cask it may load (a dependency of an
        installed formula, one it uninstalls) belongs in `homebrew.brews` or
        `homebrew.casks` by its full name (`user/tap/name`).
      '';
    };

    trustedCasks = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = "Casks from non-official taps to trust (see trustedFormulae).";
    };

    trustedTaps = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      description = ''
        Whole taps to trust. Prefer trustedFormulae/trustedCasks for standalone
        entries; use this for cohesive single-author taps whose formulae depend
        on each other (e.g. slp/krunkit → libkrun-efi → virglrenderer), where
        enumerating every dependency is brittle.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    nix-homebrew = {
      user = cfg.user;
      enable = true;
      taps = {
        "homebrew/homebrew-core" = cfg.homebrew-core;
        "homebrew/homebrew-cask" = cfg.homebrew-cask;
      } // cfg.extraTaps;
      mutableTaps = false;
      autoMigrate = true;
    };

    # Homebrew 6 refuses to load formulae/casks from non-official taps unless
    # they're recorded in trust.json. Seed it declaratively (per-formula) so
    # trust survives on a fresh machine instead of living only in local `brew
    # trust` state.
    #
    # This MUST be written before the `homebrew` activation script runs
    # `brew bundle`. nix-darwin's activation order is preActivation → ... →
    # homebrew → postActivation, and home-manager links `home.file` in
    # postActivation — too late to gate the bundle. So write it here in
    # preActivation as root, into the user's home (bundle runs as
    # `sudo --user=${cfg.user} --set-home`, i.e. HOME=/Users/${cfg.user}, so
    # Homebrew reads ~/.homebrew/trust.json with XDG_CONFIG_HOME unset).
    # Only emitted when non-empty, so hosts with no third-party taps are
    # untouched.
    system.activationScripts.preActivation.text = lib.mkIf declaresTrust ''
      # piste.homebrew: seed Homebrew tap-trust before `brew bundle`
      install -d -m 700 -o ${cfg.user} -g staff ${home}/.homebrew
      printf '%s\n' ${lib.escapeShellArg trustJson} > ${trustFile}
      ${ownTrustFile}
    '';

    # `brew bundle --force-cleanup` (homebrew.onActivation.cleanup) replaces
    # trust.json with the Brewfile's own fully-qualified `trusted: true`
    # entries, so the declared ones are gone after it. Put them back, merged
    # into what bundle left (each list a sorted union), so a plain `brew`
    # afterwards loads them too.
    system.activationScripts.postActivation.text = lib.mkIf declaresTrust ''
      # piste.homebrew: restore declared tap-trust after `brew bundle`
      if [ -s ${trustFile} ] && merged=$(${lib.getExe pkgs.jq} -c --argjson want ${lib.escapeShellArg trustJson} \
          '(if type == "object" then . else {} end) as $have
           | reduce ($want | keys[]) as $k ($have; .[$k] = (((.[$k] // []) + $want[$k]) | unique))' \
          ${trustFile} 2>/dev/null); then
        printf '%s\n' "$merged" > ${trustFile}
      else
        install -d -m 700 -o ${cfg.user} -g staff ${home}/.homebrew
        printf '%s\n' ${lib.escapeShellArg trustJson} > ${trustFile}
      fi
      ${ownTrustFile}
    '';
  };
}
