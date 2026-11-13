{
  description = "@user@'s overlay on the team layer";

  # The engine, pinned to the release the piste checkout was on, and the team
  # layer, which follows it. `piste switch` builds against the checkout around
  # this overlay instead of the engine pin; `piste update` moves both pins.
  inputs = {
    piste.url = "@engine@";
    layer = {
      url = "@layer@";
      inputs.piste.follows = "piste";
    };
  };

  outputs =
    inputs@{ self, piste, layer, ... }:
    piste.lib.mkFlake {
      inherit inputs self;
      user = "@user@";
      zone = "@zone@";

      hosts."@hostKey@" = {
        system = "@system@";
        roles = @roles@;
        modules = [ ./hosts/@hostname@.nix ];
      };

      extraSpecialArgs = system: { inherit layer; };

      # The team's agent zone.
      agents = layer.pisteLayer.agents;
    };
}
