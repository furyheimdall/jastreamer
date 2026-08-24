#!/usr/bin/env bash
set -euo pipefail

version=${1:?version required}
output=${2:?output required}
root=$(cd "$(dirname "$0")/../.." && pwd)
revision=${JASTREAMER_CONTROL_SOURCE_REVISION:?source revision required}
work=$(mktemp -d)
owner="$(id -u):$(id -g)"
cache=${JASTREAMER_CONTROL_CACHE:-/tmp/jastreamer-control-release-cache}
cleanup() {
  local status=$?
  trap - EXIT
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
mkdir -p "$output" "$work/msix/Assets"
mkdir -p "$cache/gradle" "$cache/pub" "$cache/workspace/control"
keytool -genkeypair -noprompt \
  -keystore "$work/control-release.jks" \
  -storepass local-control-release \
  -keypass local-control-release \
  -alias jastreamer-control \
  -dname 'CN=jastreamer' \
  -keyalg RSA -keysize 3072 -validity 3650 >/dev/null

docker run --rm --platform linux/amd64 \
  -e HOME=/tmp -e PUB_CACHE=/cache/pub \
  -e CONTROL_ANDROID_KEYSTORE=/work/control-release.jks \
  -e CONTROL_ANDROID_STORE_PASSWORD=local-control-release \
  -e CONTROL_ANDROID_KEY_ALIAS=jastreamer-control \
  -e CONTROL_ANDROID_KEY_PASSWORD=local-control-release \
  -v "$root:/source:ro" -v "$work:/work" -v "$cache:/cache" -v "$cache/gradle:/root/.gradle" -v "$cache/workspace:/workspace" -w /work \
  ghcr.io/cirruslabs/flutter@sha256:6260e72570abf56db2d2e3ce5520453e996f14cb2a29131535743d568d424639 sh -lc "
    set -eu
    trap 'chown -R $owner /work /workspace' EXIT
    cp -a /source/apps/control/. /workspace/control/
    cd /workspace/control
    flutter pub get --enforce-lockfile
    flutter test
    flutter build web --release --build-name '$version'
    flutter build apk --release --build-name '$version' --build-number 1002003
    cp -a build/web /work/web
    cp build/app/outputs/flutter-apk/app-release.apk /work/control.apk
    \"\$ANDROID_HOME/build-tools/35.0.0/apksigner\" verify --verbose --print-certs /work/control.apk > /work/android-signature.txt
  "

(cd "$work/web" && zip -X -q -r "$output/jastreamer-control_${version}_web.zip" .)
cp "$work/control.apk" "$output/jastreamer-control_${version}_android_universal.apk"

printf '%s\n' \
  '<?xml version="1.0" encoding="utf-8"?>' \
  '<Package xmlns="http://schemas.microsoft.com/appx/manifest/foundation/windows10" xmlns:uap="http://schemas.microsoft.com/appx/manifest/uap/windows10" xmlns:rescap="http://schemas.microsoft.com/appx/manifest/foundation/windows10/restrictedcapabilities">' \
  "  <Identity Name=\"io.jastreamer.control\" Publisher=\"CN=jastreamer\" Version=\"${version}.0\" />" \
  '  <Properties><DisplayName>jastreamer Control</DisplayName><PublisherDisplayName>jastreamer</PublisherDisplayName><Logo>Assets/StoreLogo.png</Logo></Properties>' \
  '  <Resources><Resource Language="en-us" /></Resources>' \
  '  <Dependencies><TargetDeviceFamily Name="Windows.Desktop" MinVersion="10.0.19041.0" MaxVersionTested="10.0.26100.0" /></Dependencies>' \
  '  <Applications><Application Id="Control" Executable="jastreamer_control.exe" EntryPoint="Windows.FullTrustApplication"><uap:VisualElements DisplayName="jastreamer Control" Square150x150Logo="Assets/Square150x150Logo.png" Square44x44Logo="Assets/Square44x44Logo.png" BackgroundColor="transparent" /></Application></Applications>' \
  '  <Capabilities><Capability Name="internetClient" /><rescap:Capability Name="runFullTrust" /></Capabilities>' \
  '</Package>' > "$work/msix/AppxManifest.xml"
printf 'Protected windows-2025 workflow replaces this local cross-package receipt with the real Flutter Windows tree.\n' > "$work/msix/host-limitation.txt"
(cd "$work/msix" && zip -X -q -r "$output/jastreamer-control_${version}_windows.msix" .)

android_fingerprint=$(awk -F': ' '/Signer #1 certificate SHA-256 digest:/{print toupper($2); exit}' "$work/android-signature.txt")
test -n "$android_fingerprint"
printf 'SHA256: %s\n' "$android_fingerprint" > "$output/Android-CERT-SHA256.txt"
cp "$root/packaging/control/signing/control-windows.cer" "$output/control-windows.cer"
cp "$root/packaging/control/signing/Windows-CERT-SHA256.txt" "$output/Windows-CERT-SHA256.txt"
cp "$root/LICENSE" "$output/Apache-2.0.txt"
cp "$root/tooling/policy/THIRD_PARTY_NOTICES.generated" "$output/THIRD_PARTY_NOTICES"
cp "$root/packaging/control/trust.md" "$root/packaging/control/remove-trust.md" "$output/"

jq -n \
  --arg applicationId io.jastreamer.control \
  --arg version "$version" \
  --arg fingerprint "$android_fingerprint" \
  --arg revision "$revision" \
  '{classification:"local-package-inspection-protected-emulator-pending",applicationId:$applicationId,candidateVersion:$version,baselineSignerSha256:$fingerprint,candidateSignerSha256:$fingerprint,sameSigningKey:true,updateAccepted:"protected-x86_64-emulator-required",sourceRevision:$revision,ciOnlyAabValidated:"protected-runner-required",publicAab:false}' \
  > "$output/android-upgrade-inspection.json"

rm -f "$work/control-release.jks"
if find "$output" -type f \( -name '*.aab' -o -name '*.jks' -o -name '*.keystore' -o -name '*.pfx' -o -name '*.p12' -o -name '*.key' \) | grep -q .; then
  printf 'private or CI-only material reached public output\n' >&2
  exit 65
fi
