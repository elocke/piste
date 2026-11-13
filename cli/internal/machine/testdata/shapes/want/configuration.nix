# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
# Mentions <nixpkgs/nixos/modules/virtualisation/qemu-guest.nix> in a comment only.
/* A block comment with <home-manager/nixos> in it. */
{ modulesPath, config, lib, extra ? { }, ... }@args:

let
  note = "see <nixpkgs/nixos/modules/profiles/minimal.nix> and <nixpkgs>";
  merged = lib.id { a = 1; }//lib.id { b = 2; };
in
{
  imports = [
    (modulesPath + "/virtualisation/qemu-guest.nix")
    ./net.nix
    ./sub/dir.nix
  ];

  # piste: <nixpkgs> is a channel lookup, which the flake build can't resolve
  environment.etc."note".text = ''
    <nixpkgs/nixos/modules/profiles/headless.nix> in an indented string,
    with ''${not.code} and three quotes: '''
    ${(import <nixpkgs> { }).hello.name}
  '';
  environment.etc."merged".text = builtins.toJSON merged;
  environment.etc."note2".text = note;
  # piste: ./data.json isn't carried over (only .nix files are)
  environment.etc."data".source = ./data.json;
  # piste: ./files isn't carried over (only .nix files are)
  environment.etc."files".source = ./files;
  environment.etc."archive".source = builtins.fetchurl https://example.com/archive.tar.gz;

  system.stateVersion = "24.05";
}
