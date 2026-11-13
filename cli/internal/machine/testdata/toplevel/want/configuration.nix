# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
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
  # piste: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
  # networking.hostName = "example";

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
