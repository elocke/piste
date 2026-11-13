{ lib, ... }:
{
  networking = lib.mkMerge [
    { hostName = "example"; }
    { useDHCP = true; }
  ];
}
