{
  description = "@user@'s piste overlay";

  # The engine, pinned to the release the piste checkout was on. `piste build`,
  # `piste switch` and the rest build against the checkout around this overlay
  # instead; CI and a bare clone use this pin.
  inputs.piste.url = "@engine@";

  # Packages you want newer than the engine's tested nixpkgs: list them in
  # fresh below and uncomment this; piste pkgs update moves it every run.
  # inputs.nixpkgs-fresh.url = "github:nixos/nixpkgs/nixos-unstable";

  outputs =
    inputs@{ self, piste, ... }:
    piste.lib.mkFlake {
      inherit inputs self;
      user = "@user@";
      zone = "@zone@";

      hosts."@hostKey@" = {
        system = "@system@";
        @nixosLine@
        roles = @roles@;
        modules = [ @hostModule@ ];
      };

      # This overlay's agent zone root (skills, instructions, presets).
      agents = ./agents;

      # fresh = [ "gh" ];
    };
}
