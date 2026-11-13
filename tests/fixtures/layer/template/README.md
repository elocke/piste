# @user@'s overlay on the team layer

The team's setup comes from the layer (the `layer` input); this repo holds
what's yours: your hosts, your identity and your secrets' values.

- `piste switch` applies it; `piste update` moves the engine and the layer.
- Your own settings go in `hosts/`. A plain value of yours wins over the
  layer's; your list entries (packages, casks) go beside the team's.
- Secrets: the layer names them, in `secrets/env/api-keys.sops.yaml`.
  - If you set up sops when you started, `piste secrets edit
    secrets/env/api-keys.sops.yaml` fills in the values.
  - If not, run `piste secrets init` once, then that same `piste secrets edit`.
    It encrypts the file before opening it. Then set
    `piste.secrets.enable = true` in your host file,
    `git add .sops.yaml hosts/ secrets/` and `piste switch`.
- After a switch, `piste agents sync --user` and `piste mcp sync` bring the
  team's plugins and MCP servers to Claude Code and Codex.
