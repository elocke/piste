# The generic trust-zone checks of an overlay, as flake
# checks. Whatever the zone is called, its hosts resolve only shared and
# their own zone's content, a source of another zone fails evaluation, and
# its bundle and registry name only those two zones.
#
#   piste.lib.zoneChecks {
#     pkgs;
#     zone = "work";                 # the overlay's host zone
#     homes = { devbox = <HM config>; mac = <HM config>; };   # `.config` of each host's home
#     withSource ? null;             # module -> HM config: one host plus an extra module
#     bundle ? null;                 # a built agent bundle (has manifest.json)
#     registry ? null;               # a built agent registry (has registry.json)
#   }
#   => { zone-clean; zone-foreign-rejected (with withSource);
#        zone-unapproved-rejected (with withSource, if the zone's own
#          [policy] is unlisted = "deny" and does not allow the source of
#          the fixture's marketplace, github:zone-check/unapproved);
#        zone-bundle (with bundle); zone-registry (with registry); }
#
# Evaluation-time failures throw "zoneChecks: <check>: …"; the bundle and
# registry checks fail their build, naming the offending entries. An
# overlay's own policy (which third-party sources it approves, marker greps)
# stays in the overlay.
{ lib }:
{
  pkgs,
  zone,
  homes,
  withSource ? null,
  bundle ? null,
  registry ? null,
}:
let
  agentsLib = import ./agents.nix { inherit lib; };
  fail = check: msg: throw "zoneChecks: ${check}: ${msg}";

  validZone = builtins.match "[a-z][a-z0-9-]*" zone != null && zone != "shared";
  allowed = [ "shared" zone ];
  ok = name: drv: if validZone then drv else
    fail name "zone \"${zone}\": want a zone name other than \"shared\" (lowercase letters, digits, dashes)";
  pass = name: pkgs.runCommand "check-${name}" { } "touch $out";

  resolvedOf = cfg: { inherit (cfg.piste.agents.resolved) links deferred; };
  throws = x: !(builtins.tryEval (builtins.deepSeq x x)).success;

  # Why a home is not clean; [ ] when it is. The home's own zone is the
  # machine zone piste agents admits content for, so it must be `zone` too.
  # Third-party links are locked fetches a zone source declared, so they
  # count as that source's.
  problems = label: cfg:
    let
      sources = lib.attrValues cfg.piste.agents.sources;
      repos = map (s: s.name) sources ++ [ "third-party" ];
      links = cfg.piste.agents.resolved.links;
    in
    lib.optional (cfg.piste.agents.zone != zone) "${label}: host zone is ${cfg.piste.agents.zone}, not ${zone}"
    ++ lib.optional (links == [ ]) "${label}: resolves no agent links"
    ++ map (s: "${label}: source ${s.name} is zone ${s.zone}, not shared or ${zone}")
      (lib.filter (s: !lib.elem s.zone allowed) sources)
    ++ map (l: "${label}: link ${l.name} comes from ${l.repo}, which is not a source")
      (lib.filter (l: !lib.elem l.repo repos) links);

  # An extra source of this zone, from a fixture.
  zoneSource = name: store: {
    piste.agents.sources.${name} = {
      inherit zone store;
      workdir = "/nonexistent";
    };
  };
  # The zone's own [policy] denies unlisted third-party items and does not
  # allow the unapproved fixture's marketplace. A zone that allows it (say
  # with github:*) leaves the fixture nothing to test.
  policy = agentsLib.zonePolicy (lib.attrValues (withSource { }).piste.agents.sources) zone;
  unapprovedSource = (lib.head (lib.importTOML ./fixtures/unapproved/agents.toml).claude.marketplace).source;
  rejectsUnapproved =
    withSource != null
    && (policy.unlisted or "warn") == "deny"
    && !agentsLib.policyAllows policy unapprovedSource;

  # The zone the foreign fixture source claims: any other valid name.
  foreign = if zone == "zone-check-foreign" then "zone-check-other" else "zone-check-foreign";

  # jq over one JSON file: each expression lists offenders; any output fails
  # the build with the label and the offenders.
  jqChecks = name: file: checks:
    ok name (pkgs.runCommand "check-${name}" { nativeBuildInputs = [ pkgs.jq ]; } ''
      f=${file}
      failed=0
      offenders() {
        found=$(jq -r --arg z ${lib.escapeShellArg zone} "$2" "$f") || { echo "zoneChecks: ${name}: $f: unreadable" >&2; exit 1; }
        if [ -n "$found" ]; then
          printf 'zoneChecks: ${name}: %s:\n%s\n' "$1" "$found" >&2
          failed=1
        fi
      }
      ${lib.concatMapStrings (c: "offenders ${lib.escapeShellArg c.label} ${lib.escapeShellArg c.expr}\n") checks}
      [ "$failed" = 0 ]
      touch $out
    '');
in
{
  zone-clean = ok "zone-clean" (
    let errs = lib.concatLists (lib.mapAttrsToList problems homes); in
    if homes == { } then fail "zone-clean" "no homes to check"
    else if errs != [ ] then fail "zone-clean" (lib.concatStringsSep "; " errs)
    else pass "zone-clean"
  );
}
// lib.optionalAttrs (withSource != null) {
  zone-foreign-rejected = ok "zone-foreign-rejected" (
    let
      foreignSource = {
        piste.agents.sources.zone-check-foreign = {
          zone = foreign;
          store = ./fixtures/foreign-zone;
          workdir = "/nonexistent";
        };
      };
    in
    if throws (resolvedOf (withSource { })) then
      fail "zone-foreign-rejected" "withSource { } does not evaluate on its own"
    else if !throws (resolvedOf (withSource foreignSource)) then
      fail "zone-foreign-rejected" "a source of zone ${foreign} evaluates on a ${zone} host"
    else pass "zone-foreign-rejected"
  );
}
// lib.optionalAttrs rejectsUnapproved {
  # The approved source is the control: the two differ only in the
  # unapproved marketplace, so the throw is the policy's.
  zone-unapproved-rejected = ok "zone-unapproved-rejected" (
    if throws (resolvedOf (withSource (zoneSource "zone-check-approved" ./fixtures/approved))) then
      fail "zone-unapproved-rejected" "the approved fixture source does not evaluate"
    else if !throws (resolvedOf (withSource (zoneSource "zone-check-unapproved" ./fixtures/unapproved))) then
      fail "zone-unapproved-rejected" "an unapproved marketplace evaluates under ${zone}'s deny policy"
    else pass "zone-unapproved-rejected"
  );
}
// lib.optionalAttrs (bundle != null) {
  zone-bundle = jqChecks "zone-bundle" "${bundle}/manifest.json" [
    { label = "manifest zone is not ${zone}"; expr = ''select(.zone != $z) | "zone: \(.zone)"''; }
    { label = "sources of another zone"; expr = ''.sources[] | select(.zone != "shared" and .zone != $z) | "\(.name) (\(.zone))"''; }
  ];
}
// lib.optionalAttrs (registry != null) {
  zone-registry = jqChecks "zone-registry" "${registry}/registry.json" [
    { label = "registry zone is not ${zone}"; expr = ''select(.zone != $z) | "zone: \(.zone)"''; }
    { label = "roots of another zone"; expr = ''.roots[] | select(.zone != "shared" and .zone != $z) | "\(.name) (\(.zone))"''; }
    { label = "items of another zone"; expr = ''.items[].id | select(split(":")[0] | . != "shared" and . != $z)''; }
  ];
}
