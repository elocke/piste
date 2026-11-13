# @hostname@ (Linux, home-manager): this machine's own settings. What's yours on
# every machine is in ../home.nix; the roles in flake.nix bring shell, git,
# ssh, editor and tmux (dev), and mkFlake the engine's package set (trim it
# here with piste.packages.*: docs/customising.md).
{ config, pkgs, user, ... }:
{
  imports = [ ../home.nix ];

  home = {
    username = user;
    homeDirectory = "@home@";
    # The home-manager release this machine started at; leave it as it is
    # (home-manager's news says when a change is worth bumping it for).
    stateVersion = "26.11";
  };

  # This machine's own packages; `piste adopt` adds what you installed outside Nix here.
  home.packages = with pkgs; [ ];

  # Where the piste checkout is on this machine; agent skill links point into
  # it, so an edit to a SKILL.md there is live without a switch.
  piste.checkout = @checkout@;
}
