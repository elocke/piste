pkgs: {
  imports = [ <nixpkgs/nixos/modules/profiles/base.nix> ];
  environment.systemPackages = [ pkgs.vim ];
}
