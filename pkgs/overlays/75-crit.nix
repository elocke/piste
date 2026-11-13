# crit — local review loop for agent plans and code (https://crit.md).
# Not in nixpkgs. Built from source like the Homebrew formula (go build of
# ./cmd/crit). No @overlay-update marker: that updater rewrites release-asset
# hashes, not vendorHash. To bump: set version and hash, build once, and copy
# the vendorHash from the mismatch error.
self: super:

{
  crit = super.buildGoModule rec {
    pname = "crit";
    version = "0.22.0";

    src = super.fetchFromGitHub {
      owner = "tomasz-tomczyk";
      repo = "crit";
      rev = "v${version}";
      hash = "sha256-HGigdrTMa+Cs/4wcEJAUT/PRYXHT3LG1Ue1VomJaAX4=";
    };

    vendorHash = "sha256-WUTBKcMEnOnMnHsABjP2iKr1pNJcHfG7RNw7cBx0Hl4=";

    subPackages = [ "cmd/crit" ];
    nativeCheckInputs = [ super.git ]; # tests run git init
    # These write fake-agent.sh with a #!/bin/bash shebang; the Linux build
    # sandbox has no /bin/bash (macOS does), so they fail only on Linux.
    checkFlags = [ "-skip=^TestStoryLLM_" ];
    ldflags = [ "-s" "-w" "-X main.version=${version}" "-X main.commit=nix" ];

    meta = {
      description = "Review plans and code locally with inline comments for coding agents";
      homepage = "https://crit.md";
      license = super.lib.licenses.mit;
      mainProgram = "crit";
    };
  };
}
