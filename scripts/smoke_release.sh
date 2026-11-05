#!/usr/bin/env bash
# Installs a packaged archive with the real install script, in a folder with a space
# in its name, and checks that the result runs and reports the right version.
#
#     scripts/smoke_release.sh
set -euo pipefail

port=8731
version="$(cat VERSION)"
root="$(pwd)"

case "$(uname -s)" in
  MINGW* | MSYS* | CYGWIN*) windows=true binary=treadle.exe python=python ;;
  *) windows=false binary=treadle python=python3 ;;
esac

temp="${RUNNER_TEMP:-$(mktemp -d)}"
[ "$windows" = true ] && temp="$(cygpath -u "$temp")"
home="$temp/Treadle Install"

(cd dist && exec "$python" -m http.server "$port" --bind 127.0.0.1) >/dev/null 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true' EXIT

for _ in $(seq 1 50); do
  curl -fsS -o /dev/null "http://127.0.0.1:$port/" && break
  sleep 0.2
done

export TREADLE_DOWNLOAD_URL="http://127.0.0.1:$port"

install() {
  if [ "$windows" = true ]; then
    TREADLE_HOME="$(cygpath -w "$home")" powershell -NoProfile -ExecutionPolicy Bypass -File "$(cygpath -w "$root/scripts/install.ps1")"
  else
    TREADLE_HOME="$home" HOME="$temp" SHELL=/bin/sh sh "$root/scripts/install.sh"
  fi
}

check() {
  got="$("$home/bin/$binary" --version | tr -d '\r')"
  [ "$got" = "treadle $version" ] || { echo "Got '$got', want 'treadle $version'." >&2; exit 1; }
}

echo '--- first install'
install
check

echo '--- update over an existing install'
install
check

echo "Smoke test passed: $got"
