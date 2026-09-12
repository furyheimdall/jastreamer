#!/usr/bin/env sh
set -eu

out=${1:-dist/release}
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
version=$(cat "$root/apps/server/VERSION")
revision=${JASTREAMER_SOURCE_REVISION:?set JASTREAMER_SOURCE_REVISION to the full source revision}
created=${SOURCE_DATE_EPOCH:?set SOURCE_DATE_EPOCH for reproducible image metadata}
case "$version" in
  0.2.0) ;;
  *) echo "VERSION must be 0.2.0" >&2; exit 65 ;;
esac
case "$revision" in
  *[!0-9a-fA-F]*|'') echo "JASTREAMER_SOURCE_REVISION must be a hexadecimal revision" >&2; exit 65 ;;
esac
case "${#revision}" in
  40|64) ;;
  *) echo "JASTREAMER_SOURCE_REVISION must be a full Git object ID" >&2; exit 65 ;;
esac
case "$created" in
  *[!0-9]*|'') echo "SOURCE_DATE_EPOCH must be an integer" >&2; exit 65 ;;
esac
created_iso=$(date -u -d "@$created" +%Y-%m-%dT%H:%M:%SZ)
case "$out" in
  /*) release_dir=$out ;;
  *) release_dir=$root/$out ;;
esac
mkdir -p "$release_dir"
artifact="$release_dir/jastreamer-server_${version}_linux_amd64-arm64.oci"
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --build-arg "VERSION=$version" \
  --build-arg "REVISION=$revision" \
  --build-arg "CREATED=$created_iso" \
  --provenance=false \
  --output "type=oci,dest=$artifact" \
  --file "$root/apps/server/Dockerfile" "$root"
cp "$root/packaging/server/manifest.json" "$release_dir/manifest.json"
cp "$root/packaging/server/server.json" "$release_dir/server.json"
cp "$root/packaging/server/THIRD-PARTY-NOTICES.txt" "$release_dir/THIRD-PARTY-NOTICES.txt"
cp "$root/LICENSE" "$release_dir/LICENSE"
(
  cd "$release_dir"
  sha256sum "$(basename "$artifact")" manifest.json server.json LICENSE THIRD-PARTY-NOTICES.txt > SHA256SUMS
)
printf '%s\n' "Created $artifact and SHA256SUMS; nothing was published."
