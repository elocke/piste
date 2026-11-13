# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ modulesPath, ... }:
{
  environment.etc."a".text = ''
    a $${b
    see <nixpkgs/nixos/modules/profiles/minimal.nix>
  '';
  environment.etc."b".text = "a $${b <nixpkgs/nixos/modules/profiles/minimal.nix>";
  imports = [ (modulesPath + "/profiles/base.nix") ];
}
