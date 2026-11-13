# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ config, ... }:
{
  nix = {
    # piste: the flake build doesn't use channels
    nixPath = [ "nixpkgs=/nix/var/nix/profiles/per-user/root/channels/nixos" ];
    settings.experimental-features = [ "nix-command" "flakes" ];
  };
  # piste: the flake build doesn't use channels
  nix.channel.enable = false;
  # piste: system.autoUpgrade would rebuild from /etc/nixos, not this overlay
  # system.autoUpgrade.enable = true;
  services.example.enable = config.system.autoUpgrade.enable;
}
