# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ modulesPath, config, pkgs, ... }:

{
  imports = [
    (modulesPath + "/virtualisation/proxmox-image.nix")
    ./users.nix
  ];

  boot.loader.grub.enable = true;
  boot.loader.grub.device = "/dev/vda";

  fileSystems."/" = {
    device = "/dev/disk/by-label/nixos";
    fsType = "ext4";
  };

  # piste: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
  # networking.hostName = "example";
  networking.useDHCP = true;

  # piste: system.autoUpgrade would rebuild from /etc/nixos, not this overlay
  # system.autoUpgrade.enable = true;

  services.qemuGuest.enable = true;
  services.openssh.enable = true;
  security.sudo.wheelNeedsPassword = false;

  system.stateVersion = "24.05";
}
