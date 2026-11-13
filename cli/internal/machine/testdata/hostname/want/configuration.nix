# Carried over from /etc/nixos by piste host add. These are your
# machine's own settings; trim them as your host file takes them over.
{ config, ... }:

{
  imports = [
    ./nested.nix
    ./multiline.nix
    ./shared.nix
    ./shared-nested.nix
  ];

  # The one-line form, with a comment after it.
  # piste: the host's name is its key in flake.nix (mkFlake sets networking.hostName)
  # networking.hostName = "example"; # from the installer
  networking.useDHCP = true;

  # A read is no setting.
  environment.etc."hostname-copy".text = config.networking.hostName;
}
