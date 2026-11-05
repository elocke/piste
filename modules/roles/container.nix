# Home-manager "container" profile: the dev role, minus anything that only
# makes sense on a real host. It is what lib.mkDevcontainer bakes into a
# devcontainer image, and it is deliberately not host-specific — no hostname,
# no SSH identities, no secrets. Zones layer their own modules on top.
#
# The image never runs home-manager activation (there is no Nix inside it),
# so only home.packages and home.file reach the container. Anything a module
# does in home.activation is skipped; see lib/devcontainer.nix.
{ config, lib, pkgs, ... }:

let cfg = config.piste.roles.container; in
{
  options.piste.roles.container = {
    mutableFiles = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ ".config/gh/config.yml" ];
      description = ''
        Paths under $HOME that the image copies as real, user-owned files
        instead of symlinks into the read-only store. Use it for config files
        a tool rewrites at runtime.
      '';
    };
  };

  imports = [
    ./dev.nix
    ../packages-module.nix
  ];

  config = {
    # Headless and throwaway: drop the GUI, font and host-only buckets. dev +
    # cli + security stay on; zones can switch more off.
    piste.packages = {
      fonts.enable = lib.mkDefault false;
      terminal.enable = lib.mkDefault false;
      spelling.enable = lib.mkDefault false;
      media.enable = lib.mkDefault false;
      networking.enable = lib.mkDefault false;
      darwinOnly.enable = lib.mkDefault false;
      casks.enable = lib.mkDefault false;
      brews.enable = lib.mkDefault false;
      # Dropped from the dev/cli buckets to keep the image near 2 GB:
      # - proto: pulls a full gcc (~260 MB); mise covers runtime versions.
      # - second-string agent CLIs and host/network tools with no use in a
      #   container (mosh/et need a listening host, bandwhich/iftop need caps).
      # Zones add any of these back through extraPackages, which exclude
      # never filters.
      exclude = [
        "proto"
        "gemini-cli"
        "zrok"
        "doctl"
        "cmake"
        "portaudio"
        "mosh"
        "eternal-terminal"
        "bandwhich"
        "iftop"
        "hunspell"
        "unrar"
        "fontconfig"
        # its previewers pull ffmpeg, resvg and poppler (~150 MB)
        "yazi"
      ];
      # zellij normally rides in the terminal bucket, which is off here; the
      # zellij module only configures it. The image has no distro base, so no
      # ssh client either (the packages module leaves openssh to the machine's
      # own): git over ssh needs one. The Docker client is the dev bucket's on
      # Linux (the daemon can't run unprivileged; the client drives a
      # forwarded socket).
      extraPackages = [ pkgs.openssh pkgs.zellij ];
    };

    home.packages = config.piste.packages.resolved.homePackages;

    # home-manager points LOCALE_ARCHIVE at all of glibcLocales (~220 MB).
    i18n.glibcLocales = lib.mkDefault (pkgs.glibcLocales.override {
      allLocales = false;
      locales = [ "en_US.UTF-8/UTF-8" "C.UTF-8/UTF-8" ];
    });

    # The editor module's vim is vim-full, whose default GTK3 GUI drags in
    # gtk, pango and friends, plus a Ruby interpreter. Terminal-only here.
    programs.vim.packageConfigurable = lib.mkDefault (pkgs.vim-full.override {
      guiSupport = "none";
      waylandSupport = false;
      rubySupport = false;
    });

    # zsh + zellij are the interactive surface.
    piste.programs.zellij.enable = lib.mkDefault true;

    # Signing needs a key file the image does not have, even when the zone's
    # home names one (the base git module signs by default when it does).
    # mkOverride 900, so a zone that forwards a signing key can still turn
    # it back on with a plain definition.
    programs.git.settings.commit.gpgsign = lib.mkOverride 900 false;

    # gh rewrites config.yml on `gh auth login` / `gh config set`. On hosts an
    # activation script makes it writable; the image has no activation.
    piste.roles.container.mutableFiles = [ ".config/gh/config.yml" ];
    xdg.configFile."gh/config.yml".enable = true;

    # Claude and Codex keep all their state under one directory each, so a
    # single volume per harness persists logins and history across rebuilds.
    home.sessionVariables = {
      CLAUDE_CONFIG_DIR = "${config.home.homeDirectory}/.claude";
      CODEX_HOME = "${config.home.homeDirectory}/.codex";
    };

    programs.home-manager.enable = lib.mkDefault false;
    home.stateVersion = lib.mkDefault "24.05";
  };
}
