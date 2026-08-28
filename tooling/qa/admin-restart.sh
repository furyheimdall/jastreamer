#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift 2 ;;
    *) printf 'unknown admin restart QA argument: %s\n' "$1" >&2; exit 64 ;;
  esac
done
if [ -z "$output" ]; then
  printf '%s\n' 'admin restart QA requires --output <directory>' >&2
  exit 64
fi
mkdir -p "$output"
output=$(realpath "$output")
cd "$root/tooling/qa"
ADMIN_RESTART_OUTPUT=$output bunx --no-install playwright test admin-restart.spec.mjs --browser chromium --workers 1 --reporter line
