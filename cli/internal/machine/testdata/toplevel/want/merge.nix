# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ lib, ... }:
{
  networking = lib.mkMerge [
    # piste: networking.hostName here may override the host's name; remove it if this machine's name should be its key in flake.nix
    { hostName = "example"; }
    { useDHCP = true; }
  ];
}
