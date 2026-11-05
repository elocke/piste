# The extras role (piste.homeManagerModules.extras): "give me everything
# cool", opt-in. A host lists it beside its others (roles = [ "dev" "extras" ])
# and gets the opinionated extras the engine's defaults leave out. One
# file per part, so each can be filled in on its own; each part declares its
# toggle, piste.roles.extras.<part>.enable (on when the role is), and sets
# nothing when it is off.
#
# The Mac's side of the role (casks, macos) is darwin.nix,
# piste.darwinModules.extras, which mkFlake adds to a *-darwin host that lists
# the role. Nothing identity-, host-, service- or account-specific belongs in
# any part.
{
  imports = [
    ./shell.nix
    ./editor.nix
    ./tmux.nix
    ./ghostty.nix
  ];
}
