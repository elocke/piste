---
targets: ["claude", "codex"]
order: 15
---
## This machine is set up by piste

- Its packages, shell, git and app settings come from the person's overlay, a git repo under the piste checkout (`piste where` prints the checkout). Change them there, in `home.nix`, not on the machine: the next `piste switch` would undo it.
- Don't use `brew install`, `npm install -g`, `pip install --user` or edits to `~/.zshrc` or `~/.gitconfig` for something that should stay. Add the package or setting to the overlay.
- Edit the overlay and run `piste build` to check it; the person runs `piste switch` and `piste update` from their own shell. Don't run them.
