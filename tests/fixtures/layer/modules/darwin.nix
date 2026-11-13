# What every Mac on the team gets: an app, and the engine for the homes.
{ piste, ... }:

{
  piste.packages.casks.extra = [ "example-team-app" ];
  home-manager.extraSpecialArgs = { inherit piste; };
}
