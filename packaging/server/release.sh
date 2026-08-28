#!/usr/bin/env bash
set -euo pipefail
version=${1:?version required}; out=${2:?output required}
root=$(cd "$(dirname "$0")/../.." && pwd)
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo VERSION_INVALID >&2; exit 65; }
[[ ${JASTREAMER_RELEASE_TAG:-} == "server-v$version" ]] || { echo TAG_VERSION_MISMATCH >&2; exit 65; }
revision=${JASTREAMER_SOURCE_REVISION:?source revision required}
work=$(mktemp -d); wix_image=; wix_container=; wix_builder=
cleanup_release() {
  local status=$?; trap - EXIT; local cleanup_failed=false
  if [[ -n $wix_container ]] && docker container inspect "$wix_container" >/dev/null 2>&1; then
    if ! docker container rm -f "$wix_container" >/dev/null; then cleanup_failed=true; fi
  fi
  if [[ -n $wix_container ]] && docker container inspect "$wix_container" >/dev/null 2>&1; then cleanup_failed=true; fi
  if [[ -n $wix_image ]] && docker image inspect "$wix_image" >/dev/null 2>&1; then
    if ! docker image rm -f "$wix_image" >/dev/null; then cleanup_failed=true; fi
  fi
  if [[ -n $wix_image ]] && docker image inspect "$wix_image" >/dev/null 2>&1; then cleanup_failed=true; fi
  if [[ -n $wix_builder ]] && docker buildx inspect "$wix_builder" >/dev/null 2>&1; then
    if ! docker buildx rm "$wix_builder" >/dev/null; then cleanup_failed=true; fi
  fi
  if [[ -n $wix_builder ]] && docker buildx inspect "$wix_builder" >/dev/null 2>&1; then cleanup_failed=true; fi
  rm -rf "$work"
  [[ $cleanup_failed == false ]] || { echo WIXL_OWNED_RESOURCE_CLEANUP_FAILED >&2; exit 1; }
  exit "$status"
}
trap cleanup_release EXIT
mkdir -p "$out" "$work/bin" "$work/pkgroot" "$work/source"

nfpm_version=2.43.4
nfpm_sha=e4365707dedfda6e089f597dcdab9497beea80accb2c2704be18981e4a4d9b9b
syft_version=1.30.0
syft_sha=17cd933561b18210960526f32c9258087c5c8dce595e8f0b39477330023add13
fetch_tool() {
  local name=$1 version=$2 expected=$3 archive
  archive="$work/$name.tgz"
  curl --fail --silent --show-error --location "https://github.com/$([[ $name == nfpm ]] && echo goreleaser/nfpm || echo anchore/syft)/releases/download/v$version/${name}_${version}_$([[ $name == nfpm ]] && echo Linux || echo linux)_arm64.tar.gz" -o "$archive"
  echo "$expected  $archive" | sha256sum --check --status || { echo "${name^^}_CHECKSUM_MISMATCH" >&2; exit 65; }
  tar -xzf "$archive" -C "$work/bin" "$name"
}
fetch_tool nfpm "$nfpm_version" "$nfpm_sha"
fetch_tool syft "$syft_version" "$syft_sha"

build_go() {
  local os=$1 arch=$2 output=$3 target=$4
  (cd "$root/apps/server" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
    -ldflags="-s -w -X main.productVersion=$version -X main.sourceRevision=$revision" -o "$output" "$target")
}
for arch in amd64 arm64; do build_go linux "$arch" "$work/jastreamer-server-$arch" ./cmd/jastreamer-server; done
build_go windows amd64 "$work/source/jastreamer-server-core.exe" ./cmd/jastreamer-server
(cd "$root/apps/server" && CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o "$work/source/jastreamer-server.exe" ../../packaging/server/windows-service.go)
cp "$root/LICENSE" "$work/source/LICENSE"
cp "$root/packaging/container/THIRD_PARTY_NOTICES" "$work/source/THIRD_PARTY_NOTICES"
cp "$root/packaging/server/windows-server.json" "$work/source/server.json"
cp "$root/apps/server/migrations/"*.sql "$work/source/"
cp "$work/source/jastreamer-server-core.exe" "$out/jastreamer-server_${version}_windows_amd64.exe"

