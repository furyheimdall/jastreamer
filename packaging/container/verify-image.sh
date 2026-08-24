#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
JASTREAMER_SETUP_SECRET=compose-validation JASTREAMER_CONFIG_PATH=/tmp/jastreamer-config JASTREAMER_DATA_PATH=/tmp/jastreamer-data docker compose -f "$root/deploy/docker/server/compose.synology.yaml" config >/dev/null
bun test "$root/tooling/container/container.test.ts"
(cd "$root/tooling/container" && bunx tsc --noEmit)
printf '%s\n' 'container contract OK'
