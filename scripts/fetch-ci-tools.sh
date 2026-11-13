#!/usr/bin/env bash
# Pinned, verified Linux x86-64 tools for CI. No credentials are required.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
tools_dir="${CANGZHOU_TOOLS_DIR:-$root/tools}"
mkdir -p "$tools_dir"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl --fail --location --silent --show-error --retry 3 \
  'https://static2.fnnas.com/fnpack/fnpack-1.2.3-linux-amd64' -o "$tmp/fnpack"
printf '%s  %s\n' '54b97fa7b70968c4d05c79840f5daeff508957d0bb2062fdb0376d00d9615c93' "$tmp/fnpack" | sha256sum -c -
curl --fail --location --silent --show-error --retry 3 \
  'https://github.com/kopia/kopia/releases/download/v0.23.1/kopia-0.23.1-linux-x64.tar.gz' -o "$tmp/kopia.tar.gz"
printf '%s  %s\n' '416d0f84a3dbb321a8b2d8f0997b1a0a6e915babe79ee76fa6e4d2bd1e1c5178' "$tmp/kopia.tar.gz" | sha256sum -c -
mkdir -p "$tmp/kopia-extract"
tar -xzf "$tmp/kopia.tar.gz" -C "$tmp/kopia-extract" 'kopia-0.23.1-linux-x64/kopia'
install -m 755 "$tmp/fnpack" "$tools_dir/fnpack"
install -m 755 "$tmp/kopia-extract/kopia-0.23.1-linux-x64/kopia" "$tools_dir/kopia"