cp "$root/packaging/server/server-local.wxs" "$work/server-local.wxs"
cert_trust_id=$(openssl x509 -inform DER -in "$root/packaging/server/cert/server.cer" -noout -fingerprint -sha1 | cut -d= -f2 | tr -d ':')
wix_image="jastreamer-wixl-task18-$$:0.101"
wix_container="jastreamer-wixl-task18-$$"
wix_builder="jastreamer-wixl-builder-task18-$$"
docker buildx create --name "$wix_builder" --driver docker-container >/dev/null
docker buildx build --builder "$wix_builder" --no-cache --platform linux/amd64 --quiet --load -f "$root/packaging/server/Dockerfile.wixl" -t "$wix_image" "$root/packaging/server" >"$work/wix-image-id"
docker run --name "$wix_container" --rm --platform linux/amd64 --entrypoint sh -v "$work:/work" "$wix_image" -ec \
  'cd /work; wixl -a x64 -D Version='"$version"' -D CertThumbprint='"$cert_trust_id"' -D SourceDir=source -o server.msi server-local.wxs; msiinfo export server.msi ServiceInstall; msiinfo export server.msi ServiceControl; msiinfo export server.msi LaunchCondition; msiinfo export server.msi RegLocator' >"$work/msi-tables.txt"
cp "$work/server.msi" "$out/jastreamer-server_${version}_windows_amd64.msi"
wix_container=
docker image rm -f "$wix_image" >/dev/null
if docker image inspect "$wix_image" >/dev/null 2>&1; then echo WIXL_IMAGE_CLEANUP_FAILED >&2; exit 1; fi
wix_image=
docker buildx rm "$wix_builder" >/dev/null
if docker buildx inspect "$wix_builder" >/dev/null 2>&1; then echo WIXL_CACHE_CLEANUP_FAILED >&2; exit 1; fi
wix_builder=
exe_inspect=$(file "$work/source/jastreamer-server-core.exe"; go version -m "$work/source/jastreamer-server-core.exe")
jq -n --arg builder 'wixl 0.101+repack-1 from Debian snapshot 20250601 in digest-pinned base under QEMU' --arg toolImage "$(<"$work/wix-image-id")" --arg inspector 'msiinfo/msitools 0.101+repack-1' --arg exe "$exe_inspect" --rawfile tables "$work/msi-tables.txt" \
  '{platform:"windows/amd64",classification:"cross-compiled-and-qemu-packaged",builder:$builder,toolImageId:$toolImage,inspector:$inspector,standaloneExecutable:"runnable Server core",msiServiceExecutable:"SCM host supervising bundled core",executable:$exe,tables:$tables,authenticode:"not available locally; authoritative verification is the Windows workflow",cleanWindowsTrust:"not claimed"}' >"$out/windows-msi-inspection.json"

make_nfpm() {
  local arch=$1 format=$2 package_arch config
  package_arch=$arch
  config="$work/nfpm-$arch-$format.yaml"
  cat >"$config" <<EOF
name: jastreamer-server
arch: $package_arch
platform: linux
version: $version
maintainer: jastreamer
vendor: jastreamer
homepage: https://github.com/furyheimdall/jastreamer
license: Apache-2.0
description: Local-first jastreamer server
contents:
  - src: $work/jastreamer-server-$arch
    dst: /usr/lib/jastreamer-server/jastreamer-server
    file_info: { mode: 0755 }
  - src: $root/apps/server/migrations
    dst: /usr/lib/jastreamer-server/migrations
  - src: $root/packaging/server/jastreamer-server.service
    dst: /usr/lib/systemd/system/jastreamer-server.service
  - src: $root/packaging/server/server.json
    dst: /etc/jastreamer/server.json
    type: config|noreplace
  - src: $root/packaging/server/server.env
    dst: /etc/jastreamer/server.env
    type: config|noreplace
    file_info: { mode: 0600 }
  - src: $root/LICENSE
    dst: /usr/share/licenses/jastreamer-server/LICENSE
  - src: $root/packaging/container/THIRD_PARTY_NOTICES
    dst: /usr/share/doc/jastreamer-server/THIRD_PARTY_NOTICES
scripts:
  postinstall: $root/packaging/server/postinstall.sh
  preremove: $root/packaging/server/preremove.sh
  postremove: $root/packaging/server/postremove.sh
EOF
  "$work/bin/nfpm" package --config "$config" --packager "$format" --target "$out/jastreamer-server_${version}_linux_${arch}.$format" >/dev/null
}
for arch in amd64 arm64; do for format in deb rpm; do make_nfpm "$arch" "$format"; done; done

