{ config, pkgs, lib, ... }:

let
  cfg = config.piste.programs.zellij;
  copyCommand = if pkgs.stdenv.hostPlatform.isDarwin then ''copy_command "pbcopy"'' else "";

  zellijPlugins = pkgs.zellij-plugins;
in
{
  options.piste.programs.zellij.enable =
    lib.mkEnableOption "zellij config (keybindings, zjstatus/zellaude layout, opt-in autostart)";

  options.piste.programs.zellij.timezone = lib.mkOption {
    type = lib.types.str;
    default = "Etc/UTC";
    example = "America/New_York";
    description = "The zone of the status bar's clock. zjstatus can't read the machine's zone, so set yours.";
  };

  config = lib.mkIf cfg.enable {
    programs.zsh.initContent = lib.mkAfter ''
      # Zellij autostart (opt-in): set ZELLIJ_AUTOSTART=1 to attach to "main" or
      # create it. Off by default — new terminals are plain shells; run `zellij`
      # or `zellij attach main` manually when you want a session. On reattach to an
      # existing session, open a fresh tab via --create-background + action new-tab
      # (works around zellij's missing client-attached hook; issues #1721/#3671).
      # Also skips: inside zellij, SSH sessions (devbox manages its own), VS Code
      # terminal, ZELLIJ_SKIP=1 escape hatch.
      if [[ -n "$ZELLIJ_AUTOSTART" ]] \
         && [[ -o interactive ]] \
         && [[ -z "$ZELLIJ" ]] \
         && [[ -z "$SSH_TTY" ]] \
         && [[ -z "$ZELLIJ_SKIP" ]] \
         && [[ "$TERM_PROGRAM" != "vscode" ]]; then
        if zellij list-sessions --short 2>/dev/null | grep -qx "main"; then
          zellij attach --create-background main 2>/dev/null || true
          zellij --session main action new-tab --name "$(date +%H:%M)" 2>/dev/null || true
          exec zellij attach main
        else
          exec zellij --session main
        fi
      fi
    '';

    xdg.configFile."zellij/config.kdl".text = ''
      // ============================================================
      // Zellij Configuration - Modern Terminal Workspace
      // ============================================================
      // Unlock-first model: all keys pass through by default.
      // Press Ctrl+a to enter Zellij control, then:
      //   p            → Pane mode (split, close, navigate)
      //   t            → Tab mode (new, close, rename)
      //   s            → Scroll mode (search, copy)
      //   o            → Session mode (detach, quit)
      //   Alt+hjkl     → Navigate panes (vim-style, always works)
      //   Alt+[]       → Previous/next tab
      //   Esc          → Back to locked (passthrough)
      // ============================================================

      // Core settings
      default_shell "zsh"
      default_layout "compact"
      on_force_close "detach"

      // Clipboard
      ${copyCommand}
      copy_clipboard "system"
      copy_on_select true

      // Scrollback
      scroll_buffer_size 50000
      scrollback_editor "/usr/bin/vim"

      // Mouse
      mouse_mode true

      // Performance
      pane_frames false
      auto_layout true

      // Session
      session_serialization true
      serialize_pane_viewport true

      // Mirror sessions for pair programming
      mirror_session false

      // Theme
      theme "custom-solarized"

      // Don't show default tips
      disable_session_metadata false

      // Styled underlines (modern terminals)
      styled_underlines true

      // UI tweaks
      ui {
          pane_frames {
              rounded_corners true
              hide_session_name false
          }
      }

      // ============================================================
      // Keybindings — Option C: documentation-aligned, locked-by-default
      // ============================================================
      // Default mode is locked. All keys pass through to the shell.
      // Press Ctrl+g (Zellij's documented unlock key) to enter Normal
      // mode, where every upstream-documented key works per the docs:
      //   Ctrl+p  → Pane mode
      //   Ctrl+t  → Tab mode
      //   Ctrl+s  → Scroll mode
      //   Ctrl+o  → Session mode
      //   Ctrl+n  → Resize mode
      //   Ctrl+h  → Move mode
      //   Ctrl+b  → Tmux-compat
      //   Ctrl+q  → Quit
      //
      // In locked mode we additionally bind Alt+ navigation so common
      // pane/tab movement does not require unlocking. We deliberately
      // do NOT bind Alt+f or Alt+b in locked mode — those are zsh
      // word-forward / word-back at the shell prompt.
      // ============================================================
      default_mode "locked"

      keybinds {
          locked {
              bind "Alt h" { MoveFocusOrTab "Left"; }
              bind "Alt j" { MoveFocus "Down"; }
              bind "Alt k" { MoveFocus "Up"; }
              bind "Alt l" { MoveFocusOrTab "Right"; }
              bind "Alt [" { GoToPreviousTab; }
              bind "Alt ]" { GoToNextTab; }
              bind "Alt n" { NewPane; }

              // Seamless vim+zellij navigation (Ctrl+hjkl)
              // The vim-zellij-navigator wasm uses `list-clients` to detect
              // whether (n)vim is focused; if so it forwards Ctrl+hjkl to vim,
              // otherwise it moves focus between zellij panes. Payload format
              // per https://github.com/hiasr/vim-zellij-navigator README.
              bind "Ctrl h" {
                  MessagePlugin "vim-zellij-navigator" {
                      name "move_focus_or_tab";
                      payload "left";
                      move_mod "ctrl";
                  };
              }
              bind "Ctrl j" {
                  MessagePlugin "vim-zellij-navigator" {
                      name "move_focus";
                      payload "down";
                      move_mod "ctrl";
                  };
              }
              bind "Ctrl k" {
                  MessagePlugin "vim-zellij-navigator" {
                      name "move_focus";
                      payload "up";
                      move_mod "ctrl";
                  };
              }
              bind "Ctrl l" {
                  MessagePlugin "vim-zellij-navigator" {
                      name "move_focus_or_tab";
                      payload "right";
                      move_mod "ctrl";
                  };
              }
          }
          session {
              bind "m" {
                  LaunchOrFocusPlugin "monocle" {
                      floating true
                      move_to_focused_tab true
                  };
                  SwitchToMode "Locked";
              }
              bind "?" {
                  LaunchOrFocusPlugin "forgot" {
                      floating true
                      move_to_focused_tab true
                  };
                  SwitchToMode "Locked";
              }
              bind "s" {
                  LaunchOrFocusPlugin "zsm" {
                      floating true
                      move_to_focused_tab true
                  };
                  SwitchToMode "Locked";
              }
              bind "w" {
                  LaunchOrFocusPlugin "worktree" {
                      floating true
                      move_to_focused_tab true
                  };
                  SwitchToMode "Locked";
              }
          }
      }

      // ============================================================
      // Custom theme - Solarized Dark
      // ============================================================
      themes {
          custom-solarized {
              fg "#839496"
              bg "#002b36"
              black "#073642"
              red "#dc322f"
              green "#859900"
              yellow "#b58900"
              blue "#268bd2"
              magenta "#d33682"
              cyan "#2aa198"
              white "#eee8d5"
              orange "#cb4b16"
          }
      }

      // ============================================================
      // Plugins
      // ============================================================
      plugins {
          tab-bar location="zellij:tab-bar"
          status-bar location="zellij:status-bar"
          strider location="zellij:strider"
          compact-bar location="zellij:compact-bar"
          session-manager location="zellij:session-manager"
          filepicker location="zellij:strider" {
              cwd "/"
          }
          // zellij-autolock intentionally NOT loaded with Option C.
          // Its hardcoded behavior (trigger → Locked, no trigger → Normal)
          // fights default_mode "locked": pressing Ctrl+g to lock at a
          // shell prompt makes autolock immediately revert to Normal.
          // The plugin stays packaged in pkgs/overlays/40-zellij-plugins.nix
          // for future re-enabling if the design changes.
          zjstatus location="file:${zellijPlugins.zjstatus}/zjstatus.wasm" {
              format_left   "#[fg=#b58900,bold]{mode} #[fg=#839496]{session}"
              format_center ""
              format_right  "#[fg=#586e75]{hints}#[fg=#839496] {datetime}"
              format_space  "#[bg=#002b36]"

              hide_frame_for_single_pane "true"

              mode_normal  "#[bg=#268bd2,fg=#002b36,bold] NORMAL "
              mode_locked  "#[bg=#586e75,fg=#fdf6e3,bold] LOCKED "
              mode_pane    "#[bg=#859900,fg=#002b36,bold] PANE "
              mode_tab     "#[bg=#cb4b16,fg=#002b36,bold] TAB "
              mode_resize  "#[bg=#dc322f,fg=#002b36,bold] RESIZE "
              mode_scroll  "#[bg=#d33682,fg=#002b36,bold] SCROLL "
              mode_session "#[bg=#6c71c4,fg=#002b36,bold] SESSION "
              mode_move    "#[bg=#2aa198,fg=#002b36,bold] MOVE "
              mode_tmux    "#[bg=#cb4b16,fg=#002b36,bold] TMUX "

              tab_normal "#[fg=#586e75] {index} {name} "
              tab_active "#[fg=#fdf6e3,bg=#073642,bold,italic] {index} {name} "

              datetime          "#[fg=#586e75] {format}"
              datetime_format   "%a %d %b %H:%M"
              datetime_timezone "${cfg.timezone}"
          }

          zjstatus_hints location="file:${zellijPlugins.zjstatus-hints}/zjstatus-hints.wasm" {
              max_length "0"
              overflow_str " … "
              hide_in_base_mode "false"
          }

          monocle location="file:${zellijPlugins.monocle}/monocle.wasm"
          forgot location="file:${zellijPlugins.zellij-forgot}/zellij_forgot.wasm"
          zsm location="file:${zellijPlugins.zsm}/zsm.wasm"
          vim-zellij-navigator location="file:${zellijPlugins.vim-zellij-navigator}/vim-zellij-navigator.wasm"
          worktree location="file:${zellijPlugins.zellij-worktree}/zellij-worktree.wasm"
          zellaude location="file:${zellijPlugins.zellaude}/zellaude.wasm"
      }

      // Background plugins (no UI; loaded at session start)
      load_plugins {
          "file:${zellijPlugins.zjstatus-hints}/zjstatus-hints.wasm"
          "file:${zellijPlugins.vim-zellij-navigator}/vim-zellij-navigator.wasm"
      }
    '';

    # Default layout with cleaner look.
    # Two top bars: zellaude (Claude Code activity per tab) above zjstatus
    # (mode/session/datetime). Note: zjstatus's format_center "{tabs}" widget
    # overlaps with zellaude's tab list — consider removing {tabs} from zjstatus
    # if the double-tab display feels noisy.
    xdg.configFile."zellij/layouts/default.kdl".text = ''
      layout {
          default_tab_template {
              pane size=1 borderless=true {
                  plugin location="zellaude"
              }
              pane size=1 borderless=true {
                  plugin location="zjstatus"
              }
              children
          }
          tab name="main"
      }
    '';
  };
}
