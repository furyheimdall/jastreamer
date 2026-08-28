#!/usr/bin/env bash
set -euo pipefail
source=${1:?source directory required}
destination=${2:?destination directory required}
mkdir -p "$destination"
tar -C "$source" \
  --exclude='./jastreamer-server' --exclude='./jastreamer-server.exe' --exclude='./bin' --exclude='./dist' \
  -cf - . | tar -C "$destination" -xf -
