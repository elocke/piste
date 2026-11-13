# Pure resolver for the piste.agents module.
#
# Given the zone sources, the host's presets and harness targets, it decides
# every link and every composed instruction block. The module turns the result
# into home.file entries + an activation splice; the flake checks feed it
# fixture zones and compare the result against golden files. Nothing here
# touches $HOME or reads anything but the explicit `store` roots.
#
# This file is the resolver the Nix module uses; `piste agents` does the
# splice and `check`.
{ lib }:

let
  inherit (builtins)
    attrNames
    fromTOML
    readDir
    readFile
    pathExists
    ;

  # Read a source as `store + "/rel"`, never "${store}/rel": under lazy trees
  # (Determinate Nix) the interpolation is a virtual store path, and
  # pathExists/readFile on it must realise it first, which
  # `nix flake check --no-build` refuses ("path ... is not valid").

  # Split "---\n<frontmatter>\n---\n<body>" by lines. Nix regexes don't span
  # newlines portably, so this walks lines instead.
  splitFrontmatter =
    text:
    let
      lines = lib.splitString "\n" text;
      closing = lib.lists.findFirstIndex (l: l == "---") null (lib.drop 1 lines);
    in
    if lines == [ ] || lib.head lines != "---" || closing == null then
      {
        fm = [ ];
        body = text;
      }
    else
      {
        fm = lib.sublist 1 closing lines;
        body = lib.concatStringsSep "\n" (lib.drop (closing + 2) lines);
      };

  fmValue =
    fm: key:
    let
      hits = lib.filter (m: m != null) (map (builtins.match "${key}:[[:space:]]*(.*)") fm);
    in
    if hits == [ ] then null else lib.head (lib.head hits);

  trim = s: lib.strings.trim s;
