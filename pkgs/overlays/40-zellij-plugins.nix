# @overlay-update github:fresh2dev/zellij-autolock
# @overlay-update github:dj95/zjstatus
# @overlay-update github:b0o/zjstatus-hints
# @overlay-update github:imsnif/monocle
# @overlay-update github:karimould/zellij-forgot
# @overlay-update github:liam-mackie/zsm
# @overlay-update github:hiasr/vim-zellij-navigator
# @overlay-update github:sharph/zellij-worktree
# @overlay-update github:ishefi/zellaude
self: super:

let
  mkZellijPlugin = { pname, version, url, sha256, wasmName ? pname }:
    super.stdenv.mkDerivation {
      inherit pname version;
      src = super.fetchurl { inherit url sha256; };
      dontUnpack = true;
      installPhase = ''
        mkdir -p $out
        install -m644 $src $out/${wasmName}.wasm
      '';
      meta = with super.lib; {
        description = "Zellij plugin: ${pname}";
        platforms = platforms.all;
      };
    };
in
{
  zellij-plugins = {
    zellij-autolock = mkZellijPlugin {
      pname = "zellij-autolock";
      version = "0.3.0";
      url = "https://github.com/fresh2dev/zellij-autolock/releases/download/0.3.0/zellij-autolock.wasm";
      sha256 = "00mbb5ipr2szsknwf2ygf4cs4vg8k0d9s9qb93dfz0arg0dhfzi1";
    };

    zjstatus = mkZellijPlugin {
      pname = "zjstatus";
      version = "0.25.0";
      url = "https://github.com/dj95/zjstatus/releases/download/v0.25.0/zjstatus.wasm";
      sha256 = "1zmqhzpqqhsz7smzm1bxxzhw7rhz4h3kkhzsr441jvp536rflb18";
    };

    zjstatus-hints = mkZellijPlugin {
      pname = "zjstatus-hints";
      version = "0.1.4";
      url = "https://github.com/b0o/zjstatus-hints/releases/download/v0.1.4/zjstatus-hints.wasm";
      sha256 = "17bir2z85ip7x6zndy94x5wdrpqv2py3wf116kadn3jw0blmav4k";
    };

    monocle = mkZellijPlugin {
      pname = "monocle";
      version = "0.100.2";
      url = "https://github.com/imsnif/monocle/releases/download/v0.100.2/monocle.wasm";
      sha256 = "0b3r2d3wz42lffdsh28a4kwjxrhdgx7f94swfm75p5rdj76f5dsc";
    };

    zellij-forgot = mkZellijPlugin {
      pname = "zellij-forgot";
      version = "0.4.2";
      url = "https://github.com/karimould/zellij-forgot/releases/download/0.4.2/zellij_forgot.wasm";
      sha256 = "1ns9wjn1ncjapqpp9nn9kyhqydvl0fbnyiavd0lc3gcxa52l269i";
      wasmName = "zellij_forgot";
    };

    zsm = mkZellijPlugin {
      pname = "zsm";
      version = "0.5.0";
      url = "https://github.com/liam-mackie/zsm/releases/download/v0.5.0/zsm.wasm";
      sha256 = "1sd7csf6drm1h8pdi4277hp2k71zcc64qb5mdgc1xcajm6wan67y";
    };

    vim-zellij-navigator = mkZellijPlugin {
      pname = "vim-zellij-navigator";
      version = "0.3.0";
      url = "https://github.com/hiasr/vim-zellij-navigator/releases/download/0.3.0/vim-zellij-navigator.wasm";
      sha256 = "13f54hf77bwcqhsbmkvpv07pwn3mblyljx15my66j6kw5zva5rbp";
    };

    zellij-worktree = mkZellijPlugin {
      pname = "zellij-worktree";
      version = "0.1.0";
      url = "https://github.com/sharph/zellij-worktree/releases/download/v0.1.0/zellij-worktree.wasm";
      sha256 = "10hzrzx68zjwmnx14wdpsxs5hrwk5izssjbw5gspdgkxwr92y7jk";
    };

    # Claude Code activity awareness for the zellij tab bar.
    # On first plugin load, zellaude auto-installs ~/.config/zellij/plugins/zellaude-hook.sh
    # and registers the hook in ~/.claude/settings.json — no Nix-side hook wiring needed.
    zellaude = mkZellijPlugin {
      pname = "zellaude";
      version = "0.5.1";
      url = "https://github.com/ishefi/zellaude/releases/download/v0.5.1/zellaude.wasm";
      sha256 = "1y8p9yzm4i3yvqaa7w66zpb13myv30f5q03ww7kdz61gr7gaqx7b";
    };
  };
}
