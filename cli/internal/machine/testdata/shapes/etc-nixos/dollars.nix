{ ... }:
{
  environment.etc."a".text = ''
    a $${b
    see <nixpkgs/nixos/modules/profiles/minimal.nix>
  '';
  environment.etc."b".text = "a $${b <nixpkgs/nixos/modules/profiles/minimal.nix>";
  imports = [ <nixpkgs/nixos/modules/profiles/base.nix> ];
}
