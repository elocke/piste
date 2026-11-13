# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{
  nix = {
    # piste: the flake build doesn't use channels
    nixPath = [ "nixpkgs=/nix/var/nix/profiles/per-user/root/channels/nixos" ];
    # piste: the flake build doesn't use channels
    channel.enable = true;
    settings.experimental-features = [ "nix-command" "flakes" ];
  };

  # The same names, not the top-level nix's: left alone.
  services.example = {
    nixPath = [ "unrelated" ];
    channel.enable = true;
  };
  containers.web.config = {
    nix = {
      nixPath = [ "nixpkgs=/nix/var/nix/profiles/per-user/root/channels/nixos" ];
    };
  };
}
