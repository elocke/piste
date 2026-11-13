# @overlay-update github:rtk-ai/rtk
self: super:

let
  version = "1.1.0";

  platformMap = {
    "aarch64-darwin" = {
      asset = "rtk-aarch64-apple-darwin.tar.gz";
      hash = "19rv0ypnqsi40aqmsg13vqwlrw10ni74wp69bq8hpyacjnq779wd";
    };
    "x86_64-darwin" = {
      asset = "rtk-x86_64-apple-darwin.tar.gz";
      hash = "083cd844cah01zl75ni5a46lqb5r7wf9pi09cj744n1bajwyh8qy";
    };
    "x86_64-linux" = {
      asset = "rtk-x86_64-unknown-linux-musl.tar.gz";
      hash = "139pr7bsnlai2gjwj9hgi8mwhkdgzbifpx59n1xabnl9jhdssm4l";
    };
    "aarch64-linux" = {
      asset = "rtk-aarch64-unknown-linux-gnu.tar.gz";
      hash = "1v8rjzw2h7rlgjlrsxphyjhfyl56q6plilspnyy8960jn0x0rh55";
    };
  };

  platform = platformMap.${super.stdenv.hostPlatform.system} or null;
in
{
  rtk = super.stdenv.mkDerivation {
    pname = "rtk";
    inherit version;

    src = super.fetchurl {
      url = "https://github.com/rtk-ai/rtk/releases/download/v${version}/${platform.asset}";
      sha256 = platform.hash;
    };

    sourceRoot = ".";
    dontStrip = true;

    installPhase = ''
      mkdir -p $out/bin
      install -m755 rtk $out/bin/rtk
    '';

    meta = with super.lib; {
      description = "CLI proxy that reduces LLM token consumption by 60-90%";
      homepage = "https://github.com/rtk-ai/rtk";
      license = licenses.unfree;
      platforms = builtins.attrNames platformMap;
      mainProgram = "rtk";
    };
  };
}
