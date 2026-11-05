# The fixture overlay's darwin host (checks.mkflake-fixture): the platform
# module mkFlake adds does the rest. Like the real hosts, it brings the
# sops-nix home-manager module the platform's secrets settings need.
{ user, piste, ... }:
{
  networking.hostName = "mac";
  home-manager.extraSpecialArgs = { inherit piste; };
  home-manager.users.${user}.imports = [
    piste.homeManagerModules.sops
    ../agents.nix
  ];
}
