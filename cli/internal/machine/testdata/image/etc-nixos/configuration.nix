{ config, pkgs, ... }:

{
  imports = [
    <nixpkgs/nixos/modules/virtualisation/proxmox-image.nix>
    ./users.nix
  ];

  boot.loader.grub.enable = true;
  boot.loader.grub.device = "/dev/vda";

  fileSystems."/" = {
    device = "/dev/disk/by-label/nixos";
    fsType = "ext4";
  };

  networking.hostName = "example";
  networking.useDHCP = true;

  services.qemuGuest.enable = true;
  services.openssh.enable = true;
  security.sudo.wheelNeedsPassword = false;

  system.stateVersion = "24.05";
}
