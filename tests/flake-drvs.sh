#!/usr/bin/env bash
# tests/flake-drvs.sh <overlay-flake-ref> <piste-ref|->: what an overlay flake
# builds, as sorted JSON, for "no behaviour change" comparisons.
# Hosts and packages map to drvPaths, checks to their names. Compare two states
# against one engine ref, and give the overlay as a path: ref so neither state
# has a rev (registries and bundles record it).
#
# stdout is the JSON and nothing else. Keys: darwinConfigurations.<n>,
# homeConfigurations.<n> (drvPaths), packages.<sys>.<n> (a drvPath, or the string
# "eval-error" if the package does not evaluate), checks.<sys> (sorted names).
#
#   <piste-ref>  the flake ref passed as --override-input piste. Pass - to
#                   skip the override, for a flake with no piste input.
#   NIX             env var: the nix binary to run (default: nix on PATH).
#
# A top-level output the flake does not have (no darwinConfigurations, no checks,
# ...) counts as empty; any other evaluation failure stops the script with
# "flake-drvs: evaluating <output> failed", nix's own error, and exit status 1.
# Packages that do not evaluate are listed in a warning on stderr; the exit
# status stays 0 and they appear in the JSON as "eval-error".
set -euo pipefail
[ $# -eq 2 ] || { echo "usage: $0 <overlay-flake-ref> <piste-ref|->" >&2; exit 2; }
overlay=$1 piste=$2
nix=${NIX:-nix}
override=()
[ "$piste" = - ] || override=(--override-input piste "$piste")

# nix's stderr goes to a file so a failure can show why (and a success stays quiet).
errf=$(mktemp "${TMPDIR:-/tmp}/flake-drvs.XXXXXX")
trap 'rm -f "$errf"' EXIT

# ev <attr> <apply> [nix eval flags]: eval <overlay>#<attr> through <apply>, on stdout.
# (The array expansion is guarded so an empty override is fine on bash 3.2.)
ev() {
  local attr=$1 apply=$2
  shift 2
  "$nix" eval "$@" ${override[@]+"${override[@]}"} "$overlay#$attr" --apply "$apply" 2>"$errf"
}

# fail <what>: report a failed evaluation with nix's error. Call it from the main
# shell (`x=$(...) || fail ...`); exit inside a $(...) would only leave the subshell.
fail() {
  echo "flake-drvs: evaluating $1 failed" >&2
  tail -n 40 "$errf" >&2
  exit 1
}

# `<overlay>#.` is the flake's own outputs (`#` alone is the default package).
# Asking it which of our four outputs exist forces only attribute names, so a
# flake without one is told apart from one whose evaluation fails.
present=$(ev . 'o: builtins.concatStringsSep " " (builtins.filter (n: builtins.hasAttr n o)
  [ "darwinConfigurations" "homeConfigurations" "packages" "checks" ])' --raw) || fail "the flake's outputs"

# get <attr> <apply>: the JSON for a top-level output, {} when the flake has none.
# Run as `x=$(get ...) || exit 1` (fail already printed the reason).
get() {
  case " $present " in
    *" $1 "*) ev "$1" "$2" --json || fail "$1" ;;
    *) echo '{}' ;;
  esac
}
darwin=$(get darwinConfigurations 'cs: builtins.mapAttrs (_: c: c.system.drvPath) cs') || exit 1
home=$(get homeConfigurations 'cs: builtins.mapAttrs (_: c: c.activationPackage.drvPath) cs') || exit 1
packages=$(get packages 'ps: builtins.mapAttrs (_: s: builtins.mapAttrs (_: p:
  let r = builtins.tryEval p.drvPath; in if r.success then r.value else "eval-error") s) ps') || exit 1
checks=$(get checks 'cs: builtins.mapAttrs (_: s: builtins.attrNames s) cs') || exit 1

{
  echo "darwinConfigurations $darwin"
  echo "homeConfigurations $home"
  echo "packages $packages"
  echo "checks $checks"
} | python3 -c '
import json, sys
out = {}
for line in sys.stdin:
    kind, doc = line.split(" ", 1)
    for k, v in json.loads(doc).items():
        if kind == "packages":
            for n, d in v.items():
                out[f"packages.{k}.{n}"] = d
        else:
            out[f"{kind}.{k}"] = v
print(json.dumps(out, indent=1, sort_keys=True))
bad = sorted(k for k, v in out.items() if v == "eval-error")
if bad:
    print(f"flake-drvs: warning: {len(bad)} package(s) did not evaluate (recorded as \"eval-error\"):", file=sys.stderr)
    for k in bad:
        print(f"  {k}", file=sys.stderr)
'
