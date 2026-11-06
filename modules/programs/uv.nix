{ ... }:

{
  # The piece's handle: disabledModules = [ { key = <this>; } ] drops it (its path doesn't).
  key = "pack:core/uv";

  programs.uv = {
    enable = true;
    # uv itself ships via piste.packages (packages-module.nix); this module
    # only owns ~/.config/uv/uv.toml.
    package = null;
    settings = {
      # Prefer uv's own portable CPython builds over the Nix interpreter for
      # `uv venv` / `uv run` / `uv tool`. They link against the host's libstdc++
      # and glibc, so compiled wheels just work; the Nix python needs the
      # LD_LIBRARY_PATH wrapper in pkgs/overlays/80-python-manylinux.nix. "managed"
      # (not "only-managed") still honours an explicit --python /path.
      python-preference = "managed";
    };
  };
}
