# NixOS/darwin module: piste.packages.*
# Defines options for toggling package categories and excluding specific items.
# Resolves final package lists that darwin.nix or devbox hosts can consume.
#
# Usage in darwin.nix (system level):
#   imports = [ ./packages-module.nix ];
#   environment.systemPackages = config.piste.packages.resolved.systemPackages;
#   homebrew.casks = config.piste.packages.resolved.casks;
#
# Usage in devbox (home-manager level):
#   imports = [ ./packages-module.nix ];
#   home.packages = config.piste.packages.resolved.homePackages;

{
  config,
  pkgs,
  lib,
  ...
}:

let
  cfg = config.piste.packages;
  system = pkgs.stdenv.hostPlatform.system;
  inherit (lib)
    mkOption
    mkEnableOption
    types
    optional
    optionals
    filter
    elem
    ;

  mkCategoryEnable = desc: (mkEnableOption desc) // { default = true; };

  # --- Package lists ---

  cliPkgs =
    (with pkgs; [
      # Shell essentials
      bash-completion
      coreutils
      direnv
      fontconfig
      fzf
      killall
      # No openssh: the machine's own ssh reads the machine's own ssh_config
      # (a Mac's UseKeychain, a distribution's GSSAPIAuthentication), which
      # Nix's OpenSSH rejects as an unknown option.
      tmux
      trash-cli # trash-put: recoverable delete for humans and agents
      tree
      wget

      # Modern Rust CLI tools
      atuin
      bat
      bottom
      broot
      delta
      difftastic
      dust
      duf
      eza
      fd
      hyperfine
      hexyl
      jq
      jless
      lazygit
      procs
      ripgrep
      sd
      tealdeer
      tokei
      xh
      yazi
      yq
      zoxide

      # Compression
      gzip
      unrar
      unzip
      xz
      zip

      # System monitoring
      bandwhich
      btop
      htop
      iftop

      # Other
      bash
      git
      hunspell
      eternal-terminal
      mosh
      fastfetch
      sqlite
      sops         # edit/inspect sops-nix encrypted secrets (e.g. secrets/env/*.sops.yaml)
      websocat
    ]);

  devPkgs =
    with pkgs;
    [
      # Version control
      gh
      go-task
      just
      prek

      # Agent tooling: rtk (unfree) comes with the `rtk` agents preset
      # (modules/programs/agents.nix).

      # Container tools
      # On Linux the client only: the daemon and containerd can't run from a
      # home profile (named "docker" too, so `exclude = [ "docker" ]` drops it).
      (if pkgs.stdenv.hostPlatform.isLinux then docker-client else docker)
      docker-compose
      kubectl

      # Python
      # Wrapped interpreter (pkgs/overlays/80-python-manylinux.nix) so compiled
      # wheels in venvs find libstdc++ on non-NixOS Linux; plain python312 on
      # Darwin.
      python312-manylinux
      python312Packages.pip
      # pipx's test suite is stale on the current nixpkgs: test_package_specifier
      # asserts old `name@ url` formatting, but the newer `packaging` normalizes to
      # `name @ url`, failing checkPhase. The package itself is fine — skip the
      # tests. Remove once nixpkgs catches up.
      (python312Packages.pipx.overridePythonAttrs (_: {
        doCheck = false;
        doInstallCheck = false;
      }))
      python312Packages.pyyaml
      python312Packages.virtualenv
      uv

      # Node.js
      nodejs
      pnpm

      # Cloud & tunneling
      doctl

      # Nix
      nixd
      nixfmt
      nh
      nix-output-monitor

      # AI/Coding assistants (resolved via piste.overlays.ai + pkgs/overlays/)
      claude-code
      codex
      gemini-cli
      # Opt-in tools (antigravity-cli, crit, herdr, komodo-cli, psst) are
      # packaged here (pkgs/overlays/) but not installed by default: add them
      # with piste.packages.extraPackages.

      # Editor
      fresh-editor    # terminal text editor with LSP + TypeScript plugins

      # Build tools
      cmake
      portaudio
    ]
    ++ optionals (system != "aarch64-darwin") [
      zrok
    ]
    ++ [
      # Other
      mise            # Polyglot runtime/version manager (asdf-compatible)
      proto
    ];

  fontPkgs = with pkgs; [
    # Nerd Fonts (patched with icons)
    nerd-fonts.fira-code
    nerd-fonts.jetbrains-mono
    nerd-fonts.hack
    nerd-fonts.meslo-lg
    nerd-fonts.symbols-only

    # Standard fonts
    dejavu_fonts
    font-awesome
    fira-code
    fira-code-symbols
    noto-fonts
    noto-fonts-color-emoji
    inter
    source-code-pro
  ];

  terminalPkgs =
    with pkgs;
    [
      zsh-autosuggestions
      zsh-syntax-highlighting
      zsh-completions
      zellij
    ]
    # Alacritty needs GL: a Mac's terminal, not a home profile's.
    ++ optional pkgs.stdenv.hostPlatform.isDarwin alacritty;

  securityPkgs = with pkgs; [
    age
    age-plugin-yubikey
    gnupg
    libfido2
  ];

  spellingPkgs = with pkgs; [
    aspell
    aspellDicts.en
  ];

  mediaPkgs = with pkgs; [
    ffmpeg
  ];

  networkingPkgs = [ ];

  darwinOnlyPkgs = with pkgs; [
    terminal-notifier  # clickable desktop notifications for zellaude, the engine's zellij tab bar (programs/zellij.nix)
  ];

  # The one cask the engine configures (modules/programs/ghostty.nix). The
  # other apps are the extras role's (modules/extras/casks.nix) or yours
  # (casks.extra); Homebrew's cleanup is off, so nothing is uninstalled.
  allCasks = [
    "ghostty"
  ];

  allBrews = [ ];

  # --- Resolved lists ---

  collectIf = cond: items: if cond then items else [ ];

  # `exclude` trims the categories only; extraPackages is never filtered, so
  # a host can swap a category package for a variant with the same name
  # (e.g. docker -> docker-client, both named "docker").
  notExcluded = p: !elem (lib.getName p) cfg.exclude;

  # piste sits outside the categories and exclude: dropping cli must not drop
  # the command that switches back. Only piste.packages.piste = null drops it.
  # (It keeps its old place in the list, after the cli tools.)
  resolvedPkgs =
    filter notExcluded (collectIf cfg.cli.enable cliPkgs)
    ++ optional (cfg.piste != null) cfg.piste
    ++ filter notExcluded (
      collectIf cfg.dev.enable devPkgs
      ++ collectIf cfg.terminal.enable terminalPkgs
      ++ collectIf cfg.security.enable securityPkgs
      ++ collectIf cfg.spelling.enable spellingPkgs
      ++ collectIf cfg.media.enable mediaPkgs
      ++ collectIf cfg.networking.enable networkingPkgs
    )
    ++ cfg.extraPackages;

  resolvedDarwinPkgs = resolvedPkgs ++ collectIf cfg.darwinOnly.enable darwinOnlyPkgs;

  # `casks.exclude` and `brews.exclude` drop a name wherever it comes from:
  # the engine's list, the extras role's, a team layer's or `extra`.
  resolvedCasks =
    filter (c: !elem c cfg.casks.exclude) (lib.unique ((if cfg.casks.enable then allCasks else [ ]) ++ cfg.casks.extra));

  resolvedBrews =
    filter (b: !elem b cfg.brews.exclude) (lib.unique ((if cfg.brews.enable then allBrews else [ ]) ++ cfg.brews.extra));

  resolvedFonts = collectIf cfg.fonts.enable fontPkgs;

