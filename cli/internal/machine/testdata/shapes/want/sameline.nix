# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ ... }:
{
  # piste: <home-manager/nixos> is a channel lookup, which the flake build can't resolve
  # piste: /home/user/extra.nix is outside /etc/nixos, so it isn't carried over
  imports = [ <home-manager/nixos> /home/user/extra.nix ];
}
