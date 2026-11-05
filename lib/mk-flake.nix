# piste.lib.mkFlake: an overlay's flake outputs from a short description.
# The paved road, not the only one: the modules it
# wires are the same darwinModules / homeManagerModules an overlay can wire
# by hand.
#
#   piste.lib.mkFlake {
#     inherit inputs self;            # the overlay's flake inputs and itself
#     user = "jane.doe";              # login name on every host
#     zone = "work";                  # the hosts' trust zone
#     hosts = {
#       MB2222 = { system = "aarch64-darwin"; modules = [ ./hosts/mac.nix ]; aliases = [ ]; };
#       "jane@devbox" = { system = "x86_64-linux"; roles = [ "dev" ]; modules = [ ./hosts/devbox.nix ]; };
#     };                              # each may also set overlays (its nixpkgs overlays)
#     extraSpecialArgs = system: { }; # module args for every host on `system`
#     homebrew = { extraTaps = { }; };  # merged into piste.homebrew
#     packages = system: { };         # the overlay's own packages
#     agents = ./agents;              # the zone's agent root: source "<zone>-agents"
#     bundle.presets = [ … ];         # the zone's container agent bundle (needs agents)
#     devcontainer = { name; modules ? [ ]; extraSpecialArgs ? (system: { }); };
#     checks = system: { };           # the overlay's own checks (its policy)
#     systems = [ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ];  # the default
#   }
#   => { darwinConfigurations; homeConfigurations; packages; apps; checks; }
#
# extraSpecialArgs, packages, checks and devcontainer.extraSpecialArgs each
# take a function of the system or one attrset for every system.
#
# A host has system and, each optional, modules, overlays, roles and aliases.
# A role is a name in piste.homeManagerModules (base, dev, desktop, container,
# extras); a *-darwin host that lists one with a darwinModules entry of the
# same name (extras) gets that too.
# A *-darwin host is a nix-darwin system (plus one per alias); a *-linux host
# is a standalone home-manager configuration, its name being "user@host", and
# has no aliases. mkFlake throws "mkFlake: …" on any other host key or
# system, on aliases on a *-linux host, and on an alias that is a host's
# name or is given twice.
#
# What mkFlake adds to every host (its wiring), so a host's own modules
# bring none of it:
#   - the sops home module (sops-nix's, piste.homeManagerModules.sops: the
#     dev and desktop roles set its options) and piste.homeManagerModules.agents
#     in the home (a Mac's home-manager.users.<user>.imports);
#   - the piste CLI: piste.packages.piste = the engine's piste, a mkDefault
#     (on a Mac a system option, so it lands in the system packages); null
#     leaves it out, for an overlay that installs its own;
#   - on a Mac, home-manager.extraSpecialArgs.piste, so a home module there
#     can take `piste` (without it: infinite recursion).
# A host that wires these itself still evaluates:
# imports dedupe and equal definitions merge.
# What mkFlake adds to a *-linux host: the engine's package set (the
# packages module, its resolved list in home.packages and overlays.shared
# ahead of the host's overlays), which a Mac gets from its platform module.
# extraSpecialArgs, with user and piste, reaches a darwin host's system
# modules and a *-linux host's home modules, but not the home modules inside
# a darwin host: those get home-manager.extraSpecialArgs only (piste, user
# and whatever the host's modules add).
#
# systems: what packages and checks are made for, with every host's system
# added.
# packages.<system>: the engine's, plus agent-registry (with agents) and,
# on Linux, agent-bundle (with bundle) and devcontainer{,-load} (with
# devcontainer), plus the overlay's own; a package replaces one of the same
# name from an earlier set whole.
# checks.<system>: lib.zoneChecks over every host's home, whatever its
# system (withSource: the first home-manager host, else the first darwin
# host's user), plus the overlay's own, which must not reuse a zoneChecks
# name (mkFlake throws).
#
# The nixpkgs / darwin / home-manager / nix-homebrew / homebrew-* inputs are
# the overlay's when it declares them, else piste's own.
{ piste }:
{
  inputs,
  self,
  user,
  zone,
  hosts,
  extraSpecialArgs ? (_: { }),
  homebrew ? { },
  packages ? (_: { }),
  agents ? null,
  bundle ? null,
  devcontainer ? null,
  checks ? (_: { }),
  systems ? [ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ],
}:
let
  nf = inputs.piste or piste;
  inp = name: inputs.${name} or nf.inputs.${name};
  lib = (inp "nixpkgs").lib;

  # A function of the system, or one attrset for every system.
  perSystem = v: if lib.isFunction v then v else _: v;
  ownPackages = perSystem packages;
  ownChecks = perSystem checks;

  args = system: { inherit user; piste = nf; } // perSystem extraSpecialArgs system;
  isDarwin = system: lib.hasSuffix "-darwin" system;
  isLinux = system: lib.hasSuffix "-linux" system;
  roleModules = host: map (r: nf.homeManagerModules.${r}) (host.roles or [ ]);
  # A role's Mac side, when it has one (extras): a nix-darwin module of the
  # same name, added to the system beside the role's home module.
  roleDarwinModules = host: map (r: nf.darwinModules.${r}) (lib.filter (r: nf.darwinModules ? ${r}) (host.roles or [ ]));

  # The module order is the one the overlays wired by hand, so list options
  # (nixpkgs.overlays above all) merge to the same values.
  darwinHost =
    host: extra:
    (inp "darwin").lib.darwinSystem {
      inherit (host) system;
      specialArgs = args host.system;
      modules = [
        # Determinate Nix manages Nix itself; a host sets nix.enable = true
        # to hand it to nix-darwin.
        { nix.enable = lib.mkDefault false; }
        { nixpkgs.overlays = host.overlays or [ ]; }
        (inp "home-manager").darwinModules.home-manager
        (inp "nix-homebrew").darwinModules.nix-homebrew
        nf.darwinModules.homebrew-base
        {
          piste.homebrew = {
            enable = true;
            inherit user;
            homebrew-core = inp "homebrew-core";
            homebrew-cask = inp "homebrew-cask";
          } // homebrew;
        }
        nf.darwinModules.platform
        # Ahead of the host's modules: an attrs option, merged with //, so a
        # host's own extraSpecialArgs.piste still wins.
        { home-manager.extraSpecialArgs.piste = nf; }
      ]
      ++ lib.optional ((host.roles or [ ]) != [ ]) { home-manager.users.${user}.imports = roleModules host; }
      ++ roleDarwinModules host
      ++ (host.modules or [ ])
      ++ extra
      ++ [ (darwinWiring host.system) ];
    };

  # The wiring every host gets, after the host's own modules.
  pistePackage = system: lib.mkDefault (nf.packages.${system}.piste or null);
  homeWiring = {
    imports = [ nf.homeManagerModules.sops nf.homeManagerModules.agents ];
  };
  darwinWiring = system: {
    home-manager.users.${user} = homeWiring;
    piste.packages.piste = pistePackage system;
  };
  linuxWiring = system: {
    imports = [ homeWiring ];
    piste.packages.piste = pistePackage system;
  };

  # A *-linux host gets the engine's package set, as a Mac gets it from
  # darwinModules.platform: the packages module, its resolved list in
  # home.packages and overlays.shared (which the list needs; listing it
  # again in the host's overlays does nothing). A host trims it with
  # piste.packages.<category>.enable and .exclude.
  linuxPackages =
    { config, ... }:
    {
      imports = [ nf.homeManagerModules.packages ];
      home.packages = config.piste.packages.resolved.homePackages;
    };

  homeHost =
    host: extra:
    (inp "home-manager").lib.homeManagerConfiguration {
      pkgs = import (inp "nixpkgs") {
        inherit (host) system;
        config.allowUnfree = true;
        overlays = [ nf.overlays.shared ] ++ (host.overlays or [ ]);
      };
      extraSpecialArgs = args host.system;
      modules = [ linuxPackages ] ++ roleModules host ++ (host.modules or [ ]) ++ extra ++ [ (linuxWiring host.system) ];
    };

  darwinHosts = lib.filterAttrs (_: h: isDarwin h.system) hosts;
  homeHosts = lib.filterAttrs (_: h: !isDarwin h.system) hosts;

  darwinConfigurations = lib.concatMapAttrs (
    name: host:
    let
      system = darwinHost host [ ];
    in
    { ${name} = system; } // lib.genAttrs (host.aliases or [ ]) (_: system)
  ) darwinHosts;
  homeConfigurations = lib.mapAttrs (_: host: homeHost host [ ]) homeHosts;

  # --- the zone's agent artifacts ---------------------------------------------
  pkgsFor = system: (inp "nixpkgs").legacyPackages.${system};
  zoneSources = [
    { name = "piste-agents"; zone = "shared"; store = "${nf}/agents"; rev = nf.rev or "dirty"; }
    { name = "${zone}-agents"; inherit zone; store = agents; rev = self.rev or self.dirtyRev or "dirty"; }
  ];
  registryFor = system: nf.lib.mkAgentRegistry {
    pkgs = pkgsFor system;
    inherit zone;
    sources = zoneSources;
  };
  bundleFor =
    system:
    if agents == null then
      throw "mkFlake: bundle needs agents (the zone's agents/ dir)"
    else
      nf.lib.mkAgentBundle {
        pkgs = pkgsFor system;
        inherit zone;
        sources = zoneSources;
        inherit (bundle) presets;
      };
  devcontainerFor = system: nf.lib.mkDevcontainer {
    inherit system;
    inherit (devcontainer) name;
    modules = devcontainer.modules or [ ];
    extraSpecialArgs = perSystem (devcontainer.extraSpecialArgs or { }) system;
    agentRegistry = if agents != null then registryFor system else null;
    agentBundle = if bundle != null then bundleFor system else null;
  };
  zonePackages =
    system:
    lib.optionalAttrs (agents != null) { agent-registry = registryFor system; }
    // lib.optionalAttrs (isLinux system && bundle != null) { agent-bundle = bundleFor system; }
    // lib.optionalAttrs (isLinux system && devcontainer != null) (
      let
        dc = devcontainerFor system;
      in
      {
        devcontainer = dc.image;
        devcontainer-load = dc.load;
      }
    );

  # --- zone checks -------------------------------------------------------------
  homes = lib.mapAttrs (
    name: host:
    if isDarwin host.system then
      darwinConfigurations.${name}.config.home-manager.users.${user}
    else
      homeConfigurations.${name}.config
  ) hosts;
  withSource =
    let
      homeNames = lib.attrNames homeHosts;
      darwinNames = lib.attrNames darwinHosts;
    in
    if homeNames != [ ] then
      m: (homeHost hosts.${lib.head homeNames} [ m ]).config
    else if darwinNames != [ ] then
      m:
      (darwinHost hosts.${lib.head darwinNames} [ { home-manager.users.${user}.imports = [ m ]; } ])
      .config.home-manager.users.${user}
    else
      null;
  zoneChecksFor =
    system:
    nf.lib.zoneChecks (
      {
        pkgs = pkgsFor system;
        inherit zone homes withSource;
      }
      // lib.optionalAttrs (agents != null) { registry = registryFor system; }
      // lib.optionalAttrs (isLinux system && bundle != null) { bundle = bundleFor system; }
    );
  # The generic checks plus the overlay's own; a name in both throws rather
  # than one hiding the other.
  checksFor =
    system:
    let
      generic = zoneChecksFor system;
      own = ownChecks system;
      clash = lib.attrNames (builtins.intersectAttrs generic own);
    in
    if clash != [ ] then
      throw "mkFlake: checks.${system}: lib.zoneChecks already has ${lib.concatStringsSep ", " clash}; the overlay's own checks need other names"
    else
      generic // own;
  hostSystems = lib.unique (lib.mapAttrsToList (_: h: h.system) hosts);
  allSystems = lib.unique (systems ++ hostSystems);

  # --- mistakes in hosts, which would otherwise pass silently ----------------
  hostKeys = [ "system" "modules" "overlays" "roles" "aliases" ];
  allAliases = lib.concatLists (lib.mapAttrsToList (_: h: h.aliases or [ ]) hosts);
  mistakes =
    lib.concatLists (
      lib.mapAttrsToList (
        name: host:
        let
          system = host.system or null;
          known = builtins.isString system && (isDarwin system || isLinux system);
        in
        map (k: "host ${name}: unknown key ${k} (a host takes ${lib.concatStringsSep ", " hostKeys})") (
          lib.subtractLists hostKeys (lib.attrNames host)
        )
        ++ lib.optional (!known) (
          if host ? system then
            "host ${name}: system ${builtins.toJSON system} is neither *-darwin nor *-linux"
          else
            "host ${name} has no system (*-darwin or *-linux)"
        )
        ++ lib.optional (known && isLinux system && (host.aliases or [ ]) != [ ])
          "host ${name}: aliases are for *-darwin hosts only"
      ) hosts
    )
    ++ map (a: "alias ${a} is also a host's name") (lib.filter (a: hosts ? ${a}) (lib.unique allAliases))
    ++ map (a: "alias ${a} is given more than once") (
      lib.filter (a: lib.count (x: x == a) allAliases > 1) (lib.unique allAliases)
    );
in
if mistakes != [ ] then
  throw (lib.concatMapStringsSep "\n" (m: "mkFlake: ${m}") mistakes)
else
{
  inherit darwinConfigurations homeConfigurations;
  # Per system and with //: a package of the overlay's replaces the engine's
  # of that name whole, where recursiveUpdate would merge the two derivations.
  packages = lib.genAttrs allSystems (
    system: (nf.packages.${system} or { }) // zonePackages system // ownPackages system
  );
  apps = nf.apps;
  checks = lib.genAttrs allSystems checksFor;
}
