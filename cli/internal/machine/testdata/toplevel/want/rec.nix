# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{
  networking = rec {
    # piste: networking.hostName here may override the host's name; remove it if this machine's name should be its key in flake.nix
    hostName = "example";
    extraHosts = "127.0.0.1 ${hostName}";
  };
}
