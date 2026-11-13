# piste.packages.pnpm: pnpm globals the engine installs at activation and
# `piste pkgs update` upgrades. Nothing is set while the list is empty.
{ config, lib, pkgs, ... }:
let
  cfg = config.piste.packages.pnpm;
  pnpm = import ../../lib/pnpm.nix;
in
{
  options.piste.packages.pnpm = lib.mkOption {
    type = lib.types.listOf lib.types.str;
    default = [ ];
    example = [ "tsx" "serve@14.2.0" ];
    description = ''
      pnpm global packages the engine installs on `piste switch`, when they
      aren't installed yet. `piste pkgs update` upgrades the unpinned ones to
      their latest version; an entry written `name@1.2.3` is pinned and is
      left alone.
    '';
  };

  config = lib.mkIf (cfg != [ ]) {
    home.activation.pistePnpm = pnpm.installHook {
      inherit lib;
      name = "piste";
      pnpmPackages = cfg;
    };
    home.packages = [ (pnpm.script { inherit pkgs; pnpmPackages = cfg; }) ];
  };
}