inspect_linux() {
  local arch=$1 platform="linux/$1" deb rpm
  local deb_info rpm_info smoke debian_image rocky_image
  deb="$out/jastreamer-server_${version}_linux_${arch}.deb"
  rpm="$out/jastreamer-server_${version}_linux_${arch}.rpm"
  if [[ $arch == arm64 ]]; then
    debian_image='debian@sha256:817e6cf99d6fc127ff4ffe8580049b60deba0adfbbb2bd65ddc3ef8fbb7aade0'
    rocky_image='rockylinux@sha256:99a073e7e92dc4cd2882c9418936bdd1c2298279c5af0f3642261286e135f6c7'
  else
    debian_image='debian@sha256:362e64223cc0da95422b3b13c045186fc0a81250e765d31c025fbddf257f6143'
    rocky_image='rockylinux@sha256:197b1569a8e5d46de75412cfd80b88a437d25bb2a5338dc82d5421d835245ec7'
  fi
  deb_info=$(dpkg-deb --info "$deb"; dpkg-deb --contents "$deb")
  rpm_info=$(docker run --rm --platform "$platform" -v "$rpm:/package.rpm:ro" "$rocky_image" sh -ec 'rpm -qip /package.rpm; rpm -qlp /package.rpm; rpm -qp --scripts /package.rpm')
  smoke=$(docker run --rm --platform "$platform" -v "$deb:/package.deb:ro" "$debian_image" sh -ec '
    dpkg -i /package.deb >/dev/null; test -f /usr/lib/systemd/system/jastreamer-server.service
    export JASTREAMER_SETUP_SECRET=release-smoke JASTREAMER_ADDR=127.0.0.1:8443
    mkfifo /tmp/ready; cd /usr/lib/jastreamer-server
    ./jastreamer-server --config /etc/jastreamer/server.json >/tmp/ready 2>/tmp/error & pid=$!
    IFS= read -r ready </tmp/ready; case "$ready" in ready\ https://*) ;; *) cat /tmp/error >&2; exit 1;; esac
    kill -TERM "$pid"; wait "$pid"; printf "%s; machine=%s; uid=%s" "$ready" "$(uname -m)" "$(id -u jastreamer)"')
  docker run --rm --platform "$platform" -v "$rpm:/package.rpm:ro" "$rocky_image" sh -ec 'rpm -ivh --nodeps /package.rpm >/dev/null; test -x /usr/lib/jastreamer-server/jastreamer-server; rpm -e jastreamer-server >/dev/null'
  jq -n --arg platform "$platform" --arg classification "$([[ $arch == arm64 ]] && echo native || echo qemu-emulated)" --arg inspector 'dpkg-deb 1.22.21 and rpm 4.16 in pinned clean containers' --arg deb "$deb_info" --arg rpm "$rpm_info" --arg smoke "$smoke" \
    '{platform:$platform,classification:$classification,inspectors:$inspector,debInspection:$deb,rpmInspection:$rpm,repositoryFreeDebInstallAndRuntimeSmoke:$smoke,repositoryFreeRpmInstallUninstall:true,systemdUnitInspected:true}' >"$out/linux-${arch}-inspection.json"
}
inspect_linux arm64
inspect_linux amd64

todo17="$work/todo17-root"
mkdir -p "$todo17/apps" "$todo17/packaging" "$todo17/deploy/docker" "$todo17/tooling"
"$root/packaging/server/stage-source.sh" "$root/apps/server" "$todo17/apps/server"
cp -a "$root/packaging/container" "$todo17/packaging/container"
cp -a "$root/deploy/docker/server" "$todo17/deploy/docker/server"
cp -a "$root/tooling/container" "$todo17/tooling/container"
mkdir -p "$todo17/tooling/fixtures"
cp -a "$root/tooling/fixtures/music" "$todo17/tooling/fixtures/music"
cp "$root/LICENSE" "$todo17/LICENSE"
printf '%s\n' "$version" >"$todo17/apps/server/VERSION"
JASTREAMER_REVISION="$revision" SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "$root" show -s --format=%ct HEAD)}" bun "$todo17/tooling/container/cli.ts" \
  --platform linux/amd64,linux/arm64 --compose "$todo17/deploy/docker/server/compose.synology.yaml" --scenario replacement-persistence \
  --oci-layout "$work/server.oci" --output "$work/oci-results.json"
