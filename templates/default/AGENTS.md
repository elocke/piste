# AGENTS.md

This repo is a piste overlay: @user@'s machines. It lives in `overlays/` of a
piste checkout and builds against that checkout's engine.

- First read the checkout's `AGENTS.md` and its skills in `.agents/skills/`
  (both two directories up): piste-config, piste-fix.
- What's on every machine is in `home.nix`; one machine's, in its `hosts/` file.
- Edit only this repo. `git add -N` a new file: builds see tracked files only.
- Check a change with `piste build` (`piste build --host <name>` for another machine).
- Never run `piste switch`, `piste update`, `git commit` or `git push`: @user@ does.
- Never put a secret's value in a .nix file (the Nix store is readable by every user on the
  machine); secrets live encrypted in `secrets/`. An exported variable reaches every process.
