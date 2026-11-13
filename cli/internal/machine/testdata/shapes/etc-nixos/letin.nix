let
  inherit (builtins) toString;
in
{
  imports = [ <nixpkgs/nixos/modules/profiles/base.nix> ];
  networking.hostName = toString "example";
}
