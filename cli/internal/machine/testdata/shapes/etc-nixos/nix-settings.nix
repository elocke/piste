{ config, ... }:
{
  nix = {
    nixPath = [ "nixpkgs=/nix/var/nix/profiles/per-user/root/channels/nixos" ];
    settings.experimental-features = [ "nix-command" "flakes" ];
  };
  nix.channel.enable = false;
  system.autoUpgrade.enable = true;
  services.example.enable = config.system.autoUpgrade.enable;
}
