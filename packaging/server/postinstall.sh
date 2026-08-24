#!/bin/sh
set -eu
if ! getent group jstreamer >/dev/null 2>&1; then groupadd --system jstreamer; fi
if ! getent passwd jstreamer >/dev/null 2>&1; then
  useradd --system --gid jstreamer --home-dir /var/lib/jstreamer --shell /usr/sbin/nologin jstreamer
fi
install -d -o jstreamer -g jstreamer -m 0750 /var/lib/jstreamer /var/lib/jstreamer/catalog
if command -v systemctl >/dev/null 2>&1; then systemctl daemon-reload || true; fi
