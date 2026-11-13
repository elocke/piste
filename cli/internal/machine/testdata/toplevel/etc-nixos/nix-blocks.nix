{
  nix = {
    nixPath = [ "nixpkgs=/nix/var/nix/profiles/per-user/root/channels/nixos" ];
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
