#!/usr/bin/env bash
# Fails unless VERSION is MAJOR.MINOR.PATCH. Given a tag, also fails unless the tag
# is that version, so a release can't ship under the wrong number.
#
#     scripts/check_version.sh
#     scripts/check_version.sh v1.2.3
set -euo pipefail

version="$(cat VERSION)"

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] ||
  { echo "VERSION is '$version', want MAJOR.MINOR.PATCH." >&2; exit 1; }

tag="${1:-}"
if [ -n "$tag" ] && [ "$tag" != "v$version" ]; then
  echo "The tag is $tag but VERSION says $version. Update VERSION, or tag v$version." >&2
  exit 1
fi

echo "Version $version is fine."
