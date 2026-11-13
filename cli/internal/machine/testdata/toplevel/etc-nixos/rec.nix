{
  networking = rec {
    hostName = "example";
    extraHosts = "127.0.0.1 ${hostName}";
  };
}
