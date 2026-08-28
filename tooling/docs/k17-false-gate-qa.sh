#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/jastreamer-task23-k17.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM
cat >"$work/candidate.json" <<'JSON'
{"component":"server","source_revision":"0123456789abcdef","artifacts":[{"name":"server.tar","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}
JSON
if [ -n "${TASK23_INVENTORY_READY:-}" ]; then
  printf 'ready\n' >"$TASK23_INVENTORY_READY"
  IFS= read -r _ <"$TASK23_INVENTORY_ACK"
fi
bun "$root/tooling/qa/k17/cli.mjs" authorization --matrix "$root/deploy/synology/support-matrix.yaml" --output "$work/auth.json"
bun "$root/tooling/qa/k17/cli.mjs" gate --matrix "$root/deploy/synology/support-matrix.yaml" --candidate-manifest "$work/candidate.json" --output "$work/gate.json"
python3 - "$work/auth.json" "$work/gate.json" <<'PY'
import json,sys
a=json.load(open(sys.argv[1])); g=json.load(open(sys.argv[2]))
print(json.dumps({"schema_version":1,"runner_enabled":a["authorized"],"status":g["qualification_status"],"network_calls":g["network_calls"],"audio_mutations":g["audio_mutations"],"publication_code":g["publication"]["code"],"external_writes":g["publication"]["external_writes"],"release_ready":False},sort_keys=True))
PY
