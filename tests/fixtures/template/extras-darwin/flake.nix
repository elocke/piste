{
  description = "jane's piste overlay";

  # The engine, pinned to the release the piste checkout was on. `piste build`,
  # `piste switch` and the rest build against the checkout around this overlay
  # instead; CI and a bare clone use this pin.
  inputs.piste.url = "github:elocke/piste/v0.0.0";

  outputs =
    inputs@{ self, piste, ... }:
    piste.lib.mkFlake {
      inherit inputs self;
      user = "jane";
      zone = "personal";

      hosts."jane-mac" = {
        system = "aarch64-darwin";
        roles = [ "extras" ];
        modules = [ ./hosts/jane-mac.nix ];
      };

      # This overlay's agent zone root (skills, instructions, presets).
      agents = ./agents;
    };
}
