# The fixture overlay's NixOS host (checks.mkflake-fixture): the platform
# module and the wiring mkFlake adds do the rest. It brings what every NixOS
# host must (a root filesystem, a boot loader, the releases it started at)
# and, like the other hosts, its home's agent zone.
{ user, ... }:
{
  fileSystems."/" = { device = "/dev/disk/by-label/nixos"; fsType = "ext4"; };
  boot.loader.grub.devices = [ "/dev/vda" ];
  system.stateVersion = "26.11";
  home-manager.users.${user} = {
    imports = [ ../agents.nix ];
    home.stateVersion = "26.11";
    programs.git.enable = true;
  };
}
