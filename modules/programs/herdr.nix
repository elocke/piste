{ config, lib, pkgs, ... }:

# Herdr's settings. Only the Herdr app has telemetry: the herdr
# CLI and herdr-core have none. The app reads and writes
# ~/.config/herdr-app/settings.json itself, so the file can't be a store link;
# each switch merges the declared keys back in instead. This module manages
# settings only: the app and the CLI are installed as packages.
let
  cfg = config.piste.programs.herdr;
  json = pkgs.formats.json { };
  merge = import ./herdr-app-settings.nix {
    inherit pkgs;
    inherit (lib) hm;
    # Not xdg.configHome: the app opened from Finder sees no XDG_CONFIG_HOME.
    path = "${config.home.homeDirectory}/.config/herdr-app/settings.json";
    settings = cfg.app.settings;
  };
in
{
  options.piste.programs.herdr = {
    enable = lib.mkEnableOption "herdr's settings: the Herdr app's telemetry off unless set";
    app.settings = lib.mkOption {
      inherit (json) type;
      default = { };
      example = { telemetry = true; };
      description = ''
        Keys merged into the Herdr app's settings.json on every switch (Mac
        only). Declared keys win; the rest of the file is left as it is.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    piste.programs.herdr.app.settings.telemetry = lib.mkDefault false;

    home.activation.herdrAppSettings = lib.mkIf pkgs.stdenv.hostPlatform.isDarwin (
      lib.hm.dag.entryAfter [ "linkGeneration" ] ''
        run ${merge} || warnEcho "herdr: the Herdr app's settings weren't updated; the switch carries on"
      ''
    );
  };
}
