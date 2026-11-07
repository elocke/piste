# extras.tmux: tmux plugins and settings
# Prefix C-x (in place of C-b), x and v split the pane (the defaults " and %
# are unbound), and sessions are saved every 5 minutes and restored on start
# (resurrect, continuum). The save keeps the layout and the running programs,
# not what is on screen: resurrect's pane-contents capture is off, because it
# writes each pane's visible text to ~/.cache/tmux/resurrect in the clear (a
# token on screen would be on disk). resurrectExtraConfig is where a person
# who wants it turns it on.
# Everything it sets goes under `lib.mkIf cfg.enable`.
{ config, lib, pkgs, ... }:

let cfg = config.piste.roles.extras.tmux; in
{
  # The piece's handle: disabledModules = [ { key = <this>; } ] drops it (its path doesn't).
  key = "pack:extras/tmux";

  options.piste.roles.extras.tmux.enable =
    (lib.mkEnableOption "the extras pack's tmux plugins and settings") // { default = true; };

  options.piste.roles.extras.tmux.resurrectExtraConfig = lib.mkOption {
    type = lib.types.lines;
    default = "";
    example = lib.literalExpression ''
      '''
        set -g @resurrect-capture-pane-contents 'on'
        set -g @resurrect-pane-contents-area 'visible'
      '''
    '';
    description = ''
      tmux lines placed with resurrect's own settings, before it loads. The
      pack leaves `@resurrect-capture-pane-contents` off (its default):
      turned on, a save writes each pane's visible text to
      `~/.cache/tmux/resurrect` unencrypted, so a token on screen is on
      disk. The example turns it on.
    '';
  };

  config = lib.mkIf cfg.enable {
    programs.tmux.prefix = lib.mkDefault "C-x";

    # After the engine's plugins: continuum hooks the status line, so it comes
    # after catppuccin, which sets it.
    programs.tmux.plugins = lib.mkAfter (with pkgs.tmuxPlugins; [
      {
        plugin = resurrect; # Used by tmux-continuum

        # Use XDG data directory
        # https://github.com/tmux-plugins/tmux-resurrect/issues/348
        extraConfig = ''
          set -g @resurrect-dir '$HOME/.cache/tmux/resurrect'
        '' + lib.optionalString (cfg.resurrectExtraConfig != "")
          (lib.removeSuffix "\n" cfg.resurrectExtraConfig + "\n");
      }
      {
        plugin = continuum;
        extraConfig = ''
          set -g @continuum-restore 'on'
          set -g @continuum-save-interval '5' # minutes
        '';
      }
    ]);

    programs.tmux.extraConfig = lib.mkAfter ''
      # Split panes, vertical or horizontal
      unbind '"'
      unbind %
      bind-key x split-window -v
      bind-key v split-window -h
    '';
  };
}
