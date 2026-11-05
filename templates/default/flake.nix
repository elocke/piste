{
  description = "@user@'s piste overlay";

  # The engine, pinned to the release the piste checkout was on. `piste build`,
  # `piste switch` and the rest build against the checkout around this overlay
  # instead; CI and a bare clone use this pin.
  inputs.piste.url = "@engine@";

  outputs =
    inputs@{ self, piste, ... }:
    piste.lib.mkFlake {
      inherit inputs self;
      user = "@user@";
      zone = "@zone@";

      hosts."@hostKey@" = {
        system = "@system@";
        roles = @roles@;
        modules = [ ./hosts/@hostname@.nix ];
      };

      # This overlay's agent zone root (skills, instructions, presets).
      agents = ./agents;
    };
}
