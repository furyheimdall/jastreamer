#!/bin/sh
set -eu
if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload || true; fi
# Deliberately retain /var/lib/jastreamer and the service account for safe reinstall/recovery.
