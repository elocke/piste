#!/usr/bin/env bash
# Bring cli/default.nix's vendorHash up to date with cli/go.sum.
#
# Renovate bumps go.mod and go.sum but can't run Nix, so buildGoModule's
# vendorHash goes stale and checks.piste fails on the go-modules fixed-output
# derivation. Building that derivation with the old hash proves nothing: a
# store (or a cache) that already holds the old output answers without
# fetching. So it builds with a fake hash, which no store holds, and takes the
# hash Nix reports it got.
#
# Exit 0 with the file current (changed or not); non-zero on any other error,
# with the file as it was.
set -euo pipefail

file=cli/default.nix
attr=".#packages.x86_64-linux.piste.piste-go.goModules"
fake="sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

old=$(sed -nE 's/^[[:space:]]*vendorHash = "(sha256-[A-Za-z0-9+/=]+)";.*/\1/p' "$file")
if [ -z "$old" ] || [ "$(printf '%s\n' "$old" | wc -l)" -ne 1 ]; then
  echo "go-vendor-hash: expected exactly one vendorHash in $file" >&2
  exit 1
fi

log=$(mktemp)
trap 'rm -f "$log"' EXIT
sed -i "s#\"$old\"#\"$fake\"#" "$file"
if nix build --no-link -L "$attr" >"$log" 2>&1; then
  sed -i "s#\"$fake\"#\"$old\"#" "$file"
  echo "go-vendor-hash: the go-modules build passed with a fake hash" >&2
  exit 1
fi

new=$(sed -nE 's/^[[:space:]]*got:[[:space:]]*(sha256-[A-Za-z0-9+/=]+).*/\1/p' "$log" | head -n 1)
if [ -z "$new" ]; then
  sed -i "s#\"$fake\"#\"$old\"#" "$file"
  cat "$log" >&2
  echo "go-vendor-hash: the go-modules build failed, and not on its hash" >&2
  exit 1
fi

sed -i "s#\"$fake\"#\"$new\"#" "$file"
if [ "$new" = "$old" ]; then
  echo "go-vendor-hash: $old is current."
else
  echo "go-vendor-hash: $old -> $new"
fi
