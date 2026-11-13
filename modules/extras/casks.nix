# extras.casks: extra Homebrew casks and the default apps for file types
# (A nix-darwin module, imported by darwin.nix; not a home-manager one.)
# Everything it sets goes under `lib.mkIf cfg.enable`, so
# piste.roles.extras.casks.enable = false drops it.
#
# The engine's own cask list is Ghostty alone (modules/packages-module.nix).
# These are the apps a developer's Mac tends to want on top. Each name goes
# through piste.packages.casks.extra, so `piste.packages.casks.exclude` drops
# one (an app your employer's MDM already pushes, say); Homebrew's cleanup is
# off, so a cask dropped here later is never uninstalled. Nothing here needs
# an account, a licence or a particular mouse: those are your overlay's
# casks.extra. checks.tap-names keeps every name in the pinned tap.
{ config, lib, pkgs, user, ... }:

let
  cfg = config.piste.roles.extras.casks;
  casks = [
    # Browsers
    "firefox"
    "google-chrome"

    # Development
    "bruno"
    "cmux"                       # Ghostty-based terminal for AI coding agents
    "cyberduck"
    "docker-desktop"
    "httpie-desktop"
    "rapidapi"
    "visual-studio-code"

    # Productivity
    "raycast"

    # Utilities
    "jordanbaird-ice"            # menu bar manager
    "karabiner-elements"
    "syncthing-app"
  ];
in
{
  # The piece's handle: disabledModules = [ { key = <this>; } ] drops it (its path doesn't).
  key = "pack:extras/casks";

  options.piste.roles.extras.casks.enable =
    (lib.mkEnableOption "the extras pack's extra Homebrew casks and the default apps for file types") // { default = true; };

  config = lib.mkIf cfg.enable {
    piste.packages.casks.extra = casks;

    # Markdown opens in VS Code (macOS Launch Services file associations, set
    # with duti on each activation; an app such as RapidAPI Client registers
    # itself for .md on install otherwise). Only where VS Code is installed.
    home-manager.users.${user} = { lib, ... }: {
      config = lib.mkIf (lib.elem "visual-studio-code" config.piste.packages.resolved.casks) {
        home.packages = [ pkgs.duti ];

        home.activation.setFileAssociations =
          lib.hm.dag.entryAfter [ "writeBoundary" ] ''
            run --quiet ${pkgs.duti}/bin/duti -s com.microsoft.VSCode .md all || true

            # .mdx has no app-declared UTI on macOS, so LaunchServices invents a
            # dynamic one (dyn.ah62d4qmxhk2x45peta = "?0=13:1=mdx") and duti can't
            # bind a handler to it: it fails with error -50 (paramErr) on every
            # switch. Harmless but noisy, so swallow stderr. If some app ever
            # declares .mdx properly this starts working with no change here.
            run --quiet ${pkgs.duti}/bin/duti -s com.microsoft.VSCode .mdx all 2>/dev/null || true
          '';
      };
    };
  };
}
