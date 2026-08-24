#!/bin/sh
set -eu
if [ "${1:-}" = health ]; then
  wget -q --no-check-certificate -O - https://127.0.0.1:8443/healthz >/dev/null
  exit 0
fi
exec /usr/local/lib/jstreamer-server "$@"
