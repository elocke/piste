# Herdr — the desktop app for herdr-core's terminals (Apple silicon builds).
# https://github.com/herdrdev/herdr-app
#
# Release builds from herdr's feed, releases.herdr.dev/app/latest.json, which
# gives each target's sha256; `piste update-overlays` keeps this current. The
# bundle is signed, so it is copied as it is. Its own updater can't replace a
# copy in the store: new versions arrive with an update and a switch.
# @overlay-update herdr:app
self: super:

let
  version = "0.1.7";

  platformMap = {
    "aarch64-darwin" = {
      asset = "herdr-app-macos-aarch64.tar.gz";
      hash = "81af774cb0d934bf6c52b334b53270417d0d8f1d057148f97dbc2285568db11a";
    };
  };

  system = super.stdenv.hostPlatform.system;
  platform = platformMap.${system} or (throw "herdr-app: unsupported platform ${system}");
in
{
  herdr-app = super.stdenvNoCC.mkDerivation {
    pname = "herdr-app";
    inherit version;

    src = super.fetchurl {
      url = "https://releases.herdr.dev/app/${version}/${platform.asset}";
      sha256 = platform.hash;
    };

    sourceRoot = ".";
    dontFixup = true;

    installPhase = ''
      runHook preInstall
      mkdir -p $out/Applications
      cp -R Herdr.app $out/Applications/
      runHook postInstall
    '';

    meta = with super.lib; {
      description = "Desktop app for herdr-core's coding-agent terminals";
      homepage = "https://github.com/herdrdev/herdr-app";
      license = licenses.asl20;
      sourceProvenance = [ sourceTypes.binaryNativeCode ];
      platforms = builtins.attrNames platformMap;
    };
  };
}
