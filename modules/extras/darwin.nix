# The extras role's Mac side (piste.darwinModules.extras): the parts that are
# nix-darwin settings, not home-manager ones. mkFlake adds it to a *-darwin
# host that lists the extras role; the rest of the role is default.nix.
{
  imports = [
    ./casks.nix
    ./macos.nix
  ];
}
