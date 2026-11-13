# The fixture overlay's agent zone for a host's home (like an overlay's
# modules/agents.nix): zone `agent`, its source named "<zone>-agents".
{ piste, ... }:
{
  imports = [ piste.homeManagerModules.agents ];
  piste.agents = {
    enable = true;
    zone = "agent";
    presets = [ "base" "agent" ];
    sources.agent-agents = {
      zone = "agent";
      store = ../agents/agent;
      workdir = "/nonexistent";
    };
  };
}
