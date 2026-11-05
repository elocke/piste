# jane-mac (macOS) on the team layer. The layer brings the team's apps,
# tools, shell and agents; add your own settings here (a plain value of yours
# wins over the layer's; your list entries go beside the team's).
{ user, layer, ... }:
{
  imports = [ layer.darwinModules.default ];

  # The nix-darwin release this Mac started at; leave it as it is
  # (`darwin-rebuild changelog` says when a change is worth bumping it for).
  system.stateVersion = 7;

  home-manager.users.${user} =
    { config, ... }:
    {
      imports = [ layer.homeManagerModules.default ];

      # The home-manager release this overlay started at; leave it as it is
      # (home-manager's news says when a change is worth bumping it for).
      home.stateVersion = "26.11";

      programs.git.settings.user = {
        name = "Jane Doe";
        email = "jane@example.com";
      };

      # Where the piste checkout is.
      piste.checkout = "${config.home.homeDirectory}/piste";

      # The team layer names the secrets; their values are yours, in
      # secrets/env/api-keys.sops.yaml (`piste secrets edit` to fill them in).
      piste.secrets = {
        enable = true;
        sopsFile = ../secrets/env/api-keys.sops.yaml;
      };
    };
}
