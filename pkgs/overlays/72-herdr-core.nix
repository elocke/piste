# herdr-core — the terminals the Herdr app (71-herdr-app.nix) drives; the app
# finds it on the login shell's PATH. https://github.com/herdrdev/herdr-core
#
# Release builds from herdr's feed, releases.herdr.dev/core/latest.json;
# `piste update-overlays` keeps this current. Only the Apple silicon build: the
# app is the reason to install it.
# @overlay-update herdr:core
self: super:

let
  version = "0.1.14";

  platformMap = {
    "aarch64-darwin" = {
      asset = "herdr-core-macos-aarch64.tar.gz";
      hash = "ca524329d2e0630219550055dcc71f42aafd85fba356e0d543d6f51244487970";
    };
  };

  system = super.stdenv.hostPlatform.system;
  platform = platformMap.${system} or (throw "herdr-core: unsupported platform ${system}");
in
{
  herdr-core = super.stdenvNoCC.mkDerivation {
    pname = "herdr-core";
    inherit version;

    src = super.fetchurl {
      url = "https://releases.herdr.dev/core/${version}/${platform.asset}";
      sha256 = platform.hash;
    };

    sourceRoot = ".";
    dontFixup = true;

    # `hc` is the core's short name; the app looks for either.
    installPhase = ''
      runHook preInstall
      install -Dm755 herdr-core $out/bin/herdr-core
      ln -s herdr-core $out/bin/hc
      runHook postInstall
    '';

    meta = with super.lib; {
      description = "Terminals for coding agents, driven by the Herdr app";
      homepage = "https://github.com/herdrdev/herdr-core";
      license = licenses.asl20;
      sourceProvenance = [ sourceTypes.binaryNativeCode ];
      platforms = builtins.attrNames platformMap;
      mainProgram = "herdr-core";
    };
  };
}
