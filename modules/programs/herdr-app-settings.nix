# A script that merges `settings` into the Herdr app's settings.json at `path`,
# with home-manager's own merger (lib.hm.generators.mkImpureConfigMerger, the
# one its programs.herdr, claude-code and codex use). Declared keys win; every
# other key stays as the person or the app left it. A file that won't parse,
# or that changes while the script runs, is left alone and the script exits
# non-zero. It's a script of its own so `set -e` holds inside it, whatever the
# caller does with its status (checks.herdr-settings runs it on a temp home).
{ pkgs, hm, path, settings }:

pkgs.writeShellScript "herdr-app-settings" ''
  set -euo pipefail
  errorEcho() { echo "$*" >&2; }
  ${hm.generators.mkImpureConfigMerger {
    inherit pkgs path;
    format = "json";
    empty = "{}";
    jqOperation = "$dynamic * $static";
    staticSettings = (pkgs.formats.json { }).generate "herdr-app-settings.json" settings;
  }}
''
