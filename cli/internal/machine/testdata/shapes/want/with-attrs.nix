# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ modulesPath, ... }:
with builtins;
{
  imports = [ (modulesPath + "/profiles/base.nix") ];
  # piste: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
  # networking.hostName = toString "example";
}
