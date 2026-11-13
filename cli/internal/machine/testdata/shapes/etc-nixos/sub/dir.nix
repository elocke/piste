{
  config,
  pkgs,
  ...
}:

{
  imports = [
    ../net.nix
    <nixpkgs/nixos/modules/profiles/base.nix>
    ../../escape.nix
  ];
}
