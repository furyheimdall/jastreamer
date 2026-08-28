#!/bin/sh
set -eu
work=$(mktemp -d "${TMPDIR:-/tmp}/jastreamer-task23-web.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM
mkdir -p "$work/source"
printf '<!doctype html><title>task23</title>\n' >"$work/source/index.html"
(cd "$work/source" && zip -q "$work/control-web.zip" index.html)
unzip -t "$work/control-web.zip" >/dev/null
mkdir -p "$work/deploy"
unzip -q "$work/control-web.zip" -d "$work/deploy"
if [ -n "${TASK23_INVENTORY_READY:-}" ]; then
  printf 'ready\n' >"$TASK23_INVENTORY_READY"
  IFS= read -r _ <"$TASK23_INVENTORY_ACK"
fi
test -f "$work/deploy/index.html"
sha=$(sha256sum "$work/control-web.zip" | cut -d' ' -f1)
rm -rf "$work/deploy"
printf '{"schema_version":1,"archive_sha256":"%s","test":"passed","extract":"passed","removal":"passed","publication_claim":false}\n' "$sha"
