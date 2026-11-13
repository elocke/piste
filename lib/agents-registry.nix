# The agent registry as a derivation: `piste agents
# registry build` over explicit zone roots, with every locked third-party
# skill's fixed-output fetch, into registry.json + content/.
#
#   mkAgentRegistry {
#     pkgs;
#     zone = "personal";                      # the machine zone it is for
#     sources = [ { name; zone; store; rev ? …; } ];  # rev: the root's commit (optional)
#   }
#
# The home module links it at ~/.local/share/piste-agents/registry; zone
# flakes pass it to lib.mkDevcontainer (agentRegistry), which puts it in
# the image at /etc/piste/agent-bundle/registry. Built for one zone, so
# a work registry holds no personal item: the build refuses one.
{ lib }:
let
  agentsLib = import ./agents.nix { inherit lib; };
in
{
  mkAgentRegistry =
    {
      pkgs,
      zone,
      sources,
      # The same binary as the engine's piste; its tests run in checks.piste.
      piste ? (import ../cli { inherit pkgs; }).piste-go,
    }:
    let
      fetch =
        e:
        let
          f = agentsLib.fetchSpec e;
          src = pkgs.fetchgit (
            { inherit (f) url rev hash; } // lib.optionalAttrs (f.sparse != [ ]) { sparseCheckout = f.sparse; }
          );
        in
        if (e.path or ".") == "." then "${src}" else "${src}/${e.path}";
      locked = lib.concatMap agentsLib.lockedSkills sources;
      arg = a: lib.escapeShellArg a;
      args =
        map (s: "--zone-root=${arg "${s.name}=${s.zone}:${s.store}"}") sources
        ++ map (e: "--third-party=${arg "${e.name}=${fetch e}"}") locked
        ++ map (s: "--source-commit=${arg "${s.name}=${s.rev}"}") (lib.filter (s: s ? rev) sources);
    in
    pkgs.runCommand "piste-agents-registry-${zone}" { } ''
      export HOME=$TMPDIR
      ${piste}/bin/piste agents registry build --out $out --zone ${arg zone} ${lib.concatStringsSep " " args}
    '';
}
