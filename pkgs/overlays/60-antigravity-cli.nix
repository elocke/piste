# Google Antigravity CLI — agent-first terminal that succeeds gemini-cli for
# Google One / unpaid Code Assist users (gemini-cli stops serving those tiers
# on 2026-06-18). The official installer ships a flat native Go binary called
# `agy` via a Cloud Run manifest service; we mirror the per-platform tarballs
# from storage.googleapis.com using the manifest's sha512 values.
#
# Bump procedure (no @overlay-update support — Antigravity isn't on GitHub):
#   for p in darwin_arm64 darwin_amd64 linux_amd64 linux_arm64; do
#     curl -s "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/$p.json"
#   done
# Copy version, url, sha512 into platformMap below.
self: super:

let
  version = "1.3.6";

  platformMap = {
    "aarch64-darwin" = {
      url = "https://storage.googleapis.com/antigravity-public/antigravity-cli/1.3.6-5068262057312256/darwin-arm/cli_mac_arm64.tar.gz";
      sha512 = "473a34b685d0a09d48790e8b86bc3e545dfab889fe0d8fc81cb7e1fdfc336fe78ce57022e3b438c754c93c9cffeb13fdbd177e50fdedc087d52732fa5475183f";
    };
    "x86_64-darwin" = {
      url = "https://storage.googleapis.com/antigravity-public/antigravity-cli/1.3.6-5068262057312256/darwin-x64/cli_mac_x64.tar.gz";
      sha512 = "117e328adfadb69a7543353fe87b5276c00ae189f2bb19868da6e667297c35c5f68b3537a35428b5b9f5ac3fee419296d9547990b24af81e6848e6a97b4a825c";
    };
    "x86_64-linux" = {
      url = "https://storage.googleapis.com/antigravity-public/antigravity-cli/1.3.6-5068262057312256/linux-x64/cli_linux_x64.tar.gz";
      sha512 = "0f95f2ff2474e5926e6ac8d0661a16dab2dd31596518c68d687b3d0ccb6da518bf94fd86aa2ce57f28ff07a8529e9b268a12b217070bac8b44819dce884e9323";
    };
    "aarch64-linux" = {
      url = "https://storage.googleapis.com/antigravity-public/antigravity-cli/1.3.6-5068262057312256/linux-arm/cli_linux_arm64.tar.gz";
      sha512 = "dbe9585460ba8ea30959c6a2599d3bef1e7710331ef8c9e418863a300d19351906e16be63204f7d77434c773796b5ae222ae135ac033db5abf1542b076d02b0e";
    };
  };

  system = super.stdenv.hostPlatform.system;
  platform = platformMap.${system} or (throw "antigravity-cli: unsupported platform ${system}");
in
{
  antigravity-cli = super.stdenv.mkDerivation {
    pname = "antigravity-cli";
    inherit version;

    src = super.fetchurl {
      inherit (platform) url sha512;
    };

    # Tarball contains a single file `antigravity` at the root.
    sourceRoot = ".";
    dontStrip = true;

    # Linux release is a glibc-linked Go binary; autoPatchelfHook fixes the
    # interpreter and library paths so it runs from the Nix store.
    nativeBuildInputs = super.lib.optionals super.stdenv.hostPlatform.isLinux [ super.autoPatchelfHook ];
    buildInputs = super.lib.optionals super.stdenv.hostPlatform.isLinux [ super.stdenv.cc.cc.lib ];

    installPhase = ''
      runHook preInstall
      install -Dm755 antigravity $out/bin/agy
      runHook postInstall
    '';

    meta = with super.lib; {
      description = "Google Antigravity CLI (agy) — agent-first terminal succeeding gemini-cli";
      homepage = "https://antigravity.google";
      license = licenses.unfree;
      platforms = builtins.attrNames platformMap;
      mainProgram = "agy";
    };
  };
}
