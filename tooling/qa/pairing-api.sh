#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
browser=
fixture=
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --browser) browser=$2; shift 2 ;;
    --fixture) fixture=$2; shift 2 ;;
    --output) output=$2; shift 2 ;;
    *) printf 'unknown pairing-api argument: %s\n' "$1" >&2; exit 64 ;;
  esac
done
if [ "$browser" != chromium ] || [ ! -f "$fixture" ] || [ -z "$output" ]; then
  printf '%s\n' 'pairing-api requires --browser chromium --fixture <file> --output <directory>' >&2
  exit 64
fi
mkdir -p "$output"
fixture=$(realpath "$fixture")
output=$(realpath "$output")
cd "$root/tooling/qa"
PAIRING_FIXTURE=$fixture PAIRING_OUTPUT=$output \
  bunx --no-install playwright test pairing-api.playwright.mjs --browser chromium --workers 1 --reporter line
