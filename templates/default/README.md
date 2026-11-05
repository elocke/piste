# @user@'s piste overlay

My machines, built with [piste](https://github.com/elocke/piste): this repo
holds what is mine (hosts, packages, settings, agent content); the engine
holds the shared modules.

It lives in a piste checkout at `overlays/<name>/`. From there:

```sh
piste switch          # build and apply this machine
piste build           # build only
piste adopt           # add what's installed outside Nix to this machine's host file
```

Zone: `@zone@`. What's yours on every machine is in `home.nix`; each machine's
own settings are in `hosts/`, and its entry in `flake.nix` (`hosts`). To add a
package, put it in a list in the machine's host file (`home.packages` on Linux;
`piste.packages.extraPackages`, `casks.extra` or `brews.extra` on a Mac), or
in `home.packages` in `home.nix` for every machine; the checkout's
`docs/customising.md` has the rest.
Agent skills, instructions and presets are in `agents/`.

## On a new machine

1. If this overlay keeps secrets (`.sops.yaml`), copy your age key to
   `~/.config/sops/age/keys.txt`. Or don't: then this machine gets a key of
   its own, and a machine that can read the secrets runs
   `piste secrets updatekeys` for it (setup prints the steps).
2. Run `nix run github:elocke/piste/stable -- setup <this repo's url>`. When
   this overlay has no host for the machine, setup offers to add it.
