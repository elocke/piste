# devbox (Linux, home-manager) on the team layer. The layer brings the
# team's tools, shell and agents; add your own settings here (a plain value of
# yours wins over the layer's; your list entries go beside the team's).
{ config, user, layer, ... }:
{
  imports = [ layer.homeManagerModules.default ];

  home = {
    username = user;
    homeDirectory = "/home/jane";
    # The home-manager release this overlay started at; leave it as it is
    # (home-manager's news says when a change is worth bumping it for).
    stateVersion = "26.11";
  };

  programs.git.settings.user = {
    name = "Jane Doe";
    email = "jane@example.com";
  };

  # Where the piste checkout is.
  piste.checkout = "${config.home.homeDirectory}/piste";

  # The team layer names the secrets; their values are yours, in
  # secrets/env/api-keys.sops.yaml (`piste secrets edit` to fill them in).
  piste.secrets = {
    enable = false;
    sopsFile = ../secrets/env/api-keys.sops.yaml;
  };
}
