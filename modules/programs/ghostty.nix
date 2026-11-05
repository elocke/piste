{ config, pkgs, lib, ... }:

{
  xdg.configFile."ghostty/config".text = ''
    # Font with Symbols Nerd Font fallback for extended icons
    font-family = Hack Nerd Font
    font-family = Symbols Nerd Font Mono
    font-size = 11

    # Window
    window-padding-x = 12
    window-padding-y = 12
    window-decoration = true
    macos-titlebar-style = native

    # Cursor
    cursor-style = block
    cursor-style-blink = false

    # Theme - Solarized Dark
    background = 002b36
    foreground = 839496

    # Normal colors (Solarized Dark)
    palette = 0=#073642
    palette = 1=#dc322f
    palette = 2=#859900
    palette = 3=#b58900
    palette = 4=#268bd2
    palette = 5=#d33682
    palette = 6=#2aa198
    palette = 7=#eee8d5

    # Bright colors (Solarized Dark)
    palette = 8=#002b36
    palette = 9=#cb4b16
    palette = 10=#586e75
    palette = 11=#657b83
    palette = 12=#839496
    palette = 13=#6c71c4
    palette = 14=#93a1a1
    palette = 15=#fdf6e3

    # Shell integration
    shell-integration = zsh

    # Mouse - full support for TUI apps and SSH
    mouse-scroll-multiplier = 3

    # Terminal compatibility for SSH (remote systems may lack ghostty terminfo)
    term = xterm-256color

    # Shift+Enter sends newline through Zellij (bracketed paste workaround)
    keybind = shift+enter=text:\x1b[200~\n\x1b[201~

    # macOS specific
    macos-non-native-fullscreen = true

    # Clipboard — OSC 52 for SSH-to-Mac copy (a program may write it, not read it)
    clipboard-write = allow
    clipboard-trim-trailing-spaces = true

    # Mouse
    mouse-hide-while-typing = true

    # Scrollback — 100 MB in bytes (default is 10 MB); Zellij owns its own per-pane buffer
    scrollback-limit = 100000000
  '';
}
