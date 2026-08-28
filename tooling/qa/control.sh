#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
. "$root/tooling/qa/control-android-signing.sh"
platforms=
fixture=
screenshots=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --platform) platforms=$2; shift 2 ;;
    --fixture) fixture=$2; shift 2 ;;
    --screenshots) screenshots=$2; shift 2 ;;
    *) printf 'unknown Control QA argument: %s\n' "$1" >&2; exit 64 ;;
  esac
done
if [ "$platforms" != web,windows,android ] || [ ! -f "$fixture" ] || [ -z "$screenshots" ]; then
  printf '%s\n' 'control QA requires --platform web,windows,android --fixture <file> --screenshots <directory>' >&2
  exit 64
fi
fixture=$(realpath "$fixture")
mkdir -p "$screenshots"
screenshots=$(realpath "$screenshots")
image=ghcr.io/cirruslabs/flutter:3.35.0@sha256:114f14a7cf973b08e4607d3e2fb4a3b2dc977c08877e651743f8cbed0e971046
owner_uid=$(id -u)
owner_gid=$(id -g)
android_cache=${CONTROL_ANDROID_CACHE:-/tmp/jastreamer-control-android-cache}
qa_signing_dir=
qa_keystore=
qa_store_password=
qa_key_alias=
qa_key_password=
mkdir -p "$android_cache/gradle" "$android_cache/ndk" "$android_cache/platforms" "$android_cache/cmake"
cleanup() {
  cleanup_control_android_qa_signing
  docker run --rm -v "$root/apps/control:/workspace" alpine:3.22 sh -lc \
    "rm -rf /workspace/.dart_tool /workspace/build && chown -R $owner_uid:$owner_gid /workspace" >/dev/null
  test -z "$(find "$screenshots" -type f \( -name '*.jks' -o -name '*.keystore' -o -name '*.p12' -o -name '*.pfx' -o -name '*.key' \) -print -quit)"
  test -z "$(docker ps -q --filter ancestor="$image")"
  test -z "$(pgrep -f 'jastreamer-control-qa-.*/jastreamer-server|control.spec.mjs' || true)"
}
trap cleanup EXIT INT TERM
node --test \
  "$root/tooling/qa/check-control-contract.test.mjs" \
  "$root/tooling/qa/control-android-signing.test.mjs"
node "$root/tooling/qa/check-control-contract.mjs"
docker run --rm -v "$root/apps/control:/workspace" alpine:3.22 \
  chown -R "$owner_uid:$owner_gid" /workspace
rm -rf "$root/apps/control/.dart_tool" "$root/apps/control/build"
docker run --rm -v "$root/apps/control:/workspace" -w /workspace "$image" sh -lc \
  'flutter pub get && dart format --output=none --set-exit-if-changed lib test integration_test && flutter analyze && flutter test && flutter build web --release'
docker run --rm -v "$root/apps/control:/workspace" alpine:3.22 \
  chown -R "$owner_uid:$owner_gid" /workspace
rm -rf "$root/apps/control/.dart_tool" "$root/apps/control/build/app"
create_control_android_qa_signing
docker run --rm --platform linux/amd64 \
  -v "$root/apps/control:/workspace" \
  -v "$qa_signing_dir:/qa-signing:ro" \
  -e CONTROL_ANDROID_KEYSTORE=/qa-signing/control-qa.jks \
  -e CONTROL_ANDROID_STORE_PASSWORD="$qa_store_password" \
  -e CONTROL_ANDROID_KEY_ALIAS="$qa_key_alias" \
  -e CONTROL_ANDROID_KEY_PASSWORD="$qa_key_password" \
  -v "$android_cache/gradle:/root/.gradle" \
  -v "$android_cache/ndk:/opt/android-sdk-linux/ndk" \
  -v "$android_cache/platforms:/opt/android-sdk-linux/platforms" \
  -v "$android_cache/cmake:/opt/android-sdk-linux/cmake" \
  -w /workspace "$image" sh -lc \
  'flutter pub get && flutter build apk --release && flutter build appbundle --release'
cleanup_control_android_qa_signing
apk="$root/apps/control/build/app/outputs/flutter-apk/app-release.apk"
aab="$root/apps/control/build/app/outputs/bundle/release/app-release.aab"
{
  file "$apk" "$aab"
  sha256sum "$apk" "$aab"
  unzip -l "$apk" | grep -E 'lib/(armeabi-v7a|arm64-v8a|x86_64)/lib(flutter|app).so'
  unzip -l "$aab" | grep -E 'base/lib/(armeabi-v7a|arm64-v8a|x86_64)/libapp.so'
} | tee "$screenshots/android-artifacts.txt"
test "$(unzip -l "$apk" | grep -Ec 'lib/(armeabi-v7a|arm64-v8a|x86_64)/libapp.so')" -eq 3
test "$(unzip -l "$aab" | grep -Ec 'base/lib/(armeabi-v7a|arm64-v8a|x86_64)/libapp.so')" -eq 3
test -f "$root/apps/control/windows/CMakeLists.txt"
grep -q 'identity_name: io.jastreamer.control' "$root/apps/control/pubspec.yaml"
cd "$root/tooling/qa"
CONTROL_FIXTURE=$fixture CONTROL_OUTPUT=$screenshots \
  bunx --no-install playwright test control.spec.mjs --browser chromium --workers 1 --reporter line
