# Resolver tests for lib/agents.nix (golden link map,
# instruction composition, zone guard). Pure: fixture zones only.
#
#   nix eval --json --impure --expr 'import ./tests/agents.nix { lib = (import <nixpkgs> {}).lib; }'
#
# flake check `agents-resolver` diffs `golden` against
# tests/fixtures/agents/golden/*.
{ lib }:

let
  agents = import ../lib/agents.nix { inherit lib; };
  fx = ./fixtures/agents;

  src = name: zone: {
    inherit name zone;
    store = fx + "/${name}";
    workdir = "/work/${name}";
    capabilities = name == "shared";
  };
  shared = src "shared" "shared";
  personal = src "personal" "personal";
  work = src "work" "work";
  workbad = src "workbad" "work";
  workinst = src "workinst" "work";
  workmcp = src "workmcp" "work";
  workuse = src "workuse" "work";
  workown = src "workown" "work";
  workownbare = src "workownbare" "work";
  workownloose = src "workownloose" "work";
  marketsOf = sources: lib.mapAttrs (_: map (m: m.name)) (agents.usedMarketplaces (agents.manifestDecls sources));
  workHost = extra: presets: agents.resolve {
    sources = [ shared ] ++ extra;
    inherit presets;
    targets = [ "claude" "codex" ];
    zone = "work";
  };
  collide = src "collide" "personal";
  badhook = src "badhook" "shared";

  hookHost = presets: targets: agents.resolve {
    sources = [ shared ];
    inherit presets targets;
    zone = "personal";
  };
  stale = src "stale" "personal";
  orphan = src "orphan" "personal";

  personalHost = agents.resolve {
    sources = [ shared personal ];
    presets = [ "base" "personal" ];
    targets = [ "claude" "codex" ];
    zone = "personal";
  };

  # Does evaluating `x` fully throw? (tryEval catches `throw`.)
  throws = x: !(builtins.tryEval (builtins.deepSeq x x)).success;
