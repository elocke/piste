{
  config,
  pkgs,
  lib,
  ...
}:

let
  cfg = config.piste.programs.vscode;
  m = pkgs.vscode-marketplace;

  coreExtensions = [
    # Nix
    m.bbenoist.nix
    m.jnoortheen.nix-ide

    # Editor / formatting / spelling
    m.editorconfig.editorconfig
    m.esbenp.prettier-vscode
    m.dbaeumer.vscode-eslint
    m.mikestead.dotenv
    m.mechatroner.rainbow-csv
    m.janisdd.vscode-edit-csv
    m.usernamehw.errorlens
    m.streetsidesoftware.code-spell-checker
    m.davidanson.vscode-markdownlint
    m.yzhang.markdown-all-in-one
    m.shd101wyy.markdown-preview-enhanced
    m.tombonnike.vscode-status-bar-format-toggle

    # Theme (referenced by workbench.colorTheme)
    m.ryanolsonx.solarized

    # Diff viewer (referenced by semanticdiff.defaultDiffViewer)
    m.semanticdiff.semanticdiff

    # GitHub
    m.github.vscode-pull-request-github
    m.github.vscode-github-actions
    m.github.copilot-chat

    # Containers
    m.ms-azuretools.vscode-docker
    m.ms-azuretools.vscode-containers

    # Python / Jupyter
    m.ms-python.python
    m.ms-python.vscode-pylance
    m.ms-python.debugpy
    m.ms-python.vscode-python-envs
    m.charliermarsh.ruff
    m.ms-toolsai.jupyter
    m.ms-toolsai.jupyter-keymap
    m.ms-toolsai.jupyter-renderers
    m.ms-toolsai.vscode-jupyter-cell-tags
    m.ms-toolsai.vscode-jupyter-slideshow

    # Remote
    m.ms-vscode-remote.remote-ssh
    m.ms-vscode-remote.remote-ssh-edit
    m.ms-vscode-remote.remote-containers
    m.ms-vscode.remote-explorer

    # AI assistants
    m.anthropic.claude-code
    m.supermaven.supermaven

    # Web / JS
    m.bradlc.vscode-tailwindcss
    m.christian-kohler.npm-intellisense
    m.firefox-devtools.vscode-firefox-debug

    # Languages
    m.golang.go
    m.redhat.vscode-xml

    # Utilities
    m.humao.rest-client
    m.ms-vscode.makefile-tools
    m.tomoki1207.pdf
    m.alefragnani.project-manager
    m.jetpack-io.devbox
  ];
in
{
  options.piste.programs.vscode = {
    enable = lib.mkEnableOption "VS Code with shared settings, keybindings, and core extensions";

    extraExtensions = lib.mkOption {
      type = lib.types.listOf lib.types.package;
      default = [ ];
      description = "Extensions to add on top of the shared core set (host-specific opt-ins).";
      example = lib.literalExpression ''
        with pkgs.vscode-marketplace; [ vue.volar nuxt.mdc ]
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    programs.vscode = {
      enable = true;
      mutableExtensionsDir = true;

      profiles.default = {
        extensions = coreExtensions ++ cfg.extraExtensions;

        userSettings = {
          # Update / extension management — Nix manages VS Code and the
          # declared extensions, so suppress the in-app update nags.
          "update.mode" = "none";
          "extensions.autoCheckUpdates" = false;
          "extensions.autoUpdate" = false;

          # Telemetry
          "telemetry.telemetryLevel" = "off";

          # Files
          "files.trimTrailingWhitespace" = true;
          "files.insertFinalNewline" = true;
          "files.trimFinalNewlines" = true;

          # Terminal
          "terminal.integrated.defaultProfile.osx" = "zsh";
          "terminal.integrated.scrollback" = 5000;
          "terminal.integrated.persistentSessionScrollback" = 1000;
          "terminal.integrated.fontLigatures.enabled" = true;

          # Editor
          "editor.fontFamily" =
            "'FiraCode Nerd Font', 'JetBrainsMono Nerd Font', 'JetBrainsMono Nerd Font Mono', 'Symbols Nerd Font Mono'";
          "editor.fontLigatures" = true;
          "editor.formatOnPaste" = true;
          "editor.formatOnSave" = true;
          "editor.formatOnType" = true;
          "editor.gotoLocation.multipleDefinitions" = "goto";
          "editor.bracketPairColorization.enabled" = true;
          "editor.guides.bracketPairs" = "active";

          # Workbench / theme
          "workbench.editor.enablePreview" = false;
          "workbench.colorTheme" = "Solarized Dark+";
          "workbench.colorCustomizations" = {
            "[Solarized Dark+]" = {
              "diffEditor.insertedTextBackground" = "#85990040";
              "diffEditor.removedTextBackground" = "#dc322f40";
              "diffEditor.insertedLineBackground" = "#85990020";
              "diffEditor.removedLineBackground" = "#dc322f20";
              "diffEditorGutter.insertedLineBackground" = "#85990050";
              "diffEditorGutter.removedLineBackground" = "#dc322f50";
              "diffEditorOverview.insertedForeground" = "#859900aa";
              "diffEditorOverview.removedForeground" = "#dc322faa";
              "diffEditor.diagonalFill" = "#073642";
            };
          };

          # Per-language formatter routing — pairs with editor.formatOnSave
          # to run the right tool for each language. Languages without an
          # explicit override fall back to whatever the language extension
          # registers as default.
          "[json]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[jsonc]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[yaml]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[html]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[css]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[scss]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[javascript]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[javascriptreact]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[typescript]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[typescriptreact]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[vue]" = {
            "editor.defaultFormatter" = "esbenp.prettier-vscode";
          };
          "[markdown]" = {
            "editor.defaultFormatter" = "yzhang.markdown-all-in-one";
          };
          "[nix]" = {
            "editor.defaultFormatter" = "jnoortheen.nix-ide";
          };
          "[python]" = {
            "editor.defaultFormatter" = "charliermarsh.ruff";
            "editor.codeActionsOnSave" = {
              "source.fixAll.ruff" = "explicit";
              "source.organizeImports.ruff" = "explicit";
            };
          };

          # Diff viewer
          "semanticdiff.defaultDiffViewer" = true;
          "diffEditor.ignoreTrimWhitespace" = false;

          # Git
          "git.autofetch" = true;
          "git.confirmSync" = false;

          # Claude Code extension
          "claudeCode.selectedModel" = "default";
          "claudeCode.preferredLocation" = "panel";

          # Python
          "python.analysis.typeCheckingMode" = "standard";
          # python.defaultInterpreterPath is left unset: the extension
          # auto-detects, or set it per workspace.

          # Misc
          "makefile.configureOnOpen" = false;
          "explorer.confirmDragAndDrop" = false;
          "xml.server.vmargs" = "-Xmx1024M";
        };

        keybindings = [
          {
            key = "shift+enter";
            command = "workbench.action.terminal.sendSequence";
            args = {
              text = "\\\r\n";
            };
            when = "terminalFocus";
          }
        ];
      };
    };
  };
}
