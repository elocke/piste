# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
# A let with a nested let, an ./in/ path segment, then a rec attribute set.
{ modulesPath, ... }:
let
  name = let n = "example"; in n;
  dir = ./in/let;
in
rec {
  imports = [ (modulesPath + "/profiles/base.nix") ];
  # piste: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
  # networking.hostName = name;
  environment.etc."dir".source = dir;
}
