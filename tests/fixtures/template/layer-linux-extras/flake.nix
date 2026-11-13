{
  description = "jane's overlay on the team layer";

  # The engine, pinned to the release the piste checkout was on, and the team
  # layer, which follows it. `piste switch` builds against the checkout around
  # this overlay instead of the engine pin; `piste update` moves both pins.
  inputs = {
    piste.url = "github:elocke/piste/v0.0.0";
    layer = {
      url = "git+file:///layer";
      inputs.piste.follows = "piste";
    };
  };

  outputs =
    inputs@{ self, piste, layer, ... }:
    piste.lib.mkFlake {
      inherit inputs self;
      user = "jane";
      zone = "work";

      hosts."jane@devbox" = {
        system = "x86_64-linux";
        roles = [ "dev" "extras" ];
        modules = [ ./hosts/devbox.nix ];
      };

      extraSpecialArgs = system: { inherit layer; };

      # The team's agent zone.
      agents = layer.pisteLayer.agents;
    };
}
