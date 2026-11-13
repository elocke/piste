{ config, lib, ... }:
{
  options.example.enable = lib.mkEnableOption "an example";
  config = lib.mkIf config.example.enable {
    networking.hostName = "example";
  };
}
