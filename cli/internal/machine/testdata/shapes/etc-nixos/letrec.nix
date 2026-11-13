# A let with a nested let, an ./in/ path segment, then a rec attribute set.
let
  name = let n = "example"; in n;
  dir = ./in/let;
in
rec {
  imports = [ <nixpkgs/nixos/modules/profiles/base.nix> ];
  networking.hostName = name;
  environment.etc."dir".source = dir;
}
