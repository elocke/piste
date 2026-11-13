{ config, ... }:

{
  imports = [
    ./config-block.nix
    ./config-dotted.nix
    ./config-mkif.nix
    ./inherit.nix
    ./merge.nix
    ./nix-blocks.nix
    ./rec.nix
    ./with.nix
  ];

  # The host's own name: commented out.
  networking.hostName = "example";

  # A container's name is its own: left as it is.
  containers.web.config.networking.hostName = "web";
  containers.db = {
    autoStart = true;
    config = { config, ... }: {
      networking.hostName = "db";
    };
  };
  containers.cache.config = {
    networking = {
      hostName = "cache";
    };
  };

  # So is a specialisation's, and any other nested set's.
  specialisation.other.configuration = {
    networking.hostName = "other";
  };
}
