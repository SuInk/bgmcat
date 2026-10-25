#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
sh scripts/install-aria2.sh
npm ci
npm run typecheck
npm run build
go test ./...
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/bgmcat ./cmd/bgmcat
