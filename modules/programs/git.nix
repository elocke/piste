{ config, pkgs, lib, ... }:

let
  cfg = config.programs.git;
  # A key to sign with: home-manager's signing.key, or user.signingkey set in
  # settings directly.
  user = if lib.isAttrs cfg.settings then cfg.settings.user or { } else { };
  hasKey = cfg.signing.key != null || (user.signingkey or user.signingKey or null) != null;
in
{
  # The piece's handle: disabledModules = [ { key = <this>; } ] drops it (its path doesn't).
  key = "pack:core/git";

  # Sign commits by default only with a key to sign with: without one, a
  # newcomer's first `git commit` fails ("No secret key"). In iniContent, not
  # settings, so reading settings above can't loop; a person's own
  # settings.commit.gpgsign wins, and signing.signByDefault, when set, is
  # home-manager's to apply (its commit.gpgSign) alone.
  programs.git.iniContent = lib.mkIf (cfg.signing.signByDefault == null) {
    commit.gpgsign = lib.mkDefault hasKey;
  };

  programs.git = {
    enable = true;
    lfs.enable = true;

    ignores = [
      # OS
      ".DS_Store"
      "Thumbs.db"

      # Editors
      "*.swp"
      "*.swo"
      "*~"
      "*.sublime-*"

      # Build artifacts
      "*.o"
      "*.pyc"
      "__pycache__/"
      "node_modules/"
      ".direnv/"
      "result"
      "result-*"

      # Environment
      ".env"
      ".env.local"
    ];

    settings = {
      init.defaultBranch = "main";

      core = {
        autocrlf = "input";
        whitespace = "trailing-space,space-before-tab";
        pager = "delta";
      };

      # Delta - beautiful diffs
      interactive.diffFilter = "delta --color-only";
      delta = {
        navigate = true;
        light = false;
        side-by-side = true;
        line-numbers = true;
        syntax-theme = "Solarized (dark)";
        plus-style = "syntax #003d1a";
        minus-style = "syntax #3d1a1a";
        decorations = {
          commit-decoration-style = "blue ol";
          commit-style = "raw";
          file-style = "omit";
          hunk-header-decoration-style = "blue box";
          hunk-header-file-style = "red";
          hunk-header-line-number-style = "#067a00";
          hunk-header-style = "file line-number syntax";
        };
      };

      merge = {
        conflictstyle = "diff3";
        tool = "vimdiff";
      };

      diff = {
        colorMoved = "default";
        algorithm = "histogram";
      };

      pull.rebase = lib.mkDefault true;
      push.autoSetupRemote = true;
      rebase.autoStash = true;
      rerere.enabled = true;  # Remember conflict resolutions

      fetch.prune = true;

      # Better branch sorting
      branch.sort = "-committerdate";
      tag.sort = "-version:refname";

      # URL shortcuts
      url = {
        "git@github.com:" = {
          insteadOf = "gh:";
        };
      };

      # Colors
      color = {
        ui = "auto";
        branch = {
          current = "yellow bold";
          local = "green";
          remote = "cyan";
        };
        status = {
          added = "green";
          changed = "yellow";
          untracked = "red";
        };
      };

      # Aliases
      alias = {
        # Status & Info
        s = "status -sb";
        st = "status";
        info = "remote show origin";

        # Branching
        br = "branch";
        bra = "branch -a";
        brd = "branch -d";
        brD = "branch -D";
        sw = "switch";
        swc = "switch -c";

        # Commits
        ci = "commit";
        cia = "commit --amend";
        cian = "commit --amend --no-edit";
        cm = "commit -m";

        # Checkout
        co = "checkout";
        cob = "checkout -b";

        # Diff
        d = "diff";
        ds = "diff --staged";
        dw = "diff --word-diff";

        # Adding
        a = "add";
        aa = "add --all";
        ap = "add --patch";

        # Push/Pull
        p = "push";
        pf = "push --force-with-lease";
        pl = "pull";
        f = "fetch --all --prune";

        # Stash
        ss = "stash";
        sp = "stash pop";
        sl = "stash list";
        sd = "stash drop";

        # Log - pretty formats
        l = "log --oneline -20";
        lg = "log --graph --oneline --decorate -20";
        lga = "log --graph --oneline --decorate --all";
        ll = "log --pretty=format:'%C(yellow)%h%Creset %s %Cgreen(%cr) %C(bold blue)<%an>%Creset' --abbrev-commit";
        hist = "log --pretty=format:'%h %ad | %s%d [%an]' --graph --date=short";
        week = "log --since='1 week ago' --oneline";

        # Undo
        unstage = "reset HEAD --";
        undo = "reset --soft HEAD~1";

        # Utils
        aliases = "config --get-regexp alias";
        whoami = "config user.email";
        root = "rev-parse --show-toplevel";
        last = "log -1 HEAD --stat";
        contributors = "shortlog -sne";
        tags = "tag -l --sort=-version:refname";
        branches = "branch -a --sort=-committerdate";
        remotes = "remote -v";

        # Interactive
        ri = "rebase -i";
        rc = "rebase --continue";
        ra = "rebase --abort";

        # Worktrees
        wt = "worktree";
        wta = "worktree add";
        wtl = "worktree list";
        wtr = "worktree remove";

        # Quick fixup
        fixup = "!f() { git commit --fixup=$1 && git rebase -i --autosquash $1~1; }; f";

        # Sync fork with upstream
        sync = "!git fetch upstream && git rebase upstream/main";

        # Clean merged branches
        cleanup = "!git branch --merged | grep -v '\\*\\|main\\|master' | xargs -n 1 git branch -d";

        # WIP commits
        wip = "!git add -A && git commit -m 'WIP'";
        unwip = "!git log -1 --format='%s' | grep -q 'WIP' && git reset HEAD~1";
      };
    };

  };

  # GitHub CLI. gh rewrites config.yml itself (`gh auth login`, `gh config
  # set`), so on hosts it is a mutable file seeded once from these settings,
  # not a home-manager link. (Linking it too made every switch find the
  # seeded file "in the way" and move it to config.yml.backup-<epoch>.)
  # The container role turns the link back on: the image has no activation
  # and copies it as a mutable file instead (roles/container.nix).
  programs.gh = {
    enable = true;
    settings = {
      git_protocol = "ssh";
      prompt = "enabled";
      aliases = {
        co = "pr checkout";
        pv = "pr view";
        pc = "pr create";
        pl = "pr list";
        il = "issue list";
        ic = "issue create";
        iv = "issue view";
        rv = "repo view --web";
      };
    };
  };
  xdg.configFile."gh/config.yml".enable = lib.mkDefault false;

  # Seed the writable config.yml from the generated one when it is missing or
  # still a store symlink; never overwrite what gh has written since.
  home.activation.setupGhConfig = lib.hm.dag.entryAfter [ "writeBoundary" ] ''
    GH_CONFIG="${config.xdg.configHome}/gh/config.yml"
    mkdir -p "$(dirname "$GH_CONFIG")"
    if [ -L "$GH_CONFIG" ] || [ ! -f "$GH_CONFIG" ]; then
      run rm -f "$GH_CONFIG"
      run install -m 0644 ${config.xdg.configFile."gh/config.yml".source} "$GH_CONFIG"
    fi
  '';
}
