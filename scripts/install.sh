#!/bin/sh
# Install a verified release binary without touching application data.
set -eu
repo=${ANIME_RSS_REPOSITORY:-SuInk/bgmcat}
version=latest
dest=${ANIME_RSS_BIN_DIR:-"$HOME/.local/bin"}
local_dir=
private=false
start=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --repository|--version|--dir|--local-dir)
      [ "$#" -ge 2 ] || { echo "Missing option value" >&2; exit 1; }
      case "$1" in --repository) repo=$2;; --version) version=$2;; --dir) dest=$2;; --local-dir) local_dir=$2;; esac
      shift 2;;
    --private) private=true; shift;;
    --start) start=true; shift;;
    --skip-aria2) shift;;
    --help|-h) echo 'Usage: sh install.sh --repository OWNER/REPO [--private] [--version vX.Y.Z] [--dir PATH] [--local-dir RELEASE_DIR] [--start]'; exit 0;;
    *) echo "Unknown option: $1" >&2; exit 1;;
  esac
done
case "$dest" in /*) ;; *) echo 'Install directory must be absolute' >&2; exit 1;; esac
case "$(uname -s)" in Darwin) platform=macos;; Linux) platform=linux;; *) echo 'Use install.ps1 on Windows' >&2; exit 1;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo 'Unsupported architecture' >&2; exit 1;; esac
asset="bgmcat-$platform-$arch.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
if [ -n "$local_dir" ]; then
  cp "$local_dir/$asset" "$local_dir/SHA256SUMS" "$tmp/"
else
  printf '%s\n' "$repo" | LC_ALL=C grep -Eq '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' || { echo 'Set --repository OWNER/REPO' >&2; exit 1; }
  if [ "$version" != latest ]; then
    printf '%s\n' "$version" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$' || { echo 'Invalid version' >&2; exit 1; }
  fi
  if [ "$private" = true ]; then
    command -v gh >/dev/null || { echo 'Install GitHub CLI, then run gh auth login' >&2; exit 1; }
    if [ "$version" = latest ]; then
      gh release download --repo "$repo" --pattern "$asset" --pattern SHA256SUMS --dir "$tmp"
    else
      gh release download "$version" --repo "$repo" --pattern "$asset" --pattern SHA256SUMS --dir "$tmp"
    fi
  else
    if [ "$version" = latest ]; then base="https://github.com/$repo/releases/latest/download"; else base="https://github.com/$repo/releases/download/$version"; fi
    curl --fail --location --proto '=https' --proto-redir '=https' --output "$tmp/$asset" "$base/$asset"
    curl --fail --location --proto '=https' --proto-redir '=https' --output "$tmp/SHA256SUMS" "$base/SHA256SUMS"
  fi
fi
expected=$(awk -v name="$asset" '$2==name {print $1}' "$tmp/SHA256SUMS")
printf '%s\n' "$expected" | LC_ALL=C grep -Eq '^[a-fA-F0-9]{64}$' || { echo 'Missing or ambiguous checksum' >&2; exit 1; }
if command -v sha256sum >/dev/null; then actual=$(sha256sum "$tmp/$asset" | awk '{print $1}'); else actual=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}'); fi
[ "$expected" = "$actual" ] || { echo 'SHA-256 mismatch; installation unchanged' >&2; exit 1; }
entry="bgmcat-$platform-$arch/bgmcat"
mkdir "$tmp/extract"
tar -xzf "$tmp/$asset" -C "$tmp/extract" "$entry"
[ -f "$tmp/extract/$entry" ] && [ ! -L "$tmp/extract/$entry" ] || { echo 'Invalid package binary' >&2; exit 1; }
mkdir -p "$dest"
stage=$(mktemp "$dest/.bgmcat-install.XXXXXX")
trap 'rm -rf "$tmp"; rm -f "${stage:-}"' EXIT HUP INT TERM
install -m 755 "$tmp/extract/$entry" "$stage"
"$stage" --version
if [ -f "$dest/bgmcat" ]; then cp -p "$dest/bgmcat" "$dest/bgmcat.previous"; fi
mv -f "$stage" "$dest/bgmcat"
printf 'Installed: %s/bgmcat\nData is preserved.\n' "$dest"
if [ "$start" = true ]; then
  echo 'Starting bgmcat in this terminal. Press Ctrl+C to stop. Use the address printed below.'
  "$dest/bgmcat"
else
  printf 'Start: "%s/bgmcat"\n' "$dest"
fi
