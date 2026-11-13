# extras.macos: macOS input and system settings
# (A nix-darwin module, imported by darwin.nix; not a home-manager one.)
# Everything it sets goes under `lib.mkIf cfg.enable`, so
# piste.roles.extras.macos.enable = false drops it.
#
# Settings that change how the keyboard and the trackpad behave, beyond speed:
# the engine's defaults (modules/platforms/darwin.nix) leave them alone. Each
# is lib.mkDefault, so a plain value in your host wins.
{ config, lib, pkgs, ... }:

let cfg = config.piste.roles.extras.macos; in
{
  # The piece's handle: disabledModules = [ { key = <this>; } ] drops it (its path doesn't).
  key = "pack:extras/macos";

  options.piste.roles.extras.macos.enable =
    (lib.mkEnableOption "the extras pack's macOS input and system settings") // { default = true; };

  config = lib.mkIf cfg.enable {
    system.defaults = {
      # Holding a key repeats it, where the default offers é, ü and the rest
      # of its accents.
      NSGlobalDomain.ApplePressAndHoldEnabled = lib.mkDefault false;

      # Three fingers drag a window or a selection, which takes over the
      # three-finger swipes between Spaces.
      trackpad.TrackpadThreeFingerDrag = lib.mkDefault true;
    };
  };
}
