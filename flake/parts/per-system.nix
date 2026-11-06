{ self, lib, inputs, ... }: {
  systems = [ "aarch64-darwin" "x86_64-linux" "aarch64-linux" ];

  perSystem = { pkgs, system, ... }:
  let
    # Zone-less base image: the container role and nothing else. The zones
    # build their own with piste.lib.mkDevcontainer; this one exists so
    # the shared layer can be built and smoke-tested on its own.
    devcontainer = self.lib.mkDevcontainer {
      inherit system;
      name = "piste-devcontainer";
    };
    # The Go piste and its runtime PATH (cli/default.nix).
    piste = import ../../cli { inherit pkgs; };
    # A home with the agents module and `modules` on top, for the module's
    # eval checks; `throws` forces a value and reports whether it failed.
    agentHome = modules: inputs.home-manager.lib.homeManagerConfiguration {
      inherit pkgs;
      modules = [
        self.homeManagerModules.agents
        {
          home.username = "t";
          home.homeDirectory = if pkgs.stdenv.hostPlatform.isDarwin then "/Users/t" else "/home/t";
          home.stateVersion = "24.05";
        }
      ] ++ modules;
    };
    throws = x: !(builtins.tryEval (builtins.deepSeq x x)).success;
    # A committed starter instance (tests/fixtures/template/*) as an overlay
    # of this engine.
    instance = dir: (import (dir + "/flake.nix")).outputs {
      self = { outPath = dir; };
      piste = self;
    };
    # docs/options.md: every option the engine declares, from a
    # home with every home module plus sops-nix and from the darwin starter
    # (Homebrew), in one list, declarations as links into the repo.
    # Built on x86_64-linux only, so one page fits every machine.
    docsOptions =
      let
        root = toString self.outPath + "/";
        home = inputs.home-manager.lib.homeManagerConfiguration {
          inherit pkgs;
          modules = lib.attrValues self.homeManagerModules ++ [
            inputs.sops-nix.homeManagerModules.sops
            { home = { username = "jane"; homeDirectory = "/home/jane"; stateVersion = "24.05"; }; }
          ];
        };
        # With the extras role's Mac side, which no starter lists.
        darwin = ((instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac.extendModules {
          modules = [ self.darwinModules.extras ];
        });
        options = lib.recursiveUpdateUntil (_: l: r: lib.isOption l || lib.isOption r) darwin.options home.options;
        ours = opt: opt.declarations != [ ]
          && lib.all (d: lib.hasPrefix root (toString d)) opt.declarations
          && !(lib.hasPrefix "home-manager.users." opt.name);
        doc = pkgs.nixosOptionsDoc {
          inherit options;
          transformOptions = opt: opt // {
            visible = opt.visible && ours opt;
            declarations = map (d:
              let rel = lib.removePrefix root (toString d);
              in { name = rel; url = "../${rel}"; }) opt.declarations;
          };
        };
        header = pkgs.writeText "options-header.md" ''
          # Options reference

          - Which `piste.*` settings can my overlay set, and what does each default to?
          - Which engine file declares an option, so I can read what it does?

          Generated from the engine's modules by `nix build .#docs-options` (on
          x86_64-linux); don't edit it by hand. A change to an option fails
          `checks.docs-options` until the page is rebuilt: `nix build .#docs-options
          && install -m 644 result docs/options.md`. Set these in your overlay's host
          files; [customising.md](customising.md) shows where. Any home-manager or
          nix-darwin option works there too.

        '';
      in
      pkgs.runCommand "options.md" { nativeBuildInputs = [ pkgs.python3 ]; } ''
        # One alphabetical list, an `enable` first under its parent (the
        # renderer's own order also floats every name starting "package").
        python3 - ${doc.optionsCommonMark} > body.md <<'EOF'
        import re, sys
        blocks = re.split(r"(?m)^(?=## \S+$)", open(sys.argv[1]).read())
        blocks = [b.rstrip("\n") + "\n" for b in blocks if b.strip()]
        name = lambda b: re.sub(r"\\(.)", r"\1", b.split("\n", 1)[0][3:])
        key = lambda b: [(c != "enable", c) for c in name(b).split(".")]
        print("\n\n".join(sorted(blocks, key=key)), end="")
        EOF
        cat ${header} body.md > $out
        # A default that prints a store path or a package changes with every
        # lock bump, and the bump PR's check would fail on it.
        if grep -n -e /nix/store/ -e '«derivation' -e '<derivation' $out >&2; then
          echo "docs-options: an option's default prints a store path or a package: give it a defaultText" >&2
          exit 1
        fi
        # Both evaluations reached the page: a home-only role, the darwin-only
        # Homebrew options, and none of the starter's own homes. The wiring
        # options (internal) are not on it.
        for h in '## piste\.agents\.enable' '## piste\.roles\.desktop\.ghostty\.enable' \
                 '## piste\.homebrew\.extraTaps'; do
          grep -qxF "$h" $out || { echo "docs-options: no '$h' on the page" >&2; exit 1; }
        done
        if grep -qF 'home-manager\.users' $out; then
          echo "docs-options: the page lists the darwin starter's home-manager.users.* options" >&2
          exit 1
        fi
        # One alphabetical list.
        line() { grep -nxF "$1" $out | cut -d: -f1; }
        if [ "$(line '## piste\.agents\.enable')" -gt "$(line '## piste\.packages\.brews\.enable')" ]; then
          echo "docs-options: the options aren't in alphabetical order" >&2
          exit 1
        fi
      '';
    # An attribute name on x86_64-linux, else null (no attribute). A name, not
    # a value: checks is a lazyAttrsOf, where mkIf false still leaves the
    # attribute, and optionalAttrs on system would stop flake-parts telling
    # that there's no formatter.
    onLinux = name: if system == "x86_64-linux" then name else null;
    # A host of zone z with only the shared zone: nothing but the option
    # type can refuse z (a zone source of its own would be refused by the
    # resolver guard first).
    bareHome = z: agentHome [ { piste.agents = { enable = true; zone = z; }; } ];
    # An `agent`-zone host over the fixture zone tests/fixtures/agents/agent.
    agentZone = {
      piste.agents = {
        enable = true;
        zone = "agent";
        presets = [ "base" "agent" ];
        sources.agent = {
          zone = "agent";
          store = ../../tests/fixtures/agents/agent;
          workdir = "/nonexistent";
        };
      };
    };
    # The static piste for machines without Nix:
    # piste-{linux,darwin}-{amd64,arm64} + SHA256SUMS, cross-compiled from the
    # same source and vendored modules as piste (CGO off: no runtime deps).
    # .github/workflows/piste-release.yml attaches it to a piste release.
    piste-release = pkgs.stdenvNoCC.mkDerivation {
      pname = "piste-release";
      inherit (piste.piste-go) version src;
      nativeBuildInputs = [ pkgs.go ];
      buildPhase = ''
        runHook preBuild
        export HOME=$TMPDIR GOCACHE=$TMPDIR/go-cache GOFLAGS="-mod=vendor -trimpath" CGO_ENABLED=0
        cp -r --no-preserve=mode ${piste.piste-go.goModules} vendor
        for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
          GOOS=''${t%/*} GOARCH=''${t#*/} go build \
            -ldflags "-s -w -X github.com/elocke/piste/cli/internal/cli.Version=${piste.piste-go.version}" \
            -o piste-''${t%/*}-''${t#*/} ./cmd/piste
        done
        sha256sum piste-* > SHA256SUMS
        runHook postBuild
      '';
      installPhase = ''
        mkdir -p $out
        cp piste-* SHA256SUMS $out/
      '';
      dontFixup = true; # static binaries, some for other platforms
      doInstallCheck = pkgs.stdenv.hostPlatform.system == "x86_64-linux";
      installCheckPhase = ''
        $out/piste-linux-amd64 --version
        $out/piste-linux-amd64 agents --help >/dev/null
      '';
    };
  in {
    packages = {
      inherit piste piste-release;
    } // lib.optionalAttrs pkgs.stdenv.hostPlatform.isDarwin {
      # nix-darwin's, at this engine's lock: piste runs it from the checkout on
      # a fresh Mac, before nix-darwin's first switch installs one.
      darwin-rebuild = inputs.darwin.packages.${system}.darwin-rebuild;
    } // lib.optionalAttrs pkgs.stdenv.hostPlatform.isLinux {
      devcontainer = devcontainer.image;
      devcontainer-load = devcontainer.load;
    };

    apps.piste = {
      type = "app";
      program = "${piste}/bin/piste";
    };
    # `nix run github:elocke/piste/stable -- setup`.
    apps.default = self.apps.${system}.piste;

    devShells.default = with pkgs; mkShell {
      # python3 runs the fake claude / codex the Go tests use.
      nativeBuildInputs = [ bashInteractive git age age-plugin-yubikey go gopls python3 ];
      shellHook = ''
        export EDITOR=vim
      '';
    };

    # Builds the Go piste with its tests on: the unit tests and the
    # end-to-end harness (goldens in cli/e2e/testdata/golden). The piste a
    # machine builds is the same Go build with the tests off (cli/default.nix), so a
    # test that is flaky in a user's sandbox can't fail their switch.
    checks.piste = piste.piste-go-tested;
    # piste's names: it is the default app and the package, and the system's
    # openssh and a tests-off build stay as they are.
    checks.piste-names =
      let
        isPiste = lib.attrNames self.apps.${system} == [ "default" "piste" ]
          && self.apps.${system}.default.program == self.apps.${system}.piste.program;
        home = (agentHome [
          self.homeManagerModules.dev
          inputs.sops-nix.homeManagerModules.sops
          { piste.agents = { enable = true; zone = "work"; }; }
        ]).config;
        init = home.programs.zsh.initContent;
        files = lib.attrNames home.home.file;
      in
      assert lib.assertMsg (lib.hasInfix "pisteup()" init)
        "piste-names: the zsh init has no pisteup()";
      assert lib.assertMsg (lib.elem ".config/piste-agents/manifest.json" files)
        "piste-names: the manifest is not .config/piste-agents/manifest.json";
      # piste is also the default app: `nix run github:elocke/piste/stable -- setup`.
      assert lib.assertMsg isPiste
        "piste-names: apps are ${toString (lib.attrNames self.apps.${system})}, want default (= piste) and piste";
      assert lib.assertMsg (self.packages.${system} ? piste)
        "piste-names: packages lack piste";
      # A fresh Mac has no darwin-rebuild until nix-darwin's first switch:
      # piste runs this one from the checkout.
      assert lib.assertMsg (self.packages.aarch64-darwin ? darwin-rebuild)
        "piste-names: the darwin packages lack darwin-rebuild";
      # Users build without tests, checks.piste runs them.
      assert lib.assertMsg (!(piste.piste-go.doCheck or false) && piste.piste-go-tested.doCheck)
        "piste-names: piste-go must have doCheck = false and piste-go-tested doCheck = true";
      # The system's ssh is the one ~/.ssh/config was written for.
      assert lib.assertMsg (!(lib.any (d: lib.getName d == "openssh") piste.runtimeDeps))
        "piste-names: openssh is back in runtimeDeps";
      pkgs.runCommand "check-piste-names" { } "touch $out";
    # piste.lib.mkPackDefault, a pack's (and a team layer's) priority: 950,
    # between a person's plain value (100), which wins over it, and the
    # engine's mkDefault (1000), which it wins over.
    checks.pack-default =
      let
        inherit (self.lib) mkPackDefault;
        value = defs: (lib.evalModules {
          modules = [ { options.v = lib.mkOption { type = lib.types.str; }; } ] ++ map (d: { v = d; }) defs;
        }).config.v;
      in
      assert lib.assertMsg ((mkPackDefault 1).priority == 950)
        "pack-default: mkPackDefault's priority is ${toString (mkPackDefault 1).priority}, not 950";
      assert lib.assertMsg (value [ (lib.mkDefault "engine") (mkPackDefault "pack") ] == "pack")
        "pack-default: a pack's value doesn't win over the engine's mkDefault";
      assert lib.assertMsg (value [ (mkPackDefault "pack") "mine" ] == "mine")
        "pack-default: a person's plain value doesn't win over a pack's";
      pkgs.runCommand "check-pack-default" { } "touch $out";
    # templates.default, instantiated: the committed instances
    # (what `piste init` renders for fixed answers; cli/internal/overlay's
    # TestRenderInstances keeps them equal) evaluate as overlays against this
    # engine: the linux home, the darwin system and the instance's own checks.
    checks.template-instance =
      let
        linux = instance ../../tests/fixtures/template/linux;
        darwin = instance ../../tests/fixtures/template/darwin;
        # The linux instance with jane-mac added by piste host add.
        two = instance ../../tests/fixtures/template/two-hosts;
        twoLinux = two.homeConfigurations."jane@devbox".config;
        twoMac = two.darwinConfigurations.jane-mac.config.home-manager.users.jane;
        fromHomeNix = h: h.programs.git.settings.user.email == "jane@example.com" && h.piste.agents.zone == "personal";
        drv = d: builtins.unsafeDiscardStringContext d.drvPath;
        hasPiste = lib.any (p: lib.getName p == "piste");
        optInToolNames = [ "komodo-cli" "crit" "psst" "antigravity-cli" "herdr" ];
        optInTools = lib.any (p: lib.elem (lib.getName p) optInToolNames);
        linuxHome = linux.homeConfigurations."jane@devbox".config.home;
        darwinHome = darwin.darwinConfigurations.jane-mac.config.home-manager.users.jane.home;
        # What `piste drift` and `piste adopt` read of a host.
        declared = import ../../cli/internal/drift/declared.nix [ "hello" "no-such-package" ] darwin.darwinConfigurations.jane-mac;
        linuxLists = (import ../../cli/internal/drift/declared.nix [ ] linux.homeConfigurations."jane@devbox").files;
        engineCasks = darwin.darwinConfigurations.jane-mac.config.piste.packages.resolved.casks;
        ownLists = [ "piste.packages.extraPackages" "piste.packages.casks.extra" "piste.packages.brews.extra" ];
      in
      assert lib.assertMsg (builtins.pathExists (self.templates.default.path + "/flake.nix"))
        "template-instance: templates.default has no flake.nix";
      # `nix flake init -t` copies @placeholders@ as they are: say so.
      assert lib.assertMsg (self.templates.default ? welcomeText)
        "template-instance: templates.default has no welcomeText";
      # A new overlay starts at the engine's home-manager release: after a
      # home-manager bump, set the templates' stateVersion to it and re-render
      # the instances (PISTE_UPDATE_INSTANCES=1 go test ./internal/overlay/).
      assert lib.assertMsg (linuxHome.stateVersion == linuxHome.version.release && darwinHome.stateVersion == linuxHome.version.release)
        "template-instance: the templates' home.stateVersion (${linuxHome.stateVersion}, ${darwinHome.stateVersion}) isn't home-manager's release ${linuxHome.version.release}";
      # The opt-in tools (packaged in pkgs/overlays, not installed by default;
      # add them via piste.packages.extraPackages): a new overlay starts
      # without them.
      assert lib.assertMsg (!(optInTools linux.homeConfigurations."jane@devbox".config.home.packages))
        "template-instance: the linux instance installs an opt-in tool (${lib.concatStringsSep ", " optInToolNames})";
      assert lib.assertMsg (!(optInTools darwin.darwinConfigurations.jane-mac.config.environment.systemPackages
        || optInTools darwin.darwinConfigurations.jane-mac.config.home-manager.users.jane.home.packages))
        "template-instance: the darwin instance installs an opt-in tool (${lib.concatStringsSep ", " optInToolNames})";
      # piste is installed, so `piste switch` works after the first switch
      # (mkFlake installs it: on a Mac, in the system packages).
      assert lib.assertMsg (hasPiste linux.homeConfigurations."jane@devbox".config.home.packages)
        "template-instance: the linux instance doesn't install piste";
      assert lib.assertMsg (hasPiste darwin.darwinConfigurations.jane-mac.config.environment.systemPackages)
        "template-instance: the darwin instance doesn't install piste";
      # A second machine (piste host add) gets what's jane's on every machine
      # from home.nix, and its own checkout from its host file.
      assert lib.assertMsg (fromHomeNix twoLinux && fromHomeNix twoMac)
        "template-instance: a two-hosts host lacks home.nix's git email or zone";
      assert lib.assertMsg (twoLinux.piste.checkout == "/home/jane/piste"
          && twoMac.piste.checkout == "${twoMac.home.homeDirectory}/piste"
          && twoLinux.piste.checkout != twoMac.piste.checkout)
        "template-instance: the two-hosts hosts don't each set their own piste.checkout (${twoLinux.piste.checkout}, ${twoMac.piste.checkout})";
      # declared.nix on the darwin instance: a package here is found and a
      # missing one isn't; the engine's casks count as declared; the Mac's
      # own three lists are in its host file, where `piste adopt` writes.
      assert lib.assertMsg (lib.attrNames declared.pkgs == [ "hello" ])
        "template-instance: declared.nix found ${builtins.toJSON (lib.attrNames declared.pkgs)} of hello and no-such-package";
      assert lib.assertMsg (engineCasks != [ ] && lib.all (c: lib.elem c declared.casks) engineCasks)
        "template-instance: declared.nix's casks lack the engine's";
      assert lib.assertMsg (lib.all (o: (declared.files.${o} or [ ]) != [ ] && lib.all (lib.hasSuffix "/hosts/jane-mac.nix") declared.files.${o}) ownLists)
        "template-instance: the darwin instance's own lists aren't in hosts/jane-mac.nix: ${builtins.toJSON (lib.getAttrs ownLists declared.files)}";
      # The Linux host file keeps a home.packages list of its own, which
      # `piste adopt` finds first (hosts/ first): with only a shared module's
      # (home.nix, which every machine imports), one machine's finds would go
      # into every machine's config.
      assert lib.assertMsg (lib.any (lib.hasSuffix "/hosts/devbox.nix") (linuxLists."home.packages" or [ ]))
        "template-instance: the linux instance's hosts/devbox.nix has no home.packages list for piste adopt: ${builtins.toJSON (linuxLists."home.packages" or [ ])}";
      pkgs.runCommand "check-template-instance" {
        linux = drv linux.homeConfigurations."jane@devbox".activationPackage;
        darwin = drv darwin.darwinConfigurations.jane-mac.system;
        twoLinux = drv two.homeConfigurations."jane@devbox".activationPackage;
        twoMac = drv two.darwinConfigurations.jane-mac.system;
        checks = toString (map drv (lib.attrValues linux.checks.${system}));
        darwinChecks = toString (map drv (lib.attrValues darwin.checks.aarch64-darwin));
      } "touch $out";
    # The Linux starter gets the engine's package set from mkFlake, as a Mac
    # gets it from its platform module, so the tools the shell's settings
    # call (zoxide, fzf, atuin, git's delta) come with it; the shell runs
    # each tool's init only when the tool is there. The engine's nixpkgs
    # overlays apply once, however many times a host lists them.
    checks.linux-starter-tools =
      let
        home = (instance ../../tests/fixtures/template/linux).homeConfigurations."jane@devbox".config;
        names = map lib.getName home.home.packages;
        tools = [ "zoxide" "fzf" "atuin" "delta" ];
        init = home.programs.zsh.initContent;
        guarded = tool: lib.hasInfix "if command -v ${tool} >/dev/null 2>&1; then" init;
        unguarded = lib.filter (t: !guarded t) [ "zoxide" "fzf" "atuin" ];
        # cd stays the builtin (the extras role aliases it to z; engine-defaults
        # and extras-role check that), and zoxide's own z and zi come from
        # its init, inside the guard.
        cut = re: str: builtins.split (lib.escapeRegex re) str;
        afterGuard = cut "if command -v zoxide >/dev/null 2>&1; then" init;
        zoxideBlock = if lib.length afterGuard < 3 then "" else lib.head (cut "\nfi" (lib.elemAt afterGuard 2));
        cdAlias = lib.hasInfix ''eval "$(zoxide init zsh)"'' zoxideBlock && !(lib.hasInfix "alias cd=" init);
        twice = import inputs.nixpkgs {
          inherit system;
          overlays = [ self.overlays.shared self.overlays.ai self.overlays.shared ];
          config.allowUnfree = true;
        };
        once = import inputs.nixpkgs {
          inherit system;
          overlays = [ self.overlays.shared ];
          config.allowUnfree = true;
        };
        same = n: twice.${n}.drvPath == once.${n}.drvPath;
      in
      assert lib.assertMsg (lib.all (t: lib.elem t names) tools)
        "linux-starter-tools: the Linux starter lacks ${toString (lib.filter (t: !(lib.elem t names)) tools)}";
      assert lib.assertMsg (unguarded == [ ])
        "linux-starter-tools: the shell runs the init of ${toString unguarded} without checking it is installed";
      assert lib.assertMsg cdAlias
        "linux-starter-tools: zoxide's init is outside its guard, or cd is aliased";
      assert lib.assertMsg (lib.all same [ "gemini-cli" "rtk" "claude-code" ])
        "linux-starter-tools: overlays.shared or overlays.ai applied twice changes a package";
      pkgs.runCommand "check-linux-starter-tools" { } "touch $out";
    # A non-interactive shell on Linux (`ssh <host> <cmd>`) finds nix: a
    # Determinate install doesn't hook zshenv, so the shell module sources
    # nix-daemon.sh from it on Linux. A Mac's /etc/zshenv (nix-darwin) does it.
    # A single-user install (--no-daemon) has no daemon script: both the
    # .zshenv and the interactive init fall back to ~/.nix-profile's nix.sh,
    # since home-manager moves aside the files that installer hooked.
    checks.linux-zshenv-nix =
      let
        linux = (instance ../../tests/fixtures/template/linux).homeConfigurations."jane@devbox".config;
        mac = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac.config.home-manager.users.jane;
        sources = h: lib.hasInfix "/nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh" (h.programs.zsh.envExtra or "");
        singleUser = "elif [[ -f ~/.nix-profile/etc/profile.d/nix.sh ]]; then";
        singleEnv = h: lib.hasInfix singleUser (h.programs.zsh.envExtra or "");
        singleInit = h: lib.hasInfix singleUser (h.programs.zsh.initContent or "");
      in
      assert lib.assertMsg (sources linux) "linux-zshenv-nix: the Linux starter's .zshenv doesn't source nix-daemon.sh";
      assert lib.assertMsg (!(sources mac)) "linux-zshenv-nix: the Mac's .zshenv sources nix-daemon.sh too";
      assert lib.assertMsg (singleEnv linux) "linux-zshenv-nix: the Linux .zshenv doesn't fall back to ~/.nix-profile's nix.sh (single-user Nix)";
      assert lib.assertMsg (singleInit linux) "linux-zshenv-nix: the interactive init doesn't fall back to ~/.nix-profile's nix.sh (single-user Nix)";
      assert lib.assertMsg (!(singleInit mac)) "linux-zshenv-nix: the Mac's init falls back to ~/.nix-profile's nix.sh (Linux only)";
      pkgs.runCommand "check-linux-zshenv-nix" { } "touch $out";
    # Commits are signed by default only when there is a key to sign with
    # (programs.git.signing.key, or user.signingkey in programs.git.settings):
    # a newcomer's first commit doesn't fail with "No secret key". A person's
    # own commit.gpgsign, or home-manager's signing.signByDefault, decides
    # over that default.
    checks.git-sign-default =
      let
        linux = (instance ../../tests/fixtures/template/linux).homeConfigurations."jane@devbox";
        gitConfig = m: (linux.extendModules { modules = [ m ]; }).config.xdg.configFile."git/config".text;
        lines = m: map lib.strings.trim (lib.splitString "\n" (gitConfig m));
        # The [commit] section's gpgsign lines, any case, in file order; git
        # takes the last.
        signs = m:
          let
            ls = lines m;
            from = lib.lists.findFirstIndex (l: l == "[commit]") null ls;
            rest = if from == null then [ ] else lib.drop (from + 1) ls;
            section = lib.take (lib.lists.findFirstIndex (lib.hasPrefix "[") (lib.length rest) rest) rest;
          in
          lib.filter (l: lib.hasPrefix "gpgsign" (lib.toLower l)) section;
        signed = m: let s = signs m; in s != [ ] && lib.hasSuffix "true" (lib.last s);
        key = { programs.git.signing.key = "jane@example.com"; };
        cases = {
          "no key" = { m = { }; want = false; };
          "signing.key" = { m = key; want = true; };
          "settings user.signingkey" = { m = { programs.git.settings.user.signingkey = "~/.ssh/id_ed25519.pub"; }; want = true; };
          "a key, commit.gpgsign = false" = { m = { imports = [ key ]; programs.git.settings.commit.gpgsign = false; }; want = false; };
          "a key, signByDefault = false" = { m = { imports = [ key ]; programs.git.signing.signByDefault = false; }; want = false; };
          "no key, signByDefault = true" = { m = { programs.git.signing.signByDefault = true; }; want = true; };
        };
        wrong = lib.attrNames (lib.filterAttrs (_: c: signed c.m != c.want) cases);
      in
      assert lib.assertMsg (wrong == [ ]) "git-sign-default: wrong commit signing for: ${lib.concatStringsSep "; " wrong}";
      pkgs.runCommand "check-git-sign-default" { } "touch $out";
    # An overlay's plain ssh setting beats the engine's default, with no
    # mkForce: AddKeysToAgent = "no" in its own "*" block.
    checks.ssh-overridable =
      let
        linux = (instance ../../tests/fixtures/template/linux).homeConfigurations."jane@devbox";
        # Each Host block is a DAG entry: its fields are under data.
        conf = m: (linux.extendModules { modules = [ m ]; }).config.programs.ssh.settings."*".data.AddKeysToAgent;
        theirs = builtins.tryEval (conf { programs.ssh.settings."*".AddKeysToAgent = "no"; });
      in
      assert lib.assertMsg (conf { } == "yes") "ssh-overridable: the engine's AddKeysToAgent isn't yes";
      assert lib.assertMsg (theirs.success && theirs.value == "no")
        "ssh-overridable: a plain AddKeysToAgent = \"no\" doesn't win over the engine's";
      pkgs.runCommand "check-ssh-overridable" { } "touch $out";
    # piste.github (programs/github.nix): with a login, github.com uses the
    # account's key, commits are signed with it over SSH, and the git email is
    # the noreply address; an overlay's plain values win, and with no login
    # there is no github.com block at all (ssh's own defaults and agent find
    # the key: 1Password, Secretive, a YubiKey or id_rsa all keep working) and
    # no signing key.
    checks.github-account =
      let
        linux = (instance ../../tests/fixtures/template/linux).homeConfigurations."jane@devbox";
        with' = m: (linux.extendModules { modules = [ m ]; }).config;
        ident = c: c.programs.ssh.settings."github.com".data.IdentityFile;
        acct = { piste.github = { login = "jane"; id = 12345; }; };
        key = "/home/jane/.ssh/id_ed25519_github_jane";
        on = with' acct;
        plain = with' { };
        # The starter sets its own email, which wins; a home with only the
        # base role shows the default.
        bare = (inputs.home-manager.lib.homeManagerConfiguration {
          inherit pkgs;
          modules = [
            self.homeManagerModules.base
            { home = { username = "jane"; homeDirectory = "/home/jane"; stateVersion = "24.05"; }; }
            acct
          ];
        }).config;
        own = with' {
          imports = [ acct ];
          programs.ssh.settings."github.com".IdentityFile = [ "/home/jane/.ssh/other" ];
          programs.git.settings.user.email = "jane@example.com";
        };
        gitConf = c: c.xdg.configFile."git/config".text;
      in
      assert lib.assertMsg (!(plain.programs.ssh.settings ? "github.com")) "github-account: without a login, there is a github.com block in ssh's config";
      assert lib.assertMsg (on.programs.ssh.settings."github.com".data.HostName == "github.com" && on.programs.ssh.settings."github.com".data.IdentitiesOnly)
        "github-account: with a login, github.com lacks its HostName or IdentitiesOnly";
      assert lib.assertMsg (plain.programs.git.signing.key or null == null) "github-account: without a login, a signing key is set";
      assert lib.assertMsg (ident on == [ key ]) "github-account: github.com doesn't use the account's key: ${toString (ident on)}";
      assert lib.assertMsg (on.programs.git.signing.key == "${key}.pub") "github-account: the signing key isn't the account's .pub";
      assert lib.assertMsg (on.programs.git.signing.format == "ssh") "github-account: the signing format isn't ssh";
      assert lib.assertMsg (bare.programs.git.settings.user.email == "12345+jane@users.noreply.github.com") "github-account: the git email isn't the noreply address";
      assert lib.assertMsg (lib.hasInfix "format = \"ssh\"" (gitConf on)) "github-account: git config has no gpg format = ssh";
      assert lib.assertMsg (ident own == [ "/home/jane/.ssh/other" ]) "github-account: an overlay's plain IdentityFile doesn't win";
      assert lib.assertMsg (own.programs.git.settings.user.email == "jane@example.com") "github-account: an overlay's plain email doesn't win";
      assert lib.assertMsg (bare.programs.git.settings.gpg.ssh.allowedSignersFile == "/home/jane/.config/git/allowed_signers") "github-account: allowedSignersFile isn't set";
      assert lib.assertMsg (lib.hasInfix "12345+jane@users.noreply.github.com" bare.home.activation.githubAllowedSigners.data
        && lib.hasInfix "${key}.pub" bare.home.activation.githubAllowedSigners.data) "github-account: allowed_signers doesn't pair the email with the key";
      assert lib.assertMsg (!(plain.home.activation ? githubAllowedSigners)) "github-account: without a login, allowed_signers is written";
      pkgs.runCommand "check-github-account" { } "touch $out";
    # The engine's defaults leave a stranger's habits alone: no classic
    # command is shadowed (grep, find, ps, du, df, diff, top, htop), cd is the
    # builtin, EDITOR and VISUAL are nano at mkDefault priority, git ignores no editor directory and has no
    # destructive aliases, tmux's prefix is C-b and it saves nothing to disk,
    # Ghostty reads no clipboard and changes no keys, and github.com gets an
    # ssh block only with a login. The extras role puts most of them back
    # (extras-role), but never the destructive git settings or tmux's capture
    # of pane text.
    checks.engine-defaults =
      let
        linux = (instance ../../tests/fixtures/template/linux).homeConfigurations."jane@devbox";
        mac = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac.config.home-manager.users.jane;
        h = linux.config;
        init = h.programs.zsh.initContent;
        git = h.programs.git;
        tmux = h.xdg.configFile."tmux/tmux.conf".text;
        ghostty = mac.xdg.configFile."ghostty/config".text;
        theirs = linux.extendModules { modules = [ { home.sessionVariables = { EDITOR = "vim"; VISUAL = "code -w"; }; } ]; };
        shadowed = lib.filter (a: lib.hasInfix "alias ${a}=" init) [ "grep" "find" "ps" "du" "df" "diff" "top" "htop" "cd" ];
        kept = lib.filter (a: !(lib.hasInfix "alias ${a}=" init)) [ "cat" "ls" "ll" "la" "lt" "tree" "rm" "cp" "mv" "cdi" ];
        habits = lib.filter (a: h.programs.zsh.shellAliases ? ${a}) [ "cc" "ccr" "cca" "top" "htop" "c" "q" "h" "j" "myip" ];
        in' = lib.flip lib.hasInfix;
      in
      assert lib.assertMsg (shadowed == [ ]) "engine-defaults: the shell aliases ${toString shadowed}";
      assert lib.assertMsg (kept == [ ]) "engine-defaults: the shell lost the alias ${toString kept}";
      assert lib.assertMsg (habits == [ ]) "engine-defaults: the shell ships personal-taste aliases ${toString habits}";
      assert lib.assertMsg (!(lib.hasInfix "shell()" init) && !(lib.hasInfix "YSU_MODE" init))
        "engine-defaults: the shell defines shell() or sets YSU_MODE";
      assert lib.assertMsg (h.home.sessionVariables.EDITOR == "nano" && h.home.sessionVariables.VISUAL == "nano")
        "engine-defaults: EDITOR and VISUAL aren't nano";
      assert lib.assertMsg (!(lib.hasInfix "export EDITOR" init) && !(lib.hasInfix "export VISUAL" init))
        "engine-defaults: the zsh init still exports EDITOR or VISUAL";
      assert lib.assertMsg (theirs.config.home.sessionVariables.EDITOR == "vim" && theirs.config.home.sessionVariables.VISUAL == "code -w")
        "engine-defaults: an overlay's plain EDITOR or VISUAL doesn't win";
      assert lib.assertMsg (!(git.settings.core ? editor)) "engine-defaults: git has a core.editor";
      assert lib.assertMsg (lib.all (i: !(lib.elem i git.ignores)) [ ".idea/" ".vscode/" ".envrc" ])
        "engine-defaults: git ignores .idea/, .vscode/ or .envrc globally";
      assert lib.assertMsg (!(git.settings.fetch ? pruneTags)) "engine-defaults: git sets fetch.pruneTags";
      assert lib.assertMsg (!(git.settings.alias ? discard) && !(git.settings.alias ? nuke))
        "engine-defaults: git defines the discard or nuke alias";
      assert lib.assertMsg (h.programs.tmux.prefix == null && !(in' tmux "prefix C-x")) "engine-defaults: tmux's prefix isn't its own default, C-b";
      assert lib.assertMsg (!(in' tmux "resurrect") && !(in' tmux "continuum") && !(in' tmux "unbind C-x"))
        "engine-defaults: tmux saves or restores sessions";
      assert lib.assertMsg (!(in' tmux "bind-key x split-window") && !(in' tmux "unbind '\"'"))
        "engine-defaults: tmux rebinds x, v or the default splits";
      assert lib.assertMsg (lib.all (k: !(in' ghostty k)) [ "clipboard-read" "confirm-close-surface" "macos-option-as-alt" ])
        "engine-defaults: Ghostty reads the clipboard, closes without asking, or takes option as alt";
      assert lib.assertMsg (in' ghostty "clipboard-write = allow") "engine-defaults: Ghostty lost clipboard-write";
      assert lib.assertMsg (!(h.programs.ssh.settings ? "github.com")) "engine-defaults: ssh has a github.com block without a login";
      assert lib.assertMsg (h.programs.ssh.includes == [ "~/.ssh/config-external" ])
        "engine-defaults: ssh includes ${toString h.programs.ssh.includes}, not ~/.ssh/config-external";
      pkgs.runCommand "check-engine-defaults" { } "touch $out";
    # The extras role (modules/extras) brings the extras back, a part
    # at a time: shell (aliases that shadow classic tools, a few shortcuts,
    # shell(), YSU_MODE, cd as z), editor (git's vim), tmux (prefix C-x, split
    # keys, resurrect without pane capture, and continuum), ghostty (clipboard-read, no close
    # prompt, option as alt). A part's toggle off drops just that part.
    checks.extras-role =
      let
        out = self.lib.mkFlake {
          inputs = { };
          self = { };
          user = "jane";
          zone = "personal";
          hosts = {
            "jane@devbox" = { system = "x86_64-linux"; roles = [ "dev" "extras" ]; modules = [ ../../tests/fixtures/template/linux/hosts/devbox.nix ]; };
            "jane@desk" = { system = "x86_64-linux"; roles = [ "desktop" "extras" ]; modules = [ ../../tests/fixtures/template/linux/hosts/devbox.nix ]; };
            jane-mac = { system = "aarch64-darwin"; roles = [ "extras" ]; modules = [ ../../tests/fixtures/template/darwin/hosts/jane-mac.nix ]; };
          };
        };
        off = parts: { piste.roles.extras = lib.genAttrs parts (_: { enable = false; }); };
        linux = parts: (out.homeConfigurations."jane@devbox".extendModules { modules = [ (off parts) ]; }).config;
        desk = parts: (out.homeConfigurations."jane@desk".extendModules { modules = [ (off parts) ]; }).config;
        mac = parts: (out.darwinConfigurations.jane-mac.extendModules {
          modules = [ { home-manager.users.jane = off parts; } ];
        }).config.home-manager.users.jane;
        tmuxWith = lines: (out.homeConfigurations."jane@devbox".extendModules {
          modules = [ { piste.roles.extras.tmux.resurrectExtraConfig = lines; } ];
        }).config;
        in' = lib.flip lib.hasInfix;
        init = c: c.programs.zsh.initContent;
        aliases = c: c.programs.zsh.shellAliases;
        tmux = c: c.xdg.configFile."tmux/tmux.conf".text;
        ghostty = c: c.xdg.configFile."ghostty/config".text;
        on = linux [ ];
        shellBack = c:
          lib.all (a: in' (init c) "alias ${a}=") [ "grep" "find" "ps" "du" "df" "diff" ]
          && lib.all (a: in' (init c) "alias ${a}=") [ "cc" "cca" "top" "htop" ]
          && in' (init c) ''alias cd="z"'' && in' (init c) "shell()" && in' (init c) "YSU_MODE=ALL"
          && lib.all (a: aliases c ? ${a}) [ "ccr" "c" "q" "h" "j" "myip" ];
        shellGone = c:
          lib.all (a: !(in' (init c) "alias ${a}=")) [ "grep" "find" "ps" "du" "df" "diff" "cc" "cca" "top" "htop" "cd" ]
          && !(in' (init c) "shell()") && !(in' (init c) "YSU_MODE")
          && lib.all (a: !(aliases c ? ${a})) [ "cc" "ccr" "cca" "top" "htop" "c" "q" "h" "j" "myip" ];
        tmuxBack = c: lib.all (k: in' (tmux c) k) [ "prefix C-x" "resurrect.tmux" "continuum.tmux" "@continuum-restore 'on'" "bind-key x split-window -v" ];
        tmuxGone = c: lib.all (k: !(in' (tmux c) k)) [ "prefix C-x" "resurrect" "continuum" "bind-key x split-window" ];
        ghosttyBack = c: lib.all (k: in' (ghostty c) k) [ "clipboard-read = allow" "confirm-close-surface = false" "macos-option-as-alt = true" ];
        ghosttyGone = c: lib.all (k: !(in' (ghostty c) k)) [ "clipboard-read" "confirm-close-surface" "macos-option-as-alt" ];
      in
      assert lib.assertMsg (shellBack on) "extras-role: the extras shell lacks an alias, shell(), YSU_MODE or cd as z";
      assert lib.assertMsg (shellGone (linux [ "shell" ])) "extras-role: extras.shell.enable = false leaves shell habits on";
      # The same guard as the engine's own human aliases: no agent shell gets
      # the shadows (the text from the extras header to the alias has it).
      # cc and top shadow the C compiler and top just as much: same guard.
      assert lib.assertMsg (lib.all (a:
        let
          fromHeader = lib.elemAt (lib.splitString "# === Extras: modern replacements (humans only) ===" (init on)) 1;
          beforeAlias = lib.head (lib.splitString "alias ${a}=" fromHeader);
        in
        in' beforeAlias ''-z "$CLAUDECODE"'' && in' beforeAlias "PASEO_AGENT_ID" && in' beforeAlias "CODEX_") [ "grep" "cc" "cca" "top" "htop" ])
        "extras-role: the extras shell aliases grep, cc, cca, top or htop outside the agent-shell guard";
      # Nothing in the pack throws work away: with every part on, git has the
      # same aliases and the same fetch settings as with every part off (so no
      # discard, nuke or fetch.pruneTags, nor any other).
      assert lib.assertMsg (let
          bare = (linux [ "shell" "editor" "tmux" "ghostty" ]).programs.git.settings;
          g = on.programs.git.settings;
        in g.alias == bare.alias && (g.fetch or { }) == (bare.fetch or { })
          && !(g.alias ? discard) && !(g.alias ? nuke) && !(g.fetch ? pruneTags))
        "extras-role: the extras pack gives git an alias or a fetch setting (no discard, nuke or fetch.pruneTags)";
      assert lib.assertMsg (on.programs.git.settings.core.editor == "vim"
        && lib.all (i: lib.elem i on.programs.git.ignores) [ ".idea/" ".vscode/" ".envrc" ])
        "extras-role: git's editor isn't vim, or it doesn't ignore .idea/, .vscode/ and .envrc";
      assert lib.assertMsg (let g = (linux [ "editor" ]).programs.git; in !(g.settings.core ? editor) && !(lib.elem ".idea/" g.ignores))
        "extras-role: extras.editor.enable = false leaves git's vim or its ignores";
      assert lib.assertMsg (on.home.sessionVariables.EDITOR == "nano") "extras-role: the extras role changed EDITOR";
      assert lib.assertMsg (tmuxBack on) "extras-role: tmux lacks the extras prefix, split keys, resurrect or continuum";
      assert lib.assertMsg (tmuxGone (linux [ "tmux" ])) "extras-role: extras.tmux.enable = false leaves tmux's taste on";
      # resurrect doesn't capture pane text unless the person asks: the
      # capture setting is in no default, and resurrectExtraConfig puts it
      # (and what it adds) with resurrect's own lines, ahead of its run-shell.
      assert lib.assertMsg (!(in' (tmux on) "capture-pane-contents") && !(in' (tmux on) "pane-contents-area"))
        "extras-role: tmux's resurrect captures pane contents by default";
      assert lib.assertMsg (let
          t = tmux (tmuxWith "set -g @resurrect-capture-pane-contents 'on'\nset -g @resurrect-pane-contents-area 'visible'\n");
          resurrect = lib.elemAt (lib.splitString "# tmuxplugin-resurrect" t) 1;
          before = lib.head (lib.splitString "resurrect.tmux" resurrect);
        in in' before "set -g @resurrect-dir '$HOME/.cache/tmux/resurrect'\nset -g @resurrect-capture-pane-contents 'on'\nset -g @resurrect-pane-contents-area 'visible'\n\nrun-shell")
        "extras-role: resurrectExtraConfig doesn't land in resurrect's block, before it loads";
      assert lib.assertMsg (ghosttyBack (mac [ ])) "extras-role: the Mac's Ghostty lacks the extras lines";
      assert lib.assertMsg (ghosttyGone (mac [ "ghostty" ])) "extras-role: extras.ghostty.enable = false leaves Ghostty's lines on";
      assert lib.assertMsg (ghosttyBack (desk [ ])) "extras-role: a desktop host's Ghostty lacks the extras lines";
      # No desktop role, no Ghostty: extras doesn't write a config of its own.
      assert lib.assertMsg (!(on.xdg.configFile ? "ghostty/config" && on.xdg.configFile."ghostty/config".enable))
        "extras-role: a host without the desktop role gets a Ghostty config from extras";
      pkgs.runCommand "check-extras-role" {
        linux = builtins.unsafeDiscardStringContext out.homeConfigurations."jane@devbox".activationPackage.drvPath;
        mac = builtins.unsafeDiscardStringContext out.darwinConfigurations.jane-mac.system.drvPath;
      } "touch $out";
    # A Mac's app settings that the app itself writes (Karabiner's keymap)
    # aren't linked from the store by the engine: they'd be one person's,
    # and every switch would move the app's own copy aside.
    checks.darwin-no-app-configs =
      let
        home = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac.config.home-manager.users.jane;
        linked = lib.filter (lib.hasInfix "karabiner") (map (f: f.target) (lib.attrValues home.home.file));
      in
      assert lib.assertMsg (linked == [ ]) "darwin-no-app-configs: the engine links ${toString linked}";
      pkgs.runCommand "check-darwin-no-app-configs" { } "touch $out";
    # The sops-nix home module is the engine's export too, so an overlay
    # needn't reach into piste.inputs: homeManagerModules.sops. Imported
    # both ways in one home, it counts once.
    checks.sops-reexport =
      let
        home = (agentHome [
          self.homeManagerModules.sops
          inputs.sops-nix.homeManagerModules.sops
          { sops.age.keyFile = "/home/t/.config/sops/age/keys.txt"; }
        ]).config;
      in
      assert lib.assertMsg (self.homeManagerModules ? sops) "sops-reexport: no homeManagerModules.sops";
      assert lib.assertMsg (home.sops.age.keyFile == "/home/t/.config/sops/age/keys.txt")
        "sops-reexport: a home importing homeManagerModules.sops and sops-nix's own doesn't evaluate";
      pkgs.runCommand "check-sops-reexport" { } "touch $out";
    # Every role toggle in docs/options.md turns its program off, on the
    # starters' hosts with the extras role added through mkFlake (roles dev +
    # extras on Linux; extras on the Mac, which has desktop already): the
    # linux home (base's, dev's and extras' home toggles) and the darwin one
    # (all nine of base/dev/desktop, extras' home parts, and extras' Mac parts
    # casks and macos, which are nix-darwin options, so read and set on the
    # system). Each is on as the host has it, off with only its toggle set to
    # false, and a home with every toggle off still evaluates to a build.
    # An extras part has no program of its own until its track fills it in:
    # until then its toggle is the option itself.
    checks.role-toggles =
      let
        fx = ../../tests/fixtures/template;
        out = self.lib.mkFlake {
          inputs = { };
          self = { };
          user = "jane";
          zone = "personal";
          hosts = {
            "jane@devbox" = { system = "x86_64-linux"; roles = [ "dev" "extras" ]; modules = [ (fx + "/linux/hosts/devbox.nix") ]; };
            jane-mac = { system = "aarch64-darwin"; roles = [ "extras" ]; modules = [ (fx + "/darwin/hosts/jane-mac.nix") ]; };
          };
        };
        linux = out.homeConfigurations."jane@devbox";
        darwin = out.darwinConfigurations.jane-mac;
        extrasOn = part: h: h.piste.roles.extras.${part}.enable;
        # piste.roles.<role>.<part>.enable, and what it turns off.
        toggles = {
          "base.shell" = h: h.programs.zsh.enable;
          "base.git" = h: h.programs.git.enable;
          "base.ssh" = h: h.programs.ssh.enable;
          "base.starship" = h: h.programs.starship.enable;
          "dev.editor" = h: h.programs.vim.enable;
          "dev.tmux" = h: h.programs.tmux.enable;
          "desktop.alacritty" = h: h.programs.alacritty.enable;
          "desktop.ghostty" = h: h.xdg.configFile."ghostty/config".enable;
        }
        // lib.genAttrs (map (p: "extras.${p}") [ "shell" "editor" "tmux" "ghostty" ])
          (t: extrasOn (lib.removePrefix "extras." t));
        # The Mac's own (nix-darwin) parts, read and set on the system.
        sysToggles = lib.genAttrs [ "extras.casks" "extras.macos" ]
          (t: s: s.piste.roles.extras.${lib.removePrefix "extras." t}.enable);
        off = ts: lib.foldl' lib.recursiveUpdate { } (map (t:
          let p = lib.splitString "." t; in
          { piste.roles.${lib.elemAt p 0}.${lib.elemAt p 1}.enable = false; }) ts);
        homeOnly = lib.filter (t: !(sysToggles ? ${t}));
        sysOnly = lib.filter (t: sysToggles ? ${t});
        darwinWith = ts: darwin.extendModules {
          modules = [ (off (sysOnly ts)) { home-manager.users.jane = off (homeOnly ts); } ];
        };
        homes = {
          linux = {
            names = lib.filter (t: !lib.hasPrefix "desktop." t) (lib.attrNames toggles);
            read = ts: t: toggles.${t} (linux.extendModules { modules = [ (off ts) ]; }).config;
            build = ts: (linux.extendModules { modules = [ (off ts) ]; }).activationPackage;
          };
          darwin = {
            names = lib.attrNames toggles ++ lib.attrNames sysToggles;
            read = ts: t:
              let c = (darwinWith ts).config; in
              if sysToggles ? ${t} then sysToggles.${t} c else toggles.${t} c.home-manager.users.jane;
            build = ts: (darwinWith ts).system;
          };
        };
        wrong = lib.concatLists (lib.mapAttrsToList (os: h:
          map (t: "${os}: ${t} is off by default") (lib.filter (t: !(h.read [ ] t)) h.names)
          ++ map (t: "${os}: ${t}.enable = false leaves it on") (lib.filter (t: h.read [ t ] t) h.names)
        ) homes);
      in
      assert lib.assertMsg (wrong == [ ]) "role-toggles: ${lib.concatStringsSep "; " wrong}";
      pkgs.runCommand "check-role-toggles" {
        linux = builtins.unsafeDiscardStringContext (homes.linux.build homes.linux.names).drvPath;
        darwin = builtins.unsafeDiscardStringContext (homes.darwin.build homes.darwin.names).drvPath;
      } "touch $out";
    # Every engine opinion file has a stable key, pack:core/<piece> or
    # pack:extras/<piece>, the handle an overlay takes a piece over with:
    # disabledModules = [ { key = K; } ] and its own copy of the file under
    # another key. For each key, on the starters' hosts (roles dev + extras on
    # Linux, extras on the Mac, with the pieces' own switches on):
    #   - a round trip: K disabled and the engine's own file imported again
    #     under a local key builds the same home or system as the engine's
    #     piece (a Mac's home pieces are disabled inside its home, the extras
    #     Mac parts casks and macos at the system's top level);
    #   - K disabled alone drops the piece's program, and the options it
    #     declares, on a host that sets none of them. A piece whose options
    #     the host does set (herdr, zellij, vscode) drops its program there
    #     too once those settings are sunk: a bare disable would leave them
    #     undeclared;
    #   - disabledModules' string form ("pack:core/starship", read as a path
    #     under modulesPath) and the file's path match nothing.
    checks.piece-keys =
      let
        fx = ../../tests/fixtures/template;
        mod = ../../modules;
        out = self.lib.mkFlake {
          inputs = { };
          self = { };
          user = "jane";
          zone = "personal";
          hosts = {
            "jane@devbox" = {
              system = "x86_64-linux";
              roles = [ "dev" "extras" ];
              overlays = [ self.overlays.vscode ];
              modules = [ (fx + "/linux/hosts/devbox.nix") self.homeManagerModules.vscode ];
            };
            jane-mac = { system = "aarch64-darwin"; roles = [ "extras" ]; modules = [ (fx + "/darwin/hosts/jane-mac.nix") ]; };
          };
        };
        # Each host as the starter has it (plain) and with the pieces' own
        # switches on (on): the round trips run on `on`, so they cover what
        # those pieces do.
        evals = {
          linux = rec {
            plain = out.homeConfigurations."jane@devbox";
            on = plain.extendModules {
              modules = [ { piste.programs = { vscode.enable = true; zellij.enable = true; herdr.enable = true; }; } ];
            };
            with' = base: _side: ms: base.extendModules { modules = ms; };
            home = e: e.config;
            sys = e: e.config;
            drv = e: e.activationPackage.drvPath;
          };
          mac = rec {
            plain = out.darwinConfigurations.jane-mac;
            on = plain.extendModules { modules = [ { home-manager.users.jane.piste.programs.herdr.enable = true; } ]; };
            with' = base: side: ms: base.extendModules {
              modules = if side == "system" then ms else [ { home-manager.users.jane.imports = ms; } ];
            };
            home = e: e.config.home-manager.users.jane;
            sys = e: e.config;
            drv = e: e.system.drvPath;
          };
        };
        in' = lib.flip lib.hasInfix;
        # key: the file, the host it's tried on, its side (home, else
        # system), its program (has, read on that side's config), the
        # options it declares (opts, a path under config) and the options
        # whose order a copy changes (reorders): a list or lines option's
        # definitions of equal priority merge in module order, and an
        # overlay's copy comes after the engine's modules (the editor's nano
        # after the other packages, extras' shell lines after zellij's, both
        # mkAfter).
        pieces = {
          "pack:core/shell" = { file = mod + "/programs/shell.nix"; on = "linux"; has = h: h.programs.zsh.enable; };
          "pack:core/git" = { file = mod + "/programs/git.nix"; on = "linux"; has = h: h.programs.git.enable; };
          "pack:core/ssh" = { file = mod + "/programs/ssh.nix"; on = "linux"; has = h: h.programs.ssh.enable; };
          "pack:core/starship" = { file = mod + "/programs/starship.nix"; on = "linux"; has = h: h.programs.starship.enable; };
          "pack:core/editor" = {
            file = mod + "/programs/editor.nix"; on = "linux";
            has = h: h.programs.vim.enable && h.home.file ? ".nanorc";
            reorders = [ [ "home" "packages" ] ];
          };
          "pack:core/tmux" = { file = mod + "/programs/tmux.nix"; on = "linux"; has = h: h.programs.tmux.enable; };
          "pack:core/uv" = { file = mod + "/programs/uv.nix"; on = "linux"; has = h: h.programs.uv.enable; };
          "pack:core/zellij" = {
            file = mod + "/programs/zellij.nix"; on = "linux";
            has = h: h.xdg.configFile ? "zellij/config.kdl";
            opts = [ "piste" "programs" "zellij" ];
          };
          "pack:core/vscode" = {
            file = mod + "/programs/vscode.nix"; on = "linux";
            has = h: h.programs.vscode.enable;
            opts = [ "piste" "programs" "vscode" ];
          };
          "pack:core/herdr" = {
            file = mod + "/programs/herdr.nix"; on = "mac";
            has = h: h.home.activation ? herdrAppSettings;
            opts = [ "piste" "programs" "herdr" ];
          };
          "pack:core/alacritty" = { file = mod + "/programs/alacritty.nix"; on = "mac"; has = h: h.programs.alacritty.enable; };
          "pack:core/ghostty" = {
            file = mod + "/programs/ghostty.nix"; on = "mac";
            has = h: in' (h.xdg.configFile."ghostty/config".text or "") "font-family = Hack Nerd Font";
          };
          "pack:extras/shell" = {
            file = mod + "/extras/shell.nix"; on = "linux";
            has = h: in' h.programs.zsh.initContent "YSU_MODE=ALL";
            opts = [ "piste" "roles" "extras" "shell" ];
            reorders = [ [ "programs" "zsh" "initContent" ] ];
          };
          "pack:extras/editor" = {
            file = mod + "/extras/editor.nix"; on = "linux";
            has = h: h.programs.git.settings.core ? editor;
            opts = [ "piste" "roles" "extras" "editor" ];
          };
          "pack:extras/tmux" = {
            file = mod + "/extras/tmux.nix"; on = "linux";
            has = h: h.programs.tmux.prefix == "C-x";
            opts = [ "piste" "roles" "extras" "tmux" ];
          };
          "pack:extras/ghostty" = {
            file = mod + "/extras/ghostty.nix"; on = "mac";
            has = h: in' (h.xdg.configFile."ghostty/config".text or "") "clipboard-read = allow";
            opts = [ "piste" "roles" "extras" "ghostty" ];
          };
          "pack:extras/casks" = {
            file = mod + "/extras/casks.nix"; on = "mac"; side = "system";
            has = s: lib.elem "firefox" s.piste.packages.resolved.casks;
            opts = [ "piste" "roles" "extras" "casks" ];
          };
          "pack:extras/macos" = {
            file = mod + "/extras/macos.nix"; on = "mac"; side = "system";
            has = s: s.system.defaults.trackpad.TrackpadThreeFingerDrag == true;
            opts = [ "piste" "roles" "extras" "macos" ];
          };
        };
        sideOf = p: p.side or "home";
        # The engine's file again under a key of its own, as an overlay's copy
        # of it would be.
        copy = k: file:
          let
            m = import file;
            local = { key = "local:" + lib.removePrefix "pack:" k; };
          in
          if lib.isFunction m then lib.setFunctionArgs (a: m a // local) (lib.functionArgs m) else m // local;
        disable = k: { disabledModules = [ { key = k; } ]; };
        # A sink for the settings a disabled piece's options are left with.
        sink = p: lib.setAttrByPath ([ "options" ] ++ p.opts) (lib.mkSinkUndeclaredOptions { });
        problems = lib.concatLists (lib.mapAttrsToList (k: p:
          let
            e = evals.${p.on};
            with' = base: ms: e.with' base (sideOf p) ms;
            read = x: if sideOf p == "system" then e.sys x else e.home x;
            has = x: p.has (read x);
            declares = x: lib.hasAttrByPath p.opts (read x);
            drv = x: builtins.unsafeDiscardStringContext (e.drv x);
            # A test that fails to evaluate fails (with the key's name, not
            # the first error's).
            holds = t: let r = builtins.tryEval t; in r.success && r.value;
            took = with' e.on [ (disable k) (copy k p.file) ];
            dropped = with' e.plain [ (disable k) ];
            sunk = with' e.on [ (disable k) (sink p) ];
            # A reordered option: the copy's value is the engine's, reordered
            # (the same lines or packages), and with each such option pinned
            # to the engine's value, the two builds are the same.
            reorders = p.reorders or [ ];
            value = o: x: lib.getAttrFromPath o (read x);
            items = v: lib.sort lib.lessThan
              (if lib.isString v then lib.splitString "\n" v else map (i: builtins.unsafeDiscardStringContext (toString i)) v);
            pins = map (o: lib.setAttrByPath o (lib.mkForce (value o e.on))) reorders;
            same =
              lib.all (o: items (value o took) == items (value o e.on)) reorders
              && drv (with' e.on ([ (disable k) (copy k p.file) ] ++ pins)) == drv (if reorders == [ ] then e.on else with' e.on pins);
          in
          lib.optional (!holds (has e.on)) "${k}: the ${p.on} host doesn't have the piece's program"
          ++ lib.optional (!holds same) "${k}: taken over with the engine's own file, the ${p.on} build differs or fails"
          ++ lib.optional (!holds (!has dropped)) "${k}: disabled, its program is still on (or the host fails)"
          ++ lib.optional (p ? opts && !holds (!declares dropped)) "${k}: disabled, its options are still declared"
          ++ lib.optional (p ? opts && !holds (!has sunk)) "${k}: disabled with its settings sunk, its program is still on (or the host fails)"
        ) pieces);
        # The key is the handle: the string form and the file's path are not.
        starshipWith = ms: (evals.linux.plain.extendModules { modules = ms; }).config.programs.starship.enable;
      in
      assert lib.assertMsg (problems == [ ]) "piece-keys: ${lib.concatStringsSep "; " problems}";
      assert lib.assertMsg (starshipWith [ { disabledModules = [ "pack:core/starship" ]; } ])
        "piece-keys: disabledModules = [ \"pack:core/starship\" ] (a string, not { key = ...; }) disables starship";
      assert lib.assertMsg (starshipWith [ { disabledModules = [ (mod + "/programs/starship.nix") ]; } ])
        "piece-keys: disabledModules with starship.nix's path disables it (the key is the handle, not the path)";
      pkgs.runCommand "check-piece-keys" { } "touch $out";
    # zjstatus's clock can't read the machine's zone, so the zone is
    # piste.programs.zellij.timezone: UTC unless an overlay sets one.
    checks.zellij-timezone =
      let
        darwin = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac;
        kdl = m: (darwin.extendModules { modules = [ { home-manager.users.jane = m; } ]; })
          .config.home-manager.users.jane.xdg.configFile."zellij/config.kdl".text;
        has = tz: m: lib.hasInfix ''datetime_timezone "${tz}"'' (kdl m);
      in
      assert lib.assertMsg (has "Etc/UTC" { }) "zellij-timezone: the default isn't Etc/UTC";
      assert lib.assertMsg (has "America/New_York" { piste.programs.zellij.timezone = "America/New_York"; })
        "zellij-timezone: an overlay's zone doesn't reach config.kdl";
      pkgs.runCommand "check-zellij-timezone" { } "touch $out";
    # The Homebrew names the engine installs exist in the pinned taps: a lock
    # bump that loses a cask (microsoft-remote-desktop went to windows-app)
    # goes red here, where it would otherwise stop a fresh Mac's first switch
    # in `brew bundle`. Covers the engine's own casks and brews and the
    # extras role's, as a Mac that lists the role resolves them; a name from
    # another tap (user/tap/name) isn't ours to check.
    checks.tap-names =
      let
        darwin = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac.extendModules {
          modules = [ self.darwinModules.extras ];
        };
        resolved = darwin.config.piste.packages.resolved;
        # Paths joined with +, not interpolated: lazy trees refuse a read
        # through "${store}/f". A cask sits in Casks/<first letter>/, a
        # formula in Formula/<first letter>/ (Formula/lib/ for lib*).
        first = n: builtins.substring 0 1 n;
        caskIn = n: builtins.pathExists (inputs.homebrew-cask + "/Casks/${first n}/${n}.rb");
        brewIn = n: builtins.pathExists (inputs.homebrew-core + "/Formula/${if lib.hasPrefix "lib" n then "lib" else first n}/${n}.rb");
        official = lib.filter (n: !lib.hasInfix "/" n);
        missingCasks = lib.filter (n: !caskIn n) (official resolved.casks);
        missingBrews = lib.filter (n: !brewIn n) (official resolved.brews);
      in
      assert lib.assertMsg (lib.elem "ghostty" resolved.casks && lib.elem "visual-studio-code" resolved.casks)
        "tap-names: the Mac doesn't resolve the engine's and the extras role's casks (${toString resolved.casks})";
      # The predicates can fail: a name the tap doesn't have is found missing.
      assert lib.assertMsg (!caskIn "piste-no-such-cask" && !brewIn "piste-no-such-formula")
        "tap-names: the tap lookups find a name that isn't there";
      assert lib.assertMsg (missingCasks == [ ])
        "tap-names: casks not in the pinned homebrew-cask: ${toString missingCasks}";
      assert lib.assertMsg (missingBrews == [ ])
        "tap-names: brews not in the pinned homebrew-core: ${toString missingBrews}";
      pkgs.runCommand "check-tap-names" { } "touch $out";
    # A file in the way of a Mac's home-manager links is moved aside under a
    # name that never collides, <file>.backup-<epoch> (the Linux switch's
    # names), so the second switch has nothing to refuse or delete; the
    # script runs here, on files, as home-manager runs it.
    checks.darwin-backup-command =
      let
        macSystem = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac;
        mac = macSystem.config.home-manager;
        text = builtins.unsafeDiscardStringContext mac.backupCommand.text;
        # The same script with this machine's coreutils.
        script = pkgs.writeShellScript "piste-hm-backup-test"
          (builtins.replaceStrings [ "${builtins.unsafeDiscardStringContext macSystem.pkgs.coreutils}/bin/" ] [ "${pkgs.coreutils}/bin/" ] text);
      in
      assert lib.assertMsg (mac.backupFileExtension == null)
        "darwin-backup-command: a fixed backup extension is set (the second switch stops on, or deletes, the first one's)";
      assert lib.assertMsg (mac.backupCommand != null && lib.hasInfix ".backup-" text)
        "darwin-backup-command: home-manager.backupCommand isn't the .backup-<epoch> mover";
      pkgs.runCommand "check-darwin-backup-command" { } ''
        set -euo pipefail
        mkdir home && cd home
        echo one > .zshrc; echo two > "a file"
        ${script} "$PWD/.zshrc"
        ${script} "$PWD/a file"
        test ! -e .zshrc && test ! -e "a file"
        test "$(cat .zshrc.backup-[0-9]*)" = one
        test "$(cat "a file".backup-[0-9]*)" = two
        # A second round, as the second switch would see it: the first
        # backup is still there and the new one lands beside it.
        echo three > .zshrc
        sleep 1
        ${script} "$PWD/.zshrc"
        test "$(ls .zshrc.backup-* | wc -l)" = 2
        # The mover says where it put the file (home-manager's own notice is
        # verbose-only with a backupCommand).
        echo four > .zshrc
        ${script} "$PWD/.zshrc" 2>msg
        grep -q "^piste: moved .*/.zshrc aside to .*/.zshrc.backup-[0-9]*$" msg
        touch $out
      '';
    # A Mac's input and system defaults are defaults (lib.mkDefault): a
    # host's plain value wins with no mkForce. Press-and-hold and
    # three-finger drag are the extras role's, off the engine's list; the
    # stateVersion is a default for the host's own.
    checks.macos-defaults =
      let
        darwin = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac;
        with' = ms: (darwin.extendModules { modules = ms; }).config.system;
        d = ms: (with' ms).defaults;
        extras = [ self.darwinModules.extras ];
      in
      assert lib.assertMsg ((d [ ]).NSGlobalDomain.ApplePressAndHoldEnabled == null && (d [ ]).trackpad.TrackpadThreeFingerDrag == null)
        "macos-defaults: the engine alone changes press-and-hold or three-finger drag";
      assert lib.assertMsg ((d [ ]).NSGlobalDomain.KeyRepeat == 2 && (d [ ]).dock.autohide == true)
        "macos-defaults: the engine's key repeat or Dock defaults are gone";
      assert lib.assertMsg ((d extras).NSGlobalDomain.ApplePressAndHoldEnabled == false && (d extras).trackpad.TrackpadThreeFingerDrag == true)
        "macos-defaults: the extras role doesn't set press-and-hold or three-finger drag";
      assert lib.assertMsg ((d (extras ++ [ { system.defaults.NSGlobalDomain.ApplePressAndHoldEnabled = true; } ])).NSGlobalDomain.ApplePressAndHoldEnabled == true)
        "macos-defaults: a plain press-and-hold of the host's doesn't win over the extras role's";
      assert lib.assertMsg ((d (extras ++ [ { piste.roles.extras.macos.enable = false; } ])).NSGlobalDomain.ApplePressAndHoldEnabled == null)
        "macos-defaults: piste.roles.extras.macos.enable = false leaves the extras settings on";
      assert lib.assertMsg (
        (d [ { system.defaults.NSGlobalDomain.KeyRepeat = 6; system.defaults.dock.autohide = false; } ]).NSGlobalDomain.KeyRepeat == 6
        && (d [ { system.defaults.dock.autohide = false; } ]).dock.autohide == false)
        "macos-defaults: a plain KeyRepeat or Dock value of the host's conflicts with the engine's";
      # The starter's Mac host sets system.stateVersion = 7 plainly:
      # it evaluates, and wins over the engine's lib.mkDefault 4.
      assert lib.assertMsg ((with' [ ]).stateVersion == 7)
        "macos-defaults: the host's plain system.stateVersion (7) doesn't win over the engine's default";
      pkgs.runCommand "check-macos-defaults" { } "touch $out";
    # The extras role's casks go through casks.extra, so casks.exclude drops
    # one (an app an employer's MDM pushes) as it does an engine cask; a Mac
    # without the role has Ghostty alone. The file associations follow VS
    # Code, and are the role's, not the engine's.
    checks.extras-casks =
      let
        darwin = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac;
        with' = ms: (darwin.extendModules { modules = ms; }).config;
        casks = ms: (with' ms).piste.packages.resolved.casks;
        extras = [ self.darwinModules.extras ];
        duti = ms: lib.any (p: lib.getName p == "duti") (with' ms).home-manager.users.jane.home.packages;
        assoc = ms: (with' ms).home-manager.users.jane.home.activation ? setFileAssociations;
      in
      assert lib.assertMsg (casks [ ] == [ "ghostty" ])
        "extras-casks: a Mac without the extras role has casks besides ghostty: ${toString (casks [ ])}";
      assert lib.assertMsg (lib.all (c: lib.elem c (casks extras)) [ "ghostty" "firefox" "visual-studio-code" "raycast" ])
        "extras-casks: the extras role's casks aren't resolved: ${toString (casks extras)}";
      assert lib.assertMsg (!(lib.elem "google-chrome" (casks (extras ++ [ { piste.packages.casks.exclude = [ "google-chrome" ]; } ]))))
        "extras-casks: casks.exclude doesn't drop an extras cask";
      assert lib.assertMsg (!(lib.elem "firefox" (casks (extras ++ [ { piste.roles.extras.casks.enable = false; } ]))))
        "extras-casks: piste.roles.extras.casks.enable = false leaves the extras casks";
      assert lib.assertMsg (lib.elem "mine" (casks (extras ++ [ { piste.packages.casks.extra = [ "mine" ]; } ])))
        "extras-casks: a host's own casks.extra is lost beside the extras role's";
      assert lib.assertMsg (!duti [ ] && !assoc [ ])
        "extras-casks: the engine alone sets file associations";
      assert lib.assertMsg (duti extras && assoc extras)
        "extras-casks: the extras role doesn't set file associations";
      assert lib.assertMsg (!duti (extras ++ [ { piste.packages.casks.exclude = [ "visual-studio-code" ]; } ]))
        "extras-casks: .md is bound to VS Code with VS Code excluded";
      pkgs.runCommand "check-extras-casks" { } "touch $out";
    # What a Linux home profile and a Mac don't carry: no Nix openssh (a Mac's
    # UseKeychain and a distribution's GSSAPI options are ssh_config the
    # machine's own ssh reads), no daemon (docker-client on Linux), no fish or
    # powerlevel10k, no Alacritty on Linux (GL), no dockutil. The container
    # image brings its own ssh. zellij has the one switch.
    checks.lean-closure =
      let
        linuxHome = (instance ../../tests/fixtures/template/linux).homeConfigurations."jane@devbox".config.home.packages;
        mac = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac.config;
        macPkgs = mac.environment.systemPackages ++ mac.home-manager.users.jane.home.packages;
        named = ps: map lib.getName ps;
        gone = [ "openssh" "fish" "zsh-powerlevel10k" "dockutil" ];
        linuxPkgs = import inputs.nixpkgs { inherit system; overlays = [ self.overlays.shared ]; config.allowUnfree = true; };
        drvs = ps: map (p: p.drvPath) ps;
        container = (inputs.home-manager.lib.homeManagerConfiguration {
          pkgs = linuxPkgs;
          modules = [
            self.homeManagerModules.container
            inputs.sops-nix.homeManagerModules.sops
            { home = { username = "t"; homeDirectory = "/home/t"; stateVersion = "24.05"; }; }
          ];
        }).config.home.packages;
        desktop = ms: (agentHome ([ self.homeManagerModules.desktop inputs.sops-nix.homeManagerModules.sops ] ++ ms)).config;
        zellij = ms: (desktop ms).piste.programs.zellij.enable;
      in
      assert lib.assertMsg (lib.all (n: !(lib.elem n (named linuxHome))) (gone ++ [ "alacritty" ]))
        "lean-closure: the Linux home carries one of ${toString (gone ++ [ "alacritty" ])}";
      assert lib.assertMsg (lib.all (n: !(lib.elem n (named macPkgs))) gone)
        "lean-closure: the Mac carries one of ${toString gone}";
      assert lib.assertMsg (lib.elem "alacritty" (named macPkgs)) "lean-closure: the Mac lost Alacritty";
      assert lib.assertMsg (lib.elem linuxPkgs.docker-client.drvPath (drvs linuxHome) && !(lib.elem linuxPkgs.docker.drvPath (drvs linuxHome)))
        "lean-closure: the Linux home's docker isn't docker-client";
      assert lib.assertMsg (lib.elem linuxPkgs.docker-client.drvPath (drvs container) && lib.elem "openssh" (named container))
        "lean-closure: the container role lost the docker client or the ssh client";
      assert lib.assertMsg (pkgs.stdenv.hostPlatform.isDarwin || !(desktop [ ]).programs.alacritty.enable)
        "lean-closure: a Linux home on the desktop role installs Alacritty";
      assert lib.assertMsg (zellij [ ] && !(zellij [ { piste.programs.zellij.enable = false; } ]))
        "lean-closure: zellij isn't on with the desktop role, or a plain false doesn't turn it off";
      assert lib.assertMsg (!(lib.hasAttrByPath [ "piste" "roles" "desktop" "zellij" ] (desktop [ ])))
        "lean-closure: a second zellij switch (piste.roles.desktop.zellij) is back";
      pkgs.runCommand "check-lean-closure" { } "touch $out";
    # A team layer's starter, rendered: the committed instances
    # tests/fixtures/template/layer-{linux,darwin} (piste init --layer's
    # output for jane; cli/internal/overlay's TestRenderInstances keeps them
    # equal), on the fixture layer, evaluate with the layer's tool, cask,
    # zone and secret names beside jane's own. Evaluated, not built: their
    # sops file is written by a real init.
    checks.layer-instance =
      let
        fixtureLayer = (import ../../tests/fixtures/layer/flake.nix).outputs {
          self = { outPath = ../../tests/fixtures/layer; };
          piste = self;
        };
        instance = dir: (import (dir + "/flake.nix")).outputs {
          self = { outPath = dir; };
          piste = self;
          layer = fixtureLayer;
        };
        linux = (instance ../../tests/fixtures/template/layer-linux).homeConfigurations."jane@devbox".config;
        darwin = (instance ../../tests/fixtures/template/layer-darwin).darwinConfigurations.jane-mac.config;
        mac = darwin.home-manager.users.jane;
        noSecrets = (instance ../../tests/fixtures/template/layer-linux-nosecrets).homeConfigurations."jane@devbox".config;
        # jane's Linux home with her overlay's own agents/ as a second work
        # source (docs/agents-and-mcp.md, "Your own items beside a team layer's").
        withOwn = fixture: ((instance ../../tests/fixtures/template/layer-linux).homeConfigurations."jane@devbox".extendModules {
          modules = [ {
            piste.agents.sources.work-own = {
              zone = "work";
              store = ../../tests/fixtures/agents + "/${fixture}";
              workdir = "/home/jane/piste/overlays/work/agents";
            };
          } ];
        }).config;
        ownAgents = fixture: builtins.tryEval (builtins.deepSeq (withOwn fixture).piste.agents.resolved.links true);
        ownPolicy = (import ../../lib/agents.nix { inherit lib; }).zonePolicy
          (lib.attrValues (withOwn "layer-own").piste.agents.sources) "work";
        names = ps: map lib.getName ps;
        # piste comes from mkFlake: in a Mac's system packages (system), a
        # Linux home's home.packages.
        ok = h: system: lib.elem "hello" (names h.home.packages)
          && lib.elem "piste" (names (h.home.packages ++ system))
          && h.home.sessionVariables.TEAM_LAYER == "fixture"
          && h.piste.agents.zone == "work"
          && h.piste.secrets.enable
          && h.piste.secrets.envVars ? EXAMPLE_TOKEN
          && h.programs.git.settings.user.email == "jane@example.com";
      in
      assert lib.assertMsg (ok linux [ ]) "layer-instance: the Linux home lacks the layer's tool, zone or secret names, or jane's own";
      # Secrets declined (no sops file yet): the home still evaluates down to
      # its activation package, secrets off.
      assert lib.assertMsg (!noSecrets.piste.secrets.enable
          && lib.hasSuffix ".drv" (instance ../../tests/fixtures/template/layer-linux-nosecrets).homeConfigurations."jane@devbox".activationPackage.drvPath)
        "layer-instance: the starter with secrets declined doesn't evaluate, or has secrets on";
      assert lib.assertMsg (ok mac darwin.environment.systemPackages) "layer-instance: the Mac home lacks the layer's tool, zone or secret names, or jane's own";
      assert lib.assertMsg (lib.elem "example-team-app" (map (c: c.name) darwin.homebrew.casks))
        "layer-instance: the Mac lacks the layer's cask";
      # The layer's zone denies unlisted third-party sources, so an overlay on
      # it gets the zone check that an unapproved one is refused.
      assert lib.assertMsg ((instance ../../tests/fixtures/template/layer-linux).checks.${system} ? zone-unapproved-rejected)
        "layer-instance: the fixture layer's zone doesn't deny unlisted sources";
      # jane's own third-party items beside the layer's (her overlay's
      # agents/, [policy] extends = true): they evaluate under the layer's
      # deny once she allows their source, and are refused when she doesn't.
      assert lib.assertMsg (ownAgents "layer-own").success
        "layer-instance: jane's own agents/ beside the layer's doesn't evaluate";
      assert lib.assertMsg (ownPolicy.unlisted == "deny" && lib.elem "github:example/own-plugins" ownPolicy.allow)
        "layer-instance: jane's own allow entries don't join the layer's deny policy";
      assert lib.assertMsg (!(ownAgents "layer-own-unlisted").success)
        "layer-instance: jane's own marketplace, which nobody allows, evaluates under the layer's deny";
      # The layer's scalars are piste.lib.mkPackDefault (its lists plain):
      # jane's plain value wins, with no mkForce, as the starter's host files
      # say, and the layer's wins over an engine default (mkDefault).
      assert lib.assertMsg (let
          teamLayer = m: builtins.tryEval ((instance ../../tests/fixtures/template/layer-linux).homeConfigurations."jane@devbox".extendModules {
            modules = [ { home.sessionVariables.TEAM_LAYER = m; } ];
          }).config.home.sessionVariables.TEAM_LAYER;
          mine = teamLayer "mine";
          engine = teamLayer (lib.mkDefault "engine");
        in mine.success && mine.value == "mine" && engine.success && engine.value == "fixture")
        "layer-instance: jane's plain home.sessionVariables.TEAM_LAYER doesn't win over the layer's, or the layer's doesn't win over a mkDefault";
      pkgs.runCommand "check-layer-instance" { } "touch $out";
    # mkFlake does a host's wiring (the thin starter): its hosts bring none of
    # it. A Mac whose home module takes `piste` and a Linux home on the dev
    # role (which sets sops options) evaluate with no imports of their own;
    # both have the sops and agents home modules and piste installed (the
    # Mac's in its system packages), and an overlay's
    # piste.packages.piste = null leaves piste out. The older hosts that
    # wire it all themselves are checks.mkflake-fixture's.
    checks.mkflake-wiring =
      let
        # A home module that takes the engine as an argument.
        takesPiste = { piste, ... }: { home.sessionVariables.PISTE_ARG = if piste ? lib then "engine" else "?"; };
        out = self.lib.mkFlake {
          inputs = { };
          self = { };
          user = "t";
          zone = "agent";
          hosts = {
            mac = {
              system = "aarch64-darwin";
              modules = [ { home-manager.users.t = { imports = [ takesPiste ]; home.stateVersion = "26.11"; }; } ];
            };
            "t@box" = {
              system = "x86_64-linux";
              roles = [ "dev" ];
              modules = [ takesPiste { home = { username = "t"; homeDirectory = "/home/t"; stateVersion = "26.11"; }; } ];
            };
          };
        };
        mac = out.darwinConfigurations.mac;
        box = out.homeConfigurations."t@box";
        macHome = mac.config.home-manager.users.t;
        noPiste = m: m.extendModules { modules = [ { piste.packages.piste = null; } ]; };
        # piste isn't a cli tool: dropping the category keeps the command
        # that switches back (only piste.packages.piste = null drops it).
        noCli = m: m.extendModules { modules = [ { piste.packages.cli.enable = false; } ]; };
        hasPiste = lib.any (p: lib.getName p == "piste");
        wired = h: h ? sops && h.piste ? agents && h.home.sessionVariables.PISTE_ARG == "engine";
        drv = d: builtins.unsafeDiscardStringContext d.drvPath;
      in
      assert lib.assertMsg (wired macHome) "mkflake-wiring: the Mac's home lacks the sops or agents module, or its modules don't get piste";
      assert lib.assertMsg (wired box.config) "mkflake-wiring: the Linux home lacks the sops or agents module, or piste";
      assert lib.assertMsg (hasPiste mac.config.environment.systemPackages) "mkflake-wiring: the Mac doesn't install piste";
      assert lib.assertMsg (hasPiste box.config.home.packages) "mkflake-wiring: the Linux home doesn't install piste";
      assert lib.assertMsg (!(hasPiste (noPiste mac).config.environment.systemPackages)
          && !(hasPiste (noPiste box).config.home.packages))
        "mkflake-wiring: piste.packages.piste = null still installs piste";
      assert lib.assertMsg (hasPiste (noCli mac).config.environment.systemPackages
          && hasPiste (noCli box).config.home.packages)
        "mkflake-wiring: piste.packages.cli.enable = false drops piste";
      pkgs.runCommand "check-mkflake-wiring" {
        mac = drv mac.system;
        box = drv box.activationPackage;
      } "touch $out";
    # The starters' stateVersions are the releases at the engine's pin: a new
    # Mac starts at nix-darwin's newest system.stateVersion (not the engine's
    # mkDefault 4), and every new home at home-manager's release. A lock bump
    # that moves past them fails here: set the templates' literals to the new
    # values and re-render the instances (PISTE_UPDATE_INSTANCES=1 go test
    # ./internal/overlay/).
    checks.template-stateversion =
      let
        fx = ../../tests/fixtures/template;
        layer = (import ../../tests/fixtures/layer/flake.nix).outputs {
          self = { outPath = ../../tests/fixtures/layer; };
          piste = self;
        };
        # An instance, the layer ones with the fixture layer as their input.
        of = dir: (import (fx + "/${dir}/flake.nix")).outputs {
          self = { outPath = fx + "/${dir}"; };
          piste = self;
          inherit layer;
        };
        macs = lib.genAttrs [ "darwin" "two-hosts" "layer-darwin" ] (d: (of d).darwinConfigurations.jane-mac.config);
        homes = lib.mapAttrs (_: c: c.home-manager.users.jane.home) macs
          // lib.mapAttrs (_: d: (of d).homeConfigurations."jane@devbox".config.home)
            { linux = "linux"; two-hosts-linux = "two-hosts"; layer-linux = "layer-linux"; };
        release = homes.linux.version.release;
        badMacs = lib.filterAttrs (_: c: c.system.stateVersion != c.system.maxStateVersion) macs;
        badHomes = lib.filterAttrs (_: h: h.stateVersion != release) homes;
      in
      assert lib.assertMsg (badMacs == { })
        "template-stateversion: system.stateVersion isn't nix-darwin's newest (${toString macs.darwin.system.maxStateVersion}): ${toString (lib.mapAttrsToList (n: c: "${n} has ${toString c.system.stateVersion}") badMacs)}";
      assert lib.assertMsg (badHomes == { })
        "template-stateversion: home.stateVersion isn't home-manager's release ${release}: ${toString (lib.mapAttrsToList (n: h: "${n} has ${h.stateVersion}") badHomes)}";
      pkgs.runCommand "check-template-stateversion" { } "touch $out";

    # The agent-bundle validator against a minimal bundle fixture.
    checks.devcontainer-agent-bundle =
      self.lib.validateAgentBundle pkgs ../../tests/fixtures/agent-bundle;

    # piste.agents resolver: golden link map, composed
    # instruction blocks, and the zone / collision / silent-drop guards, all
    # against fixture zones in tests/fixtures/agents.
    checks.agents-resolver =
      let
        t = import ../../tests/agents.nix { inherit lib; };
        failed = lib.attrNames (lib.filterAttrs (_: ok: !ok) t.negatives);
        golden = ../../tests/fixtures/agents/golden;
      in
      if failed != [ ] then
        throw "agents-resolver: negative case(s) did not fail as expected: ${lib.concatStringsSep ", " failed}"
      else
        pkgs.runCommand "check-agents-resolver" {
          nativeBuildInputs = [ pkgs.python3 ];
          linkmap = builtins.toJSON t.golden.linkmap;
          profile = builtins.toJSON t.golden.profile;
          claude = t.golden.blocks.claude;
          codex = t.golden.blocks.codex;
          passAsFile = [ "linkmap" "profile" "claude" "codex" ];
        } ''
          python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1])), indent=2, sort_keys=True))' \
            "$linkmapPath" > linkmap.json
          diff -u ${golden}/linkmap.json linkmap.json
          python3 -c 'import json,sys; print(json.dumps(json.load(open(sys.argv[1])), indent=2, sort_keys=True))' \
            "$profilePath" > profile.json
          diff -u ${golden}/profile-lean.json profile.json
          diff -u ${golden}/claude-block.md "$claudePath"
          diff -u ${golden}/codex-block.md "$codexPath"
          touch $out
        '';

    # The agent hooks (agents/hooks): piste-guard and the subagent guards. The
    # engine itself is tested in Go (checks.piste).
    checks.agents-hooks = pkgs.runCommand "check-agents-hooks" {
      nativeBuildInputs = [ pkgs.python3 pkgs.git pkgs.gitleaks ];
    } ''
      cp -r ${../../tests/agents} tests
      chmod -R u+w tests
      patchShebangs tests/fakes
      PISTE_GUARD_PY=${../../agents/hooks/piste-guard.py} HOME=$TMPDIR \
        python3 -m unittest discover -s tests -v
      touch $out
    '';

    # The registry derivation: a work registry of the shared
    # zone builds, loads back verified, carries no personal item, and
    # offers the agent-registry skill.
    checks.agents-registry =
      let
        reg = self.lib.mkAgentRegistry {
          inherit pkgs;
          zone = "work";
          sources = [ { name = "piste-agents"; zone = "shared"; store = ../../agents; } ];
        };
        pisteGo = piste.piste-go; # tested by checks.piste
      in
      pkgs.runCommand "check-agents-registry" { nativeBuildInputs = [ pkgs.jq ]; } ''
        export HOME=$TMPDIR
        jq -e '.zone == "work" and ([.items[].id | select(startswith("personal:"))] | length == 0)' ${reg}/registry.json
        ${pisteGo}/bin/piste agents --registry ${reg} show shared:skill/agent-registry | grep -q '^trust: *first-party'
        ${pisteGo}/bin/piste agents --registry ${reg} search --harness codex registry | grep -q '^shared:skill/agent-registry'
        touch $out
      '';

    # Any zone name works: an `agent` host resolves its own
    # zone's skills, and the zone guard holds for it as for personal / work.
    # A host zone of shared, a malformed name, or a source of another zone
    # fails evaluation.
    checks.agents-open-zones =
      let
        agent = extra: agentHome [ agentZone extra ];
        drv = extra: (agent extra).activationPackage.drvPath;
        links = (agent { }).config.piste.agents.resolved.links;
        bare = z: (bareHome z).activationPackage.drvPath;
        accepted = lib.filter (z: !throws (bare z)) [ "shared" "Work" "a:b" "my zone" "-x" "../x" ];
      in
      assert lib.assertMsg (!throws (drv { })) "agents-open-zones: an `agent` host fails to evaluate";
      assert lib.assertMsg (lib.any (l: l.name == "beta2" && l.repo == "agent") links)
        "agents-open-zones: the `agent` host does not link its own skill beta2";
      assert lib.assertMsg (!throws (bare "agent"))
        "agents-open-zones: an `agent` host with only the shared zone fails to evaluate";
      assert lib.assertMsg (accepted == [ ])
        "agents-open-zones: host zone(s) accepted: ${lib.concatMapStringsSep ", " (z: "\"${z}\"") accepted}";
      assert lib.assertMsg (throws (drv {
        piste.agents.sources.intruder = {
          zone = "work";
          store = ../../tests/fixtures/agents/work;
          workdir = "/nonexistent";
        };
      })) "agents-open-zones: a work source on an `agent` host is accepted";
      # The engine's skill links point into the piste checkout:
      # ~/piste by default, wherever piste.checkout says otherwise.
      assert lib.assertMsg
        ((agent { }).config.piste.agents.sources.piste-agents.workdir
          == "${(agent { }).config.home.homeDirectory}/piste/agents")
        "agents-open-zones: the engine's workdir is not <home>/piste/agents by default";
      assert lib.assertMsg
        ((agent { piste.checkout = "/srv/ck"; }).config.piste.agents.sources.piste-agents.workdir == "/srv/ck/agents")
        "agents-open-zones: the engine's workdir does not follow piste.checkout";
      pkgs.runCommand "check-agents-open-zones" { } "touch $out";

    # lib.zoneChecks against fixtures. A clean `agent`
    # overlay's checks build; a registry or bundle naming another zone fails
    # to build; a home of another zone, or a withSource that drops the
    # foreign source, fails evaluation. zone-unapproved-rejected comes only
    # for a deny zone that doesn't allow its fixture's marketplace, which
    # evaluates where no policy denies it (so only the policy can refuse it).
    checks.zone-checks-selftest =
      let
        fx = ../../tests/fixtures/zone-checks;
        home = (agentHome [ agentZone ]).config;
        # `agent` hosts: one with no zone source, so no [policy]; one whose
        # zone denies unlisted items but allows github:*.
        noPolicy = m: (agentHome [ { piste.agents = { enable = true; zone = "agent"; }; } m ]).config;
        wildcard = m: (agentHome [ {
          piste.agents = {
            enable = true;
            zone = "agent";
            sources.agent = {
              zone = "agent";
              store = fx + "/deny-wildcard";
              workdir = "/nonexistent";
            };
          };
        } m ]).config;
        unapproved = {
          piste.agents.sources.zone-check-unapproved = {
            zone = "agent";
            store = ../../lib/fixtures/unapproved;
            workdir = "/nonexistent";
          };
        };
        good = self.lib.zoneChecks {
          inherit pkgs;
          zone = "agent";
          homes.h = home;
          withSource = m: (agentHome [ agentZone m ]).config;
          bundle = fx + "/good-bundle";
          registry = fx + "/good-registry";
        };
        bad = self.lib.zoneChecks {
          inherit pkgs;
          zone = "agent";
          homes.h = home;
          bundle = fx + "/bad-bundle";
          registry = fx + "/bad-registry";
        };
        failure = pkgs.testers.testBuildFailure;
      in
      assert lib.assertMsg
        (lib.attrNames good == [ "zone-bundle" "zone-clean" "zone-foreign-rejected" "zone-registry" "zone-unapproved-rejected" ])
        "zone-checks-selftest: zoneChecks returned ${toString (lib.attrNames good)}";
      assert lib.assertMsg (lib.attrNames bad == [ "zone-bundle" "zone-clean" "zone-registry" ])
        "zone-checks-selftest: zone-foreign-rejected without withSource";
      assert lib.assertMsg
        (throws (self.lib.zoneChecks { inherit pkgs; zone = "work"; homes.h = home; }).zone-clean.drvPath)
        "zone-checks-selftest: zone-clean passes an `agent` home as zone work";
      assert lib.assertMsg
        (throws (self.lib.zoneChecks { inherit pkgs; zone = "agent"; homes.h = (bareHome "personal").config; }).zone-clean.drvPath)
        "zone-checks-selftest: zone-clean passes a home whose own zone is personal";
      assert lib.assertMsg
        (throws (self.lib.zoneChecks { inherit pkgs; zone = "agent"; homes.h = home; withSource = _: home; }).zone-foreign-rejected.drvPath)
        "zone-checks-selftest: zone-foreign-rejected passes a withSource that drops the foreign source";
      assert lib.assertMsg
        (throws (self.lib.zoneChecks { inherit pkgs; zone = "agent"; homes.h = home; withSource = _: home; }).zone-unapproved-rejected.drvPath)
        "zone-checks-selftest: zone-unapproved-rejected passes a withSource that drops the unapproved source";
      assert lib.assertMsg
        (!(self.lib.zoneChecks { inherit pkgs; zone = "agent"; homes.h = (bareHome "agent").config; withSource = noPolicy; }
          ? zone-unapproved-rejected))
        "zone-checks-selftest: zone-unapproved-rejected for a zone with no deny policy";
      assert lib.assertMsg ((lib.importTOML (fx + "/deny-wildcard/agents.toml")).policy.unlisted or "" == "deny")
        "zone-checks-selftest: the deny-wildcard fixture is no longer deny-unlisted";
      assert lib.assertMsg
        (!(self.lib.zoneChecks { inherit pkgs; zone = "agent"; homes.h = wildcard { }; withSource = wildcard; }
          ? zone-unapproved-rejected))
        "zone-checks-selftest: zone-unapproved-rejected for a deny zone that allows github:*";
      assert lib.assertMsg (!throws { inherit ((noPolicy unapproved).piste.agents.resolved) links deferred; })
        "zone-checks-selftest: the unapproved fixture source fails to evaluate where no policy denies it";
      assert lib.assertMsg (throws (self.lib.zoneChecks { inherit pkgs; zone = "shared"; homes.h = home; }).zone-clean.drvPath)
        "zone-checks-selftest: zone = \"shared\" accepted";
      assert lib.assertMsg (throws (self.lib.zoneChecks { inherit pkgs; zone = "agent"; homes = { }; }).zone-clean.drvPath)
        "zone-checks-selftest: zone-clean with no homes passes";
      pkgs.runCommand "check-zone-checks-selftest" { } ''
        ${lib.concatMapStrings (c: "test -e ${c}\n") (lib.attrValues good)}
        grep -q 'personal:skill/x' ${failure bad.zone-registry}/testBuildFailure.log
        grep -q 'intruder (work)' ${failure bad.zone-bundle}/testBuildFailure.log
        touch $out
      '';

    # lib.mkFlake over a fixture overlay: a darwin host with
    # an alias, a home host with a role and its own nixpkgs overlay, and the
    # engine's packages and apps re-exported beside the overlay's. The zone
    # (`agent`, a deny zone) gets its registry, bundle and devcontainer, and
    # every zone check builds; an overlay with no agents/ gets hosts and the
    # zone checks that need none. Smaller calls, evaluated only: a Mac-only
    # overlay gets packages and checks for Linux and for an x86_64-darwin
    # host, an overlay package replaces the engine's of that name whole,
    # attrsets do for the per-system arguments, and bad hosts, a role that
    # isn't one, an unknown devcontainer or bundle key, or an overlay check
    # named like a zone check throw (the new refusals with their messages).
    checks.mkflake-fixture =
      let
        fx = ../../tests/fixtures/mkflake;
        out = self.lib.mkFlake {
          inputs = { };
          self = { };
          user = "t";
          zone = "agent";
          hosts = {
            mac = {
              system = "aarch64-darwin";
              modules = [ (fx + "/hosts/mac.nix") ];
              aliases = [ "mac-alias" ];
            };
            "t@box" = {
              system = "x86_64-linux";
              roles = [ "dev" ];
              modules = [ (fx + "/hosts/box.nix") ];
              overlays = [ (final: prev: { mkflake-probe = prev.hello; }) ];
            };
          };
          packages = system: { probe = inputs.nixpkgs.legacyPackages.${system}.hello; };
          agents = ../../tests/fixtures/agents/agent;
          bundle.presets = [ "shared:bundle/base" ];
          devcontainer.name = "mkflake-fixture";
          checks = _: { probe = pkgs.runCommand "mkflake-probe" { } "touch $out"; };
        };
        mk = args: self.lib.mkFlake ({ inputs = { }; self = { }; user = "t"; zone = "agent"; } // args);
        # A home host with the shared zone only.
        bareBox = {
          system = "x86_64-linux";
          modules = [
            inputs.sops-nix.homeManagerModules.sops
            self.homeManagerModules.agents
            {
              home = { username = "t"; homeDirectory = "/home/t"; stateVersion = "24.05"; };
              piste.agents = { enable = true; zone = "agent"; };
            }
          ];
        };
        # An overlay with no agents/ of its own: shared content only.
        noAgents = mk { hosts."t@box" = bareBox; };
        # A Mac-only overlay, one of its hosts on x86_64-darwin (which nixpkgs
        # no longer builds for; nothing here evaluates that host).
        macOnly = mk {
          hosts = {
            mac = { system = "aarch64-darwin"; modules = [ (fx + "/hosts/mac.nix") ]; };
            old = { system = "x86_64-darwin"; };
          };
        };
        # An overlay package named like an engine one replaces it whole.
        hello = inputs.nixpkgs.legacyPackages.x86_64-linux.hello;
        swapped = (mk {
          hosts."t@box" = bareBox;
          packages = _: { piste = hello; };
        }).packages.x86_64-linux;
        # Attrsets where functions of the system may go; the probe module
        # needs the special arg probeArg.
        probeModule = { probeArg, ... }: { home.sessionVariables.PROBE = probeArg; };
        attrs = mk {
          hosts."t@box" = bareBox // { modules = bareBox.modules ++ [ probeModule ]; };
          extraSpecialArgs.probeArg = "probe";
          packages.probe = hello;
          checks.probe = pkgs.emptyFile;
          devcontainer = {
            name = "mkflake-attrs";
            modules = [ probeModule ];
            extraSpecialArgs.probeArg = "probe";
          };
        };
        # Hosts mkFlake refuses: forcing its outputs' names throws.
        badHosts = {
          alias-is-a-host = { a = { system = "aarch64-darwin"; aliases = [ "b" ]; }; b.system = "aarch64-darwin"; };
          alias-twice.a = { system = "aarch64-darwin"; aliases = [ "x" "x" ]; };
          alias-on-two-hosts = { a = { system = "aarch64-darwin"; aliases = [ "x" ]; }; b = { system = "aarch64-darwin"; aliases = [ "x" ]; }; };
          alias-on-linux."t@box" = { system = "x86_64-linux"; aliases = [ "y" ]; };
          unknown-key."t@box" = { system = "x86_64-linux"; role = [ "dev" ]; };
          other-system."t@box".system = "x86_64-windows";
          no-system."t@box".modules = [ ];
        };
        acceptedHosts = lib.attrNames (lib.filterAttrs
          (_: hosts: !throws (lib.attrNames (mk { inherit hosts; }).darwinConfigurations))
          badHosts);
        # Arguments mkFlake refuses: a role that isn't one (another name in
        # homeManagerModules included) and a devcontainer or bundle key it
        # doesn't read (devcontainer.overlays, which mkDevcontainer takes,
        # included).
        boxWith = roles: { "t@box" = bareBox // { inherit roles; }; };
        badArgs = lib.genAttrs [ "vscode" "sops" "agents" "packages" "secrets" "nope" ] (r: { hosts = boxWith [ "dev" r ]; })
          // {
            mac-role.hosts.mac = { system = "aarch64-darwin"; roles = [ "packages" ]; };
            devcontainer-overlays = { hosts = boxWith [ ]; devcontainer = { name = "x"; overlays = [ ]; }; };
            bundle-zone = { hosts = boxWith [ ]; agents = ../../tests/fixtures/agents/agent; bundle = { presets = [ ]; zone = "agent"; }; };
          };
        acceptedArgs = lib.attrNames (lib.filterAttrs
          (_: args: !throws (lib.attrNames (mk args).darwinConfigurations))
          badArgs);
        # What mkFlake says: its message, with the throw handed back as a value.
        said = args: (import ../../lib/mk-flake.nix { piste = self; fail = msg: { error = msg; }; }
          ({ inputs = { }; self = { }; user = "t"; zone = "agent"; } // args)).error or "(no error)";
        saying = name: want: lib.optional (said badArgs.${name} != want) "${name}: ${said badArgs.${name}}";
        wrongWords =
          saying "vscode" "mkFlake: host t@box: unknown role vscode (a role is base, dev, desktop, container or extras)"
          ++ saying "mac-role" "mkFlake: host mac: unknown role packages (a role is base, dev, desktop, container or extras)"
          ++ saying "devcontainer-overlays" "mkFlake: devcontainer: unknown key overlays (devcontainer takes name, modules, extraSpecialArgs)"
          ++ saying "bundle-zone" "mkFlake: bundle: unknown key zone (bundle takes presets)"
          ++ lib.optional (said { hosts = boxWith [ "sops" "nope" ]; }
              != "mkFlake: host t@box: unknown role sops (a role is base, dev, desktop, container or extras)\nmkFlake: host t@box: unknown role nope (a role is base, dev, desktop, container or extras)")
            "two bad roles: ${said { hosts = boxWith [ "sops" "nope" ]; }}"
          # A role that isn't a string (a module, say) still gets its line.
          ++ lib.optional (said { hosts = boxWith [ (x: x) ]; }
              != "mkFlake: host t@box: unknown role <function> (a role is base, dev, desktop, container or extras)")
            "a function role: ${said { hosts = boxWith [ (x: x) ]; }}"
          ++ lib.optional (said { hosts = boxWith [ "base" "dev" "desktop" "container" "extras" ]; } != "(no error)")
            "every role: ${said { hosts = boxWith [ "base" "dev" "desktop" "container" "extras" ]; }}";
        checksOf = o: lib.attrNames o.checks.x86_64-linux;
        darwin = out.darwinConfigurations;
        box = out.homeConfigurations."t@box";
      in
      assert lib.assertMsg (lib.attrNames darwin == [ "mac" "mac-alias" ])
        "mkflake-fixture: darwinConfigurations = ${toString (lib.attrNames darwin)}";
      assert lib.assertMsg (darwin.mac-alias.system.drvPath == darwin.mac.system.drvPath)
        "mkflake-fixture: the alias is a different system";
      assert lib.assertMsg (darwin.mac.config.home-manager.users.t.home.username == "t")
        "mkflake-fixture: the darwin host has no home-manager user t";
      assert lib.assertMsg (box.config.piste.roles.dev.editor.enable or false)
        "mkflake-fixture: roles = [ \"dev\" ] did not import the dev role";
      assert lib.assertMsg (box.pkgs ? mkflake-probe)
        "mkflake-fixture: the home host's overlays are not applied";
      assert lib.assertMsg
        (out.packages.x86_64-linux.piste.drvPath == self.packages.x86_64-linux.piste.drvPath
          && out.packages.x86_64-linux ? probe)
        "mkflake-fixture: packages are not the engine's plus the overlay's";
      assert lib.assertMsg (out.apps.x86_64-linux.piste.program == self.apps.x86_64-linux.piste.program)
        "mkflake-fixture: apps are not the engine's";
      assert lib.assertMsg
        (checksOf out == [ "probe" "zone-bundle" "zone-clean" "zone-foreign-rejected" "zone-registry" "zone-unapproved-rejected" ])
        "mkflake-fixture: checks.x86_64-linux = ${toString (checksOf out)}";
      assert lib.assertMsg
        (lib.all (n: out.packages.x86_64-linux ? ${n}) [ "agent-registry" "agent-bundle" "devcontainer" "devcontainer-load" ])
        "mkflake-fixture: the zone's linux packages are missing";
      assert lib.assertMsg
        (out.packages.aarch64-darwin ? agent-registry && !(out.packages.aarch64-darwin ? agent-bundle))
        "mkflake-fixture: aarch64-darwin wants agent-registry and no agent-bundle";
      assert lib.assertMsg (checksOf noAgents == [ "zone-clean" "zone-foreign-rejected" ])
        "mkflake-fixture: an overlay with no agents/ gets checks ${toString (checksOf noAgents)}";
      assert lib.assertMsg (!(noAgents.packages.x86_64-linux ? agent-registry))
        "mkflake-fixture: an overlay with no agents/ gets an agent-registry";
      assert lib.assertMsg
        (lib.attrNames macOnly.packages == [ "aarch64-darwin" "aarch64-linux" "x86_64-darwin" "x86_64-linux" ])
        "mkflake-fixture: an overlay with an x86_64-darwin host has packages for ${toString (lib.attrNames macOnly.packages)}";
      assert lib.assertMsg (swapped.piste.drvPath == hello.drvPath && !(swapped.piste ? piste-go))
        "mkflake-fixture: the overlay's piste is merged into the engine's, not put in its place";
      assert lib.assertMsg (lib.attrNames out.checks == [ "aarch64-darwin" "aarch64-linux" "x86_64-linux" ])
        "mkflake-fixture: checks only for ${toString (lib.attrNames out.checks)}";
      assert lib.assertMsg
        (lib.attrNames macOnly.checks == lib.attrNames macOnly.packages
          && checksOf macOnly == [ "zone-clean" "zone-foreign-rejected" "zone-unapproved-rejected" ])
        "mkflake-fixture: a Mac-only overlay has checks ${toString (lib.attrNames macOnly.checks)}, on x86_64-linux ${toString (checksOf macOnly)}";
      assert lib.assertMsg
        (throws (checksOf (mk { hosts."t@box" = bareBox; checks = _: { zone-clean = pkgs.emptyFile; }; })))
        "mkflake-fixture: an overlay check named zone-clean is accepted";
      assert lib.assertMsg (acceptedHosts == [ ])
        "mkflake-fixture: mkFlake accepts the bad hosts ${toString acceptedHosts}";
      assert lib.assertMsg (acceptedArgs == [ ])
        "mkflake-fixture: mkFlake accepts the bad arguments ${toString acceptedArgs}";
      assert lib.assertMsg (wrongWords == [ ])
        "mkflake-fixture: mkFlake's messages: ${lib.concatStringsSep "; " wrongWords}";
      assert lib.assertMsg (attrs.homeConfigurations."t@box".config.home.sessionVariables.PROBE == "probe")
        "mkflake-fixture: extraSpecialArgs as an attrset does not reach the home host";
      assert lib.assertMsg
        (attrs.packages.aarch64-darwin.probe == hello && checksOf attrs == [ "probe" "zone-clean" "zone-foreign-rejected" ])
        "mkflake-fixture: packages / checks as attrsets give checks ${toString (checksOf attrs)}";
      assert lib.assertMsg (lib.isString attrs.packages.x86_64-linux.devcontainer.drvPath)
        "mkflake-fixture: devcontainer.extraSpecialArgs as an attrset does not reach the image's home";
      pkgs.runCommand "check-mkflake-fixture" { } ''
        ${lib.concatMapStrings (c: "test -e ${c}\n") (lib.attrValues out.checks.${system} ++ lib.attrValues noAgents.checks.${system})}
        touch $out
      '';

    # The checkout layout: the engine's nixpkgs overlays live in
    # pkgs/overlays/, overlays/ is left to users' repos (gitignored), and
    # .piste marks the checkout root piste walks up to.
    checks.pkgs-overlays-moved =
      let
        shared = import inputs.nixpkgs {
          inherit system;
          overlays = [ self.overlays.shared ];
          config.allowUnfree = true;
        };
        ignored = lib.elem "/overlays/" (lib.splitString "\n" (builtins.readFile ../../.gitignore));
      in
      assert lib.assertMsg (!builtins.pathExists ../../overlays) "pkgs-overlays-moved: overlays/ still holds the engine's nixpkgs overlays";
      assert lib.assertMsg (builtins.pathExists ../../pkgs/overlays/30-rtk.nix) "pkgs-overlays-moved: no pkgs/overlays/30-rtk.nix";
      assert lib.assertMsg (builtins.pathExists ../../.piste) "pkgs-overlays-moved: no .piste marker at the root";
      assert lib.assertMsg ignored "pkgs-overlays-moved: .gitignore does not ignore /overlays/";
      assert lib.assertMsg (lib.all (n: shared ? ${n}) [ "rtk" "psst" "herdr" "komodo-cli" "crit" ])
        "pkgs-overlays-moved: overlays.shared lost a package";
      pkgs.runCommand "check-pkgs-overlays-moved" { } "touch $out";

    # The Herdr app's telemetry off: the module's wiring, for a Mac
    # and for Linux, and the merge itself, run against a home on disk.
    checks.herdr-settings =
      let
        merge = settings: import ../../modules/programs/herdr-app-settings.nix {
          inherit pkgs settings;
          inherit (inputs.home-manager.lib) hm;
          path = ".config/herdr-app/settings.json";
        };
        herdrHome = hostPkgs: modules: (inputs.home-manager.lib.homeManagerConfiguration {
          pkgs = hostPkgs;
          modules = [
            self.homeManagerModules.base
            {
              home.username = "t";
              home.homeDirectory = if hostPkgs.stdenv.hostPlatform.isDarwin then "/Users/t" else "/home/t";
              home.stateVersion = "24.05";
              piste.programs.herdr.enable = true;
            }
          ] ++ modules;
        }).config;
        mac = herdrHome inputs.nixpkgs.legacyPackages.aarch64-darwin;
        macHome = mac [ ];
        theirs = mac [ { piste.programs.herdr.app.settings.telemetry = true; } ];
        linuxHome = herdrHome pkgs [ ];
        step = macHome.home.activation.herdrAppSettings or null;
        off = merge { telemetry = false; };
      in
      assert lib.assertMsg (macHome.piste.programs.herdr.app.settings == { telemetry = false; })
        "herdr-settings: the module's default isn't { telemetry = false; }";
      assert lib.assertMsg (theirs.piste.programs.herdr.app.settings.telemetry == true
          && theirs.home.activation.herdrAppSettings.data != step.data)
        "herdr-settings: a person's own telemetry setting doesn't reach the merge";
      # The app opened from Finder reads ~/.config, whatever xdg.configHome says.
      assert lib.assertMsg ((mac [ { xdg.configHome = "/Users/t/elsewhere"; } ]).home.activation.herdrAppSettings.data == step.data)
        "herdr-settings: the merge follows xdg.configHome, but the app reads ~/.config";
      assert lib.assertMsg (step != null && lib.elem "linkGeneration" step.after)
        "herdr-settings: a Mac home has no herdrAppSettings step after linkGeneration";
      # A problem with the file warns; it never fails the switch.
      assert lib.assertMsg (builtins.match "[[:space:]]*run /nix/store/[^ ]*-herdr-app-settings \\|\\| warnEcho .*" step.data != null)
        "herdr-settings: the herdrAppSettings step can fail the switch";
      assert lib.assertMsg (!(linuxHome.home.activation ? herdrAppSettings))
        "herdr-settings: a Linux home has a herdrAppSettings step, but the app is Mac-only";
      pkgs.runCommand "check-herdr-settings" { nativeBuildInputs = [ pkgs.jq ]; } ''
        set -euo pipefail
        s=.config/herdr-app/settings.json
        mkdir home && cd home
        # A Mac where the app never ran: the file holds only the declared key.
        ${off}
        test "$(jq -c . $s)" = '{"telemetry":false}'
        # The app's own keys stay, in their order; telemetry goes back to false.
        echo '{"theme":"dark","telemetry":true,"ui":{"sidebar":32}}' > $s
        ${off}
        test "$(jq -c . $s)" = '{"theme":"dark","telemetry":false,"ui":{"sidebar":32}}'
        # A file that won't parse is left as it is, and the merge fails.
        printf '{"theme":' > $s
        if ${off}; then echo "herdr-settings: merged into a file that won't parse" >&2; exit 1; fi
        test "$(cat $s)" = '{"theme":'
        touch $out
      '';

    # docs/options.md, on x86_64-linux only: one page for every
    # machine. Not in packages, which lib.mkFlake re-exports into every
    # overlay, whose `nix flake check` would then evaluate it.
    legacyPackages.${onLinux "docs-options"} = docsOptions;
    # The committed page is the one docs-options builds.
    checks.${onLinux "docs-options"} = pkgs.runCommand "check-docs-options" { } (
      if builtins.pathExists ../../docs/options.md then ''
        if ! diff -u ${../../docs/options.md} ${docsOptions}; then
          echo "docs/options.md is stale: nix build .#docs-options && install -m 644 result docs/options.md" >&2
          exit 1
        fi
        touch $out
      '' else ''
        echo "docs/options.md is missing: nix build .#docs-options && install -m 644 result docs/options.md" >&2
        exit 1
      '');

    # The engine's own agent presets: base is lean (no rtk, no htmx, no
    # third-party plugins or tools), `guard` and `rtk` are two separate
    # opt-ins, rtk comes with its preset and not with the packages,
    # piste-agents is not a package, and .claude/settings.json holds the deny list.
    checks.agents-shared-presets =
      let
        ag = import ../../lib/agents.nix { inherit lib; };
        shared = { name = "shared"; zone = "shared"; store = ../../agents; workdir = "/x"; capabilities = true; };
        host = presets: ag.resolve { sources = [ shared ]; inherit presets; targets = [ "claude" "codex" ]; zone = "personal"; };
        hookNames = presets: map (e: e.name) (host presets).hooks.entries;
        linkNames = presets: map (l: l.name) (host presets).links;
        blockOf = presets: h: (lib.findFirst (i: i.harness == h) null (host presets).instructions).block;
        has = lib.hasInfix;
        decls = ag.manifestDecls [ shared ];
        settings = builtins.fromJSON (builtins.readFile ../../.claude/settings.json);
        pkgNames = presets: map lib.getName (agentHome [ { piste.agents = { enable = true; zone = "personal"; inherit presets; }; } ]).config.home.packages;
        denied = [
          "piste switch" "piste rollback" "piste update" "piste host" "piste adopt" "piste secrets"
          "piste github" "piste clean" "sops" "darwin-rebuild" "home-manager switch" "sudo" "git push"
        ];
        deny = settings.permissions.deny;
        subs = [ "switch" "rollback" "update" "host" "adopt" "secrets" "github" "clean" ];
        # A non-rtk preset that lists the rtk-rewrite hook still gets rtk on PATH.
        viaHook = (agentHome [ {
          piste.agents = {
            enable = true; zone = "personal"; presets = [ "base" "viahook" ];
            sources.own = { zone = "personal"; store = ../../tests/fixtures/agents/rtkhook; workdir = "/x/own"; };
          };
        } ]).config.home.packages;
      in
      assert lib.assertMsg (hookNames [ "base" ] == [ ] && !(lib.elem "htmx" (linkNames [ "base" ])))
        "agents-shared-presets: base carries hooks, or the htmx skill";
      assert lib.assertMsg (lib.all (h: !(has "rtk" (blockOf [ "base" ] h)) && !(has "RTK" (blockOf [ "base" ] h))) [ "claude" "codex" ])
        "agents-shared-presets: base still tells the agent about rtk";
      assert lib.assertMsg (lib.all (h: has "set up by piste" (blockOf [ "base" ] h)) [ "claude" "codex" ])
        "agents-shared-presets: base lacks the 'set up by piste' fragment";
      assert lib.assertMsg (hookNames [ "guard" ] == [ "guard-bash" "guard-read" ])
        "agents-shared-presets: guard is not guard-bash and guard-read";
      assert lib.assertMsg (hookNames [ "rtk" ] == [ "rtk-rewrite" ] && has "RTK" (blockOf [ "rtk" ] "claude") && has "rtk" (blockOf [ "rtk" ] "codex"))
        "agents-shared-presets: rtk is not the rtk-rewrite hook and its instruction";
      assert lib.assertMsg (hookNames [ "guard" "rtk" ] == [ "guard-bash" "guard-read" "rtk-rewrite" ])
        "agents-shared-presets: guard and rtk together are not all three hooks";
      assert lib.assertMsg (throws (host [ "hooks" ]).hooks)
        "agents-shared-presets: the old hooks preset still resolves";
      assert lib.assertMsg (lib.all (d: !(d ? tool) && !(d ? claude && d.claude ? plugin)) decls)
        "agents-shared-presets: agents.base.toml declares a plugin or tool (catalogue-only)";
      assert lib.assertMsg (!(lib.elem "rtk" (pkgNames [ "base" ])) && !(lib.elem "piste-agents" (pkgNames [ "base" "rtk" ])))
        "agents-shared-presets: rtk or piste-agents is in a base host's packages";
      assert lib.assertMsg (lib.elem "rtk" (pkgNames [ "base" "rtk" ]) && !(lib.elem "rtk" (pkgNames [ "base" "guard" ])))
        "agents-shared-presets: rtk is on PATH exactly when the rtk preset is selected";
      assert lib.assertMsg (lib.elem "rtk" (map lib.getName viaHook))
        "agents-shared-presets: rtk is not on PATH when a non-rtk preset lists the rtk-rewrite hook";
      assert lib.assertMsg (lib.all (c: lib.elem "Bash(${c})" deny && lib.elem "Bash(${c} *)" deny) denied)
        "agents-shared-presets: .claude/settings.json lacks a deny rule";
      assert lib.assertMsg (lib.all (c: lib.elem "Bash(piste * ${c})" deny && lib.elem "Bash(piste * ${c} *)" deny) subs
          && lib.elem "Bash(home-manager * switch)" deny && lib.elem "Bash(home-manager * switch *)" deny)
        "agents-shared-presets: the deny list misses a reworded piste or home-manager switch";
      assert lib.assertMsg (lib.all (r: lib.hasPrefix "Bash(" r) deny && !(lib.elem "Bash(git commit *)" deny))
        "agents-shared-presets: the deny list has a non-Bash rule, or denies git commit";
      pkgs.runCommand "check-agents-shared-presets" { } "touch $out";

    checks.flake-structure = pkgs.runCommand "check-flake-structure" { } ''
      test -d ${../../modules} || (echo "missing modules/" && exit 1)
      test -d ${../../cli} || (echo "missing cli/" && exit 1)
      touch $out
    '';

    # init's "Start minimal or with everything?" (piste init and init --layer):
    # the committed instances tests/fixtures/template/extras-darwin (the
    # engine's starter on a Mac, everything) and layer-linux-extras (the team
    # layer's starter on Linux, everything; cli/internal/overlay's
    # TestRenderInstances keeps both equal to what init renders) list the
    # extras role and get every part of it, the Mac's own parts included; the
    # minimal starters have none of it.
    checks.extras-starter =
      let
        fixtureLayer = (import ../../tests/fixtures/layer/flake.nix).outputs {
          self = { outPath = ../../tests/fixtures/layer; };
          piste = self;
        };
        instance = dir: (import (dir + "/flake.nix")).outputs {
          self = { outPath = dir; };
          piste = self;
          layer = fixtureLayer;
        };
        mac = (instance ../../tests/fixtures/template/extras-darwin).darwinConfigurations.jane-mac;
        macHome = mac.config.home-manager.users.jane;
        layerHome = (instance ../../tests/fixtures/template/layer-linux-extras).homeConfigurations."jane@devbox".config;
        minimalLinux = (instance ../../tests/fixtures/template/linux).homeConfigurations."jane@devbox".config;
        minimalMac = (instance ../../tests/fixtures/template/darwin).darwinConfigurations.jane-mac.config;
        # Every part a config declares, each on.
        parts = c: lib.attrNames (c.piste.roles.extras or { });
        allOn = c: parts c != [ ] && lib.all (p: c.piste.roles.extras.${p}.enable) (parts c);
      in
      assert lib.assertMsg (allOn layerHome && layerHome.programs.tmux.enable
          && layerHome.home.sessionVariables.TEAM_LAYER == "fixture")
        "extras-starter: the layer starter with everything lacks an extras part, dev's tmux or the layer's settings";
      assert lib.assertMsg (allOn macHome && allOn mac.config && lib.elem "casks" (parts mac.config))
        "extras-starter: the Mac starter with everything lacks an extras part (home: ${toString (parts macHome)}; system: ${toString (parts mac.config)})";
      assert lib.assertMsg (parts minimalLinux == [ ] && parts minimalMac == [ ]
          && parts minimalMac.home-manager.users.jane == [ ])
        "extras-starter: a minimal starter has the extras role";
      pkgs.runCommand "check-extras-starter" {
        mac = builtins.unsafeDiscardStringContext mac.system.drvPath;
      } "touch $out";
  };
}
