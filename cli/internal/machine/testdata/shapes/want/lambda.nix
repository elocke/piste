# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
# piste: modulesPath is used here but is not an argument of this file; add it to the file's arguments
pkgs: {
  imports = [ (modulesPath + "/profiles/base.nix") ];
  environment.systemPackages = [ pkgs.vim ];
}
