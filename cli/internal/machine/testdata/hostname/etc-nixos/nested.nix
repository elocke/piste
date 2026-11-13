{
  networking = {
    hostName = "example";
    firewall.enable = true;
  };

  # Not directly inside networking: left alone.
  services.avahi = {
    hostName = "example";
  };
}
