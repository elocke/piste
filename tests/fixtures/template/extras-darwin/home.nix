# What's yours on every machine (a home-manager module): each host file in
# hosts/ imports it, so a machine added with `piste host add` gets all of
# this. One machine's own settings go in its host file. mkFlake (flake.nix)
# brings the rest: the sops and agents modules and the piste CLI.
{ config, pkgs, ... }:
{
  # Packages for every machine (one machine's own go in its host file).
  home.packages = with pkgs; [ ];

  programs.git.settings.user = {
    name = "Jane Doe";
    email = "jane@example.com";
  };

  # Skills, instructions and hooks for Claude Code and Codex: the engine's
  # shared zone plus this overlay's agents/ (see presets/default.toml).
  piste.agents = {
    enable = true;
    zone = "personal";
    presets = [ "base" "default" ];
    sources.personal-agents = {
      zone = "personal";
      store = ./agents;
      workdir = "${config.piste.checkout}/overlays/personal/agents";
    };
  };

  # Secrets as shell env vars, encrypted with sops (`piste init --sops` set
  # up an age key and .sops.yaml; add values with `piste secrets edit`):
  # piste.secrets = {
  #   enable = true;
  #   sopsFile = ./secrets/env/api-keys.sops.yaml;
  #   envVars.ANTHROPIC_API_KEY = "anthropic/api_key";
  # };
}
