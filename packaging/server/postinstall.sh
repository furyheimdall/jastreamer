#!/bin/sh
set -eu
if ! getent group jastreamer >/dev/null 2>&1; then groupadd --system jastreamer; fi
if ! getent passwd jastreamer >/dev/null 2>&1; then
  useradd --system --gid jastreamer --home-dir /var/lib/jastreamer --shell /usr/sbin/nologin jastreamer
fi
install -d -o jastreamer -g jastreamer -m 0750 /var/lib/jastreamer /var/lib/jastreamer/catalog
if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload || true; fi
