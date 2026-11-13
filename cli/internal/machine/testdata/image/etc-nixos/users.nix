{
  imports = [ <nixpkgs/nixos/modules/profiles/headless.nix> ];

  users.users.jane = {
    isNormalUser = true;
    extraGroups = [ "wheel" ];
  };
}
