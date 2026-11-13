{ config, pkgs, lib, ... }:

let cfg = config.piste.roles.desktop; in
{
  options.piste.roles.desktop = {
    alacritty.enable = (lib.mkEnableOption "the engine's Alacritty (programs.alacritty)")     // { default = true; };
    ghostty.enable   = (lib.mkEnableOption "the engine's Ghostty settings (ghostty/config)")  // { default = true; };
  };

  imports = [
    ./dev.nix
    ../programs/alacritty.nix
    ../programs/ghostty.nix
  ];

  # A toggle turned off forces its program off (see base.nix).
  config = lib.mkMerge [
    # zellij's one switch is piste.programs.zellij.enable: on with this role,
    # and a plain `false` in a host turns it off.
    { piste.programs.zellij.enable = lib.mkDefault true; }
    (lib.mkIf (!cfg.alacritty.enable) {
      programs.alacritty.enable = lib.mkForce false;
    })
    (lib.mkIf (!cfg.ghostty.enable) {
      xdg.configFile."ghostty/config".enable = lib.mkForce false;
    })
  ];
}
