#!/usr/bin/env bash
# Builds the archive the install scripts download.
#
#     scripts/package_release.sh linux-x64
#
# Writes dist/treadle-<target>.tar.gz and its .sha256. Every platform uses tar.gz
# because Windows 10 and later ship a tar that reads it.
set -euo pipefail

target="${1:?usage: package_release.sh <linux-x64|linux-arm64|macos-arm64|windows-x64>}"

case "$target" in
  linux-x64) goos=linux goarch=amd64 binary=treadle ;;
  linux-arm64) goos=linux goarch=arm64 binary=treadle ;;
  macos-arm64) goos=darwin goarch=arm64 binary=treadle ;;
  windows-x64) goos=windows goarch=amd64 binary=treadle.exe ;;
  *) echo "Unknown target: $target" >&2; exit 1 ;;
esac

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# No cgo, so one binary runs on any distribution, glibc or musl.
CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
  go build -trimpath -ldflags="-s -w" -o "$work/$binary" ./cmd/treadle

cp VERSION "$work/"

archive="treadle-$target.tar.gz"
mkdir -p dist
tar -czf "dist/$archive" -C "$work" "$binary" VERSION

cd dist
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$archive" > "$archive.sha256"
else
  shasum -a 256 "$archive" > "$archive.sha256"
fi

echo "Wrote dist/$archive"
