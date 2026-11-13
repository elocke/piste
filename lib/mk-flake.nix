# piste.lib.mkFlake: an overlay's flake outputs from a short description.
# The paved road, not the only one: the modules it
# wires are the same darwinModules / nixosModules / homeManagerModules an
# overlay can wire by hand.
#
#   piste.lib.mkFlake {
#     inherit inputs self;            # the overlay's flake inputs and itself
#     user = "jane.doe";              # login name on every host
#     zone = "work";                  # the hosts' trust zone
#     hosts = {
#       MB2222 = { system = "aarch64-darwin"; modules = [ ./hosts/mac.nix ]; aliases = [ ]; };
#       "jane@devbox" = { system = "x86_64-linux"; roles = [ "dev" ]; modules = [ ./hosts/devbox.nix ]; };
#       server = { system = "x86_64-linux"; nixos = true; roles = [ "dev" ]; modules = [ ./hosts/server.nix ]; };
#     };                              # each may also set overlays (its nixpkgs overlays)
#     extraSpecialArgs = system: { }; # module args for every host on `system`
#     homebrew = { extraTaps = { }; };  # merged into piste.homebrew
#     packages = system: { };         # the overlay's own packages
#     fresh = [ "gh" ];               # packages from inputs.nixpkgs-fresh, newer than the engine's
#     agents = ./agents;              # the zone's agent root: source "<zone>-agents"
#     bundle.presets = [ … ];         # the zone's container agent bundle (needs agents)
#     devcontainer = { name; modules ? [ ]; extraSpecialArgs ? (system: { }); };
#     checks = system: { };           # the overlay's own checks (its policy)
#     systems = [ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ];  # the default
#   }
#   => { darwinConfigurations; homeConfigurations; nixosConfigurations;
#        packages; apps; checks; }   (each configurations set { } when empty)
#
# extraSpecialArgs, packages, checks and devcontainer.extraSpecialArgs each
# take a function of the system or one attrset for every system.
#
# A host has system and, each optional, modules, overlays, roles, aliases
# and nixos (true or false; false is the same as leaving it out).
# A role is one of lib/roles.nix (base, dev, desktop, container, extras),
# each a name in piste.homeManagerModules (no other name there is a role);
# a *-darwin host that lists one with a darwinModules entry of the same name
# (extras) gets that too, and a NixOS host one with a nixosModules entry.
# There are three kinds of host:
#   - a *-darwin host is a nix-darwin system (plus one per alias), with
#     home-manager inside it;
#   - a *-linux host with nixos = true is a NixOS system (nixpkgs'
#     lib.nixosSystem, nixosModules.platform), with home-manager inside it as
#     on a Mac; its name is its hostname (networking.hostName, at priority
#     900: above the mkDefault "" of image modules, below a host's own value),
#     the name nixos-rebuild picks, so no "user@" and no dots;
#   - any other *-linux host is a standalone home-manager configuration, its
#     name being "user@host".
# Only a *-darwin host has aliases. mkFlake throws "mkFlake: …" on any other
# host key or system, on aliases on a *-linux host, on nixos on a *-darwin
# host or not a boolean, on a NixOS host named with "@" or a dot, on a role
# that isn't one of lib/roles.nix, and on an alias that is a host's name or
# is given twice (lib/host-mistakes.nix); and on a devcontainer key other
# than name, modules and extraSpecialArgs, or a bundle key other than
# presets (the only ones it reads).
#
# What mkFlake adds to every host (its wiring), so a host's own modules
# bring none of it:
#   - the sops home module (sops-nix's, piste.homeManagerModules.sops: the
#     dev and desktop roles set its options), piste.homeManagerModules.agents
#     and piste.homeManagerModules.pnpm (piste.packages.pnpm; inert while the
#     list is empty) in the home (on a Mac or a NixOS host, home-manager.users.<user>.imports);
#   - the piste CLI: piste.packages.piste = the engine's piste, a mkDefault
#     (on a Mac or a NixOS host a system option, so it lands in the system
#     packages); null leaves it out, for an overlay that installs its own;
#   - on a Mac or a NixOS host, home-manager.extraSpecialArgs.piste, so a
#     home module there can take `piste` (without it: infinite recursion).
# A host that wires these itself still evaluates:
# imports dedupe and equal definitions merge.
# What mkFlake adds to a home-manager host: the engine's package set (the
# packages module, its resolved list in home.packages and overlays.shared
# ahead of the host's overlays), which a Mac gets from its platform module
# (pkgs/overlays/) and mkFlake (overlays.ai, ahead of the host's overlays).
# A NixOS host gets the set from nixosModules.platform (in
# environment.systemPackages) and overlays.shared from mkFlake, ahead of the
# host's overlays in nixpkgs.overlays.
# extraSpecialArgs, with user and piste, reaches a darwin or NixOS host's
# system modules and a home-manager host's home modules, but not the home
# modules inside a darwin or NixOS host: those get
# home-manager.extraSpecialArgs only (piste, user and whatever the host's
# modules add).
#
# systems: what packages and checks are made for, with every host's system
# added.
# packages.<system>: the engine's, plus agent-registry (with agents) and,
# on Linux, agent-bundle (with bundle) and devcontainer{,-load} (with
# devcontainer), plus the overlay's own; a package replaces one of the same
# name from an earlier set whole.
# checks.<system>: lib.zoneChecks over every host's home, whatever its
# system (withSource: the first home-manager host, else the first darwin
# host's user, else the first NixOS host's), plus the overlay's own, which
# must not reuse a zoneChecks name (mkFlake throws).
#
# fresh: attribute paths (dotted strings, "python3Packages.foo") taken from the
# overlay's own inputs.nixpkgs-fresh (no engine fallback), through one nixpkgs
# overlay added after every other, on all three kinds of host; everything else
# stays on the tested nixpkgs. mkFlake throws "mkFlake: fresh …" (lib/fresh.nix)
# when the list has no input, when nixpkgs-fresh lacks a name, or when a name is
# one the engine's overlays.shared provides. [ ] (the default) adds nothing.
#
# The nixpkgs / darwin / home-manager / nix-homebrew / homebrew-* inputs are
# the overlay's when it declares them, else piste's own.
# `fail` is for the engine's own checks, which read the message: mkFlake
# hands it the message it would throw.
{ piste, fail ? throw }:
{
  inputs,
  self,
  user,
  zone,
  hosts,
  extraSpecialArgs ? (_: { }),
  homebrew ? { },
  packages ? (_: { }),
  fresh ? [ ],
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

  # The fresh pin's overlay for a system: none without fresh, so a host
  # without it evaluates as before.
  freshOverlays =
    system:
    lib.optional (fresh != [ ]) (
      import ./fresh.nix {
        inherit lib;
        freshInput = inputs.nixpkgs-fresh or null;
        names = fresh;
        engineOverlay = nf.overlays.shared;
      } system
    );

  # A function of the system, or one attrset for every system.
  perSystem = v: if lib.isFunction v then v else _: v;
  ownPackages = perSystem packages;
  ownChecks = perSystem checks;

  args = system: { inherit user; piste = nf; } // perSystem extraSpecialArgs system;
  isDarwin = system: lib.hasSuffix "-darwin" system;
  isLinux = system: lib.hasSuffix "-linux" system;
  # The roles a host may take: the engine's home-manager modules of these names.
  roles = import ./roles.nix;
  roleModules = host: map (r: nf.homeManagerModules.${r}) (host.roles or [ ]);
  # A role's Mac side, when it has one (extras): a nix-darwin module of the
  # same name, added to the system beside the role's home module.
  roleDarwinModules = host: map (r: nf.darwinModules.${r}) (lib.filter (r: lib.elem r roles && nf.darwinModules ? ${r}) (host.roles or [ ]));
  # The same for a NixOS host: a NixOS module of the role's name.
  roleNixosModules = host: map (r: nf.nixosModules.${r}) (lib.filter (r: lib.elem r roles && nf.nixosModules ? ${r}) (host.roles or [ ]));

  # The module order is the one the overlays wired by hand used, so list
  # options merge as they did there; the exception is nixpkgs.overlays, where
  # overlays.ai now comes first (a Mac wired by hand that didn't list it got
  # nixpkgs' claude-code, codex and gemini-cli).
  darwinHost =
    host: extra:
    (inp "darwin").lib.darwinSystem {
      inherit (host) system;
      specialArgs = args host.system;
      modules = [
        # Determinate Nix manages Nix itself; a host sets nix.enable = true
        # to hand it to nix-darwin.
        { nix.enable = lib.mkDefault false; }
        # darwinModules.platform brings pkgs/overlays/ (modules/shared) but
        # not overlays.ai: without it a Mac's claude-code, codex and
        # gemini-cli would be nixpkgs' (gemini-cli a removed one), not the
        # engine's. Ahead of the host's, as overlays.shared is on Linux.
        { nixpkgs.overlays = [ nf.overlays.ai ] ++ (host.overlays or [ ]); }
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
      ++ [ (darwinWiring host.system) ]
      ++ lib.optional (fresh != [ ]) { nixpkgs.overlays = freshOverlays host.system; };
    };

  # A NixOS host, in darwinHost's order: home-manager inside the system, its
  # name the hostname. nixosModules.platform doesn't import modules/shared,
  # so overlays.shared comes in here, ahead of the host's overlays (as on a
  # home-manager host).
  nixosHost =
    name: host: extra:
    (inp "nixpkgs").lib.nixosSystem {
      inherit (host) system;
      specialArgs = args host.system;
      modules = [
        { nixpkgs.overlays = [ nf.overlays.shared ] ++ (host.overlays or [ ]); }
        (inp "home-manager").nixosModules.home-manager
        nf.nixosModules.platform
        { home-manager.extraSpecialArgs.piste = nf; }
        # Above mkDefault (1000), which image modules use for "" (EC2, GCE,
        # Azure, DigitalOcean: the name from metadata), so they don't
        # conflict; below a plain value, so a host's own setting wins.
        { networking.hostName = lib.mkOverride 900 name; }
      ]
      ++ lib.optional ((host.roles or [ ]) != [ ]) { home-manager.users.${user}.imports = roleModules host; }
      ++ roleNixosModules host
      ++ (host.modules or [ ])
      ++ extra
      ++ [ (nixosWiring host.system) ]
      ++ lib.optional (fresh != [ ]) { nixpkgs.overlays = freshOverlays host.system; };
    };

  # The wiring every host gets, after the host's own modules.
  pistePackage = system: lib.mkDefault (nf.packages.${system}.piste or null);
  homeWiring = {
    imports = [ nf.homeManagerModules.sops nf.homeManagerModules.agents nf.homeManagerModules.pnpm ];
  };
  darwinWiring = system: {
    home-manager.users.${user} = homeWiring;
    piste.packages.piste = pistePackage system;
  };
  # A NixOS host holds its home as a Mac does: the same wiring.
  nixosWiring = darwinWiring;
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
        overlays = [ nf.overlays.shared ] ++ (host.overlays or [ ]) ++ freshOverlays host.system;
      };
      extraSpecialArgs = args host.system;
      modules = [ linuxPackages ] ++ roleModules host ++ (host.modules or [ ]) ++ extra ++ [ (linuxWiring host.system) ];
    };

  isNixos = h: h.nixos or false;
  darwinHosts = lib.filterAttrs (_: h: isDarwin h.system) hosts;
  homeHosts = lib.filterAttrs (_: h: !isDarwin h.system && !isNixos h) hosts;
  nixosHosts = lib.filterAttrs (_: isNixos) hosts;

  darwinConfigurations = lib.concatMapAttrs (
    name: host:
    let
      system = darwinHost host [ ];
    in
    { ${name} = system; } // lib.genAttrs (host.aliases or [ ]) (_: system)
  ) darwinHosts;
  homeConfigurations = lib.mapAttrs (_: host: homeHost host [ ]) homeHosts;
  nixosConfigurations = lib.mapAttrs (name: host: nixosHost name host [ ]) nixosHosts;

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
    else if isNixos host then
      nixosConfigurations.${name}.config.home-manager.users.${user}
    else
      homeConfigurations.${name}.config
  ) hosts;
  # NixOS hosts come last, so an overlay that has a home-manager or darwin
  # host keeps the one it had before NixOS hosts existed.
  withSource =
    let
      homeNames = lib.attrNames homeHosts;
      darwinNames = lib.attrNames darwinHosts;
      nixosNames = lib.attrNames nixosHosts;
    in
    if homeNames != [ ] then
      m: (homeHost hosts.${lib.head homeNames} [ m ]).config
    else if darwinNames != [ ] then
      m:
      (darwinHost hosts.${lib.head darwinNames} [ { home-manager.users.${user}.imports = [ m ]; } ])
      .config.home-manager.users.${user}
    else if nixosNames != [ ] then
      m:
      (nixosHost (lib.head nixosNames) hosts.${lib.head nixosNames} [ { home-manager.users.${user}.imports = [ m ]; } ])
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

  # --- mistakes in hosts and arguments, which would otherwise pass silently --
  # (lib/host-mistakes.nix: messages without the "mkFlake: " put before each)
  devcontainerKeys = [ "name" "modules" "extraSpecialArgs" ];
  bundleKeys = [ "presets" ];
  # An argument's keys mkFlake doesn't read.
  unknownKeys =
    arg: v: keys:
    lib.optionals (lib.isAttrs v) (
      map (k: "${arg}: unknown key ${k} (${arg} takes ${lib.concatStringsSep ", " keys})") (
        lib.subtractLists keys (lib.attrNames v)
      )
    );
  mistakes =
    import ./host-mistakes.nix { inherit lib roles; } hosts
    ++ unknownKeys "devcontainer" devcontainer devcontainerKeys
    ++ unknownKeys "bundle" bundle bundleKeys;
in
if mistakes != [ ] then
  fail (lib.concatMapStringsSep "\n" (m: "mkFlake: ${m}") mistakes)
else
{
  inherit darwinConfigurations homeConfigurations nixosConfigurations;
  # Per system and with //: a package of the overlay's replaces the engine's
  # of that name whole, where recursiveUpdate would merge the two derivations.
  packages = lib.genAttrs allSystems (
    system: (nf.packages.${system} or { }) // zonePackages system // ownPackages system
  );
  apps = nf.apps;
  checks = lib.genAttrs allSystems checksFor;
}
