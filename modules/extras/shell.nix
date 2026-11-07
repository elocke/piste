# extras.shell: shell aliases and settings
# The aliases the engine's defaults leave out because they change what a
# pasted command does (grep, find, ps, du, df and diff run the modern tools;
# top and htop run btm; cc is `claude --continue`, which hides the C compiler;
# cd is zoxide's z), plus a few shortcuts (ccr and cca for Claude Code, c q h
# j, myip), shell() and you-should-use's YSU_MODE=ALL. Nothing here throws
# work away: git aliases that do (`git nuke`, `git discard`) and
# fetch.pruneTags are not part of the pack. The shadows are humans-only (an
# agent's shell keeps the real cc, top and grep) and come last in the init, so
# the engine's functions (pisteup, mkcd, gclone) keep the real grep and cd.
# Everything it sets goes under `lib.mkIf cfg.enable`, so
# piste.roles.extras.shell.enable = false drops it.
{ config, lib, pkgs, ... }:

let
  cfg = config.piste.roles.extras.shell;

  # Shadows of classic tools, defined only in shells no agent started (the
  # same guard as the engine's own human aliases, programs/shell.nix): an
  # agent parses classic output and passes classic flags (`du -sh`,
  # `diff -u`, `find -name`).
  humanAliases = {
    grep = "rg";
    find = "fd";
    ps = "procs";
    du = "dust";
    df = "duf";
    diff = "difft";
    cc = "claude --continue";
    cca = "claude --permission-mode auto";
    top = "btm";
    htop = "btm";
  };
in
{
  # The piece's handle: disabledModules = [ { key = <this>; } ] drops it (its path doesn't).
  key = "pack:extras/shell";

  options.piste.roles.extras.shell.enable =
    (lib.mkEnableOption "the extras pack's shell aliases and settings") // { default = true; };

  config = lib.mkIf cfg.enable {
    # mkDefault: an overlay's plain alias of the same name wins.
    programs.zsh.shellAliases = lib.mapAttrs (_: lib.mkDefault) {
      # Claude Code: pick a session (cc and cca are in the humans-only set above)
      ccr = "claude --resume";

      c = "clear";
      q = "exit";
      h = "history";
      j = "jobs -l";
      myip = "curl -s ifconfig.me";
    };

    # After the engine's init (mkAfter), so zoxide's init, which defines z,
    # has run.
    programs.zsh.initContent = lib.mkAfter ''
      # === Extras: modern replacements (humans only) ===
      # Skipped when Claude Code ($CLAUDECODE), Codex ($CODEX_*) or Paseo
      # ($PASEO_TERMINAL_ID / $PASEO_AGENT_ID) started this shell.
      if [[ -z "$CLAUDECODE" && -z "$PASEO_TERMINAL_ID" && -z "$PASEO_AGENT_ID" ]] \
        && (( ''${#''${(k)parameters[(I)CODEX_*]}} == 0 )); then
        ${lib.concatStringsSep "\n  " (lib.mapAttrsToList (n: v: "alias ${n}=${lib.escapeShellArg v}") humanAliases)}
      fi

      # cd is zoxide's z, where zoxide is installed.
      if command -v zoxide >/dev/null 2>&1; then
        alias cd="z"
      fi

      # you-should-use: remind about every alias, not just the best match.
      export YSU_MODE=ALL

      # Quick nix-shell
      shell() { nix-shell '<nixpkgs>' -A "$1"; }
    '';
  };
}
