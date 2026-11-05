# extras.ghostty: Ghostty settings
# Option is Alt (so a US layout gets Alt shortcuts; a layout that types @ { } |
# ~ with Option does not), tabs close without asking, and programs in the
# terminal can read the clipboard (OSC 52) as well as write it, which lets a
# program over SSH copy to the Mac and also lets any program read what you
# copied. Only on a host with the desktop role, which owns the config file.
# Everything it sets goes under `lib.mkIf cfg.enable`.
{ config, lib, pkgs, ... }:

let
  cfg = config.piste.roles.extras.ghostty;
  # The desktop role's toggle exists only on a host that has the role.
  desktop = config.piste.roles.desktop.ghostty.enable or false;
in
{
  options.piste.roles.extras.ghostty.enable =
    (lib.mkEnableOption "the extras pack's Ghostty settings") // { default = true; };

  config = lib.mkIf (cfg.enable && desktop) {
    xdg.configFile."ghostty/config".text = lib.mkAfter ''

      # Extras
      macos-option-as-alt = true
      confirm-close-surface = false
      clipboard-read = allow
    '';
  };
}
