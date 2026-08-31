#!/usr/bin/env bash
set -euo pipefail

version=${1:?version required}
output=${2:?output required}
root=$(cd "$(dirname "$0")/../.." && pwd)
revision=${JASTREAMER_CONTROL_SOURCE_REVISION:?source revision required}
work=$(mktemp -d)
owner="$(id -u):$(id -g)"
cache=${JASTREAMER_CONTROL_CACHE:-/tmp/jastreamer-control-release-cache}
web_sdk_cache="$cache/flutter-web-sdk-1e9a811bf8e70466596bcf0ea3a8b5adb5f17f7f"
android_ndk_cache="$cache/ndk/27.0.12077973"
android_platform_cache="$cache/platforms/android-36"
android_cmake_cache="$cache/cmake/3.22.1"
web_only=${JASTREAMER_CONTROL_WEB_ONLY:-0}
bun "$root/packaging/control/tooling/font-contract.ts" "$root/apps/control" >/dev/null
if [[ "$web_only" != 0 && "$web_only" != 1 ]]; then
  printf 'JASTREAMER_CONTROL_WEB_ONLY must be 0 or 1\n' >&2
  exit 64
fi
cleanup() {
  local status=$?
  trap - EXIT
  rm -rf "$work"
  exit "$status"
}
trap cleanup EXIT
mkdir -p "$output" "$cache/pub" "$cache/workspace/apps/control"
if [[ "$web_only" == 1 ]]; then
  docker run --rm --network none --platform linux/amd64 \
    -e HOME=/tmp -e PUB_CACHE=/cache/pub \
    -v "$root:/source:ro" -v "$work:/work" -v "$cache:/cache" -v "$cache/workspace:/workspace" \
    -v "$web_sdk_cache/flutter_web_sdk:/sdks/flutter/bin/cache/flutter_web_sdk:ro" \
    -v "$web_sdk_cache/flutter_web_sdk.stamp:/sdks/flutter/bin/cache/flutter_web_sdk.stamp:ro" -w /work \
    ghcr.io/cirruslabs/flutter@sha256:6260e72570abf56db2d2e3ce5520453e996f14cb2a29131535743d568d424639 sh -lc "
      set -eu
      trap 'chown -R $owner /work /workspace' EXIT
      rm -rf /workspace/apps/control /workspace/contracts
      cp -a /source/apps/control /workspace/apps/control
      cp -a /source/contracts /workspace/contracts
      rm -rf /workspace/apps/control/build /workspace/apps/control/.dart_tool
      cd /workspace/apps/control
      flutter_root=\$(cd \"\$(dirname \"\$(command -v flutter)\")/..\" && pwd)
      python3 /source/packaging/control/tooling/flutter_web_sdk_preflight.py /cache/flutter-web-sdk-1e9a811bf8e70466596bcf0ea3a8b5adb5f17f7f/flutter-web-sdk.zip \"\$flutter_root/bin/cache/flutter_web_sdk\" \"\$flutter_root\" \"\$flutter_root/bin/cache/flutter_web_sdk.stamp\"
      python3 /source/packaging/control/tooling/offline_package_config.py /workspace/apps/control /cache/pub "\$flutter_root"
      test \"\$(sha256sum \"\$flutter_root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf\" | cut -d' ' -f1)\" = 79e851404657dac2106b3d22ad256d47824a9a5765458edb72c9102a45816d95
      mkdir -p .release-web-fonts
      cp \"\$flutter_root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf\" .release-web-fonts/Roboto-Regular.ttf
      sed -i '/^  fonts:$/a\\    - family: Roboto\\n      fonts:\\n        - asset: .release-web-fonts/Roboto-Regular.ttf\\n          weight: 400' pubspec.yaml
      flutter test --no-pub
      rm -rf build/web
      flutter build web --no-pub --release --no-web-resources-cdn --build-name '$version'
      cp -a build/web /work/web
    "
  bun "$root/packaging/control/tooling/normalize-web.ts" "$work/web" "$version" "$revision" \
    'sha256:6260e72570abf56db2d2e3ce5520453e996f14cb2a29131535743d568d424639'
  (cd "$work/web" && LC_ALL=C find . -mindepth 1 -printf '%P\n' | LC_ALL=C sort | TZ=UTC zip -X -q "$output/jastreamer-control_${version}_web.zip" -@)
  unzip -t "$output/jastreamer-control_${version}_web.zip" >/dev/null
  exit 0
fi
test -f "$android_ndk_cache/source.properties"
grep -qx 'Pkg.Revision = 27.0.12077973' "$android_ndk_cache/source.properties"
test -f "$android_platform_cache/package.xml"
grep -q 'path="platforms;android-36"' "$android_platform_cache/package.xml"
test -x "$android_cmake_cache/bin/cmake"
test -f "$android_cmake_cache/package.xml"
grep -q 'path="cmake;3.22.1"' "$android_cmake_cache/package.xml"
mkdir -p "$work/msix/Assets" "$cache/gradle"
keytool -genkeypair -noprompt \
  -keystore "$work/control-release.jks" \
  -storepass local-control-release \
  -keypass local-control-release \
  -alias jastreamer-control \
  -dname 'CN=jastreamer' \
  -keyalg RSA -keysize 3072 -validity 3650 >/dev/null

docker run --rm --network none --platform linux/amd64 \
  -e HOME=/tmp -e PUB_CACHE=/cache/pub \
  -e GRADLE_OPTS=-Dorg.gradle.offline=true \
  -e CONTROL_ANDROID_KEYSTORE=/work/control-release.jks \
  -e CONTROL_ANDROID_STORE_PASSWORD=local-control-release \
  -e CONTROL_ANDROID_KEY_ALIAS=jastreamer-control \
  -e CONTROL_ANDROID_KEY_PASSWORD=local-control-release \
  -v "$root:/source:ro" -v "$work:/work" -v "$cache:/cache" -v "$cache/gradle:/root/.gradle" -v "$cache/workspace:/workspace" \
  -v "$android_ndk_cache:/opt/android-sdk-linux/ndk/27.0.12077973:ro" \
  -v "$android_platform_cache:/opt/android-sdk-linux/platforms/android-36:ro" \
  -v "$android_cmake_cache:/opt/android-sdk-linux/cmake/3.22.1:ro" \
  -v "$web_sdk_cache/flutter_web_sdk:/sdks/flutter/bin/cache/flutter_web_sdk:ro" \
  -v "$web_sdk_cache/flutter_web_sdk.stamp:/sdks/flutter/bin/cache/flutter_web_sdk.stamp:ro" -w /work \
  ghcr.io/cirruslabs/flutter@sha256:6260e72570abf56db2d2e3ce5520453e996f14cb2a29131535743d568d424639 sh -lc "
    set -eu
    trap 'chown -R $owner /work /workspace' EXIT
    rm -rf /workspace/apps/control /workspace/contracts
    cp -a /source/apps/control /workspace/apps/control
    cp -a /source/contracts /workspace/contracts
    cd /workspace/apps/control
    rm -rf .dart_tool build
    flutter_root=\$(cd \"\$(dirname \"\$(command -v flutter)\")/..\" && pwd)
    python3 /source/packaging/control/tooling/flutter_web_sdk_preflight.py /cache/flutter-web-sdk-1e9a811bf8e70466596bcf0ea3a8b5adb5f17f7f/flutter-web-sdk.zip \"\$flutter_root/bin/cache/flutter_web_sdk\" \"\$flutter_root\" \"\$flutter_root/bin/cache/flutter_web_sdk.stamp\"
    python3 /source/packaging/control/tooling/offline_package_config.py /workspace/apps/control /cache/pub "\$flutter_root"
    test \"\$(sha256sum \"\$flutter_root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf\" | cut -d' ' -f1)\" = 79e851404657dac2106b3d22ad256d47824a9a5765458edb72c9102a45816d95
    mkdir -p .release-web-fonts
    cp \"\$flutter_root/bin/cache/artifacts/material_fonts/Roboto-Regular.ttf\" .release-web-fonts/Roboto-Regular.ttf
    sed -i '/^  fonts:$/a\\    - family: Roboto\\n      fonts:\\n        - asset: .release-web-fonts/Roboto-Regular.ttf\\n          weight: 400' pubspec.yaml
    flutter test --no-pub
    rm -rf build/web
    flutter build web --no-pub --release --no-web-resources-cdn --build-name '$version'
    flutter build apk --release --config-only --build-name '$version' --build-number 1002003
    flutter build apk --no-pub --release --build-name '$version' --build-number 1002003
    cp -a build/web /work/web
    cp build/app/outputs/flutter-apk/app-release.apk /work/control.apk
    \"\$ANDROID_HOME/build-tools/35.0.0/apksigner\" verify --verbose --print-certs /work/control.apk > /work/android-signature.txt
  "

bun "$root/packaging/control/tooling/normalize-web.ts" "$work/web" "$version" "$revision" \
  'sha256:6260e72570abf56db2d2e3ce5520453e996f14cb2a29131535743d568d424639'
(cd "$work/web" && LC_ALL=C find . -mindepth 1 -printf '%P\n' | LC_ALL=C sort | TZ=UTC zip -X -q "$output/jastreamer-control_${version}_web.zip" -@)
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
