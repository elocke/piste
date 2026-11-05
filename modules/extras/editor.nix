# extras.editor: editor settings
# git's editor is vim (the engine leaves it to $VISUAL and $EDITOR, which are
# nano), and git ignores the editors' project directories (.idea/, .vscode/)
# and .envrc everywhere. Everything it sets goes under `lib.mkIf cfg.enable`,
# so piste.roles.extras.editor.enable = false drops it.
{ config, lib, pkgs, ... }:

let cfg = config.piste.roles.extras.editor; in
{
  options.piste.roles.extras.editor.enable =
    (lib.mkEnableOption "the extras pack's editor settings") // { default = true; };

  config = lib.mkIf cfg.enable {
    programs.git.settings.core.editor = lib.mkDefault "vim";
    programs.git.ignores = lib.mkAfter [
      ".idea/"
      ".vscode/"
      ".envrc"
    ];
  };
}
