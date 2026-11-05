{ user, config, pkgs, lib, ... }:

{
  imports = [
    ../shared
    ../packages-module.nix
  ];

  # Nix configuration handled by Determinate Nix (nix.enable = false in flake.nix)
  # Custom settings go in /etc/nix/nix.custom.conf

  # Every value under system.defaults is a default (lib.mkDefault): a host
  # overrides it with a plain value. What changes how the keyboard and the
  # trackpad behave beyond speed (press-and-hold, three-finger drag) is the
  # extras role's (modules/extras/macos.nix).
  system = {
    # nix-darwin's own, a default only: the host sets it (the starter writes
    # the release it started at), and changing one later is a migration.
    stateVersion = lib.mkDefault 4;

    defaults = {
      NSGlobalDomain = {
        AppleShowAllExtensions = lib.mkDefault true;

        # 120, 90, 60, 30, 12, 6, 2
        KeyRepeat = lib.mkDefault 2;

        # 120, 94, 68, 35, 25, 15
        InitialKeyRepeat = lib.mkDefault 15;

        "com.apple.mouse.tapBehavior" = lib.mkDefault 1;
        "com.apple.sound.beep.volume" = lib.mkDefault 0.0;
        "com.apple.sound.beep.feedback" = lib.mkDefault 0;
      };

      dock = {
        autohide = lib.mkDefault true;
        show-recents = lib.mkDefault false;
        launchanim = lib.mkDefault true;
        orientation = lib.mkDefault "bottom";
        tilesize = lib.mkDefault 48;
      };

      finder = {
        _FXShowPosixPathInTitle = lib.mkDefault false;
      };

      trackpad = {
        Clicking = lib.mkDefault true;
      };
    };
  };

  # Set the primary user for nix-darwin (required for homebrew, launchd agents, etc.)
  system.primaryUser = user;

  # The primary user's account.
  users.users.${user} = {
    name = "${user}";
    home = "/Users/${user}";
    isHidden = false;
    shell = pkgs.zsh;
  };

  security.pam.services.sudo_local.touchIdAuth = true;

  homebrew = {
    enable = true;
    casks = config.piste.packages.resolved.casks;
    brews = config.piste.packages.resolved.brews;

    # Optional: Declarative tap management
    # taps = {
    #   "homebrew/homebrew-core" = homebrew-core;
    #   "homebrew/homebrew-cask" = homebrew-cask;
    # };

    # onActivation.cleanup = "uninstall";

    # These app IDs are from using the mas CLI app
    # mas = mac app store
    # https://github.com/mas-cli/mas
    #
    # $ nix shell nixpkgs#mas
    # $ mas search <app name>
    #
    # If you have previously added these apps to your Mac App Store profile (but not installed them on this system),
    # you may receive an error message "Redownload Unavailable with This Apple ID".
    # This message is safe to ignore. (https://github.com/dustinlyons/nixos-config/issues/83)

    masApps = {
      # "1password" = 1333542190;
      # "wireguard" = 1451685025;
    };
  };

  # Load configuration that is shared across systems
  environment.systemPackages = config.piste.packages.resolved.systemPackages;
  # Note: packages.networking excluded - Linux-specific packages (Tailscale via cask on macOS)

  # Fonts - managed by Nix, installed to /Library/Fonts/Nix Fonts
  fonts.packages = config.piste.packages.resolved.fonts;

  # Enable home-manager
  home-manager = {
    useGlobalPkgs = true;
    # A file in the way is moved aside as <file>.backup-<epoch>, the name the
    # Linux switch gives (home-manager switch -b backup-<epoch>). A fixed
    # extension would block the next switch once a backup of that name exists.
    backupCommand = pkgs.writeShellScript "piste-hm-backup" ''
      b="$1.backup-$(${pkgs.coreutils}/bin/date +%s)"
      ${pkgs.coreutils}/bin/mv -- "$1" "$b" && echo "piste: moved $1 aside to $b" >&2
    '';
    extraSpecialArgs = { inherit user; };
    users.${user} = { pkgs, config, lib, ... }:{
      imports = [
        ../roles/desktop.nix
      ];

      home = {
        enableNixpkgsReleaseCheck = false;
        packages = []; # the packages module puts the engine's list in environment.systemPackages

        # A host's own (piste init's template sets the release it started at).
        stateVersion = lib.mkDefault "23.11";
      };

      # Enable fontconfig for proper font discovery
      fonts.fontconfig.enable = true;

      # Add nix-darwin fonts directory to fontconfig
      xdg.configFile."fontconfig/conf.d/10-nix-fonts.conf".text = ''
        <?xml version="1.0"?>
        <!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
        <fontconfig>
          <dir>/Library/Fonts/Nix Fonts</dir>
        </fontconfig>
      '';

      # Marked broken Oct 20, 2022 check later to remove this
      # https://github.com/nix-community/home-manager/issues/3344
      manual.manpages.enable = false;
    };
  };

  # What's on the Dock is yours: nix-darwin's system.defaults.dock.persistent-apps
  # lists it (the engine leaves the items alone), and system.defaults.dock
  # above is how the Dock behaves.
}
