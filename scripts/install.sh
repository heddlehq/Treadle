#!/bin/sh
# Installs Treadle on Linux or macOS, with nothing else required:
#
#     curl -fsSL https://github.com/heddlehq/Treadle/releases/latest/download/install.sh | sh
#
# Everything goes in ~/.treadle. To remove it, delete that folder and the line this
# script added to your shell profile. Run it again to update. Your login is kept
# elsewhere, so updating won't sign you out.
#
# TREADLE_HOME     install somewhere else
# TREADLE_VERSION  install a specific version, such as 0.1.0
set -eu

repository="https://github.com/heddlehq/Treadle"

fail() {
  printf '\n%s\n\n' "$1" >&2
  exit 1
}

download_url() {
  if [ -n "${TREADLE_DOWNLOAD_URL:-}" ]; then
    echo "$TREADLE_DOWNLOAD_URL"
  elif [ -n "${TREADLE_VERSION:-}" ]; then
    echo "$repository/releases/download/v${TREADLE_VERSION#v}"
  else
    echo "$repository/releases/latest/download"
  fi
}

detect_target() {
  os="$(uname -s)"
  arch="$(uname -m)"

  case "$os/$arch" in
    Linux/x86_64 | Linux/amd64) echo linux-x64 ;;
    Linux/aarch64 | Linux/arm64) echo linux-arm64 ;;
    Darwin/arm64) echo macos-arm64 ;;
    *) fail "There is no Treadle build for $os on $arch yet. You can build it from source: $repository" ;;
  esac
}

verify_checksum() {
  (
    cd "$1"
    if command -v sha256sum >/dev/null 2>&1; then
      sha256sum -c "$2.sha256" >/dev/null
    else
      shasum -a 256 -c "$2.sha256" >/dev/null
    fi
  ) || fail "The download is damaged. Run the installer again."
}

add_to_path() {
  case "$(basename "${SHELL:-}")" in
    zsh) profile="$HOME/.zshrc" ;;
    bash) profile="$HOME/.bashrc" ;;
    *) profile="" ;;
  esac

  case ":$PATH:" in
    *":$1:"*) return ;;
  esac

  if [ -z "$profile" ]; then
    echo "Add $1 to your PATH to use the treadle command."
  elif ! grep -qs "$1" "$profile"; then
    printf '\n# Treadle\nexport PATH="%s:$PATH"\n' "$1" >> "$profile"
  fi
}

main() {
  for tool in curl tar; do
    command -v "$tool" >/dev/null 2>&1 || fail "Please install $tool and run this again."
  done

  target="$(detect_target)"
  url="$(download_url)"
  archive="treadle-$target.tar.gz"
  home="${TREADLE_HOME:-$HOME/.treadle}"
  bin="$home/bin"

  work="$(mktemp -d)"
  trap 'rm -rf "$work"' EXIT

  echo "Downloading Treadle for $target..."
  curl -fSL --progress-bar -o "$work/$archive" "$url/$archive" ||
    fail "Could not download Treadle from $url/$archive"
  curl -fsSL -o "$work/$archive.sha256" "$url/$archive.sha256" ||
    fail "Could not download the checksum from $url"
  verify_checksum "$work" "$archive"

  mkdir -p "$work/app"
  tar -xzf "$work/$archive" -C "$work/app"

  # Run it before replacing anything, so a bad build can't break a working install.
  version="$("$work/app/treadle" --version)" ||
    fail "This build doesn't run on your system. Nothing was changed."

  mkdir -p "$bin"
  mv -f "$work/app/treadle" "$bin/treadle"

  add_to_path "$bin"

  printf '\n%s is installed in %s\n' "$version" "$home"
  echo "Open a new terminal and run \`treadle\`."
}

# Last on purpose: a connection cut halfway through `curl | sh` runs nothing.
main
