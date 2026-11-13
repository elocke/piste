{ lib, ... }:

{
  config = {
    nixpkgs = {
      config = {
        allowUnfree = true;
        allowInsecure = false;
        allowUnsupportedSystem = true;
      };

      # Auto-load every file in ../../pkgs/overlays/ as an overlay.
      overlays =
        let path = ../../pkgs/overlays; in with builtins;
        map (n: import (path + ("/" + n)))
            (filter (n: match ".*\\.nix" n != null ||
                        pathExists (path + ("/" + n + "/default.nix")))
                    (attrNames (readDir path)));
    };
  };
}
