{ lib, ... }:
{
  networking.hostName =
    let
      n = "example";
    in
    lib.mkDefault n;
  networking.domain = "example.com";
}
