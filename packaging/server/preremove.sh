#!/bin/sh
set -eu
if command -v systemctl >/dev/null 2>&1; then systemctl stop jastreamer-server.service >/dev/null 2>&1 || true; fi
