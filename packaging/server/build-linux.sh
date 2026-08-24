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
(cd "$root/apps/server" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags="-s -w -X main.productVersion=$version -X main.sourceRevision=${GITHUB_SHA:-local}" -o "$work/jastreamer-server" ./cmd/jastreamer-server)
for format in deb rpm; do
  cat >"$work/nfpm.yaml" <<EOF
name: jastreamer-server
arch: $arch
platform: linux
version: $version
maintainer: jastreamer
vendor: jastreamer
homepage: https://github.com/furyheimdall/jastreamer
license: Apache-2.0
description: Local-first jastreamer server
contents:
  - { src: $work/jastreamer-server, dst: /usr/lib/jastreamer-server/jastreamer-server, file_info: { mode: 0755 } }
  - { src: $root/apps/server/migrations, dst: /usr/lib/jastreamer-server/migrations }
  - { src: $root/packaging/server/jastreamer-server.service, dst: /usr/lib/systemd/system/jastreamer-server.service }
  - { src: $root/packaging/server/server.json, dst: /etc/jastreamer/server.json, type: config|noreplace }
  - { src: $root/packaging/server/server.env, dst: /etc/jastreamer/server.env, type: config|noreplace, file_info: { mode: 0600 } }
  - { src: $root/LICENSE, dst: /usr/share/licenses/jastreamer-server/LICENSE }
  - { src: $root/packaging/container/THIRD_PARTY_NOTICES, dst: /usr/share/doc/jastreamer-server/THIRD_PARTY_NOTICES }
scripts:
  postinstall: $root/packaging/server/postinstall.sh
  preremove: $root/packaging/server/preremove.sh
  postremove: $root/packaging/server/postremove.sh
EOF
  "$work/nfpm" package -f "$work/nfpm.yaml" -p "$format" -t "$out/jastreamer-server_${version}_linux_${arch}.$format"
done
file "$work/jastreamer-server" >"$out/linux-${arch}-build.txt"