in
{
  golden = {
    linkmap = {
      inherit (personalHost) links deferred;
      instructions = map (i: { inherit (i) harness target fragments; }) personalHost.instructions;
    };
    profile = agents.resolveProfile {
      sources = [ shared personal ];
      preset = "lean";
    };
    blocks = lib.listToAttrs (map (i: lib.nameValuePair i.harness i.block) personalHost.instructions);
  };

  # Each must be true.
  negatives = {
    # A work host that lists a personal source fails evaluation.
    workHostRejectsPersonal = throws (agents.resolve {
      sources = [ shared personal ];
      presets = [ "base" ];
      targets = [ "claude" ];
      zone = "work";
    });
    # And a work host with shared + work resolves, with nothing personal,
    # under the work zone's deny-unlisted policy.
    workHostClean =
      let
        r = workHost [ work ] [ "work" ];
      in
      lib.all (l: l.repo != "personal") r.links
      && lib.any (l: l.name == "gamma") r.links
      && lib.any (l: l.name == "alpha" && l.repo == "shared") r.links;
    # Work policy: each of these fails `piste build`.
    # A third-party skill from a source the policy does not allow (and unlocked).
    workPolicyRejectsSkill = throws (workHost [ work ] [ "base" "work" ]);
    # A marketplace the work zone declares itself, which its policy does not
    # allow (checked whether or not a plugin uses it).
    workPolicyRejectsMarketplace = throws (workHost [ workbad ] [ "workbad" ]);
    # The shared catalog's marketplaces no plugin of the zone uses ("catalog",
    # "codexcat", sources outside the work allow-list) are not the zone's:
    # the work host resolves, and they are not among its marketplaces.
    workIgnoresUnusedCatalogMarketplace =
      !(throws { inherit (workHost [ work ] [ "work" ]) links deferred instructions; })
      && marketsOf [ shared work ] == { claude = [ ]; codex = [ ]; };
    # A zone-declared marketplace counts even with no plugin using it.
    zoneMarketplaceUsed = marketsOf [ shared workbad ] == { claude = [ "sneaky" ]; codex = [ ]; };
    # Once a zone plugin names a catalog marketplace it is the zone's, and the
    # deny policy refuses its unlisted source.
    workPolicyRejectsUsedCatalogMarketplace =
      marketsOf [ shared workuse ] == { claude = [ "catalog" ]; codex = [ ]; }
      && throws (workHost [ workuse ] [ "workuse" ]);
    # An overlay's own root beside the zone's (a team layer's), with
    # [policy] extends = true: its allow entries join the zone's policy,
    # which is otherwise the layer's, and its own marketplace resolves.
    ownRootAdds =
      !(throws { inherit (workHost [ work workown ] [ "work" ]) links deferred instructions; })
      && agents.zonePolicy [ shared work workown ] "work" == {
        allow = [ "claude:market" "github:example/own" ];
        unlisted = "deny";
      };
    # The zone's deny holds for it: without its allow entry, refused.
    ownRootUnderZonePolicy = throws (workHost [ work workownbare ] [ "work" ]);
    # It can't loosen the zone's policy (unlisted and the rest are the
    # layer's), and a second root with a policy of its own is refused.
    ownRootCantSetPolicy = throws (workHost [ work workownloose ] [ "work" ]);
    twoPolicyRoots = throws (workHost [ work workbad ] [ "work" ]);
    # Any installer, even from an allowed source.
    workPolicyRejectsInstaller = throws (workHost [ workinst ] [ "workinst" ]);
    # An npx MCP server without an exact version.
    workPolicyRejectsLooseMcp = throws (workHost [ workmcp ] [ "workmcp" ]);
    # The personal zone's warn policy lets the same shared declarations through.
    personalPolicyWarns = !(throws { inherit (personalHost) links deferred instructions; });
    # The same skill name in two zones is an eval error.
    skillCollision = throws (agents.resolve {
      sources = [ shared collide ];
      presets = [ "collide" ];
      targets = [ "claude" ];
      zone = "personal";
    });
    # A preset naming an unknown skill is an error, not a silent drop.
    unknownSkill = throws (agents.resolve {
      sources = [ shared personal ];
      presets = [ "typo" ];
      targets = [ "claude" ];
      zone = "personal";
    });
    # A hermes-only preset on a workstation reaches no harness: error.
    hermesPresetOnWorkstation = throws (agents.resolve {
      sources = [ shared personal ];
      presets = [ "hermes-x" ];
      targets = [ "claude" "codex" ];
      zone = "personal";
    });
    # Hooks: a preset's hook reaches the harnesses both it and the
    # hook target; the command is the stable per-user bin path; `replaces` rides along.
    hooksResolve =
      let
        h = (hookHost [ "hooks-claude" ] [ "claude" "codex" ]).hooks;
        e = lib.head h.entries;
      in
      lib.length h.entries == 1
      && e.name == "guard"
      && e.harnesses == [ "claude" ]
      && e.eventName == "PreToolUse"
      && e.timeout == 10
      && e.command == "\"$HOME/.local/share/piste-agents/hooks/bin/guard\""
      && h.replaces.claude_plugins == [ "old@market" ];
    # No preset lists hooks: nothing rendered, nothing replaced.
    hooksNone =
      let
        h = (hookHost [ "base" ] [ "claude" "codex" ]).hooks;
      in
      h.entries == [ ] && h.replaces.claude_plugins == [ ];
    # A hook routed to a harness whose capabilities lack hooks is an error.
    hookWithoutCapability = throws (hookHost [ "hooks-both" ] [ "claude" "codex" ]).hooks;
    # ...but fine on a claude-only host.
    hookClaudeOnlyHost = (lib.head (hookHost [ "hooks-both" ] [ "claude" ]).hooks.entries).harnesses == [ "claude" ];
    # A preset naming an undeclared hook is an error.
    unknownHook = throws (hookHost [ "hooks-typo" ] [ "claude" ]).hooks;
    # An unknown event name is an error.
    unknownHookEvent = throws (agents.resolve {
      sources = [ shared badhook ];
      presets = [ "badhook" ];
      targets = [ "claude" ];
      zone = "personal";
    }).hooks;
    # A lock entry that no longer matches its declaration fails the switch.
    staleLock = throws (agents.resolve {
      sources = [ shared stale ];
      presets = [ "base" ];
      targets = [ "claude" ];
      zone = "personal";
    });
    # A lock entry nobody declares fails too.
    orphanLock = throws (agents.resolve {
      sources = [ shared orphan ];
      presets = [ "base" ];
      targets = [ "claude" ];
      zone = "personal";
    });
    # A profile preset is not a host preset.
    profileAsHostPreset = throws (agents.resolve {
      sources = [ shared personal ];
      presets = [ "lean" ];
      targets = [ "claude" ];
      zone = "personal";
    });
    # A profile may only enable declared plugins.
    profileUndeclaredPlugin = throws (agents.resolveProfile {
      sources = [ shared personal ];
      preset = "badprofile";
    });
    # Missing preset.
    unknownPreset = throws (agents.resolve {
      sources = [ shared ];
      presets = [ "nope" ];
      targets = [ "claude" ];
      zone = "personal";
    });
  };
}
