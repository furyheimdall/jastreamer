#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
JSTREAMER_SETUP_SECRET=compose-validation JSTREAMER_CONFIG_PATH=/tmp/jstreamer-config JSTREAMER_DATA_PATH=/tmp/jstreamer-data docker compose -f "$root/deploy/docker/server/compose.synology.yaml" config >/dev/null
bun test "$root/tooling/container/container.test.ts"
(cd "$root/tooling/container" && bunx tsc --noEmit)
printf '%s\n' 'container contract OK'