in
{
  options.piste.packages = {
    # Category toggles
    cli.enable = mkCategoryEnable "CLI tools (bat, ripgrep, fd, etc.)";
    dev.enable = mkCategoryEnable "Development tools (gh, docker, python, node, etc.)";
    fonts.enable = mkCategoryEnable "Nerd Fonts and standard fonts";
    terminal.enable = mkCategoryEnable "Terminal emulators and shells";
    security.enable = mkCategoryEnable "Security tools (age, gnupg, yubikey)";
    spelling.enable = mkCategoryEnable "Spelling and dictionaries";
    media.enable = mkCategoryEnable "Media tools (ffmpeg)";
    networking.enable = mkCategoryEnable "Networking tools";
    darwinOnly.enable = mkCategoryEnable "Darwin-only packages (terminal-notifier)";

    piste = mkOption {
      internal = true;
      type = types.nullOr types.package;
      default = null;
      description = "The piste package, installed whatever the categories and `exclude` say (mkFlake sets it; null drops it).";
    };

    casks = {
      enable = mkCategoryEnable "Homebrew casks (GUI apps)";
      exclude = mkOption {
        type = types.listOf types.str;
        default = [ ];
        description = "Cask names to leave out, from the engine's list, the extras role's and `extra` alike (e.g. MDM-managed apps like slack, zoom)";
        example = [
          "slack"
          "zoom"
          "google-chrome"
        ];
      };
      extra = mkOption {
        type = types.listOf types.str;
        default = [ ];
        description = "Additional casks to install alongside the shared set";
      };
    };

    brews = {
      enable = mkCategoryEnable "Homebrew brews (CLI via brew)";
      exclude = mkOption {
        type = types.listOf types.str;
        default = [ ];
        description = "Brew names to leave out, from the engine's list and `extra` alike";
      };
      extra = mkOption {
        type = types.listOf types.str;
        default = [ ];
        description = "Additional brews to install alongside the shared set";
      };
    };

    exclude = mkOption {
      type = types.listOf types.str;
      default = [ ];
      example = [ "docker" ];
      description = ''
        Package names (lib.getName) to drop from the enabled categories.
        Does not apply to extraPackages.
      '';
    };

    extraPackages = mkOption {
      type = types.listOf types.package;
      default = [ ];
      description = "Additional Nix packages to install alongside enabled categories";
    };

    # Read-only resolved outputs (consumed by darwin.nix / devbox hosts)
    resolved = {
      homePackages = mkOption {
        internal = true;
        type = types.listOf types.package;
        readOnly = true;
        default = resolvedPkgs;
        defaultText = lib.literalMD "the enabled cli, dev, terminal, security, spelling, media and networking packages, minus `exclude`, plus `extraPackages`";
        description = "Resolved home.packages list (for devbox/HM context)";
      };
      systemPackages = mkOption {
        internal = true;
        type = types.listOf types.package;
        readOnly = true;
        default = resolvedDarwinPkgs;
        defaultText = lib.literalMD "`homePackages` plus the `darwinOnly` category's (when `darwinOnly.enable`)";
        description = "Resolved environment.systemPackages list (for darwin context)";
      };
      casks = mkOption {
        internal = true;
        type = types.listOf types.str;
        readOnly = true;
        default = resolvedCasks;
        defaultText = lib.literalMD "the engine's casks (when `casks.enable`) plus `casks.extra`, minus `casks.exclude`";
        description = "Resolved homebrew cask list after exclusions";
      };
      brews = mkOption {
        internal = true;
        type = types.listOf types.str;
        readOnly = true;
        default = resolvedBrews;
        defaultText = lib.literalMD "the engine's brews (when `brews.enable`) plus `brews.extra`, minus `brews.exclude`";
        description = "Resolved homebrew brew list after exclusions";
      };
      fonts = mkOption {
        internal = true;
        type = types.listOf types.package;
        readOnly = true;
        default = resolvedFonts;
        defaultText = lib.literalMD "the fonts category's packages (when `fonts.enable`)";
        description = "Resolved font packages list";
      };
    };
  };
}
