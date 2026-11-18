#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
ELECTRON_SKIP_BINARY_DOWNLOAD=1 npm ci
npm run typecheck
npm run build
go test ./...
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/bgmcat ./cmd/bgmcat
