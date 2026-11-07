{ pkgs, lib, ... }:

{
  # The piece's handle: disabledModules = [ { key = <this>; } ] drops it (its path doesn't).
  key = "pack:core/ssh";

  programs.ssh = {
    enable = true;
    # Disable deprecated default config - set defaults explicitly in settings."*"
    enableDefaultConfig = false;
    # piste owns ~/.ssh/config (a link into the store). Your own hosts go in
    # ~/.ssh/config-external, which this includes, or in programs.ssh.settings.
    # (~ is the user's own home, so root's is /root and a Mac's is /Users/…)
    includes = [ "~/.ssh/config-external" ];
    # `settings` replaced `matchBlocks` in home-manager. Attribute names are
    # `Host` patterns and the values use upstream ssh_config(5) directive
    # names verbatim, so there is no camelCase translation layer any more.
    # There is no github.com block here: programs/github.nix adds it when a
    # GitHub login is set, so a key in 1Password, Secretive, a YubiKey or
    # ~/.ssh/id_rsa keeps working until then.
    settings = {
      # Default settings for all hosts; an overlay's plain value wins.
      "*" = {
        AddKeysToAgent = lib.mkDefault "yes";
      };
    };
  };
}
