# What every home on the team gets: a tool, a variable, the team's agent zone
# and the names of its secrets (their values are each person's).
# A layer's rule: scalars mkPackDefault (piste.lib's, priority 950), lists
# plain. A coworker's plain value then wins over the team's, the team's wins
# over the engine's mkDefault, and their list entries go beside the team's.
{ meta, mkPackDefault }:
{ pkgs, piste, ... }:

{
  key = "fixture-layer/home";
  _file = toString ./home.nix;

  imports = [
    piste.homeManagerModules.sops
    piste.homeManagerModules.agents
  ];

  home.packages = [ pkgs.hello ];
  home.sessionVariables.TEAM_LAYER = mkPackDefault "fixture";

  piste.secrets.envVars = meta.secrets;

  piste.agents = {
    enable = mkPackDefault true;
    zone = mkPackDefault meta.zone;
    presets = [ "base" "team" ];
    sources."${meta.zone}-agents" = {
      inherit (meta) zone;
      store = ../agents;
      workdir = mkPackDefault "${../agents}";
    };
  };
}
