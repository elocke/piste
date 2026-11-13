# A stand-in for an overlay's nixpkgs-fresh input (checks.mkflake-fresh): a
# package set of its own, so the check evaluates under `nix flake check
# --no-build` too, where builtins.toFile writes nothing. Every package here is
# marked pisteFresh, and claude-code stands for a name the engine provides.
# Imported for another package set's platforms (lib/fresh.nix: localSystem,
# and crossSystem for a cross set) instead of a system, each package records
# their systems as pisteLocal and pisteCross.
args:
let
  recorded =
    (if args ? localSystem then { pisteLocal = args.localSystem.system; } else { })
    // (if args ? crossSystem then { pisteCross = args.crossSystem.system; } else { });
  pkg =
    name:
    builtins.derivation (
      {
        inherit name;
        system = args.system or args.localSystem.system;
        builder = "/bin/sh";
        args = [ "-c" "echo > $out" ];
        pisteFresh = true;
      }
      // recorded
    );
in
{
  hello = pkg "hello-fresh";
  claude-code = pkg "claude-code-fresh";
  python3Packages.pisteFreshProbe = pkg "probe-fresh";
}
