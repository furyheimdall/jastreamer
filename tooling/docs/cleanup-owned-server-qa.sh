#!/bin/sh
set -eu
pid=1874845
expected_cmd='/tmp/tmp.pTKGP0RhrR/jastreamer-server --config /tmp/tmp.pTKGP0RhrR/server.json '
expected_cwd='/home/furyheimdall/Dev/upnp-control/apps/server'
if [ -n "${TASK23_INVENTORY_READY:-}" ]; then
  printf 'ready\n' >"$TASK23_INVENTORY_READY"
  IFS= read -r _ <"$TASK23_INVENTORY_ACK"
fi
if [ ! -d "/proc/$pid" ]; then
  printf '{"schema_version":1,"pid":%s,"ownership":"previously-confirmed","result":"already-absent"}\n' "$pid"
  exit 0
fi
actual_cmd=$(tr '\0' ' ' <"/proc/$pid/cmdline")
actual_cwd=$(readlink "/proc/$pid/cwd")
[ "$actual_cmd" = "$expected_cmd" ]
[ "$actual_cwd" = "$expected_cwd" ]
identity=$(printf '%s\n%s\n' "$actual_cmd" "$actual_cwd" | sha256sum | cut -d' ' -f1)
kill -TERM "$pid"
if ! timeout 10s tail --pid="$pid" -f /dev/null; then
  kill -KILL "$pid"
  timeout 5s tail --pid="$pid" -f /dev/null
fi
[ ! -d "/proc/$pid" ]
printf '{"schema_version":1,"pid":%s,"identity_sha256":"%s","signal":"TERM","bounded_wait":"pidfd-compatible-tail","result":"terminated"}\n' "$pid" "$identity"
