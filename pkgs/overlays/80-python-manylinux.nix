# python312 wrapped so pip/uv-installed manylinux wheels load on non-NixOS Linux.
#
# Nix's python is built against Nix's glibc/libstdc++ and sets no rpath for
# them, so a wheel with compiled extensions (numpy, torch, docling, …)
# installed into a venv dies at import time with
#   ImportError: libstdc++.so.6: cannot open shared object file
# on a home-manager-on-Ubuntu host. NixOS fixes this with nix-ld; a standalone
# home-manager host has no system hook, so instead the interpreter itself
# carries LD_LIBRARY_PATH pointing at nixpkgs' manylinux2014 library set.
#
# Scope: only python processes see the variable. venvs created from this
# interpreter symlink back to the wrapper (argv[0] is preserved), so they
# inherit the fix. Darwin needs none of this and gets plain python312.
self: super:

let
  isLinux = super.stdenv.hostPlatform.isLinux;
  python = super.python312;
  # libstdc++, libgcc_s, zlib, glib, libX11… — the set manylinux wheels may
  # assume the host provides.
  manylinuxLibs = super.pythonManylinuxPackages.manylinux2014Package;
in
{
  python312-manylinux =
    if !isLinux then
      python
    else
      super.symlinkJoin {
        name = "python312-manylinux-${python.version}";
        paths = [ python ];
        nativeBuildInputs = [ super.makeBinaryWrapper ];
        # symlinkJoin links each bin entry to the original store path, where
        # python3 -> python3.12 resolves *inside* the unwrapped package. Wrap
        # the real binary once and re-point the aliases at the wrapper.
        postBuild = ''
          rm -f $out/bin/python $out/bin/python3
          wrapProgram $out/bin/python${python.pythonVersion} \
            --prefix LD_LIBRARY_PATH : "${manylinuxLibs}/lib:${super.stdenv.cc.cc.lib}/lib"
          ln -s python${python.pythonVersion} $out/bin/python3
          ln -s python${python.pythonVersion} $out/bin/python
        '';
        passthru = python.passthru // { inherit (python) pythonVersion version; };
        inherit (python) meta;
      };
}
