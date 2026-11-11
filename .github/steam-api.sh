#!/usr/bin/env bash
# Copies the Steamworks redistributable for GOOS/GOARCH next to the binary in DEST,
# like nwmpublisher ships it. Platforms without a library are skipped.
set -euo pipefail
goos=$1 goarch=$2 dest=$3
src=third_party/steamworks
case "$goos/$goarch" in
	linux/amd64) file=linux64/libsteam_api.so ;;
	linux/arm64) file=linuxarm64/libsteam_api.so ;;
	windows/amd64) file=win64/steam_api64.dll ;;
	darwin/*) file=osx/libsteam_api.dylib ;; # universal binary
	*) echo "no Steamworks library for $goos/$goarch, skipping"; exit 0 ;;
esac
(cd "$src" && grep " $file\$" SHA256SUMS | sha256sum -c -)
cp "$src/$file" "$dest/"
cp "$src/README.md" "$dest/STEAMWORKS.md"
