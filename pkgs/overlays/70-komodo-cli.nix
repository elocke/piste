# @overlay-update github:moghtech/komodo
# Komodo CLI (`km`) — build/deploy/stack/server management for Komodo.
# Ships prebuilt release binaries (raw ELF/Mach-O, not tarballs).
self: super:

let
  version = "2.3.4";

  # Release assets: km-x86_64 (linux), km-aarch64 (linux), km-apple (macOS universal).
  platformMap = {
    "x86_64-linux" = {
      asset = "km-x86_64";
      hash = "0db49yflr36yn2zyv8z5356x1m7aizchmpcfn4965wzjy2sfzx6d";
    };
    "aarch64-linux" = {
      asset = "km-aarch64";
      hash = "0p1nq0wh9h7qw44c9ridaf9bvnm5fvixzk01lj4dy5r41s3f6dw3";
    };
    "x86_64-darwin" = {
      asset = "km-apple";
      hash = "1z68rw1v29wsbga3wvcz6y8ivp1i5lqb2473j2yms2is9vf8jj6i";
    };
    "aarch64-darwin" = {
      asset = "km-apple";
      hash = "1z68rw1v29wsbga3wvcz6y8ivp1i5lqb2473j2yms2is9vf8jj6i";
    };
  };

  platform = platformMap.${super.stdenv.hostPlatform.system} or null;
  isLinux = super.stdenv.hostPlatform.isLinux;
in
{
  komodo-cli = super.stdenv.mkDerivation {
    pname = "komodo-cli";
    inherit version;

    src = super.fetchurl {
      url = "https://github.com/moghtech/komodo/releases/download/v${version}/${platform.asset}";
      sha256 = platform.hash;
    };

    dontUnpack = true;
    dontStrip = true;

    # The Linux binaries are dynamically linked glibc PIEs (libgcc_s, libm, libc);
    # patch them onto the Nix runtime. macOS binaries need no patching.
    nativeBuildInputs = super.lib.optionals isLinux [ super.autoPatchelfHook ];
    buildInputs = super.lib.optionals isLinux [ super.stdenv.cc.cc.lib ];

    installPhase = ''
      runHook preInstall
      install -Dm755 $src $out/bin/km
      runHook postInstall
    '';

    meta = with super.lib; {
      description = "Komodo CLI (km) — manage Komodo builds, deploys, stacks, and servers";
      homepage = "https://komo.do/docs/ecosystem/cli";
      license = licenses.gpl3Only;
      platforms = builtins.attrNames platformMap;
      mainProgram = "km";
    };
  };
}
