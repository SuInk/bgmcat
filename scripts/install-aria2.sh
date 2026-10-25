#!/bin/sh
# Install the managed download engine through the system package manager.
set -eu
if [ -n "${BGMCAT_ARIA2_PATH:-}" ]; then
  "$BGMCAT_ARIA2_PATH" --version >/dev/null
  exit 0
fi
if command -v aria2c >/dev/null 2>&1; then exit 0; fi
as_root() {
  if [ "$(id -u)" = 0 ]; then "$@"; else sudo "$@"; fi
}
case "$(uname -s)" in
  Darwin)
    command -v brew >/dev/null || { echo 'Install Homebrew, then run: brew install aria2' >&2; exit 1; }
    brew install aria2;;
  Linux)
    if command -v apt-get >/dev/null; then as_root apt-get update; as_root apt-get install -y aria2
    elif command -v dnf >/dev/null; then as_root dnf install -y aria2
    elif command -v pacman >/dev/null; then as_root pacman -S --needed --noconfirm aria2
    elif command -v apk >/dev/null; then as_root apk add aria2
    else echo 'Install aria2 using your package manager, or set BGMCAT_ARIA2_PATH.' >&2; exit 1
    fi;;
  *) echo 'Install aria2 for this platform or set BGMCAT_ARIA2_PATH.' >&2; exit 1;;
esac
aria2c --version >/dev/null
