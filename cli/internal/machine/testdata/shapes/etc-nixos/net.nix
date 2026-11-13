args@{ config, ... }:
{
  imports = [ <nixpkgs/nixos/modules/profiles/qemu-guest.nix> ];
  networking.useDHCP = true;
}
