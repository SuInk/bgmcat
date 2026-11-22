#!/usr/bin/env bash
# Copyright (c) 2025-now SuInk.
# Licensed under the Limited Redistribution License in the repository root.
#
# Cross-compiles the Go backend for the Android app and drops it into
# android/app/src/main/jniLibs/<abi>/libbgmcat.so. Android only lets an app
# execute files from its native library directory, so the executable ships
# under a lib*.so name and the app runs it as a child process.
#
# cgo is on because Android has no /etc/resolv.conf: the pure-Go resolver
# cannot find a DNS server, while the cgo resolver goes through bionic.
#
# Requires: Go, the Android NDK (ANDROID_NDK_HOME, ANDROID_NDK_LATEST_HOME or
# $ANDROID_HOME/ndk/<version>), and a built frontend in web/dist (npm run build).
# Usage: scripts/build-android-backend.sh [abi ...]   (default: arm64-v8a x86_64)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

API_LEVEL=26 # Must match minSdk in android/app/build.gradle.kts.

find_ndk() {
	local candidate
	for candidate in "${ANDROID_NDK_HOME:-}" "${ANDROID_NDK_LATEST_HOME:-}" "${ANDROID_NDK_ROOT:-}"; do
		if [[ -n "$candidate" && -d "$candidate/toolchains/llvm" ]]; then
			echo "$candidate"
			return
		fi
	done
	local sdk
	for sdk in "${ANDROID_HOME:-}" "${ANDROID_SDK_ROOT:-}"; do
		if [[ -n "$sdk" && -d "$sdk/ndk" ]]; then
			candidate="$(ls -1d "$sdk"/ndk/*/ 2>/dev/null | sort -V | tail -n 1)"
			if [[ -n "$candidate" ]]; then
				echo "${candidate%/}"
				return
			fi
		fi
	done
	echo "找不到 Android NDK：请设置 ANDROID_NDK_HOME 或 ANDROID_HOME" >&2
	exit 1
}

if [[ ! -f web/dist/index.html ]]; then
	echo "web/dist 为空：请先执行 npm run build 构建前端" >&2
	exit 1
fi

NDK="$(find_ndk)"
case "$(uname -s)" in
	Darwin) HOST_TAG=darwin-x86_64 ;; # The NDK ships one universal macOS toolchain.
	Linux) HOST_TAG=linux-x86_64 ;;
	*) echo "不支持在 $(uname -s) 上构建安卓后端" >&2; exit 1 ;;
esac
TOOLCHAIN="$NDK/toolchains/llvm/prebuilt/$HOST_TAG/bin"
VERSION="$(tr -d '[:space:]' < internal/version/VERSION)"

abis=("$@")
if [[ ${#abis[@]} -eq 0 ]]; then
	abis=(arm64-v8a x86_64)
fi

for abi in "${abis[@]}"; do
	case "$abi" in
		arm64-v8a) goarch=arm64; triple=aarch64-linux-android ;;
		x86_64) goarch=amd64; triple=x86_64-linux-android ;;
		*) echo "不支持的 ABI：$abi" >&2; exit 1 ;;
	esac
	out="android/app/src/main/jniLibs/$abi/libbgmcat.so"
	mkdir -p "$(dirname "$out")"
	echo "构建安卓后端 $abi → $out"
	# -checklinkname=0: github.com/wlynxg/anet (via pion/webrtc) linknames into
	# package net on Android, which newer Go linkers refuse by default.
	# -static-libstdc++: go-libutp is C++, and the app does not ship libc++_shared.so.
	CGO_ENABLED=1 GOOS=android GOARCH="$goarch" \
		CC="$TOOLCHAIN/${triple}${API_LEVEL}-clang" \
		CXX="$TOOLCHAIN/${triple}${API_LEVEL}-clang++" \
		CGO_LDFLAGS="-static-libstdc++" \
		go build -trimpath \
		-ldflags "-s -w -checklinkname=0 -X github.com/SuInk/bgmcat/internal/version.Override=$VERSION" \
		-o "$out" ./cmd/bgmcat
done
