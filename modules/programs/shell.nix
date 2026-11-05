{ config, pkgs, lib, ... }:

let
  # Modern replacements that shadow classic tools, and interactive safety
  # prompts. Agents (Claude Code, Codex, Paseo) parse classic output and pass
  # classic flags (`du -sh`, `diff -u`, `find -name`), and have no TTY to answer
  # `rm -i`: the prompt reads EOF, answers "no", and the file silently stays.
  # So these are only defined in shells no agent started. Agent deletes are
  # guarded by the piste-guard hook instead (agents/hooks.toml).
  humanAliases = {
    cat = "bat";
    ls = "eza --icons --group-directories-first";
    ll = "eza -l --icons --group-directories-first --git";
    la = "eza -la --icons --group-directories-first --git";
    lt = "eza --tree --icons --level=2";
    tree = "eza --tree --icons";

    # Safety nets for humans.
    rm = "rm -i";
    cp = "cp -i";
    mv = "mv -i";
  };

  # A single-user Nix (--no-daemon) on Linux has no daemon script: its
  # installer hooks .profile, which a zsh login doesn't read (and
  # home-manager moves aside the zsh files it hooked), so source its nix.sh.
  # Linux only: a Mac's Nix is the daemon install piste expects.
  singleUserNix = lib.optionalString pkgs.stdenv.hostPlatform.isLinux ''
    elif [[ -f ~/.nix-profile/etc/profile.d/nix.sh ]]; then
      . ~/.nix-profile/etc/profile.d/nix.sh
  '';
