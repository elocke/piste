{ config, lib, ... }:

let cfg = config.piste.roles.dev; in
{
  options.piste.roles.dev = {
    editor.enable = (lib.mkEnableOption "the engine's vim (programs.vim)")   // { default = true; };
    tmux.enable   = (lib.mkEnableOption "the engine's tmux (programs.tmux)") // { default = true; };
  };

  imports = [
    ./base.nix
    ../programs/editor.nix
    ../programs/tmux.nix
    ../programs/secrets.nix
    ../programs/uv.nix
    ../programs/zellij.nix
  ];

  # A toggle turned off forces its program off (see base.nix).
  config = lib.mkMerge [
    (lib.mkIf (!cfg.editor.enable) {
      programs.vim.enable = lib.mkForce false;
    })
    (lib.mkIf (!cfg.tmux.enable) {
      programs.tmux.enable = lib.mkForce false;
    })
  ];
}
