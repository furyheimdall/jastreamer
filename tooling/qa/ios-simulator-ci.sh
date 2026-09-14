#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"

: "${DEVELOPER_DIR:?Set DEVELOPER_DIR to the selected Xcode developer directory}"
results="${1:-dist/ios-evidence}"
derived="${2:-build/ios-simulator}"
mkdir -p "$results"

runtime="com.apple.CoreSimulator.SimRuntime.iOS-18-5"
device_type="com.apple.CoreSimulator.SimDeviceType.iPhone-16"
if ! xcrun simctl list runtimes available | grep -Fq "iOS 18.5"; then
    echo "The pinned iOS 18.5 simulator runtime is unavailable" >&2
    exit 69
fi
if ! xcrun simctl list devicetypes | grep -Fq "iPhone 16"; then
    echo "The pinned iPhone 16 simulator device type is unavailable" >&2
    exit 69
fi

udid="$(xcrun simctl create "Jastreamer iOS 18.5 CI" "$device_type" "$runtime")"
cleanup() {
    xcrun simctl shutdown "$udid" >/dev/null 2>&1 || true
    xcrun simctl delete "$udid" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

defaults write com.apple.iphonesimulator ConnectHardwareKeyboard -bool NO
xcrun simctl boot "$udid"
xcrun simctl bootstatus "$udid" -b
xcrun simctl status_bar "$udid" override --time 09:41 --batteryState charged --batteryLevel 100

common=(
    -project apps/ios/Jastreamer.xcodeproj
    -scheme Jastreamer
    -configuration Debug
    -destination "platform=iOS Simulator,id=$udid"
    -derivedDataPath "$derived"
    -parallel-testing-enabled NO
    COMPILER_INDEX_STORE_ENABLE=NO
)

python3 tooling/qa/ios-boundary-fixture.py xcodebuild "${common[@]}" \
    -resultBundlePath "$results/JastreamerTests.xcresult" \
    -only-testing:JastreamerTests \
    test

python3 tooling/qa/android-server-smoke.py \
    python3 tooling/qa/ios-boundary-fixture.py \
    xcodebuild "${common[@]}" \
        -resultBundlePath "$results/JastreamerUITests.xcresult" \
        -only-testing:JastreamerUITests \
        test
