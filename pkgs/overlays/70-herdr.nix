# Herdr — terminal-based multiplexer for AI coding agents (real terminal per
# agent, state-aware sidebar, persistent sessions). Single static Rust binary.
# https://github.com/herdrdev/herdr
#
# Release assets are flat binaries; `piste update-overlays` keeps this current
# (it hashes the file fetchurl downloads).
# @overlay-update github:herdrdev/herdr
self: super:

let
  version = "0.9.5";

  platformMap = {
    "aarch64-darwin" = {
      asset = "herdr-macos-aarch64";
      hash = "1qm7qdbh2afh7npli3m17174w5zlj91qjb1q45a07a0558iw4xzv";
    };
    "x86_64-darwin" = {
      asset = "herdr-macos-x86_64";
      hash = "0s8k3z8fmysm14yap4f3j5iw8mmdpw38nws5wbilarvzxh4akc2z";
    };
    "x86_64-linux" = {
      asset = "herdr-linux-x86_64";
      hash = "17cjb6gxh2wna8vv4bil82dz0qflfrpjcckwswc037jaifyfin8l";
    };
    "aarch64-linux" = {
      asset = "herdr-linux-aarch64";
      hash = "0z49850w8bkcvbl6zr7l6nif9j6azyfjgvw06r95j2555mpm7mqp";
    };
  };

  system = super.stdenv.hostPlatform.system;
  platform = platformMap.${system} or (throw "herdr: unsupported platform ${system}");
in
{
  herdr = super.stdenv.mkDerivation {
    pname = "herdr";
    inherit version;

    src = super.fetchurl {
      url = "https://github.com/herdrdev/herdr/releases/download/v${version}/${platform.asset}";
      sha256 = platform.hash;
    };

    # Asset is a single flat binary, not an archive — skip the unpack phase and
    # install $src directly. The Linux build is static-pie linked, so no
    # autoPatchelfHook is needed; keep symbols (dontStrip) to match upstream.
    dontUnpack = true;
    dontStrip = true;

    installPhase = ''
      runHook preInstall
      install -Dm755 $src $out/bin/herdr
      runHook postInstall
    '';

    meta = with super.lib; {
      description = "Terminal multiplexer for managing multiple AI coding agents";
      homepage = "https://github.com/herdrdev/herdr";
      license = licenses.agpl3Plus;
      platforms = builtins.attrNames platformMap;
      mainProgram = "herdr";
    };
  };
}
