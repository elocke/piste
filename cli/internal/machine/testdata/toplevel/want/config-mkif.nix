# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ config, lib, ... }:
{
  options.example.enable = lib.mkEnableOption "an example";
  config = lib.mkIf config.example.enable {
    # piste: networking.hostName here may override the host's name; remove it if this machine's name should be its key in flake.nix
    networking.hostName = "example";
  };
}
