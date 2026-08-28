#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/jastreamer-task23-compose.XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM
ffmpeg="$work/ffmpeg"
printf '#!/bin/sh\nexit 0\n' >"$ffmpeg"
chmod 700 "$ffmpeg"
if [ -n "${TASK23_INVENTORY_READY:-}" ]; then
  printf 'ready\n' >"$TASK23_INVENTORY_READY"
  IFS= read -r _ <"$TASK23_INVENTORY_ACK"
fi
cd "$root"
JASTREAMER_SERVER_IMAGE='ghcr.io/furyheimdall/jastreamer-server@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' \
JASTREAMER_SETUP_SECRET='task23-compose-validation-value' \
JASTREAMER_CONFIG_PATH="$work/config" JASTREAMER_DATA_PATH="$work/data" JASTREAMER_FFMPEG_PATH="$ffmpeg" \
docker compose -f deploy/docker/server/compose.synology.yaml -f deploy/docker/server/compose.ffmpeg.yaml config >"$work/rendered.yaml"
python3 - "$work/rendered.yaml" <<'PY'
import hashlib,json,sys
value=open(sys.argv[1],'rb').read()
print(json.dumps({"schema_version":1,"rendered_sha256":hashlib.sha256(value).hexdigest(),"services":["jastreamer-server"],"ffmpeg_target":"opt-jastreamer-external-ffmpeg","external_writes":0,"cleanup_contract":"temporary-root-removed"},sort_keys=True))
PY