in
rec {
  inherit splitFrontmatter;

  # name -> "skills/<category>/<name>" for every owned skill in a source.
  ownedSkills =
    src:
    let
      root = (src.store + "/skills");
      isDir = t: t == "directory";
      cats = lib.filterAttrs (n: t: isDir t && !lib.hasPrefix "." n) (
        if pathExists root then readDir root else { }
      );
    in
    lib.foldl' (
      acc: cat:
      let
        entries = lib.filterAttrs (
          n: t: isDir t && !lib.hasPrefix "." n && pathExists (root + "/${cat}/${n}/SKILL.md")
        ) (readDir (root + "/${cat}"));
      in
      lib.foldl' (
        acc': n:
        if acc' ? ${n} then
          throw "piste.agents: skill '${n}' appears twice in source '${src.name}' (${acc'.${n}} and skills/${cat}/${n})"
        else
          acc' // { ${n} = "skills/${cat}/${n}"; }
      ) acc (attrNames entries)
    ) { } (attrNames cats);

  # The zone manifests of a source: shared defaults and the
  # zone's own third-party declarations.
  manifestFiles = [ "agents.base.toml" "agents.toml" ];

  # Third-party [[skill]] declarations (agents.base.toml / agents.toml).
  thirdPartyDecls =
    src:
    lib.concatMap (
      f:
      let
        p = (src.store + "/${f}");
      in
      if pathExists p then (fromTOML (readFile p)).skill or [ ] else [ ]
    ) manifestFiles;

  thirdPartySkills = src: map (s: s.name) (thirdPartyDecls src);

  # Third-party [[claude.plugin]] ids declared by a source.
  claudePlugins =
    src:
    lib.concatMap (
      f:
      let
        p = (src.store + "/${f}");
      in
      if pathExists p then map (x: x.id) ((fromTOML (readFile p)).claude.plugin or [ ]) else [ ]
    ) manifestFiles;

  # The [policy] table of a zone's own agents.toml. Only the
  # host zone's sources count: the shared agents.base.toml policy never widens
  # a zone's allow-list. One source of the zone sets the policy; another
  # (an overlay's own agents/ beside its team layer's) may say
  # `[policy] extends = true` and add allow entries, nothing else. The Go
  # `piste agents` (userscope.NewZone) applies the same rule.
  zonePolicy =
    sources: zone:
    let
      ps = lib.concatMap (
        s:
        let
          p = (s.store + "/agents.toml");
          parsed = if s.zone == zone && pathExists p then fromTOML (readFile p) else { };
        in
        lib.optional (parsed ? policy) { inherit (s) name; inherit (parsed) policy; }
      ) sources;
      adds = lib.filter (p: p.policy.extends or false) ps;
      roots = lib.filter (p: !(p.policy.extends or false)) ps;
      loose = lib.filter (p: lib.attrNames (removeAttrs p.policy [ "extends" "allow" ]) != [ ]) adds;
      root = if roots == [ ] then { } else (lib.head roots).policy;
    in
    if lib.length roots > 1 then
      throw "piste.agents: more than one ${zone} source sets the zone's [policy] (${
        lib.concatMapStringsSep ", " (p: p.name) roots
      }); another source of the zone adds to it with [policy] extends = true"
    else if loose != [ ] then
      throw "piste.agents: ${(lib.head loose).name}'s [policy] extends the ${zone} zone's, so it may only add allow entries (it sets ${
        lib.concatStringsSep ", " (lib.attrNames (removeAttrs (lib.head loose).policy [ "extends" "allow" ]))
      })"
    else if adds == [ ] then
      root
    else
      root // { allow = (root.allow or [ ]) ++ lib.concatMap (p: p.policy.allow or [ ]) adds; };

  # allow patterns: a trailing * matches any suffix, anything else is exact.
  # nix: sources are built from the flake and always allowed.
  policyAllows =
    policy: source:
    lib.hasPrefix "nix:" source
    || lib.any (
      pat: if lib.hasSuffix "*" pat then lib.hasPrefix (lib.removeSuffix "*" pat) source else pat == source
    ) (policy.allow or [ ]);

  # The package an npx / uvx MCP server runs: { source, version } or null.
  mcpPackage =
    m:
    let
      cmd = baseNameOf (m.command or "");
      args = m.args or [ ];
      valued = [ "--with" "--from" "--python" "-p" "--index" "--index-url" "--extra-index-url" ];
      # uvx: the first positional argument, skipping flags and their values.
      positional = (lib.foldl' (
        acc: a:
        if acc.found != null then acc
        else if acc.skip then acc // { skip = false; }
        else if lib.elem a valued then acc // { skip = true; }
        else if lib.hasPrefix "-" a then acc
        else acc // { found = a; }
      ) { found = null; skip = false; } args).found;
      npmSpec = lib.findFirst (a: !lib.hasPrefix "-" a) null args;
    in
    if lib.elem cmd [ "npx" "pnpx" "bunx" ] && npmSpec != null then
      let
        v = builtins.match "(.+)@([^@]*)" npmSpec;
      in
      if v == null then { source = "npm:${npmSpec}"; version = null; }
      else { source = "npm:${lib.elemAt v 0}"; version = lib.elemAt v 1; }
    else if cmd == "uvx" && positional != null then
      let
        v = builtins.match "([^=]+)==(.+)" positional;
      in
      if v == null then { source = "pypi:${positional}"; version = null; }
      else { source = "pypi:${lib.elemAt v 0}"; version = lib.elemAt v 1; }
    else
      null;

  # Every manifest of the sources, parsed, tagged `_file` = "<source>/<file>".
  manifestDecls =
    sources:
    lib.concatMap (
      s:
      lib.concatMap (
        f:
        let
          p = (s.store + "/${f}");
        in
        lib.optional (pathExists p) ((fromTOML (readFile p)) // { _file = "${s.name}/${f}"; })
      ) manifestFiles
    ) sources;

  # The marketplaces a zone uses, from its manifestDecls. A
  # [[claude.marketplace]] / [[codex.marketplace]] is used if the zone
  # declares it itself (agents.toml) or a plugin of the same harness in any
  # manifest names it (`x@<name>`). The shared catalog's (agents.base.toml)
  # other marketplaces are not this zone's: they are neither policy-checked
  # nor installed for it, like an unused [[skill]]. The Go `piste agents`
  # (userscope.NewZone, engine.UsedMarketplace) applies the same rule.
  usedMarketplaces =
    decls:
    let
      all = k: lib.concatMap (d: map (x: x // { _file = d._file; }) (k d)) decls;
      marketOf = p: lib.last (lib.splitString "@" p.id);
      used =
        markets: plugins:
        let
          named = map marketOf plugins;
        in
        lib.filter (
          m: !(lib.hasSuffix "/agents.base.toml" m._file) || lib.elem m.name named
        ) markets;
    in
    {
      claude = used (all (d: d.claude.marketplace or [ ])) (all (d: d.claude.plugin or [ ]));
      codex = used (all (d: d.codex.marketplace or [ ])) (all (d: d.codex.plugin or [ ]));
    };

  # Every third-party item declared in the sources that a deny-unlisted zone
  # policy refuses, as messages. `usedSkills` are the third-party skills the
  # host's presets select; they must be allowed and locked. Marketplaces count
  # when the zone uses them (usedMarketplaces). Plugins, MCP, tools and
  # installers count whether a preset names them or not, since `piste agents
  # sync` installs every declaration.
  policyViolations =
    {
      sources,
      policy,
      usedSkills,
      declByName,
      locked,
    }:
    let
      decls = manifestDecls sources;
      all = k: lib.concatMap (d: map (x: x // { _file = d._file; }) (k d)) decls;
      markets = usedMarketplaces decls;
      marketNames = map (m: m.name) markets.claude;
      bad = cond: msg: lib.optional cond msg;
      installersOk = policy.installers or false;
    in
    lib.concatMap (
      n:
      let
        d = declByName.${n};
      in
      bad (!policyAllows policy d.source) "skill ${n}: source ${d.source} is not in [policy] allow"
      ++ bad (!(locked ? ${n})) "skill ${n}: not locked in agents.lock (require = commit)"
    ) usedSkills
    ++ map (m: "${m._file}: marketplace ${m.name}: source ${m.source} is not in [policy] allow") (
      lib.filter (m: !policyAllows policy m.source) (markets.claude ++ markets.codex)
    )
    ++ lib.concatMap (
      p:
      let
        mkt = lib.last (lib.splitString "@" p.id);
      in
      bad (!(lib.elem mkt marketNames) && !policyAllows policy "claude:${mkt}")
        "${p._file}: claude plugin ${p.id}: marketplace ${mkt} is neither declared nor allowed"
    ) (all (d: d.claude.plugin or [ ]))
    ++ lib.concatMap (
      p:
      let
        mkt = lib.last (lib.splitString "@" p.id);
      in
      bad (!policyAllows policy "codex:${mkt}") "${p._file}: codex plugin ${p.id}: codex:${mkt} is not in [policy] allow"
    ) (all (d: d.codex.plugin or [ ]))
    ++ lib.concatMap (
      t: bad (!policyAllows policy (t.source or "")) "${t._file}: tool ${t.name}: source ${t.source or "(none)"} is not in [policy] allow"
    ) (all (d: d.tool or [ ]))
    ++ lib.concatMap (
      h:
      bad (!installersOk) "${h._file}: hook ${h.name}: installer ${h.installer}: this zone allows no installers"
      ++ bad (installersOk && !policyAllows policy h.installer) "${h._file}: hook ${h.name}: installer ${h.installer} is not in [policy] allow"
    ) (lib.filter (h: h ? installer) (all (d: d.hook or [ ])))
    ++ lib.concatMap (
      m:
      let
        pkg = mcpPackage m;
      in
      lib.optionals (pkg != null) (
        bad (!policyAllows policy pkg.source) "${m._file}: mcp ${m.name}: package ${pkg.source} is not in [policy] allow"
        ++ bad (pkg.version == null || pkg.version == "latest") "${m._file}: mcp ${m.name}: package ${pkg.source} has no exact version"
      )
    ) (all (d: d.mcp or [ ]));

  # Locked [[skill]] entries from a source's agents.lock (written by
  # `piste agents update`). One lock per zone; it also locks the
  # shared agents.base.toml items that zone uses.
  lockedSkills =
    src:
    let
      p = (src.store + "/agents.lock");
    in
    if pathExists p then (fromTOML (readFile p)).skill or [ ] else [ ];

  # "github:owner/repo" -> a fetchgit spec at the locked commit. The module
  # turns it into pkgs.fetchgit; sparse keeps a big monorepo to the skill dir.
  fetchSpec =
    entry:
    let
      m = builtins.match "github:([^/]+)/([^/]+)" entry.source;
      path = entry.path or ".";
    in
    if m == null then
      throw "piste.agents: locked skill '${entry.name}' has source '${entry.source}'; only github:owner/repo is supported"
    else if !(entry ? commit && entry ? narHash) then
      throw "piste.agents: locked skill '${entry.name}' lacks commit or narHash; re-run `piste agents update ${entry.name}`"
    else
      {
        url = "https://github.com/${lib.elemAt m 0}/${lib.elemAt m 1}.git";
        rev = entry.commit;
        hash = entry.narHash;
        sparse = if path == "." then [ ] else [ path ];
      };

  # Parse one instruction fragment.
  fragment =
    src: name:
    let
      p = (src.store + "/instructions/${name}.md");
      parts = splitFrontmatter (readFile p);
      targetsRaw = fmValue parts.fm "targets";
      orderRaw = fmValue parts.fm "order";
    in
    if targetsRaw == null then
      throw "piste.agents: instruction fragment '${name}' (${src.name}) declares no targets"
    else
      {
        inherit name;
        source = src.name;
        rel = "instructions/${name}.md";
        targets = builtins.fromJSON targetsRaw;
        order = if orderRaw == null then 0 else lib.toInt (trim orderRaw);
        body = trim parts.body;
      };

  # Replace whole-line `@file` imports with the file's text, for harnesses that
  # don't resolve imports (Codex). Looked up per harness first:
  # instructions/include/<harness>/<file>, then instructions/include/<file>.
  inlineImports =
    src: harness: body:
    lib.concatStringsSep "\n" (
      map (
        line:
        let
          m = builtins.match "@([^[:space:]]+)" line;
        in
        if m == null then
          line
        else
          let
            file = lib.head m;
            candidates = [
              (src.store + "/instructions/include/${harness}/${file}")
              (src.store + "/instructions/include/${file}")
            ];
            found = lib.filter pathExists candidates;
          in
          if found == [ ] then
            throw "piste.agents: '@${file}' in source '${src.name}' has no include for harness '${harness}' (looked in instructions/include/{${harness}/,})"
          else
            trim (readFile (lib.head found))
      ) (lib.splitString "\n" body)
    );

  sortFragments = lib.sort (a: b: if a.order == b.order then a.name < b.name else a.order < b.order);

  # The managed block body for one harness: fragments sorted by (order, name),
  # each preceded by a source marker (ruler-style), imports inlined if the
  # harness can't resolve them.
  composeBlock =
    {
      fragments,
      sourcesByName,
      harness,
      inline,
    }:
    let
      selected = sortFragments (lib.filter (f: lib.elem harness f.targets) fragments);
      render =
        f:
        let
          body = if inline then inlineImports sourcesByName.${f.source} harness f.body else f.body;
        in
        "<!-- source: ${f.source}/${f.rel} -->\n${body}";
    in
    lib.concatStringsSep "\n\n" (map render selected);

  # Canonical hook events (rulesync tool-hooks-converter vocabulary) -> the
  # PascalCase names Claude Code and Codex both use.
  hookEvents = {
    preToolUse = "PreToolUse";
    postToolUse = "PostToolUse";
    userPromptSubmit = "UserPromptSubmit";
    sessionStart = "SessionStart";
    stop = "Stop";
  };

  # Stable per-user path of a hook script's wrapper; the harness command.
  hookBinName = script: lib.head (lib.splitString "." script);
  hookBinDir = ".local/share/piste-agents/hooks/bin";
  hookCommand = script: "\"$HOME/${hookBinDir}/${hookBinName script}\"";

  # Hook declarations ([[hook]] in <source>/hooks.toml) by name, across all
  # sources; a name defined twice is an error.
  hookDecls =
    sources:
    lib.foldl' (
      acc: src:
      let
        p = (src.store + "/hooks.toml");
        parsed = if pathExists p then fromTOML (readFile p) else { };
      in
      lib.foldl' (
        acc': h:
        if acc'.byName ? ${h.name} then
          throw "piste.agents: hook '${h.name}' is defined in both '${acc'.byName.${h.name}.source}' and '${src.name}'"
        else if !(hookEvents ? ${h.event}) then
          throw "piste.agents: hook '${h.name}' (${src.name}) has unknown event '${h.event}'; known: ${lib.concatStringsSep ", " (attrNames hookEvents)}"
        else if !(pathExists (src.store + "/hooks/${h.script}")) then
          throw "piste.agents: hook '${h.name}' (${src.name}) names script hooks/${h.script}, which does not exist"
        else
          acc'
          // {
            byName = acc'.byName // {
              ${h.name} = h // {
                source = src.name;
                rel = "hooks/${h.script}";
              };
            };
          }
      ) (acc // {
        replaces = {
          claude_plugins = acc.replaces.claude_plugins ++ (parsed.replaces.claude_plugins or [ ]);
          hook_commands = acc.replaces.hook_commands ++ (parsed.replaces.hook_commands or [ ]);
        };
      }) (parsed.hook or [ ])
    ) {
      byName = { };
      replaces = {
        claude_plugins = [ ];
        hook_commands = [ ];
      };
    } sources;

  # The hooks a host gets: one entry per selected hook, with the harnesses it
  # reaches (preset targets ∩ host targets ∩ the hook's own targets).
  resolveHooks =
    {
      sources,
      capFor,
      selected,
    }:
    let
      decls = hookDecls sources;
      # A hook named by several presets: union of the harnesses.
      merged = lib.foldl' (
        acc: s:
        acc // { ${s.name} = lib.unique ((acc.${s.name} or [ ]) ++ s.harnesses); }
      ) { } selected;
      entry =
        name:
        let
          h =
            decls.byName.${name}
              or (throw "piste.agents: a preset names hook '${name}', which no hooks.toml declares");
          hs = lib.intersectLists (h.targets or [ "claude" "codex" ]) merged.${name};
        in
        builtins.deepSeq (map (x: capFor x "hooks") hs) {
          inherit name hs;
          inherit (h) event matcher script source rel;
          timeout = h.timeout or 10;
          eventName = hookEvents.${h.event};
          bin = hookBinName h.script;
          command = hookCommand h.script;
        };
      entries = lib.filter (e: e.hs != [ ]) (map entry (lib.sort (a: b: a < b) (attrNames merged)));
    in
    {
      entries = map (e: removeAttrs e [ "hs" ] // { harnesses = e.hs; }) entries;
      replaces = if entries == [ ] then { claude_plugins = [ ]; hook_commands = [ ]; } else decls.replaces;
      inherit hookBinDir;
      # Lazy: only read when a hook reaches Codex.
      codexTarget = (capFor "codex" "hooks").path;
    };

  # Everything the host gets. `sources` is a list of
  # { name, zone, store, workdir, capabilities }.
  resolve =
    {
      sources,
      presets,
      targets,
      zone,
    }:
    let
      sourcesByName = lib.listToAttrs (map (s: lib.nameValuePair s.name s) sources);

      # Trust zones: a work host never resolves personal content, and
      # the reverse. "shared" is allowed everywhere.
      zoneViolations = lib.filter (s: s.zone != "shared" && s.zone != zone) sources;

      capabilitySources = lib.filter (s: s.capabilities) sources;
      capabilities =
        if lib.length capabilitySources != 1 then
          throw "piste.agents: exactly one source must set capabilities = true (found ${toString (lib.length capabilitySources)})"
        else
          fromTOML (readFile ((lib.head capabilitySources).store + "/capabilities.toml"));

      # name -> { source, rel } across every source; a clash is an eval error.
      owned = lib.foldl' (
        acc: src:
        lib.foldl' (
          acc': n:
          if acc' ? ${n} then
            throw "piste.agents: skill '${n}' is defined in both '${acc'.${n}.source}' and '${src.name}'; names must be unique across zones"
          else
            acc' // { ${n} = { source = src.name; rel = (ownedSkills src).${n}; }; }
        ) acc (attrNames (ownedSkills src))
      ) { } sources;

      thirdParty = lib.unique (lib.concatMap thirdPartySkills sources);

      # name -> the declaration, for the lock consistency check below.
      declByName = lib.listToAttrs (
        map (d: lib.nameValuePair d.name d) (lib.concatMap thirdPartyDecls sources)
      );

      # name -> lock entry. A lock entry must match its declaration (source,
      # path, ref); otherwise the lock is stale and the switch fails, rather
      # than deploying something nobody declared.
      locked = lib.foldl' (
        acc: e:
        let
          d = declByName.${e.name} or null;
          same = k: (d.${k} or null) == (e.${k} or null);
        in
        if acc ? ${e.name} then
          throw "piste.agents: skill '${e.name}' is locked twice across agents.lock files"
        else if d == null then
          throw "piste.agents: agents.lock locks skill '${e.name}', which no agents.toml declares; re-run `piste agents update`"
        else if !(same "source" && same "ref" && (d.path or ".") == (e.path or ".")) then
          throw "piste.agents: agents.lock entry for '${e.name}' does not match its declaration (source/path/ref); re-run `piste agents update ${e.name}`"
        else
          acc // { ${e.name} = e; }
      ) { } (lib.concatMap lockedSkills sources);

      # Work policy: with unlisted = "deny", every third-party
      # item this host would install must come from an allowed source, so an
      # unapproved source fails `piste build`, not just `piste agents check`.
      policy = zonePolicy sources zone;
      policyErrors = lib.optionals ((policy.unlisted or "warn") == "deny") (policyViolations {
        inherit sources policy declByName locked;
        usedSkills = lib.unique (
          lib.filter (n: !(owned ? ${n}) && declByName ? ${n}) (lib.concatMap (p: p.skills or [ ]) resolved)
        );
      });

      findPreset =
        name:
        let
          hits = lib.filter (s: pathExists (s.store + "/presets/${name}.toml")) sources;
        in
        if hits == [ ] then
          throw "piste.agents: preset '${name}' not found in any configured source"
        else if lib.length hits > 1 then
          throw "piste.agents: preset '${name}' is defined in more than one source"
        else
          let
            src = lib.head hits;
            parsed = fromTOML (readFile (src.store + "/presets/${name}.toml"));
            known = [ "targets" "skills" "agents" "commands" "instructions" "hooks" ];
            unknown = lib.subtractLists known (attrNames parsed);
          in
          if unknown != [ ] then
            throw "piste.agents: preset '${name}' has key(s) ${lib.concatStringsSep ", " unknown} this module does not deploy"
          else
            parsed // { _name = name; _source = src; };

      resolved = map findPreset presets;

      harnessesOf =
        p:
        let
          hs = lib.intersectLists (p.targets or [ ]) targets;
          declared = (p.skills or [ ]) ++ (p.agents or [ ]) ++ (p.commands or [ ]) ++ (p.instructions or [ ]) ++ (p.hooks or [ ]);
        in
        if declared != [ ] && hs == [ ] then
          throw "piste.agents: preset '${p._name}' declares artifacts but reaches no harness on this host (targets ${builtins.toJSON (p.targets or [ ])}, host ${builtins.toJSON targets})"
        else
          hs;

      capFor =
        harness: kind:
        capabilities.${harness}.${kind}
          or (throw "piste.agents: harness '${harness}' does not support ${kind} (capabilities.toml)");

      # Per preset: link entries + deferred third-party names.
      perPreset =
        p:
        let
          hs = harnessesOf p;
          skillEntries = lib.concatMap (
            n:
            if owned ? ${n} then
              map (h: {
                kind = "skills";
                name = n;
                harness = h;
                repo = owned.${n}.source;
                rel = owned.${n}.rel;
                target = lib.replaceStrings [ "{name}" ] [ n ] (capFor h "skills").path;
              }) hs
            else if locked ? ${n} then
              # Third-party, locked: a store fetch at the locked commit,
              # linked like an owned skill.
              map (h: {
                kind = "skills";
                name = n;
                harness = h;
                repo = "third-party";
                rel = locked.${n}.path or ".";
                fetch = fetchSpec locked.${n};
                target = lib.replaceStrings [ "{name}" ] [ n ] (capFor h "skills").path;
              }) hs
            else if lib.elem n thirdParty then
              [ ]
            else
              throw "piste.agents: preset '${p._name}' names skill '${n}', which is neither an owned skill nor a declared [[skill]] in any source"
          ) (p.skills or [ ]);
          fileEntries =
            kind:
            lib.concatMap (
              n:
              let
                src = p._source;
                rel = "${kind}/${n}.md";
              in
              if !pathExists (src.store + "/${rel}") then
                throw "piste.agents: preset '${p._name}' names ${kind}/'${n}', absent from source '${src.name}'"
              else
                map (h: {
                  inherit kind rel;
                  name = n;
                  harness = h;
                  repo = src.name;
                  target = lib.replaceStrings [ "{name}" ] [ n ] (capFor h kind).path;
                }) hs
            ) (p.${kind} or [ ]);
        in
        {
          links = skillEntries ++ fileEntries "agents" ++ fileEntries "commands";
          # Declared but not locked yet: reported, not deployed.
          deferred = lib.filter (n: !(owned ? ${n}) && !(locked ? ${n})) (p.skills or [ ]);
          instructions = p.instructions or [ ];
          harnesses = hs;
          hooks = map (n: { name = n; harnesses = hs; }) (p.hooks or [ ]);
        };

      parts = map perPreset resolved;

      # The same artifact from two presets is fine (dedupe); two different
      # sources for one target path is not.
      links = lib.attrValues (
        lib.foldl' (
          acc: e:
          if acc ? ${e.target} && (acc.${e.target}.repo != e.repo || acc.${e.target}.rel != e.rel) then
            throw "piste.agents: two presets map different artifacts to ${e.target}"
          else
            acc // { ${e.target} = e; }
        ) { } (lib.concatMap (x: x.links) parts)
      );

      # Instruction fragments resolve by name across all sources (unique).
      fragmentNames = lib.unique (lib.concatMap (x: x.instructions) parts);
      findFragment =
        n:
        let
          hits = lib.filter (s: pathExists (s.store + "/instructions/${n}.md")) sources;
        in
        if hits == [ ] then
          throw "piste.agents: instruction fragment '${n}' not found in any source"
        else if lib.length hits > 1 then
          throw "piste.agents: instruction fragment '${n}' is defined in more than one source"
        else
          fragment (lib.head hits) n;
      fragments = map findFragment fragmentNames;

      instrHarnesses = lib.unique (
        lib.concatMap (x: lib.optionals (x.instructions != [ ]) x.harnesses) parts
      );
      instructions = map (
        h:
        let
          cap = capFor h "instructions";
        in
        {
          harness = h;
          target = cap.path;
          fragments = map (f: "${f.source}/${f.rel}") (
            sortFragments (lib.filter (f: lib.elem h f.targets) fragments)
          );
          block = composeBlock {
            inherit fragments sourcesByName;
            harness = h;
            inline = cap.inline_imports or false;
          };
        }
      ) instrHarnesses;
    in
    if zoneViolations != [ ] then
      throw "piste.agents: ${zone} host resolves source(s) from another zone: ${
        lib.concatMapStringsSep ", " (s: "${s.name} (${s.zone})") zoneViolations
      }"
    else if policyErrors != [ ] then
      throw "piste.agents: the ${zone} zone policy (unlisted = \"deny\") refuses:\n  ${lib.concatStringsSep "\n  " policyErrors}"
    else
      {
        links = lib.sort (a: b: a.target < b.target) links;
        deferred = lib.sort (a: b: a < b) (lib.unique (lib.concatMap (x: x.deferred) parts));
        inherit instructions;
        hooks = resolveHooks {
          inherit sources capFor;
          selected = lib.concatMap (x: x.hooks) parts;
        };
      };

  # A Claude settings layer for `claude --settings <file>`: a lean profile,
  # e.g. for an agent that needs few skills. The preset has
  # `kind = "claude-profile"` and names what stays on:
  #
  #   plugins = [ ids ]   every [[claude.plugin]] declared in a source is
  #                       listed in enabledPlugins, true only if named here
  #   skills  = [ names ] every owned or third-party skill of the sources is
  #                       set "off" in skillOverrides unless named here
  #   off     = [ names ] extra skill names to turn off (ones the zones don't
  #                       manage, e.g. claude.ai-synced skills)
  #
  # Only declared items are listed, so an undeclared plugin keeps whatever
  # the user settings say (`piste agents sync --user --prune` is what removes those).
  resolveProfile =
    { sources, preset }:
    let
      hits = lib.filter (s: pathExists (s.store + "/presets/${preset}.toml")) sources;
      src =
        if lib.length hits != 1 then
          throw "piste.agents: profile preset '${preset}' must exist in exactly one source (found ${toString (lib.length hits)})"
        else
          lib.head hits;
      p = fromTOML (readFile (src.store + "/presets/${preset}.toml"));
      known = [ "kind" "targets" "plugins" "skills" "off" ];
      unknown = lib.subtractLists known (attrNames p);
      declaredPlugins = lib.unique (lib.concatMap claudePlugins sources);
      allSkills = lib.unique (
        lib.concatMap (s: attrNames (ownedSkills s) ++ thirdPartySkills s) sources
      );
      badPlugins = lib.subtractLists declaredPlugins (p.plugins or [ ]);
      badSkills = lib.subtractLists allSkills (p.skills or [ ]);
    in
    if (p.kind or null) != "claude-profile" then
      throw "piste.agents: preset '${preset}' is not kind = \"claude-profile\""
    else if unknown != [ ] then
      throw "piste.agents: profile '${preset}' has unknown key(s) ${lib.concatStringsSep ", " unknown}"
    else if badPlugins != [ ] then
      throw "piste.agents: profile '${preset}' enables undeclared plugin(s) ${lib.concatStringsSep ", " badPlugins}"
    else if badSkills != [ ] then
      throw "piste.agents: profile '${preset}' keeps unknown skill(s) ${lib.concatStringsSep ", " badSkills}"
    else
      {
        enabledPlugins = lib.genAttrs declaredPlugins (id: lib.elem id (p.plugins or [ ]));
        skillOverrides = lib.genAttrs (
          lib.subtractLists (p.skills or [ ]) allSkills ++ (p.off or [ ])
        ) (_: "off");
      };
}
