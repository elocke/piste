# A GitHub account on this machine, over SSH: one key per account, named
# ~/.ssh/id_ed25519_github_<login>, for SSH auth and for signing commits, with
# the account's noreply address as the git email. `piste github` writes the
# `piste.github` block into a host file; this module reads it. The login and
# id are a person's: they live in the overlay, never in the engine.
{ config, lib, ... }:

let
  cfg = config.piste.github;
  on = cfg.login != null;
  git = config.programs.git;
  allowedSigners = "${config.home.homeDirectory}/.config/git/allowed_signers";
  signersFile = git.settings.gpg.ssh.allowedSignersFile or allowedSigners;
  email = git.settings.user.email or null;
in
{
  options.piste.github = {
    login = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "jane";
      description = "The GitHub account's login. When set, github.com uses its key over SSH, commits are signed with it, and (with `id`) the git email is the account's noreply address.";
    };
    id = lib.mkOption {
      type = lib.types.nullOr lib.types.int;
      default = null;
      example = 12345;
      description = "The account's numeric user id (`gh api user --jq .id`). With `login`, the git email is `<id>+<login>@users.noreply.github.com` unless `programs.git.settings.user.email` is set.";
    };
    key = lib.mkOption {
      type = lib.types.str;
      default = "${config.home.homeDirectory}/.ssh/id_ed25519_github_${if on then cfg.login else "<login>"}";
      defaultText = lib.literalExpression ''"''${config.home.homeDirectory}/.ssh/id_ed25519_github_<login>"'';
      description = "The private key for this account (its `.pub` is the signing key). `piste github` makes it.";
    };
  };

  config = lib.mkIf on {
    # github.com uses this account's key and no other (only with a login: an
    # engine-wide block pinned ~/.ssh/id_ed25519, which broke a push for
    # anyone whose key lives elsewhere).
    programs.ssh.settings."github.com" = {
      HostName = lib.mkDefault "github.com";
      IdentitiesOnly = lib.mkDefault true;
      IdentityFile = lib.mkDefault [ cfg.key ];
    };
    programs.git = {
      signing = {
        key = lib.mkDefault "${cfg.key}.pub";
        # home-manager's default is openpgp for an older stateVersion.
        format = lib.mkDefault "ssh";
      };
      settings.user.email = lib.mkIf (cfg.id != null)
        (lib.mkDefault "${toString cfg.id}+${cfg.login}@users.noreply.github.com");
      # So `git log --show-signature` checks this machine's own commits.
      settings.gpg.ssh.allowedSignersFile = lib.mkDefault allowedSigners;
    };

    # allowed_signers pairs the final git email with the signing key. Written at
    # activation, reading the .pub then, so the build never reads ~/.ssh.
    home.activation.githubAllowedSigners = lib.mkIf (email != null)
      (lib.hm.dag.entryAfter [ "writeBoundary" ] ''
        ALLOWED=${lib.escapeShellArg signersFile}
        PUB=${lib.escapeShellArg git.signing.key}
        if [ -f "$PUB" ]; then
          mkdir -p "$(dirname "$ALLOWED")"
          printf '%s %s\n' ${lib.escapeShellArg email} "$(cut -d' ' -f1-2 "$PUB")" > "$ALLOWED"
        fi
      '');
  };
}
