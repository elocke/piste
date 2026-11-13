# Mentions <nixpkgs/nixos/modules/virtualisation/qemu-guest.nix> in a comment only.
/* A block comment with <home-manager/nixos> in it. */
{ config, lib, extra ? { }, ... }@args:

let
  note = "see <nixpkgs/nixos/modules/profiles/minimal.nix> and <nixpkgs>";
  merged = lib.id { a = 1; }//lib.id { b = 2; };
in
{
  imports = [
    <nixpkgs/nixos/modules/virtualisation/qemu-guest.nix>
    ./net.nix
    ./sub/dir.nix
  ];

  environment.etc."note".text = ''
    <nixpkgs/nixos/modules/profiles/headless.nix> in an indented string,
    with ''${not.code} and three quotes: '''
    ${(import <nixpkgs> { }).hello.name}
  '';
  environment.etc."merged".text = builtins.toJSON merged;
  environment.etc."note2".text = note;
  environment.etc."data".source = ./data.json;
  environment.etc."files".source = ./files;
  environment.etc."archive".source = builtins.fetchurl https://example.com/archive.tar.gz;

  system.stateVersion = "24.05";
}
