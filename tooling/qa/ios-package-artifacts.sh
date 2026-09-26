#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"

: "${GITHUB_SHA:?GITHUB_SHA is required for provenance}"
: "${DEVELOPER_DIR:?DEVELOPER_DIR is required for provenance}"

device_app="${1:-build/ios-device/Build/Products/Release-iphoneos/Jastreamer.app}"
simulator_app="${2:-build/ios-simulator/Build/Products/Debug-iphonesimulator/Jastreamer.app}"
output="${3:-dist/ios}"

revision="$(git rev-parse HEAD)"
test "$revision" = "$GITHUB_SHA"
test -z "$(git status --porcelain --untracked-files=normal)"
test -d "$device_app"
test -d "$simulator_app"
for result in dist/ios-evidence/JastreamerTests.xcresult dist/ios-evidence/JastreamerUITests.xcresult; do
    test -d "$result"
    summary="$(xcrun xcresulttool get test-results summary --path "$result")"
    jq -e '.result == "Passed" and .failedTests == 0 and .skippedTests == 0 and .totalTestCount > 0' <<<"$summary" >/dev/null
done

for app in "$device_app" "$simulator_app"; do
    plist="$app/Info.plist"
    test -f "$plist"
    test "$(plutil -extract CFBundleIdentifier raw -o - "$plist")" = "io.jastreamer.ios"
    test "$(plutil -extract CFBundleShortVersionString raw -o - "$plist")" = "0.2.1"
    test "$(plutil -extract CFBundleVersion raw -o - "$plist")" = "20100"
    test "$(plutil -extract MinimumOSVersion raw -o - "$plist")" = "18.4"
    cmp LICENSE "$app/LICENSE.txt"
done
test "$(plutil -extract DTPlatformName raw -o - "$device_app/Info.plist")" = "iphoneos"
test "$(plutil -extract DTPlatformName raw -o - "$simulator_app/Info.plist")" = "iphonesimulator"
if codesign --verify --strict "$device_app" >/dev/null 2>&1; then
    echo "The generic iphoneos development bundle must remain unsigned" >&2
    exit 1
fi
codesign --verify --strict "$simulator_app"
simulator_signing="$(codesign -dvv "$simulator_app" 2>&1)"
if [[ "$simulator_signing" != *"Signature=adhoc"* || "$simulator_signing" != *"TeamIdentifier=not set"* ]]; then
    echo "The simulator app must use only Xcode's ad-hoc test signature" >&2
    exit 1
fi

if [[ -e "$output" ]]; then
    echo "Refusing to replace an existing artifact directory: $output" >&2
    exit 1
fi
mkdir -p "$output"
device_name="jastreamer-ios_0.2.1_${GITHUB_SHA}_device-development-unsigned-not-installable.zip"
simulator_name="jastreamer-ios_0.2.1_${GITHUB_SHA}_simulator-development-test-adhoc.zip"
ditto -c -k --keepParent "$device_app" "$output/$device_name"
ditto -c -k --keepParent "$simulator_app" "$output/$simulator_name"
unzip -t "$output/$device_name" > "$output/device-package-integrity.txt"
unzip -t "$output/$simulator_name" > "$output/simulator-package-integrity.txt"

(
    cd "$output"
    shasum -a 256 "$device_name" "$simulator_name" > SHA256SUMS
)

xcode_version="$(xcodebuild -version | tr '\n' ' ' | sed 's/ $//')"
iphoneos_sdk="$(xcrun --sdk iphoneos --show-sdk-version)"
simulator_sdk="$(xcrun --sdk iphonesimulator --show-sdk-version)"
device_sha="$(shasum -a 256 "$output/$device_name" | cut -d ' ' -f 1)"
simulator_sha="$(shasum -a 256 "$output/$simulator_name" | cut -d ' ' -f 1)"
device_size="$(stat -f %z "$output/$device_name")"
simulator_size="$(stat -f %z "$output/$simulator_name")"

jq -n \
    --arg sourceRevision "$revision" \
    --arg workflowRevision "$GITHUB_SHA" \
    --arg version "0.2.1" \
    --arg minimumIOS "18.4" \
    --arg xcode "$xcode_version" \
    --arg iphoneosSDK "$iphoneos_sdk" \
    --arg simulatorSDK "$simulator_sdk" \
    --arg deviceFile "$device_name" \
    --arg deviceSHA256 "$device_sha" \
    --argjson deviceSize "$device_size" \
    --arg simulatorFile "$simulator_name" \
    --arg simulatorSHA256 "$simulator_sha" \
    --argjson simulatorSize "$simulator_size" \
    --arg simulatorSigning "$simulator_signing" \
    '{sourceRevision: $sourceRevision, workflowRevision: $workflowRevision, sourceMatchesWorkflow: ($sourceRevision == $workflowRevision), version: $version, minimumIOS: $minimumIOS, xcode: $xcode, sdks: {iphoneos: $iphoneosSDK, iphonesimulator: $simulatorSDK}, productionQualified: false, distributable: false, artifacts: [
      {file: $deviceFile, sha256: $deviceSHA256, size: $deviceSize, platform: "iphoneos", signing: "unsigned development bundle; not installable until separately signed; not an IPA, archive, App Store, TestFlight, or production release"},
      {file: $simulatorFile, sha256: $simulatorSHA256, size: $simulatorSize, platform: "iphonesimulator", signing: "Xcode ad-hoc test signature only; simulator-only development bundle; not installable on iPhone or iPad; not a production release", signingEvidence: $simulatorSigning}
    ]}' > "$output/provenance.json"

jq -e '.sourceMatchesWorkflow == true and .productionQualified == false and .distributable == false' "$output/provenance.json" >/dev/null
(cd "$output" && shasum -a 256 -c SHA256SUMS)
