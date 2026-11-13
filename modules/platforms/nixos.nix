{ user, config, pkgs, lib, ... }:

# The NixOS base, the counterpart of darwin.nix: the person's user, sudo, the
# Nix settings piste needs, key-only OpenSSH, nix-ld and home-manager's NixOS
# module. Every value is overridable: a default (lib.mkDefault) or a list that
# merges, so a host's own configuration wins with a plain value. The three
# exceptions below (nixpkgs.config, the user's shell, root's SSH login) say
# why.
#
# No server or taste opinions (Tailscale, a firewall, Docker), no role, no
# system.stateVersion and no authorized keys: those are the host's. The module
# doesn't import ../shared either: mkFlake supplies nixpkgs.overlays, and a
# second copy would apply pkgs/overlays/* twice.
{
  imports = [
    ../packages-module.nix
  ];

  # Plain values, deliberately: nixpkgs.config has a merge of its own (a
  # plain host value wins) that doesn't resolve priorities inside it. A nested lib.mkDefault
  # would reach nixpkgs as an unresolved { _type = "override"; } set, not a
  # boolean.
  nixpkgs.config = {
    allowUnfree = true;
    allowInsecure = false;
  };

  users.users.${user} = {
    isNormalUser = lib.mkDefault true;
    extraGroups = [ "wheel" ];
    # NixOS needs programs.zsh.enable (below) for a zsh login shell. NixOS
    # itself defines every user's shell at mkDefault priority (users.defaultUserShell),
    # so this one sits just above it: a plain host value still wins, and
    # root's shell stays NixOS's. A host's mkDefault shell or
    # users.defaultUserShell loses to this mkOverride 900.
    shell = lib.mkOverride 900 pkgs.zsh;
  };
  programs.zsh.enable = lib.mkDefault true;

  security.sudo.enable = lib.mkDefault true;

  nix.settings = {
    experimental-features = [ "nix-command" "flakes" ];
    trusted-users = [ "root" user ];
  };

  # No /share/doc: the engine's dev packages bring python3.12, whose -doc
  # output the pinned nixpkgs can't build (Sphinx fails) and the binary cache
  # doesn't have, so every toplevel would fail; a headless dev base has no use
  # for it either. Man pages and info stay on.
  documentation.doc.enable = lib.mkDefault false;

  # Keys only. The engine can't know the person's keys: the host (or the
  # overlay) declares users.users.<user>.openssh.authorizedKeys, and
  # `piste switch` refuses a config that would leave an SSH session without one.
  services.openssh = {
    enable = lib.mkDefault true;
    settings = {
      PasswordAuthentication = lib.mkDefault false;
      KbdInteractiveAuthentication = lib.mkDefault false;
      # Below lib.mkDefault (1000), above the option's default (1500): an
      # image module's own mkDefault (GCE's "prohibit-password") wins
      # instead of clashing with this one, which fails the evaluation. A
      # host without one gets "no".
      PermitRootLogin = lib.mkOverride 1001 "no";
    };
  };

  # Prebuilt binaries (pip and uv wheels, downloaded tools) find a loader.
  programs.nix-ld.enable = lib.mkDefault true;

  # The engine's list, as the Mac does: one "system packages" shape on both
  # system kinds. casks, brews, darwinOnly and fonts are inert here.
  environment.systemPackages = config.piste.packages.resolved.systemPackages;

  home-manager = {
    useGlobalPkgs = lib.mkDefault true;
    useUserPackages = lib.mkDefault true;
    # A file in the way is moved aside as <file>.backup-<epoch>, the name the
    # Linux switch gives (home-manager switch -b backup-<epoch>). A fixed
    # extension would block the next switch once a backup of that name exists.
    backupCommand = lib.mkDefault (pkgs.writeShellScript "piste-hm-backup" ''
      b="$1.backup-$(${pkgs.coreutils}/bin/date +%s)"
      ${pkgs.coreutils}/bin/mv -- "$1" "$b" && echo "piste: moved $1 aside to $b" >&2
    '');
    extraSpecialArgs = { inherit user; };
    users.${user}.home.enableNixpkgsReleaseCheck = lib.mkDefault false;
  };
}
