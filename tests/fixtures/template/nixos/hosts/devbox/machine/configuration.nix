# This machine's own NixOS configuration: its boot loader, disks and hardware.
# This is a stand-in that builds, for a virtual machine; `piste host add`
# replaces it with the machine's own /etc/nixos/configuration.nix.
{ modulesPath, ... }:
{
  imports = [ (modulesPath + "/profiles/qemu-guest.nix") ];

  boot.loader.grub.devices = [ "/dev/vda" ];
  fileSystems."/" = {
    device = "/dev/disk/by-label/nixos";
    fsType = "ext4";
  };

  # The NixOS release this machine started at; leave it as it is
  # (`man configuration.nix` and the release notes say when a change is worth
  # bumping it for).
  system.stateVersion = "26.11";
}
