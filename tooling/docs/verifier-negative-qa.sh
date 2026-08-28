#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/jastreamer-task23-verifier.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM
printf '[]\n' >"$work/claims.json"
if [ -n "${TASK23_INVENTORY_READY:-}" ]; then
  printf 'ready\n' >"$TASK23_INVENTORY_READY"
  IFS= read -r _ <"$TASK23_INVENTORY_ACK"
fi
python3 - "$root/tooling/qa/product-receipt.schema.json" "$work/missing-payload.json" <<'PY'
import json,sys
value=json.load(open(sys.argv[1]))
value['$defs']['receipt']['allOf']=[entry for entry in value['$defs']['receipt']['allOf'] if entry.get('if',{}).get('properties',{}).get('kind',{}).get('const') != 'candidate']
json.dump(value,open(sys.argv[2],'w'))
PY
set +e
bun "$root/tooling/docs/verify.mjs" --claims "$work/claims.json" --receipt-schema "$root/tooling/qa/product-receipt.schema.json" >"$work/empty.stdout" 2>"$work/empty.stderr"
empty_status=$?
bun "$root/tooling/docs/verify.mjs" --claims "$root/docs/claims.json" --receipt-schema "$work/missing-payload.json" >"$work/payload.stdout" 2>"$work/payload.stderr"
payload_status=$?
set -e
test "$empty_status" -ne 0; test "$payload_status" -ne 0
grep -q 'CLAIM_SET_INCOMPLETE' "$work/empty.stderr"
grep -q 'MISSING_EXECUTABLE_PAYLOAD_MAPPING' "$work/payload.stderr"
printf '{"schema_version":1,"results":[{"negative_class":"empty-canonical-claim-set","validator_exit":%s,"code":"CLAIM_SET_INCOMPLETE"},{"negative_class":"removed-candidate-allof","validator_exit":%s,"code":"MISSING_EXECUTABLE_PAYLOAD_MAPPING"}],"result":"rejected"}\n' "$empty_status" "$payload_status"
