# jane-mac (macOS): this Mac's own settings. What's yours on every machine is
# in ../home.nix; the engine's platform module brings the desktop role: shell,
# git, ssh, editor, tmux, terminal, Homebrew.
{ user, pkgs, ... }:
{
  # This Mac's own packages; `piste adopt` adds what you installed outside Nix here.
  piste.packages.extraPackages = with pkgs; [ ];
  piste.packages.casks.extra = [ ];
  piste.packages.brews.extra = [ ];

  # The nix-darwin release this Mac started at; leave it as it is
  # (`darwin-rebuild changelog` says when a change is worth bumping it for).
  system.stateVersion = 7;

  home-manager.users.${user} =
    { config, ... }:
    {
      imports = [ ../home.nix ];

      # The home-manager release this Mac started at; leave it as it is
      # (home-manager's news says when a change is worth bumping it for).
      home.stateVersion = "26.11";

      # Where the piste checkout is on this Mac; agent skill links point into
      # it, so an edit to a SKILL.md there is live without a switch.
      piste.checkout = "${config.home.homeDirectory}/piste";
    };
}
