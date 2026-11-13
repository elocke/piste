# What a host declares, for `piste drift` and `piste adopt`
# (cli/internal/drift, Declared): applied to the names found installed
# outside Nix, then to the host's evaluated configuration (a nix-darwin
# system or a home-manager configuration; each has config, options and
# pkgs). checks.template-instance runs it on the darwin instance.
#
# Go sends this on one line: full-line comments only, no '' strings.
names: host:
let
  inherit (host) config options pkgs;
  lib = pkgs.lib;
  getNames = map lib.getName;
  # Every Nix package: the system's, each home-manager user's (on a Mac),
  # or the home's (standalone home-manager).
  nix =
    getNames (config.environment.systemPackages or [ ])
    ++ lib.concatMap (u: getNames u.home.packages) (lib.attrValues (config.home-manager.users or { }))
    ++ getNames (config.home.packages or [ ]);
  # Homebrew's entries are attrsets.
  casks = map (c: c.name) (config.homebrew.casks or [ ]);
  brews = map (b: b.name) (config.homebrew.brews or [ ]);
  # Empty when piste.packages isn't imported.
  exclude =
    (config.piste.packages.exclude or [ ])
    ++ (config.piste.packages.casks.exclude or [ ])
    ++ (config.piste.packages.brews.exclude or [ ]);
  # Where each list adopt writes to is defined, for the options that exist.
  listOptions = [
    "piste.packages.extraPackages"
    "environment.systemPackages"
    "home.packages"
    "piste.packages.casks.extra"
    "homebrew.casks"
    "piste.packages.brews.extra"
    "homebrew.brews"
  ];
  option = path: lib.attrByPath (lib.splitString "." path) null options;
  files = lib.listToAttrs (
    lib.concatMap (
      path:
      let
        o = option path;
      in
      lib.optional (o != null && lib.isOption o) {
        name = path;
        value = o.files;
      }
    ) listOptions
  );
  # A name is a package here when pkgs.<name> is a derivation available on
  # this host's platform. A removed alias throws: tryEval.
  lookup =
    n:
    let
      p = pkgs.${n};
      r = builtins.tryEval (
        if lib.isDerivation p && lib.meta.availableOn pkgs.stdenv.hostPlatform p then
          let
            v = {
              pname = lib.getName p;
              version = lib.getVersion p;
            };
          in
          builtins.deepSeq v v
        else
          null
      );
    in
    if pkgs ? ${n} && r.success then r.value else null;
  found = lib.filterAttrs (_: v: v != null) (lib.genAttrs names lookup);
in
{
  inherit nix casks brews exclude files;
  pkgs = found;
}
