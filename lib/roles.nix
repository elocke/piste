# The roles a host may take in mkFlake's hosts: each names the engine's
# home-manager module (piste.homeManagerModules.<role>), and a darwin or NixOS
# module when the engine has one. mk-flake.nix imports this list, and so does
# checks.mkflake-mistakes, which builds its "unknown role" messages from it.
[ "base" "dev" "desktop" "container" "extras" ]
