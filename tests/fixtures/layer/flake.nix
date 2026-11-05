{
  description = "A fixture team layer: a team layer's shape, with only examples in it";

  # The engine, for this flake on its own; an overlay on the layer makes it
  # follow its own (inputs.layer.inputs.piste.follows = "piste").
  inputs.piste.url = "github:elocke/piste";

  outputs =
    { self, piste, ... }:
    let
      meta = builtins.fromTOML (builtins.readFile ./piste-layer.toml);
    in
    {
      pisteLayer = {
        inherit (meta) zone;
        agents = ./agents;
      };

      # Every home on the team imports `default`; a Mac adds darwinModules.default.
      homeManagerModules.default = import ./modules/home.nix { inherit meta; };
      darwinModules.default = ./modules/darwin.nix;

      templates.default = {
        path = ./template;
        description = "Your own overlay on this team layer";
        welcomeText = ''
          Start it with `piste setup <this layer's URL>`, which fills in the
          @placeholders@ for you; `nix flake init -t` copies them as they are.
        '';
      };
    };
}
