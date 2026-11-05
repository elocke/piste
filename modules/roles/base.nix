{ config, pkgs, lib, ... }:

let cfg = config.piste.roles.base; in
{
  options.piste.roles.base = {
    shell.enable    = (lib.mkEnableOption "the engine's zsh (programs.zsh)")         // { default = true; };
    git.enable      = (lib.mkEnableOption "the engine's git (programs.git)")         // { default = true; };
    ssh.enable      = (lib.mkEnableOption "the engine's SSH client (programs.ssh)")  // { default = true; };
    starship.enable = (lib.mkEnableOption "the starship prompt (programs.starship)") // { default = true; };
  };

  imports = [
    ../programs/checkout.nix
    ../programs/shell.nix
    ../programs/git.nix
    ../programs/ssh.nix
    ../programs/github.nix
    ../programs/starship.nix
    ../programs/herdr.nix
  ];

  # A toggle turned off forces its program off: the modules above enable it
  # with a plain `enable = true`, which a mkDefault would lose to.
  config = lib.mkMerge [
    (lib.mkIf (!cfg.shell.enable) { programs.zsh.enable = lib.mkForce false; })
    (lib.mkIf (!cfg.git.enable) { programs.git.enable = lib.mkForce false; })
    (lib.mkIf (!cfg.ssh.enable) { programs.ssh.enable = lib.mkForce false; })
    (lib.mkIf (!cfg.starship.enable) { programs.starship.enable = lib.mkForce false; })
  ];
}
