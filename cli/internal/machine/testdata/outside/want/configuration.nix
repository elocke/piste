# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ config, pkgs, ... }:

{
  imports = [
    # piste: /home/user/extra.nix is outside /etc/nixos, so it isn't carried over
    /home/user/extra.nix
    # piste: <home-manager/nixos> is a channel lookup, which the flake build can't resolve
    <home-manager/nixos>
  ];

  boot.loader.systemd-boot.enable = true;
  boot.loader.efi.canTouchEfiVariables = true;

  users.users.jane = {
    isNormalUser = true;
    extraGroups = [ "wheel" ];
  };

  system.stateVersion = "24.05";
}
