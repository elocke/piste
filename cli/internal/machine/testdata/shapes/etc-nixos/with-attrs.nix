with builtins;
{
  imports = [ <nixpkgs/nixos/modules/profiles/base.nix> ];
  networking.hostName = toString "example";
}
