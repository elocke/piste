# A module with options and config: its config's name is the host's.
{ lib, ... }:
{
  options.example.enable = lib.mkEnableOption "an example";
  config = {
    networking.hostName = "example";
    networking.useDHCP = true;
  };
}
