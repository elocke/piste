# The fixture overlay's standalone home-manager host (checks.mkflake-fixture).
# Like the real hosts, it brings the sops-nix module the dev role's secrets
# settings need.
{ user, piste, ... }:
{
  imports = [
    piste.homeManagerModules.sops
    ../agents.nix
  ];
  home = {
    username = user;
    homeDirectory = "/home/${user}";
    stateVersion = "24.05";
  };
}
