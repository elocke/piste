# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ lib, ... }:
{
  # piste: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
  # networking.hostName =
    # let
      # n = "example";
    # in
    # lib.mkDefault n;
  networking.domain = "example.com";
}