in
{
  # Session variables
  home.sessionVariables = {
    # nano: it is installed (programs/editor.nix), sops and `piste edit` use
    # it, and a newcomer can get out of it. In sessionVariables, not exported
    # from the init, and mkDefault, so an overlay's plain EDITOR or VISUAL wins.
    EDITOR = lib.mkDefault "nano";
    VISUAL = lib.mkDefault "nano";

    # Silence zoxide's "doctor" nag. It wants `zoxide init` to be the last line
    # of the shell config, but zsh-syntax-highlighting must be sourced last, so
    # the warning is a false positive here.
    _ZO_DOCTOR = "0";

    PNPM_HOME = "$HOME/.local/share/pnpm";
    # XDG-aligned prefix for npm global installs. Mirrors PNPM_HOME so
    # `~/.local/share/{npm,pnpm}` is the canonical home for JS globals.
    NPM_CONFIG_PREFIX = "$HOME/.local/share/npm";
  };

  # PATH additions (prepended in reverse order)
  home.sessionPath = [
    "$HOME/.cargo/bin"
    "$HOME/.local/bin"
    "$HOME/.local/share/bin"
    "$HOME/.local/share/npm/bin"
    "$HOME/bin"
    # pnpm v11 links global binaries into $PNPM_HOME/bin (v10 used $PNPM_HOME
    # directly). Keep both: /bin for v11, the bare dir for a host still on v10.
    "$PNPM_HOME/bin"
    "$PNPM_HOME"
  ];

  programs.direnv = {
    enable = true;
    nix-direnv.enable = true;
    enableZshIntegration = true;
  };

  programs.zsh = {
    enable = true;

    # Nix on PATH for non-interactive shells on Linux too (`ssh <host> <cmd>`,
    # scripts): a Determinate install doesn't hook zshenv there, and the
    # interactive init below doesn't reach them. nix-daemon.sh guards against
    # being sourced twice. A Mac's /etc/zshenv (nix-darwin) already does this.
    # A single-user install (--no-daemon) falls back to its nix.sh
    # (singleUserNix, above).
    envExtra = lib.mkIf pkgs.stdenv.hostPlatform.isLinux ''
      if [[ -f /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh ]]; then
        . /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh
      ${singleUserNix}fi
    '';

    autocd = true;
    cdpath = [ "~/.local/share/src" ];
    enableCompletion = true;

    # Override home-manager's default `autoload -U compinit && compinit`.
    # The default rebuilds .zcompdump on every shell (~1s with this plugin set).
    # Glob qualifier (#qN.mh+24) matches when the dump is >24h old → full rebuild;
    # otherwise compinit -C skips the security audit. Run `rm ~/.zcompdump` to force.
    completionInit = ''
      autoload -U compinit
      if [[ -n ''${ZDOTDIR:-$HOME}/.zcompdump(#qN.mh+24) ]]; then
        compinit -d "''${ZDOTDIR:-$HOME}/.zcompdump"
      else
        compinit -C -d "''${ZDOTDIR:-$HOME}/.zcompdump"
      fi
    '';

    autosuggestion.enable = true;
    syntaxHighlighting.enable = true;

    # History settings
    history = {
      size = 50000;
      save = 50000;
      ignoreDups = true;
      ignoreAllDups = true;
      ignoreSpace = true;
      extended = true;
      share = true;
    };

    # Shell aliases - modern replacements
    shellAliases = {
      # Modern replacements that shadow classic tools (cat, ls, ...) are in
      # humanAliases above, defined only outside agent shells. The ones that
      # change what a pasted command does (grep, find, ps, du, df, diff, top,
      # htop) are the extras role's (modules/extras/shell.nix).
      # Note: sd, xh have different syntax than sed/curl - use directly

      # Git shortcuts
      g = "git";
      gs = "git status -sb";
      ga = "git add";
      gc = "git commit";
      gp = "git push";
      gl = "git pull";
      gd = "git diff";
      gco = "git checkout";
      gb = "git branch";
      glog = "git log --oneline --graph --decorate -10";
      lg = "lazygit";

      # Quick navigation
      ".." = "cd ..";
      "..." = "cd ../..";
      "...." = "cd ../../..";

      # Safety nets (rm/cp/mv -i) are in humanAliases above: human shells only.

      # Misc
      path = "echo $PATH | tr ':' '\n'";
      now = "date +'%Y-%m-%d %H:%M:%S'";
      week = "date +%V";
      ports = "lsof -i -P -n | grep LISTEN";

      # Quick edit configs
      zshrc = "$EDITOR ~/.zshrc";
      nixconf = "cd ${lib.escapeShellArg config.piste.checkout} && $EDITOR .";
    };

    plugins = [
      # Reminds you of aliases you should use
      {
        name = "you-should-use";
        src = pkgs.zsh-you-should-use;
        file = "share/zsh/plugins/you-should-use/you-should-use.plugin.zsh";
      }
      # Interactive git with fzf
      {
        name = "forgit";
        src = pkgs.zsh-forgit;
        file = "share/zsh/zsh-forgit/forgit.plugin.zsh";
      }
      # fzf-powered tab completion (fetched directly to avoid nixpkgs module build issues on macOS)
      {
        name = "fzf-tab";
        src = pkgs.fetchFromGitHub {
          owner = "Aloxaf";
          repo = "fzf-tab";
          rev = "24105b15714bfec37989ed5c5b6e60f572253019";
          hash = "sha256-YhTSu0P7mFlVx1zBvbT0jNstkamcZHhPYJHKMAHgyuM=";
        };
      }
      # Community completions for 200+ tools
      {
        name = "zsh-completions";
        src = pkgs.zsh-completions;
        file = "share/zsh-completions/zsh-completions.plugin.zsh";
      }
      # Nix-specific completions
      {
        name = "nix-zsh-completions";
        src = pkgs.nix-zsh-completions;
        file = "share/zsh/plugins/nix/nix-zsh-completions.plugin.zsh";
      }
    ];

    initContent = lib.mkMerge [
      (lib.mkBefore ''
        # Nix
        if [[ -f /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh ]]; then
          . /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh
          . /nix/var/nix/profiles/default/etc/profile.d/nix.sh
        ${singleUserNix}fi


        # History settings
        export HISTIGNORE="pwd:ls:cd:exit:clear"

        # Colors
        export CLICOLOR=1
        export LSCOLORS=GxFxCxDxBxegedabagaced

        # Fix Alacritty terminfo (fallback to xterm-256color if not found)
        if [[ "$TERM" == "alacritty" ]] && ! infocmp alacritty &>/dev/null; then
          export TERM=xterm-256color
        fi

        # Disable XON/XOFF flow control so Ctrl+s does not freeze the tty.
        # Required for Zellij's default Scroll-mode key (Ctrl+s after Ctrl+g).
        stty -ixon 2>/dev/null || true

        # Homebrew (macOS only)
        if [[ -f /opt/homebrew/bin/brew ]]; then
          eval "$(/opt/homebrew/bin/brew shellenv)"
        fi

        # FZF (fuzzy finder) - Ctrl+T for files, Alt+C for cd. Each tool's init
        # runs only where the tool is installed (the engine's package set has
        # it; a host may leave it out).
        if command -v fzf >/dev/null 2>&1; then
          eval "$(fzf --zsh)"
        fi

        # Atuin (better history) - Ctrl+R for history search
        if command -v atuin >/dev/null 2>&1; then
          eval "$(atuin init zsh --disable-up-arrow)"
        fi

        # Autosuggestion keybindings
        bindkey '^F' autosuggest-accept
        bindkey '^[[C' forward-char  # Right arrow for partial accept
      '')
      ''
        # === Modern replacements (humans only) ===
        # Skipped when Claude Code ($CLAUDECODE), Codex ($CODEX_*) or Paseo
        # ($PASEO_TERMINAL_ID / $PASEO_AGENT_ID) started this shell.
        if [[ -z "$CLAUDECODE" && -z "$PASEO_TERMINAL_ID" && -z "$PASEO_AGENT_ID" ]] \
          && (( ''${#''${(k)parameters[(I)CODEX_*]}} == 0 )); then
          ${lib.concatStringsSep "\n  " (lib.mapAttrsToList (n: v: "alias ${n}=${lib.escapeShellArg v}") humanAliases)}
        fi

        # === Tool Initializations ===

        # Zoxide (smarter cd); cd stays the builtin without it.
        if command -v zoxide >/dev/null 2>&1; then
          eval "$(zoxide init zsh)"
          alias cdi="zi"  # interactive
        fi

        # mise (polyglot runtime/version manager)
        if command -v mise &>/dev/null; then
          eval "$(mise activate zsh)"
        fi

        # FZF environment (keybinding init is in zvm_after_init)
        export FZF_DEFAULT_COMMAND='fd --type f --hidden --follow --exclude .git'
        export FZF_CTRL_T_COMMAND="$FZF_DEFAULT_COMMAND"
        export FZF_ALT_C_COMMAND='fd --type d --hidden --follow --exclude .git'
        export FZF_DEFAULT_OPTS='
          --height 40%
          --layout=reverse
          --border
          --info=inline
          --preview "bat --style=numbers --color=always --line-range :500 {} 2>/dev/null || echo {}"
          --preview-window=right:50%:wrap
          --bind "ctrl-/:toggle-preview"
          --color=bg+:#073642,bg:#002b36,spinner:#2aa198,hl:#268bd2
          --color=fg:#839496,header:#268bd2,info:#b58900,pointer:#2aa198
          --color=marker:#2aa198,fg+:#eee8d5,prompt:#b58900,hl+:#268bd2
        '

        # fzf-tab configuration
        zstyle ':fzf-tab:complete:cd:*' fzf-preview 'eza -1 --color=always --icons $realpath'
        zstyle ':fzf-tab:complete:ls:*' fzf-preview 'eza -1 --color=always --icons $realpath'
        zstyle ':fzf-tab:complete:*:*' fzf-preview 'bat --style=numbers --color=always --line-range :100 $realpath 2>/dev/null || eza -1 --color=always --icons $realpath 2>/dev/null || echo $realpath'
        zstyle ':fzf-tab:*' fzf-flags --height=40% --layout=reverse --border
        zstyle ':fzf-tab:*' use-fzf-default-opts yes
        zstyle ':fzf-tab:*' switch-group ',' '.'

        # Bat theme
        export BAT_THEME="Solarized (dark)"

        # === Cached completions for tools not in zsh-completions ===
        # Cache eval output keyed by binary mtime. Invalidates automatically
        # when nix switches the symlink to a new /nix/store path (newer mtime).
        _cached_eval_init() {
          local name="$1"; shift
          local bin
          bin="$(command -v "$1" 2>/dev/null)" || return 0
          local cache="''${XDG_CACHE_HOME:-$HOME/.cache}/zsh-init/$name.zsh"
          if [[ ! -s "$cache" ]] || [[ "$bin" -nt "$cache" ]]; then
            mkdir -p "''${cache:h}"
            "$@" > "$cache" 2>/dev/null
          fi
          source "$cache"
        }
        _cached_eval_init gh   gh   completion -s zsh
        _cached_eval_init pnpm pnpm completion zsh
        _cached_eval_init just just --completions zsh

        # === you-should-use configuration ===
        export YSU_MESSAGE_POSITION="after"

        # === Functions ===

        # Create and cd into directory
        mkcd() { mkdir -p "$1" && cd "$1"; }

        # Extract any archive
        extract() {
          if [ -f "$1" ]; then
            case "$1" in
              *.tar.bz2)   tar xjf "$1"     ;;
              *.tar.gz)    tar xzf "$1"     ;;
              *.tar.xz)    tar xJf "$1"     ;;
              *.bz2)       bunzip2 "$1"     ;;
              *.rar)       unrar x "$1"     ;;
              *.gz)        gunzip "$1"      ;;
              *.tar)       tar xf "$1"      ;;
              *.tbz2)      tar xjf "$1"     ;;
              *.tgz)       tar xzf "$1"     ;;
              *.zip)       unzip "$1"       ;;
              *.Z)         uncompress "$1"  ;;
              *.7z)        7z x "$1"        ;;
              *)           echo "'$1' cannot be extracted" ;;
            esac
          else
            echo "'$1' is not a valid file"
          fi
        }

        # Git clone and cd
        gclone() {
          git clone "$1" && cd "$(basename "$1" .git)"
        }

        # Find process by name
        psg() { procs | rg -i "$1"; }

        # Quick backup
        backup() { cp "$1" "$1.bak.$(date +%Y%m%d%H%M%S)"; }

        # Refresh the piste checkout and every overlay in it, then switch.
        #
        # Why this is not just a convenience: the piste CLI overrides the
        # engine input with the *checkout* (`piste where` prints it), so
        # `piste switch` builds whatever the checkout currently has checked
        # out. A checkout left on a merged branch silently ships stale shared
        # modules, and nothing in the build complains.
        #
        #   pisteup          refresh the repos, then piste switch
        #   pisteup -n       refresh only, no switch
        #
        # Refuses a repo rather than touching it when the tree is dirty or the
        # branch has unpushed commits; everything else is fast-forwarded onto
        # its default branch. Worktrees are left alone — they are deliberately
        # on their own branches. A checkout on a release tag is left to `piste
        # update`, so a push to main reaches nobody before a release.
        pisteup() {
          local switch=1
          [[ "$1" == "-n" || "$1" == "--no-switch" ]] && switch=0
          local failed=0 repo name branch default upstream unpushed tag

          for repo in ${lib.escapeShellArg config.piste.checkout} ${lib.escapeShellArg config.piste.checkout}/overlays/*(N/); do
            [[ -d "$repo/.git" ]] || continue
            name="''${repo##*/}"

            if [[ -z "$(git -C "$repo" branch --show-current)" ]]; then
              tag="$(git -C "$repo" tag --points-at HEAD | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | tail -n 1)"
              if [[ -n "$tag" ]]; then
                print -P "%F{green}skip%f  $name: on release $tag (piste update moves releases)"
                continue
              fi
            fi

            if [[ -n "$(git -C "$repo" status --porcelain)" ]]; then
              print -P "%F{yellow}skip%f  $name: uncommitted changes"
              failed=1; continue
            fi

            git -C "$repo" fetch -q --prune origin || { print -P "%F{red}fail%f  $name: fetch"; failed=1; continue; }

            # origin's HEAD, not a hardcoded "main".
            default="$(git -C "$repo" symbolic-ref -q --short refs/remotes/origin/HEAD)"
            default="''${default#origin/}"
            [[ -n "$default" ]] || default=main
            branch="$(git -C "$repo" branch --show-current)"

            # "Is this work safe to walk away from?" — compared against the
            # branch's own upstream, not the default branch. A squash-merged
            # branch's commits never appear on main, so counting against main
            # would flag every just-merged branch as unpushed.
            upstream="$(git -C "$repo" rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)"
            if [[ -n "$upstream" ]]; then
              unpushed="$(git -C "$repo" rev-list --count "$upstream..HEAD" 2>/dev/null || echo 1)"
            elif git -C "$repo" merge-base --is-ancestor HEAD "origin/$default" 2>/dev/null; then
              unpushed=0   # no upstream, but the remote already has this commit
            else
              unpushed=1   # no upstream and not on the remote: do not touch it
            fi
            if [[ "$branch" != "$default" && "$unpushed" != 0 ]]; then
              print -P "%F{yellow}skip%f  $name: $branch has work not on the remote"
              failed=1; continue
            fi

            if [[ "$branch" != "$default" ]]; then
              git -C "$repo" checkout -q "$default" || { print -P "%F{red}fail%f  $name: checkout $default"; failed=1; continue; }
              print -P "%F{cyan}moved%f $name: $branch -> $default"
            fi

            if git -C "$repo" merge -q --ff-only "origin/$default" 2>/dev/null; then
              print -P "%F{green}ok%f    $name: $default @ $(git -C "$repo" rev-parse --short HEAD)"
            else
              print -P "%F{red}fail%f  $name: cannot fast-forward $default"
              failed=1
            fi
          done

          if (( failed )); then
            print -P "%F{red}Not switching:%f some repos were skipped. Resolve them, or: pisteup -n"
            return 1
          fi
          (( switch )) || return 0
          piste switch
        }

        # Update tldr cache on first use (silently, no job control noise)
        if [[ ! -d ~/.cache/tealdeer ]]; then
          (tldr --update &>/dev/null &) &>/dev/null
        fi

        # pnpm shortcuts
        alias pn=pnpm
        alias px=pnpx

        # VSCode shell integration
        [[ "$TERM_PROGRAM" == "vscode" ]] && . "$(code --locate-shell-integration-path zsh)"

        # Welcome message (only once per session, not in tmux or zellij)
        if [[ -o interactive ]] && [[ -z "$TMUX" ]] && [[ -z "$ZELLIJ" ]] && [[ -z "$_WELCOME_SHOWN" ]]; then
          export _WELCOME_SHOWN=1
          echo ""
          echo "  \033[1;34m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
          echo "  \033[1;36m  Modern CLI Toolkit\033[0m"
          echo "  \033[1;34m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
          echo ""
          echo "  \033[1;33mNavigation\033[0m     \033[0;37mz <dir>\033[0m jump  \033[0;37mzi\033[0m interactive  \033[0;37mbroot\033[0m explore"
          echo "  \033[1;33mFiles\033[0m          \033[0;37mll\033[0m list  \033[0;37mlt\033[0m tree  \033[0;37mbat\033[0m view  \033[0;37mrg\033[0m search"
          echo "  \033[1;33mGit\033[0m            \033[0;37mlg\033[0m lazygit  \033[0;37mgs\033[0m status  \033[0;37mglog\033[0m graph"
          echo "  \033[1;33mSystem\033[0m         \033[0;37mbtm\033[0m monitor  \033[0;37mduf\033[0m disks  \033[0;37mprocs\033[0m processes"
          echo "  \033[1;33mHistory\033[0m        \033[0;37mCtrl+R\033[0m search  \033[0;37mCtrl+T\033[0m files  \033[0;37mtldr\033[0m help"
          echo "  \033[1;33mZellij\033[0m         \033[0;37mCtrl+a\033[0m leader  \033[0;37mp\033[0m panes  \033[0;37mt\033[0m tabs  \033[0;37ms\033[0m scroll"
          echo ""
          echo "  \033[0;90mRun \033[0;37mhelp-cli\033[0;90m for full command reference\033[0m"
          echo ""
        fi

        # Full help command
        help-cli() {
          echo ""
          echo "\033[1;34m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
          echo "\033[1;36m  CLI Quick Reference\033[0m"
          echo "\033[1;34m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
          echo ""
          echo "\033[1;33m  NAVIGATION & FILES\033[0m"
          echo "    z <dir>      Smart cd (learns your habits)    zi           Interactive directory picker"
          echo "    ll           List files with icons + git      la           List all including hidden"
          echo "    lt           Tree view (2 levels)             tree         Full tree view"
          echo "    broot        Interactive file explorer        fd <pat>     Find files by name"
          echo ""
          echo "\033[1;33m  VIEWING & SEARCHING\033[0m"
          echo "    bat <file>   Cat with syntax highlighting     rg <pat>     Ripgrep search"
          echo "    jless <f>    JSON viewer                      hexyl <f>    Hex viewer"
          echo "    tldr <cmd>   Simplified man pages             tokei        Code statistics"
          echo ""
          echo "\033[1;33m  GIT\033[0m"
          echo "    lg           Lazygit TUI                      gs           Status (short)"
          echo "    ga           Add                              gc           Commit"
          echo "    gp           Push                             gl           Pull"
          echo "    gd           Diff                             glog         Log graph"
          echo "    git wip      Quick WIP commit                 git undo     Soft reset last commit"
          echo ""
          echo "\033[1;33m  GIT (forgit - fzf-powered)\033[0m"
          echo "    forgit::log  Interactive log                  forgit::diff   Interactive diff"
          echo "    forgit::add  Interactive staging              forgit::stash  Interactive stash"
          echo "    ga           Alias for forgit::add            glo            Alias for forgit::log"
          echo ""
          echo "\033[1;33m  SYSTEM & NETWORK\033[0m"
          echo "    btm          System monitor (bottom)          procs        Process list"
          echo "    duf          Disk usage                       dust <dir>   Directory size"
          echo "    bandwhich    Network by process               xh <url>     HTTP requests"
          echo ""
          echo "\033[1;33m  UTILITIES\033[0m"
          echo "    sd 's' 'r'   Find/replace in files            hyperfine    Benchmark commands"
          echo "    extract <f>  Extract any archive              mkcd <dir>   Create and cd"
          echo "    backup <f>   Create timestamped backup        zellij       Terminal multiplexer"
          echo ""
          echo "\033[1;33m  FUZZY FINDING (fzf)\033[0m"
          echo "    Ctrl+R       Search command history           Ctrl+T       Find files"
          echo "    Alt+C        cd into directory                Tab          fzf-powered completion"
          echo ""
          echo "\033[1;33m  ZELLIJ\033[0m"
          echo "    Ctrl+a       Leader key (unlock controls)     Esc          Lock (passthrough)"
          echo "    p            Pane mode                        t            Tab mode"
          echo "    s            Scroll mode                      o            Session mode"
          echo ""
          echo "\033[1;33m  NIX\033[0m"
          echo "    piste switch    Build & apply config             piste build     Build only"
          echo "    piste check     Syntax check                     piste rollback  Revert generation"
          echo ""
          echo "\033[1;34m━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\033[0m"
          echo ""
        }
      ''
    ];
  };
}
