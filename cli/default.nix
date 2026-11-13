{ pkgs }:

let
  inherit (pkgs) lib;

  # Runtime CLI dependencies — kept here as the single source of truth.
  # An overlay's piste-<name> plugin can take this list from passthru.runtimeDeps,
  # so its wrapper PATH stays in sync without restating it.
  #
  # Three binaries are deliberately absent. The wrapper uses `--prefix PATH`, so
  # anything listed here SHADOWS the system's own copy:
  #
  #   darwin-rebuild — provided by nix-darwin system activation, not by a
  #                    standalone nixpkgs package.
  #
  #   nix            — owned by whatever installed Nix (Determinate Nix here,
  #                    the upstream installer, or NixOS). A nixpkgs `nix`
  #                    overrides it for every command piste shells out to, which
  #                    silently drops installer-specific settings — you get
  #                    "warning: unknown setting 'lazy-trees' / 'eval-cores'"
  #                    — and skews the client against the running daemon.
  #                    Worse, it splits the CLI in half: commands routed
  #                    through darwin-rebuild get the real nix (darwin-rebuild
  #                    resets PATH), while `piste update` / `piste check` do not, so
  #                    verification and activation stop matching.
  #
  # A third is absent for a different reason:
  #
  #   openssh        — the system's ssh is the one a person's ~/.ssh/config was
  #                    written for. Nixpkgs' openssh rejects macOS-only options
  #                    (GitHub's own Mac instructions add UseKeychain), so a
  #                    git remote over ssh would die in `piste setup git@...`.
  #
  # The CLI checks for the first two at runtime with a clear error message
  # (internal/platform: CheckNix; darwin-rebuild before a darwin switch). ssh is
  # the system's and unchecked; setup's ssh-keygen step prints an install hint.
  runtimeDeps = with pkgs; [
    git
    gawk
    yq-go
    sops
    ssh-to-age
    age
    home-manager
    rsync
    gitleaks # piste leaks (the leak gate)
  ];

  # The top release in CHANGELOG.md (`## vX.Y.Z (date)`, newest first), 0.0.0
  # before the first; `changelog cut` writes it (cli/cmd/changelog).
  version =
    let
      releases = lib.filter (m: m != null) (
        map (builtins.match "## v([0-9]+\\.[0-9]+\\.[0-9]+)( .*)?") (
          lib.splitString "\n" (builtins.readFile ../CHANGELOG.md)
        )
      );
    in
    if releases == [ ] then "0.0.0" else lib.head (lib.head releases);

  # The Go piste itself: one static binary, no wrapper.
  piste-go = pkgs.buildGoModule {
    pname = "piste-go";
    inherit version;
    src = lib.fileset.toSource {
      root = ./.;
      fileset = lib.fileset.unions [
        ./go.mod ./go.sum ./cmd ./internal ./e2e
        ./notices.go ./notices_test.go ./THIRD_PARTY_NOTICES.md
      ];
    };
    vendorHash = "sha256-kyZ1FRknwSVWroI1SZaFq5pvNRHdOxge+v/jECEoj2A=";
    subPackages = [ "cmd/piste" ];
    env.CGO_ENABLED = 0;
    ldflags = [
      "-s"
      "-w"
      "-X github.com/elocke/piste/cli/internal/cli.Version=${version}"
    ];
    nativeBuildInputs = [ pkgs.installShellFiles ];

    # Users get this build, tests off: the same derivation as the agents
    # registry's and the devcontainer's. The tests run in `piste-go-tested`
    # (checks.piste), which carries their inputs, so none of them reaches
    # a user's closure or hash.
    doCheck = false;

    postInstall = lib.optionalString (pkgs.stdenv.buildPlatform.canExecute pkgs.stdenv.hostPlatform) ''
      installShellCompletion --cmd piste \
        --bash <($out/bin/piste completion bash) \
        --zsh <($out/bin/piste completion zsh) \
        --fish <($out/bin/piste completion fish)
      mkdir -p $out/share/man/man1
      $out/bin/piste man > $out/share/man/man1/piste.1
    '' + ''
      install -Dm644 THIRD_PARTY_NOTICES.md $out/share/doc/piste/THIRD_PARTY_NOTICES.md
    '';

    meta.mainProgram = "piste";
  };

  # piste-go with its tests on: the unit tests and the end-to-end harness (cli/e2e:
  # goldens in testdata/golden). python3 runs the fake claude /
  # codex. This is checks.piste; no machine builds it.
  piste-go-tested = piste-go.overrideAttrs (_: {
    doCheck = true;
    nativeCheckInputs = [ pkgs.python3 pkgs.git pkgs.gitleaks pkgs.sops ];
    preCheck = ''
      # The fake claude / codex for the user-scope hooks goldens.
      export PISTE_AGENTS_FAKES=${../tests/agents/fakes}
      # The starter template and its rendered instances (piste init's tests).
      export PISTE_TEMPLATE_DIR=${../templates/default}
      export PISTE_TEMPLATE_INSTANCES=${../tests/fixtures/template}
      export PISTE_LAYER_DIR=${../tests/fixtures/layer}
      # The engine's git hooks (leaks.txtar runs the real pre-push).
      export PISTE_GITHOOKS=${../.githooks}
    '';
    checkPhase = ''
      runHook preCheck
      go test ./...
      runHook postCheck
    '';
  });

  # The `piste` users run: the Go binary with the runtime PATH.
in
pkgs.runCommand "piste-${version}" {
  nativeBuildInputs = [ pkgs.makeWrapper ];
  passthru = { inherit runtimeDeps piste-go piste-go-tested version; };
  meta = {
    description = "Sets up Macs, Linux and NixOS machines from one Nix configuration, and keeps them that way";
    homepage = "https://github.com/elocke/piste";
    license = lib.licenses.bsd3;
    mainProgram = "piste";
  };
} ''
  mkdir -p $out/bin
  makeWrapper ${piste-go}/bin/piste $out/bin/piste \
    --prefix PATH : ${lib.makeBinPath runtimeDeps}
  ln -s ${piste-go}/share $out/share
''
