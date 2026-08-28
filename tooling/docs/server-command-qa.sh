#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/jastreamer-task23-server.XXXXXX")
pid=
cleanup() {
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then kill -TERM "$pid"; wait "$pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT INT TERM
cd "$root/apps/server"
go build -o "$work/jastreamer-server" ./cmd/jastreamer-server
mkdir -p "$work/data/catalog"
cat >"$work/server.json" <<EOF
{"address":"127.0.0.1:0","data_directory":"$work/data","catalog_root":"$work/data/catalog","catalog_migration":"$root/apps/server/migrations/001_catalog.sql","playback_migration":"$root/apps/server/migrations/002_playback.sql","playback_expansion":"$root/apps/server/migrations/003_todo12.sql","certificate_dns":["localhost"],"certificate_ips":["127.0.0.1"],"allowed_origins":[],"pairing_ttl":"5m"}
EOF
mkfifo "$work/ready"
JASTREAMER_SETUP_SECRET='task23-ephemeral-bootstrap-value' "$work/jastreamer-server" --config "$work/server.json" >"$work/ready" 2>"$work/server.stderr" &
pid=$!
IFS= read -r ready <"$work/ready"
origin=${ready#ready }; origin=${origin%% fingerprint=*}
if [ -n "${TASK23_INVENTORY_READY:-}" ]; then
  printf 'ready\n' >"$TASK23_INVENTORY_READY"
  IFS= read -r _ <"$TASK23_INVENTORY_ACK"
fi
curl -ksSf "$origin/healthz" >"$work/health.json"
curl -ksSf -H 'Content-Type: application/json' --data '{"setup_secret":"task23-ephemeral-bootstrap-value","name":"Task 23 admin"}' "$origin/api/v1/bootstrap" >"$work/bootstrap.json"
admin=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["token"])' "$work/bootstrap.json")
curl -ksSf -D "$work/headers" -H "Authorization: Bearer $admin" "$origin/api/v1/config" >"$work/config.json"
etag=$(awk 'tolower($1)=="etag:" {gsub("\r",""); print $2}' "$work/headers")
invalid_status=$(curl -ksS -o "$work/invalid.json" -w '%{http_code}' -X PATCH -H "Authorization: Bearer $admin" -H 'Content-Type: application/json' -H "If-Match: $etag" -H 'Idempotency-Key: invalid-relative-ffmpeg' --data '{"ffmpeg_path":"bin/ffmpeg"}' "$origin/api/v1/config")
curl -ksSf -H "Authorization: Bearer $admin" -H 'Content-Type: application/json' --data '{"role":"controller"}' "$origin/api/v1/pairing-codes" >"$work/code.json"
code=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["code"])' "$work/code.json")
curl -ksSf -H 'Content-Type: application/json' --data "{\"code\":\"$code\",\"name\":\"Task 23 controller\"}" "$origin/api/v1/pairings" >"$work/controller.json"
controller=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["token"])' "$work/controller.json")
device=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["device"]["id"])' "$work/controller.json")
curl -ksSf -o /dev/null -X DELETE -H "Authorization: Bearer $admin" "$origin/api/v1/devices/$device"
revoked_status=$(curl -ksS -o "$work/revoked.json" -w '%{http_code}' -H "Authorization: Bearer $controller" -H 'X-Jake-Supported-Protocol-Majors: 3,2' "$origin/api/v1/discovery")
sha=$(sha256sum "$work/jastreamer-server" | cut -d' ' -f1)
python3 - "$work/health.json" "$work/config.json" "$work/invalid.json" "$invalid_status" "$work/revoked.json" "$revoked_status" "$sha" <<'PY'
import json,sys
health=json.load(open(sys.argv[1])); config=json.load(open(sys.argv[2])); invalid=json.load(open(sys.argv[3])); revoked=json.load(open(sys.argv[5]))
print(json.dumps({"schema_version":1,"binary_sha256":sys.argv[7],"health":health.get("status"),"config_revision":config.get("revision"),"ffmpeg_status":config.get("diagnostics",{}).get("ffmpeg",{}).get("status"),"invalid_config":{"status":int(sys.argv[4]),"code":invalid.get("code"),"field":invalid.get("field")},"revoked_auth":{"status":int(sys.argv[6]),"code":revoked.get("code")},"cleanup_contract":"process-terminated-and-temporary-root-removed"},sort_keys=True))
PY
kill -TERM "$pid"; wait "$pid" 2>/dev/null || true; pid=
