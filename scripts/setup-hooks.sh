#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
command -v python3 >/dev/null
existing=$(git config --get core.hooksPath || true)
if [ -n "$existing" ] && [ "$existing" != .githooks ]; then
  echo "Existing hooksPath ($existing) retained. Integrate scripts/clean-commit-message.py into your commit-msg hook manually." >&2
  exit 1
fi
git config --local core.hooksPath .githooks
echo 'Enabled Claude attribution cleanup for new commits.'
