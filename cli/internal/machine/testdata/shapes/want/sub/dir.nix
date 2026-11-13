# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{
  modulesPath,
  config,
  pkgs,
  ...
}:

{
  imports = [
    ../net.nix
    (modulesPath + "/profiles/base.nix")
    # piste: ../../escape.nix is outside /etc/nixos, so it isn't carried over
    ../../escape.nix
  ];
}
