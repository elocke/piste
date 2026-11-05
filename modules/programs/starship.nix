{ config, pkgs, lib, ... }:

{
  programs.starship = {
    enable = true;
    enableZshIntegration = true;

    settings = {
      # Solarized Dark Palette
      palette = "solarized_dark";

      palettes.solarized_dark = {
        # Base colors
        base03 = "#002b36";  # darkest background
        base02 = "#073642";  # background highlights
        base01 = "#586e75";  # comments/secondary
        base00 = "#657b83";  # body text (dark)
        base0 = "#839496";   # body text (light)
        base1 = "#93a1a1";   # emphasized content
        base2 = "#eee8d5";   # background highlights (light)
        base3 = "#fdf6e3";   # lightest background
        # Accent colors
        yellow = "#b58900";
        orange = "#cb4b16";
        red = "#dc322f";
        magenta = "#d33682";
        violet = "#6c71c4";
        blue = "#268bd2";
        cyan = "#2aa198";
        green = "#859900";
      };

      # General
      add_newline = true;
      scan_timeout = 10;

      # Powerline-style prompt format
      format = lib.concatStrings [
        # Left side - powerline segments
        "[ ](blue)"
        "$os"
        "$directory"
        "[ ](fg:blue bg:violet)"
        "$git_branch"
        "$git_status"
        "[ ](fg:violet bg:base02)"
        "$nix_shell"
        "$nodejs"
        "$python"
        "$rust"
        "$golang"
        "$java"
        "$lua"
        "$php"
        "[ ](fg:base02)"
        "$fill"
        # Right side - powerline segments (reversed)
        "[ ](fg:base02)"
        "$cmd_duration"
        "$jobs"
        "[ ](fg:orange bg:base02)"
        "$aws"
        "$kubernetes"
        "$terraform"
        "$docker_context"
        "[ ](fg:blue bg:orange)"
        "$time"
        "[ ](fg:blue)"
        "$line_break"
        "$character"
      ];

      # Fill space between left and right
      fill.symbol = " ";

      # OS icon - using nf-md icons (Material Design, U+F0000+ range that works)
      os = {
        disabled = false;
        style = "bg:blue fg:base03";
        symbols = {
          # Using nf-md-apple (󰀵) for macOS - this icon works directly
          Alpaquita = "󰣇 ";
          Alpine = "󰣇 ";
          AlmaLinux = "󰣇 ";
          Amazon = "󰸏 ";
          Android = "󰀲 ";
          Arch = "󰣇 ";
          Artix = "󰣇 ";
          CentOS = "󰣇 ";
          Debian = "󰣚 ";
          EndeavourOS = "󰣇 ";
          Fedora = "󰣛 ";
          FreeBSD = "󰣠 ";
          Garuda = "󰣇 ";
          Gentoo = "󰣨 ";
          Illumos = "󰣇 ";
          Kali = "󰣇 ";
          Linux = "󰌽 ";
          Macos = "󰀵 ";
          Manjaro = "󰣇 ";
          Mint = "󰣇 ";
          NixOS = "󱄅 ";
          OpenBSD = "󰣇 ";
          openSUSE = "󰣇 ";
          Pop = "󰣇 ";
          Raspbian = "󰐿 ";
          Redhat = "󰮤 ";
          RedHatEnterprise = "󰮤 ";
          RockyLinux = "󰣇 ";
          Ubuntu = "󰕈 ";
          Unknown = "? ";
          Void = "󰣇 ";
          Windows = "󰖳 ";
        };
      };

      # Hostname (for SSH)
      hostname = {
        ssh_symbol = "󰣀 ";
      };

      # Directory
      directory = {
        style = "bg:blue fg:base03 bold";
        format = "[ $path ]($style)[$read_only]($read_only_style bg:blue)";
        truncation_length = 3;
        truncate_to_repo = true;
        read_only = " 󰌾";
        read_only_style = "fg:red";
        home_symbol = "󰋜 ";
        substitutions = {
          Documents = "󰈙 ";
          Downloads = "󰇚 ";
          Music = "󰝚 ";
          Pictures = "󰋩 ";
          code = "󰲋 ";
          src = "󰲋 ";
          piste = "󱄅 ";
        };
      };

      # Git branch
      git_branch = {
        style = "bg:violet fg:base03";
        format = "[ $symbol$branch(:$remote_branch) ]($style)";
        symbol = "󰘬 ";
      };

      # Git commit
      git_commit = {
        tag_symbol = " 󰓹 ";
      };

      # Git status
      git_status = {
        style = "bg:violet fg:base03";
        format = "[$all_status$ahead_behind]($style)";
        conflicted = "󰽜 ";
        ahead = "⇡$count ";
        behind = "⇣$count ";
        diverged = "⇕⇡$ahead_count⇣$behind_count ";
        untracked = "? $count ";
        stashed = "󰆓 $count ";
        modified = "󰏫 $count ";
        staged = "󰐕 $count ";
        renamed = "󰑕 $count ";
        deleted = "󰩹 $count ";
      };

      # Prompt character
      character = {
        success_symbol = "[❯](bold green)";
        error_symbol = "[❯](bold red)";
        vimcmd_symbol = "[❮](bold violet)";
        vimcmd_replace_one_symbol = "[❮](bold orange)";
        vimcmd_replace_symbol = "[❮](bold orange)";
        vimcmd_visual_symbol = "[❮](bold yellow)";
      };

      # Command duration
      cmd_duration = {
        min_time = 500;
        style = "bg:base02 fg:yellow";
        format = "[ 󱎫 $duration ]($style)";
      };

      # Jobs indicator
      jobs = {
        style = "bg:base02 fg:cyan";
        format = "[ $symbol$number ]($style)";
        symbol = "✦ ";
        threshold = 1;
      };

      # Time
      time = {
        disabled = false;
        style = "bg:blue fg:base03";
        format = "[ 󰥔 $time ]($style)";
        time_format = "%H:%M";
      };

      # === Languages (powerline style with nf-md icons) ===

      nix_shell = {
        symbol = "󱄅 ";
        style = "bg:base02 fg:blue";
        format = "[ $symbol($name) ]($style)";
        impure_msg = "";
        pure_msg = "pure";
      };

      nodejs = {
        symbol = "󰎙 ";
        style = "bg:base02 fg:green";
        format = "[ $symbol$version ]($style)";
        detect_files = [ "package.json" ".node-version" ".nvmrc" ];
        detect_folders = [ "node_modules" ];
      };

      python = {
        symbol = "󰌠 ";
        style = "bg:base02 fg:yellow";
        format = "[ $symbol$version( $virtualenv) ]($style)";
        detect_files = [ "requirements.txt" "pyproject.toml" "setup.py" ".python-version" "Pipfile" ];
      };

      rust = {
        symbol = "󱘗 ";
        style = "bg:base02 fg:orange";
        format = "[ $symbol$version ]($style)";
      };

      golang = {
        symbol = "󰟓 ";
        style = "bg:base02 fg:cyan";
        format = "[ $symbol$version ]($style)";
      };

      java = {
        symbol = "󰬷 ";
        style = "bg:base02 fg:red";
        format = "[ $symbol$version ]($style)";
      };

      lua = {
        symbol = "󰢱 ";
        style = "bg:base02 fg:blue";
        format = "[ $symbol$version ]($style)";
      };

      php = {
        symbol = "󰌟 ";
        style = "bg:base02 fg:violet";
        format = "[ $symbol$version ]($style)";
      };

      # Additional languages with nf-md symbols
      c.symbol = "󰙱 ";
      cpp.symbol = "󰙲 ";
      haskell.symbol = "󰲒 ";
      kotlin.symbol = "󱈙 ";
      ruby.symbol = "󰴭 ";
      swift.symbol = "󰛥 ";

      # === Cloud/Infra (powerline style with nf-md icons) ===

      aws = {
        symbol = "󰸏 ";
        style = "bg:orange fg:base03";
        format = "[ $symbol$profile(/$region) ]($style)";
        disabled = false;
      };

      kubernetes = {
        symbol = "󱃾 ";
        style = "bg:orange fg:base03";
        format = "[ $symbol$context(/$namespace) ]($style)";
        disabled = false;
      };

      terraform = {
        symbol = "󱁢 ";
        style = "bg:orange fg:base03";
        format = "[ $symbol$workspace ]($style)";
      };

      docker_context = {
        symbol = "󰡨 ";
        style = "bg:orange fg:base03";
        format = "[ $symbol$context ]($style)";
        disabled = false;
        only_with_files = true;
      };

      # Additional tools with nf-md symbols
      gcloud.symbol = "󱇶 ";
      package.symbol = "󰏓 ";
      memory_usage.symbol = "󰍛 ";

      # Disabled modules
      azure.disabled = true;
    };
  };
}
