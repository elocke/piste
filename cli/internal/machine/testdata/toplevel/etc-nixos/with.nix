{ lib, ... }:
{
  networking = with lib; {
    hostName = mkDefault "example";
  };
}
