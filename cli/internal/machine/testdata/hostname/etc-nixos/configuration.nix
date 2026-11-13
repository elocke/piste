{ config, ... }:

{
  imports = [
    ./nested.nix
    ./multiline.nix
    ./shared.nix
    ./shared-nested.nix
  ];

  # The one-line form, with a comment after it.
  networking.hostName = "example"; # from the installer
  networking.useDHCP = true;

  # A read is no setting.
  environment.etc."hostname-copy".text = config.networking.hostName;
}
