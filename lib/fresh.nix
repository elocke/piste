# mkFlake's `fresh`: packages taken from the overlay's own nixpkgs-fresh input,
# newer than the engine's tested nixpkgs. Called with
#   freshInput     the overlay's inputs.nixpkgs-fresh, or null when it has none;
#   names          mkFlake's fresh list, attribute paths as dotted strings;
#   engineOverlay  the engine's overlays.shared;
# gives `system -> overlay` (an attrset also holding `check`).
# The overlay goes last of every nixpkgs overlay. Its mistakes throw
# "mkFlake: …" when the package set is used, so they cost nothing without
# fresh. `check system prev` returns the first mistake's message, or null.
{
  lib,
  freshInput,
  names,
  engineOverlay,
}:
let
  pathOf = lib.splitString ".";

  # The first mistake's message, or null, given nixpkgs-fresh's package set
  # (only forced when the input is there).
  problemIn =
    freshPkgs: prev:
    if freshInput == null then
      "mkFlake: fresh lists ${lib.concatStringsSep ", " names}, but this overlay has no nixpkgs-fresh input. Add to flake.nix's inputs: nixpkgs-fresh.url = \"github:nixos/nixpkgs/nixos-unstable\";"
    else
      let
        # The engine's own names, from the real prev; only the first path
        # component of a fresh name is compared. The engine's overlays
        # apply once (they mark pkgs.pisteOverlays and add nothing to a prev
        # that carries the mark), and prev already has it: ask with it cleared.
        unmarked = prev // { pisteOverlays = { }; };
        engineNames = lib.attrNames (engineOverlay unmarked unmarked);
        problem =
          name:
          if lib.elem (lib.head (pathOf name)) engineNames then
            "mkFlake: fresh: ${name} comes from the engine (piste.overlays.shared) and moves with piste releases; take it out of fresh"
          else if !(lib.hasAttrByPath (pathOf name) freshPkgs) then
            "mkFlake: fresh: nixpkgs-fresh has no package ${name}"
          else
            null;
      in
      lib.findFirst (m: m != null) null (map problem names);

  check = system: problemIn (fresh system);

  # nixpkgs-fresh as a package set: no overlays of the engine's.
  fresh = system: import freshInput { inherit system; config.allowUnfree = true; };

  setIn =
    base: path: value:
    if path == [ ] then value else base // { ${lib.head path} = setIn (base.${lib.head path} or { }) (lib.tail path) value; };

  # Two elaborated platforms are the same one. Not lib.systems.equals: its
  # parsed.abi holds functions, equal only from one lib instance, and a
  # home's platform is elaborated by nixpkgs' own lib, a Mac's by the flake's
  # lib. The rest is plain data, config (the triple) standing for parsed.
  samePlatform = a: b: removeAttrs a._withoutFunctions [ "parsed" ] == removeAttrs b._withoutFunctions [ "parsed" ];

  # nixpkgs-fresh is imported once per system, outside the overlay: its
  # check and a host's own packages share it. Another package set the
  # overlay reaches (pkgsi686Linux, pkgsCross.<x>, pkgsStatic) builds on
  # other platforms: its fresh packages come from nixpkgs-fresh imported for
  # those, only once one of them is used.
  overlay =
    system:
    let
      freshPkgs = fresh system;
      bound = lib.systems.elaborate system;
    in
    final: prev:
    let
      message = problemIn freshPkgs prev;
      build = prev.stdenv.buildPlatform;
      host = prev.stdenv.hostPlatform;
      setPkgs =
        if samePlatform build bound && samePlatform host bound then
          freshPkgs
        else
          import freshInput (
            {
              localSystem = build;
              config.allowUnfree = true;
            }
            // lib.optionalAttrs (!samePlatform build host) { crossSystem = host; }
          );
    in
    if message != null then
      throw message
    else
      # Each name is set into prev's own attribute (python3Packages.foo keeps
      # the rest of python3Packages), and a package replaces prev's whole.
      lib.foldl' (
        acc: name:
        let
          path = pathOf name;
          top = lib.head path;
        in
        acc // { ${top} = setIn (acc.${top} or prev.${top} or { }) (lib.tail path) (lib.getAttrFromPath path setPkgs); }
      ) { } names;
in
{
  __functor = _: overlay;
  inherit check;
}
