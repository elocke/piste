# CLAUDE.md

@AGENTS.md

## Claude Code only

- The piste-* skills reach Claude through `.claude/skills/<n>` → `.agents/skills/<n>`
  (`/piste-fix`, `/piste-config`). Start sessions at the checkout root, so these
  and the overlays' skills are all in reach.
- `piste claude` launches Claude Code at the checkout root, with each overlay in `overlays/`
  added as an extra directory.
- `.claude/settings.json` denies `piste switch`, `rollback`, `update`, `host`, `adopt`,
  `secrets`, `github` and `clean`, plus `sops`, `darwin-rebuild`, `home-manager switch`,
  `sudo` and `git push`, for any session started here (`piste fix` included). The person
  runs those from their own shell. Add to the list there, not in a command's arguments.
