# piste.agents: deploy skills + instructions from the agent zones to
# Claude Code, Codex and ~/.agents.
#
# Structure (which skills, presets, fragments exist) is read from each
# source's *store* copy, so evaluation stays pure. Skill links point at the
# *working checkout* via mkOutOfStoreSymlink, so editing a SKILL.md is live
# with no rebuild; adding or removing a skill changes the link map and needs
# one. home-manager owns those links (and removes only links it created).
#
# Instructions are composed at build time (lib/agents.nix) and spliced into a
# marked block of ~/.claude/CLAUDE.md and ~/.codex/AGENTS.md by `piste-agents
# apply` during activation. Everything outside the block is left untouched,
# since both harnesses and the user write those files.
#
# Never sets force = true: a collision with another owner (an npx copy, another
# launcher, a plugin) fails activation instead of clobbering it.
{ config, lib, pkgs, ... }:

let
  cfg = config.piste.agents;

  # Zone names: any lowercase name; the same rule as the Go
  # engine's zone.ValidName. No ':' — item ids are <zone>:<kind>/<name>.
  # "shared" is the engine's own zone, never a host's.
  zoneName = lib.types.strMatching "^[a-z][a-z0-9-]*$";
  hostZone = lib.types.addCheck zoneName (z: z != "shared") // {
    description = ''a zone name other than "shared" (lowercase letters, digits, dashes)'';
  };
  agentsLib = import ../../lib/agents.nix { inherit lib; };

  jsonFormat = pkgs.formats.json { };

  resolved = agentsLib.resolve {
    sources = lib.attrValues cfg.sources;
    inherit (cfg) presets targets zone;
  };

  needsRtk =
    lib.any (e: e.name == "rtk-rewrite") resolved.hooks.entries
    || lib.any (i: lib.any (f: baseNameOf f == "rtk.md") i.fragments) resolved.instructions;

  home = config.home.homeDirectory;
  sourcesByName = cfg.sources;

  blockFile =
    i: pkgs.writeText "piste-agents-${i.harness}-block.md" i.block;

  manifest = {
    version = 2;
    inherit (cfg) zone presets targets;
    sources = lib.mapAttrsToList (_: s: { inherit (s) name zone workdir; }) cfg.sources;
    links = map (e: {
      inherit (e) target kind name harness repo;
      source = linkSource e;
      store = e ? fetch;
    }) resolved.links;
    instructions = map (i: {
      inherit (i) target harness fragments;
      block = "${blockFile i}";
      sha256 = builtins.hashString "sha256" i.block;
    }) resolved.instructions;
    # Third-party skills a preset names that have no agents.lock entry yet.
    # `piste-agents check` lists them; nothing is deployed for them.
    inherit (resolved) deferred;
    # Read by `piste-agents sync` (carrier install, Codex layer + trust) and
    # `check`. Present even when empty, so sync can retract what it owned.
    hooks = hooksManifest;
    # The zone manifests and locks this generation was built from (store
    # copies), read by `piste-agents check/sync` for plugins, marketplaces,
    # tools, hooks and MCP pins. Explicit roots only.
    thirdParty = lib.concatMap (
      s:
      lib.concatMap (
        f:
        lib.optional (builtins.pathExists (s.store + "/${f}")) {
          source = s.name;
          file = f;
          path = "${s.store}/${f}";
        }
      ) [ "agents.base.toml" "agents.toml" "agents.lock" ]
    ) (lib.attrValues cfg.sources);
    profiles = lib.mapAttrsToList (name: preset: {
      inherit name preset;
      target = ".claude/profiles/${name}.json";
    }) cfg.claudeProfiles;
  };

  # Locked third-party skill: a fixed-output fetch at the locked commit,
  # sparse to the skill dir for big monorepos. Store paths are
  # immutable, so edits never happen in place; `piste agents update` bumps them.
  fetchLocked =
    f:
    pkgs.fetchgit (
      { inherit (f) url rev hash; } // lib.optionalAttrs (f.sparse != [ ]) { sparseCheckout = f.sparse; }
    );

  linkSource =
    e:
    if e ? fetch then
      (if e.rel == "." then "${fetchLocked e.fetch}" else "${fetchLocked e.fetch}/${e.rel}")
    else
      "${sourcesByName.${e.repo}.workdir}/${e.rel}";

  # The registry: every item this host's zones offer, the
  # locked third-party skills included, for `piste agents search/show/add` in
  # any project. Linked at ~/.local/share/piste-agents/registry.
  registry = (import ../../lib/agents-registry.nix { inherit lib; }).mkAgentRegistry {
    inherit pkgs;
    inherit (cfg) zone;
    sources = lib.attrValues cfg.sources;
  };

  profileFiles = lib.mapAttrs (
    name: preset:
    jsonFormat.generate "claude-profile-${name}.json" (
      agentsLib.resolveProfile {
        sources = lib.attrValues cfg.sources;
        inherit preset;
      }
    )
  ) cfg.claudeProfiles;

  manifestFile = jsonFormat.generate "piste-agents-manifest.json" manifest;

  # --- hooks (agents/hooks.toml) ---------------------------
  hooks = resolved.hooks;
  hooksFor = h: lib.filter (e: lib.elem h e.harnesses) hooks.entries;

  # One wrapper per script, with its interpreter and tools pinned. The harness
  # command is the stable ~/.local/share/piste-agents/hooks/bin/<name> link to it,
  # so a rebuild changes neither the Claude carrier nor the Codex trust hash.
  # rtk only for its own hook: a guard-only host doesn't carry the unfree binary.
  hookDeps = e: [ pkgs.jq pkgs.gitleaks ] ++ lib.optional (e.name == "rtk-rewrite" && pkgs ? rtk) pkgs.rtk;
  hookBin =
    e:
    let
      script = "${sourcesByName.${e.source}.store}/${e.rel}";
      interpreter =
        if lib.hasSuffix ".py" e.script then "${pkgs.python3}/bin/python3"
        else if lib.hasSuffix ".sh" e.script then "${pkgs.bash}/bin/bash"
        else throw "piste.agents: hook script ${e.rel} needs a .py or .sh suffix";
    in
    pkgs.writeShellScriptBin e.bin ''
      # git from the user's PATH first (their config/credentials), ours as a fallback.
      export PATH="${lib.makeBinPath (hookDeps e)}:$PATH:${lib.makeBinPath [ pkgs.git pkgs.coreutils ]}"
      export PISTE_GUARD_GITLEAKS="${pkgs.gitleaks}/bin/gitleaks"
      exec ${interpreter} ${script} "$@"
    '';
  hookBins = lib.listToAttrs (map (e: lib.nameValuePair e.bin (hookBin e)) hooks.entries);

  # PascalCase event -> matcher groups (entries sorted by hook name).
  hookGroups =
    h:
    lib.foldl' (
      acc: e:
      acc
      // {
        ${e.eventName} = (acc.${e.eventName} or [ ]) ++ [
          {
            inherit (e) matcher;
            hooks = [
              {
                type = "command";
                inherit (e) command timeout;
              }
            ];
          }
        ];
      }
    ) { } (hooksFor h);

  claudeHooks = { hooks = hookGroups "claude"; };
  claudeHooksJson = builtins.toJSON claudeHooks;
  # Version = content hash, so `piste agents sync` can tell the installed copy is stale.
  carrierVersion = "0.0.0-${builtins.substring 0 12 (builtins.hashString "sha256" claudeHooksJson)}";
  carrierName = "piste-harness";
  marketplaceName = "piste-local";

  # A one-plugin local marketplace (rulesync's plugin layout + the
  # .claude-plugin manifests it lacks).
  claudeMarketplace = pkgs.runCommand "piste-harness-marketplace" { } ''
    mkdir -p $out/.claude-plugin $out/${carrierName}/.claude-plugin $out/${carrierName}/hooks
    cp ${jsonFormat.generate "marketplace.json" {
      name = marketplaceName;
      owner.name = "piste";
      description = "Generated by piste.agents; do not edit.";
      plugins = [
        {
          name = carrierName;
          source = "./${carrierName}";
          description = "Owned harness hooks from piste agents/hooks.toml";
          version = carrierVersion;
        }
      ];
    }} $out/.claude-plugin/marketplace.json
    cp ${jsonFormat.generate "plugin.json" {
      name = carrierName;
      version = carrierVersion;
      description = "Owned harness hooks from piste agents/hooks.toml (rtk rewrite, piste-guard). Generated by piste.agents; do not edit.";
    }} $out/${carrierName}/.claude-plugin/plugin.json
    cp ${jsonFormat.generate "hooks.json" claudeHooks} $out/${carrierName}/hooks/hooks.json
  '';

  hooksManifest = {
    bin_dir = hooks.hookBinDir;
    inherit (hooks) replaces;
    claude = lib.optionalAttrs (lib.elem "claude" cfg.targets && hooksFor "claude" != [ ]) {
      marketplace = "${home}/.local/share/piste-agents/claude-marketplace";
      marketplace_name = marketplaceName;
      plugin = "${carrierName}@${marketplaceName}";
      version = carrierVersion;
      hooks = claudeHooks.hooks;
    };
    codex = lib.optionalAttrs (lib.elem "codex" cfg.targets && hooksFor "codex" != [ ]) {
      target = hooks.codexTarget;
      hooks = hookGroups "codex";
    };
  };

  # The engine: every piste-agents subcommand runs in the piste
  # binary. PISTE_AGENTS_TOOL keeps it the plain tool (sync without the
  # `piste mcp sync` step).
  piste-agents = pkgs.writeShellScriptBin "piste-agents" ''
    export PISTE_AGENTS_TOOL=1
    exec ${(import ../../cli { inherit pkgs; }).piste-go}/bin/piste agents "$@"
  '';
