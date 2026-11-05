# What every home on the team gets: a tool, a variable, the team's agent zone
# and the names of its secrets (their values are each person's).
# A layer's rule: scalars mkDefault, lists plain. A coworker's plain value
# then wins over the team's, and their list entries go beside the team's.
{ meta }:
{ lib, pkgs, piste, ... }:

{
  key = "fixture-layer/home";
  _file = toString ./home.nix;

  imports = [
    piste.homeManagerModules.sops
    piste.homeManagerModules.agents
  ];

  home.packages = [ pkgs.hello ];
  home.sessionVariables.TEAM_LAYER = lib.mkDefault "fixture";

  piste.secrets.envVars = meta.secrets;

  piste.agents = {
    enable = lib.mkDefault true;
    zone = lib.mkDefault meta.zone;
    presets = [ "base" "team" ];
    sources."${meta.zone}-agents" = {
      inherit (meta) zone;
      store = ../agents;
      workdir = lib.mkDefault "${../agents}";
    };
  };
}
