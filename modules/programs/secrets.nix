{ config, lib, ... }:

let
  cfg = config.piste.secrets;
in
{
  options.piste.secrets = {
    enable = lib.mkEnableOption "shell environment secret injection via sops-nix";

    sopsFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = "Path to SOPS-encrypted YAML file containing secrets (set by overlay repos).";
    };

    ageKeyFile = lib.mkOption {
      type = lib.types.str;
      default = "${config.home.homeDirectory}/.config/sops/age/keys.txt";
      description = ''
        Path to age key file for SOPS decryption.
        Defaults to ~/.config/sops/age/keys.txt.
      '';
    };

    envFileName = lib.mkOption {
      type = lib.types.str;
      default = "piste-secrets.env";
      description = ''
        Name of the rendered `KEY=value` env file (a sops template) holding
        every `envVars` entry. Consume it from a systemd user unit with
        `EnvironmentFile = config.piste.secrets.envFilePath;` so that
        non-interactive, service-launched processes get the same secrets the
        interactive zsh exports give you.
      '';
    };

    envFilePath = lib.mkOption {
      type = lib.types.str;
      readOnly = true;
      default = config.sops.templates.${cfg.envFileName}.path or "";
      defaultText = lib.literalExpression ''config.sops.templates.''${cfg.envFileName}.path'';
      description = "Path to the rendered env file. Empty when `envVars` is empty.";
    };

    envVars = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = {};
      description = ''
        Map of environment variable names to SOPS key paths.
        Each entry creates a sops.secrets declaration and a shell export.
      '';
      example = lib.literalExpression ''
        {
          ANTHROPIC_API_KEY = "anthropic/api_key";
          OPENROUTER_API_KEY = "openrouter/api_key";
        }
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    sops.defaultSopsFile = cfg.sopsFile;
    sops.age.keyFile = cfg.ageKeyFile;

    # Point interactive sops / `piste secrets` at the same key. On macOS sops
    # defaults to ~/Library/Application Support/sops/age/keys.txt, so without
    # this bare `sops` can't find the key living under ~/.config.
    home.sessionVariables.SOPS_AGE_KEY_FILE = cfg.ageKeyFile;
    sops.secrets = lib.mapAttrs (_envName: sopsKey: {
      key = sopsKey;
    }) cfg.envVars;

    # Same secrets, rendered once as a systemd-readable `KEY=value` file.
    # The zsh exports below only reach interactive shells; anything started by
    # the systemd user manager (a launcher -> claude -> MCP servers) inherits
    # nothing from .zshrc, so services point EnvironmentFile at this instead.
    # Rendered outside the nix store (runtime dir, 0400) — never world-readable.
    sops.templates = lib.mkIf (cfg.envVars != {}) {
      ${cfg.envFileName} = {
        mode = "0400";
        content = lib.concatStringsSep "\n" (
          lib.mapAttrsToList (envName: _sopsKey:
            "${envName}=${config.sops.placeholder.${envName}}"
          ) cfg.envVars
        );
      };
    };

    programs.zsh.initContent = lib.mkBefore (
      "# Secrets injected by piste.secrets (sops-nix)\n"
      + lib.concatStringsSep "\n" (
        lib.mapAttrsToList (envName: _sopsKey:
          ''export ${envName}="$(cat ${config.sops.secrets.${envName}.path})"''
        ) cfg.envVars
      )
    );
  };
}