in
{
  imports = [ ./checkout.nix ];

  options.piste.agents = {
    enable = lib.mkEnableOption "agent skills + instructions from the piste agent zones";

    zone = lib.mkOption {
      type = hostZone;
      example = "work";
      description = ''
        This host's trust zone: any name, e.g. "personal", "work", "agent".
        Every source must be "shared" or this zone;
        anything else fails evaluation.
      '';
    };

    sources = lib.mkOption {
      description = ''
        Agent zone roots. `store` is read at eval time (structure); `workdir`
        is the live checkout the skill links point at. Both are the same
        `agents/` directory of one repo.
      '';
      default = { };
      type = lib.types.attrsOf (
        lib.types.submodule (
          { name, ... }:
          {
            options = {
              name = lib.mkOption {
                type = lib.types.str;
                default = name;
                description = "Source identifier, recorded in the manifest.";
              };
              zone = lib.mkOption {
                type = zoneName;
                description = "Trust zone of this source: \"shared\" or the host's zone.";
              };
              store = lib.mkOption {
                type = lib.types.path;
                description = "The agents/ directory as a store path. Read at eval time.";
              };
              workdir = lib.mkOption {
                type = lib.types.str;
                description = "Absolute path to the same agents/ directory in the working checkout. Link target only.";
              };
              capabilities = lib.mkOption {
                type = lib.types.bool;
                default = false;
                description = "Whether this source provides capabilities.toml.";
              };
            };
          }
        )
      );
    };

    presets = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ "base" ];
      description = "Preset names this host receives, resolved across all sources.";
      example = [ "base" "personal" ];
    };

    targets = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ "claude" "codex" ];
      description = "Harnesses to deploy to on this host.";
    };

    registry.enable = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Build the registry of this host's zones (`piste agents registry build`)
        and link it at ~/.local/share/piste-agents/registry, where `piste agents
        search/show/add` read it. `--workdir` reads the zone
        checkouts live instead.
      '';
    };

    claudeProfiles = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      example = { lean = "lean"; };
      description = ''
        Claude settings layers for `claude --settings`, name -> a preset with
        `kind = "claude-profile"`. Written to ~/.claude/profiles/<name>.json.
      '';
    };

    resolved = lib.mkOption {
      type = lib.types.raw;
      readOnly = true;
      internal = true;
      description = "The resolved link map and instruction blocks (for tests).";
    };
  };

  config = lib.mkMerge [
    {
      # The shared zone ships with this module.
      piste.agents.sources.piste-agents = {
        zone = "shared";
        store = lib.mkDefault ../../agents;
        workdir = lib.mkDefault "${config.piste.checkout}/agents";
        capabilities = lib.mkDefault true;
      };
      piste.agents.resolved = resolved;
    }

    (lib.mkIf cfg.enable {
      # piste-agents is not installed: `piste agents ...` does the same from the
      # piste on PATH. The activation script below runs the store copy.
      # rtk (unfree) comes with whatever resolves the rtk-rewrite hook or the
      # rtk instruction, whichever preset names them.
      home.packages = lib.optional (needsRtk && pkgs ? rtk) pkgs.rtk;

      home.file =
        builtins.listToAttrs (
          map (e: {
            name = e.target;
            # Owned skills link the live checkout; locked third-party skills
            # link their store fetch.
            value.source = if e.store then e.source else config.lib.file.mkOutOfStoreSymlink e.source;
          }) manifest.links
        )
        // lib.mapAttrs' (
          name: drv: lib.nameValuePair "${hooks.hookBinDir}/${name}" { source = "${drv}/bin/${name}"; }
        ) hookBins
        // lib.optionalAttrs (hooksManifest.claude != { }) {
          ".local/share/piste-agents/claude-marketplace".source = claudeMarketplace;
        }
        // lib.mapAttrs' (
          name: file: lib.nameValuePair ".claude/profiles/${name}.json" { source = file; }
        ) profileFiles
        // {
          # The declared state, read by `piste-agents apply` and `piste-agents check`.
          ".config/piste-agents/manifest.json".source = manifestFile;
        };

      # After home-manager has placed the links. Splices only the marked
      # block; refuses symlinked or store targets; never fails the switch
      # (a refusal is a warning here and a red `piste agents check`).
      home.activation.pisteAgentsInstructions = lib.hm.dag.entryAfter [ "linkGeneration" ] ''
        run ${piste-agents}/bin/piste-agents apply --manifest ${manifestFile} \
          || echo "piste-agents: instruction splice reported problems; run 'piste agents check'" >&2
      '';
    })

    (lib.mkIf (cfg.enable && cfg.registry.enable) {
      home.file.".local/share/piste-agents/registry".source = registry;
    })
  ];
}
