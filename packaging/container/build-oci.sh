#!/bin/sh
set -eu
if [ "$#" -ne 1 ]; then printf '%s\n' 'usage: build-oci.sh <output.oci>' >&2; exit 64; fi
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
exec "$root/tooling/componentctl" container build-qa \
  --platform linux/amd64,linux/arm64 \
  --compose deploy/docker/server/compose.synology.yaml \
  --scenario replacement-persistence --oci-layout "$1" --output "$(dirname "$1")/results.json"
