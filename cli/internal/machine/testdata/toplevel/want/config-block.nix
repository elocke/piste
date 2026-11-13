# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
# A module with options and config: its config's name is the host's.
{ lib, ... }:
{
  options.example.enable = lib.mkEnableOption "an example";
  config = {
    # piste: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
    # networking.hostName = "example";
    networking.useDHCP = true;
  };
}
