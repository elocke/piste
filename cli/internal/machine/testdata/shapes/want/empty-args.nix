# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ modulesPath }:
{
  imports = [ (modulesPath + "/profiles/base.nix") ];
}
