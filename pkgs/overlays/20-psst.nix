self: super: {
  psst = super.buildNpmPackage {
    pname = "psst";
    version = "0.7.3";

    src = super.fetchFromGitHub {
      owner = "lucaconlaq";
      repo = "psst";
      rev = "44a2b073f3d918b7649c7ca43cd0d5cccc19b3b4";
      hash = "sha256-sjRopRzVq1W08aN6kJ7S+oku0LvMfShblaAskyd2Y0I=";
    };

    npmDepsHash = "sha256-wHwhE+6l+vmKVEoHzh06W2Y/8aWEgWxa80Np6mDS8sU=";

    nodejs = super.nodejs;

    buildPhase = ''
      runHook preBuild
      npm run build
      runHook postBuild
    '';

    dontNpmInstall = true;

    installPhase = ''
      runHook preInstall
      mkdir -p $out/lib/psst $out/bin
      cp -r dist node_modules package.json psst $out/lib/psst/
      ln -s $out/lib/psst/psst $out/bin/psst
      runHook postInstall
    '';

    meta = with super.lib; {
      description = "Secret injection for LLM and dev commands";
      homepage = "https://github.com/lucaconlaq/psst";
      license = licenses.isc;
      platforms = platforms.unix;
    };
  };
}
