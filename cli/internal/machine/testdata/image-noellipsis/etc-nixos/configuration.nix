{ pkgs }:

{
  imports = [ <nixpkgs/nixos/modules/virtualisation/lxc-container.nix> ];

  networking.hostName = "example";
  environment.systemPackages = [ pkgs.vim ];

  system.stateVersion = "24.05";
}
