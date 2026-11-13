# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{
  networking = {
    # piste: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
    # hostName = "example";
    firewall.enable = true;
  };

  # Not directly inside networking: left alone.
  services.avahi = {
    hostName = "example";
  };
}
