#!/usr/bin/env bash
set -euo pipefail
version=${1:?version required}; arch=${2:?arch required}; out=${3:?output required}
root=$(cd "$(dirname "$0")/../.." && pwd)
[[ $arch == amd64 || $arch == arm64 ]] || exit 64
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
mkdir -p "$out"
case $arch in
  amd64) archive=nfpm_2.43.4_Linux_x86_64.tar.gz; expected=cafb544650cb0305d1b164fc0ab261eb77a81af324e18011282d326b326d20fb;;
  arm64) archive=nfpm_2.43.4_Linux_arm64.tar.gz; expected=e4365707dedfda6e089f597dcdab9497beea80accb2c2704be18981e4a4d9b9b;;
esac
curl -fsSL "https://github.com/goreleaser/nfpm/releases/download/v2.43.4/$archive" -o "$work/nfpm.tgz"
echo "$expected  $work/nfpm.tgz" | sha256sum -c --status
tar -xzf "$work/nfpm.tgz" -C "$work" nfpm
(cd "$root/apps/server" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w -X main.productVersion=$version -X main.sourceRevision=${GITHUB_SHA:-local}" -o "$work/jstreamer-server" ./cmd/jstreamer-server)
for format in deb rpm; do
  cat >"$work/nfpm.yaml" <<EOF
name: jstreamer-server
arch: $arch
platform: linux
version: $version
maintainer: Jake Streamer
vendor: Jake Streamer
homepage: https://github.com/furyheimdall/jake-streamer
license: Apache-2.0
description: Local-first Jake Streamer server
contents:
  - { src: $work/jstreamer-server, dst: /usr/lib/jstreamer-server/jstreamer-server, file_info: { mode: 0755 } }
  - { src: $root/apps/server/migrations, dst: /usr/lib/jstreamer-server/migrations }
  - { src: $root/packaging/server/jstreamer-server.service, dst: /usr/lib/systemd/system/jstreamer-server.service }
  - { src: $root/packaging/server/server.json, dst: /etc/jstreamer/server.json, type: config|noreplace }
  - { src: $root/packaging/server/server.env, dst: /etc/jstreamer/server.env, type: config|noreplace, file_info: { mode: 0600 } }
  - { src: $root/LICENSE, dst: /usr/share/licenses/jstreamer-server/LICENSE }
  - { src: $root/packaging/container/THIRD_PARTY_NOTICES, dst: /usr/share/doc/jstreamer-server/THIRD_PARTY_NOTICES }
scripts:
  postinstall: $root/packaging/server/postinstall.sh
  preremove: $root/packaging/server/preremove.sh
  postremove: $root/packaging/server/postremove.sh
EOF
  "$work/nfpm" package -f "$work/nfpm.yaml" -p "$format" -t "$out/jstreamer-server_${version}_linux_${arch}.$format"
done
file "$work/jstreamer-server" >"$out/linux-${arch}-build.txt"
