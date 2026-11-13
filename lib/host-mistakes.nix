# The mistakes in a mkFlake `hosts` attrset that would otherwise pass silently,
# as messages: a function of the hosts returning [ string ], empty when there are
# none. mk-flake.nix puts "mkFlake: " before each and throws; checks.mkflake-mistakes
# imports this file and asserts every message's text. Not exported from the flake.
#
# A host takes system (*-darwin or *-linux) and, each optional, modules, overlays,
# roles, aliases (a *-darwin host's other names) and nixos (true: a NixOS host,
# false: the same as leaving it out).
#
# Order: per host (in name order) the unknown keys, the system, aliases on a *-linux
# host, then the NixOS rules, then the unknown roles; then the aliases that are a host's name or given twice.
# roles is the list of role names a host may take (lib/roles.nix).
{ lib, roles }:
hosts:
let
  isDarwin = system: lib.hasSuffix "-darwin" system;
  isLinux = system: lib.hasSuffix "-linux" system;

  hostKeys = [ "system" "modules" "overlays" "roles" "aliases" "nixos" ];
  allAliases = lib.concatLists (lib.mapAttrsToList (_: h: h.aliases or [ ]) hosts);
in
lib.concatLists (
  lib.mapAttrsToList (
    name: host:
    let
      system = host.system or null;
      known = builtins.isString system && (isDarwin system || isLinux system);
      nixos = host.nixos or false;
      # Only a bool is a marker: anything else gets its own message, not the
      # NixOS rules (which would be about a host that isn't known to be one).
      isNixos = nixos == true;
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
    # NixOS hosts: the key is the hostname nixos-rebuild picks.
    ++ lib.optional (known && isDarwin system && isNixos) "host ${name}: nixos is for *-linux hosts only"
    ++ lib.optional (isNixos && lib.hasInfix "@" name)
      "host ${name}: a NixOS host is named by its hostname, without user@"
    ++ lib.optional (isNixos && lib.hasInfix "." name) "host ${name}: a NixOS hostname can't contain dots"
    ++ lib.optional (!builtins.isBool nixos) "host ${name}: nixos must be true or false"
    # A role that isn't a string (a module, say) is rendered, not interpolated,
    # which would throw and lose the message.
    ++ map (r: "host ${name}: unknown role ${if builtins.isString r then r else lib.generators.toPretty { multiline = false; } r} (roles are ${lib.concatStringsSep ", " roles})") (
      lib.filter (r: !(lib.elem r roles)) (host.roles or [ ])
    )
  ) hosts
)
++ map (a: "alias ${a} is also a host's name") (lib.filter (a: hosts ? ${a}) (lib.unique allAliases))
++ map (a: "alias ${a} is given more than once") (
  lib.filter (a: lib.count (x: x == a) allAliases > 1) (lib.unique allAliases)
)
