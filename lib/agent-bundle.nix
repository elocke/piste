# piste.lib.mkAgentBundle — a zone's container agent bundle,
# rendered by `piste agents bundle --target …` from the zone
# roots' store copies. The result is the directory lib.mkDevcontainer's
# `agentBundle` takes.
#
#   piste.lib.mkAgentBundle {
#     inherit pkgs;
#     zone = "work";
#     sources = [
#       { name = "piste-agents"; zone = "shared"; store = piste + "/agents"; rev = piste.rev or "dirty"; }
#       { name = "work-agents"; zone = "work"; store = ./agents; rev = self.rev or "dirty"; }
#     ];
#     presets = [ "shared:bundle/base" "work:bundle/work" ];
#   }
#
# The engine refuses a root from another zone and, for a deny-unlisted zone,
# any third-party source its [policy] does not allow; manifest.json records
# each root's zone, rev and tree hash.
{ self }:

{
  pkgs,
  zone,
  sources,
  presets,
  targets ? [ "claude" "codex" ],
  # Render the zone's [[mcp]] into /etc/claude-code/managed-mcp.json. Off by
  # default: that file takes exclusive control of MCP in Claude Code.
  managedMcp ? false,
  name ? "${zone}-agent-bundle",
}:

let
  inherit (pkgs) lib;
  # The engine's tests run in piste CI;
  # rendering a bundle doesn't repeat them.
  piste = (import (self + "/cli") { inherit pkgs; }).piste-go;
  args = lib.concatMap (s: [
    "--zone-root=${s.name}=${s.zone}:${s.store}"
  ] ++ lib.optional (s ? rev) "--source-commit=${s.name}=${s.rev}") sources;
in
pkgs.runCommand name { } ''
  export HOME=$TMPDIR NO_COLOR=1
  ${piste}/bin/piste agents bundle --zone ${lib.escapeShellArg zone} \
    --target ${lib.concatStringsSep "," targets} ${lib.optionalString managedMcp "--managed-mcp"} \
    ${lib.escapeShellArgs (args ++ presets)} --out $TMPDIR/bundle
  cp -r $TMPDIR/bundle $out
''