jq -e --arg version "$version" '.status=="passed" and .version==$version and .manifestCount==2 and ([.manifests[].platform]|sort)==["linux/amd64","linux/arm64"] and any(.runtime[]; .platform=="linux/arm64" and .classification=="native")' "$work/oci-results.json" >/dev/null || { echo TODO17_OCI_VERIFICATION_FAILED >&2; exit 65; }
cp "$work/server.oci" "$out/jastreamer-server_${version}_linux_amd64-arm64.oci"
cp "$work/oci-results.json" "$out/oci-inspection.json"

cp "$root/LICENSE" "$out/Apache-2.0.txt"
cp "$root/packaging/container/THIRD_PARTY_NOTICES" "$out/THIRD_PARTY_NOTICES"
cp "$root/packaging/server/cert/server.cer" "$root/packaging/server/cert/fingerprint.txt" "$out/"
cp "$root/packaging/server/trust.md" "$root/packaging/server/remove-trust.md" "$out/"
jq -n '{publication:false,draftRelease:false,ghcrPromotion:false,promotionReachable:false,compensatingCleanup:[],externalWrites:[]}' >"$out/promotion-ledger.json"
for asset in "$out"/jastreamer-server_*; do
  if [[ $asset == *.oci ]]; then
    sha=$(sha256sum "$asset" | cut -d' ' -f1); name=$(basename "$asset")
    jq -n --arg name "$name" --arg sha "$sha" --arg created "$(date -u -d "@${SOURCE_DATE_EPOCH:-$(git -C "$root" show -s --format=%ct HEAD)}" +%Y-%m-%dT%H:%M:%SZ)" \
      '{spdxVersion:"SPDX-2.3",dataLicense:"CC0-1.0",SPDXID:"SPDXRef-DOCUMENT",name:($name+" artifact SBOM"),documentNamespace:("https://github.com/furyheimdall/jastreamer/sbom/"+$sha),creationInfo:{created:$created,creators:["Tool: jastreamer-artifact-SPDX/1","Tool: BuildKit-Syft-scanner/stable-1"]},packages:[{name:$name,SPDXID:"SPDXRef-Package-OCI-Index",downloadLocation:"NOASSERTION",filesAnalyzed:false,licenseConcluded:"Apache-2.0",licenseDeclared:"Apache-2.0",copyrightText:"NOASSERTION",checksums:[{algorithm:"SHA256",checksumValue:$sha}],externalRefs:[{referenceCategory:"PACKAGE-MANAGER",referenceType:"purl",referenceLocator:"pkg:oci/jastreamer-server"}]}],relationships:[{spdxElementId:"SPDXRef-DOCUMENT",relationshipType:"DESCRIBES",relatedSpdxElement:"SPDXRef-Package-OCI-Index"}]}' >"$asset.spdx.json"
  else
    "$work/bin/syft" scan "file:$asset" -o "spdx-json=$asset.spdx.json"
  fi
done
