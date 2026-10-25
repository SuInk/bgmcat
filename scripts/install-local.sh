#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ ! -f bin/bgmcat ]; then sh scripts/build.sh; fi
sh scripts/install-aria2.sh
dest="${ANIME_RSS_BIN_DIR:-$HOME/.local/bin}"
mkdir -p "$dest"
install -m 755 bin/bgmcat "$dest/bgmcat.new"
mv "$dest/bgmcat.new" "$dest/bgmcat"
printf 'Installed: %s/bgmcat\nRun it to start the server; the access token is printed at startup.\n' "$dest"
