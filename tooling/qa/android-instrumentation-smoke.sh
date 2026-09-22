#!/usr/bin/env bash
set -euo pipefail

evidence="apps/android/app/build/androidTest-evidence"
mkdir -p "${evidence}"
printf '%s\n' "${GITHUB_SHA}" > "${evidence}/source-revision.txt"
if [[ "$(adb get-serialno)" != emulator-* ]]; then
  echo 'Android smoke requires an isolated emulator, not a physical device' >&2
  exit 65
fi
# Keep the real Server's Host/Origin guard intact instead of trusting the emulator's host alias.
adb reverse tcp:18080 tcp:18080
trap 'adb reverse --remove tcp:18080 >/dev/null 2>&1 || true' EXIT
adb logcat -c
adb shell settings put secure show_ime_with_hard_keyboard 1

test_status=0
tooling/qa/android-server-smoke.py --verify-native-playback-errors --require-download-codecs \
  apps/android/gradlew -p apps/android --no-daemon --stacktrace \
  :app:connectedDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.serverUrl=http://127.0.0.1:18080 \
  || test_status=$?
if (( test_status == 0 )); then
  python3 tooling/qa/android-offline-restart-smoke.py || test_status=$?
fi

capture_status=0
mkdir -p "${evidence}/screenshots"
adb pull /sdcard/Pictures/jastreamer-android-smoke/. "${evidence}/screenshots/" \
  > "${evidence}/screenshot-pull.txt" 2>&1 || capture_status=$?
adb logcat -b all -d > "${evidence}/logcat.txt" 2>&1 || capture_status=$?
adb exec-out screencap -p > "${evidence}/final-screen.png" 2> "${evidence}/screencap-error.txt" \
  || capture_status=$?

if (( test_status != 0 )); then
  exit "${test_status}"
fi
screenshots=("${evidence}/screenshots/"*.png)
if (( capture_status != 0 )) || [[ ! -s "${screenshots[0]}" || ! -s "${evidence}/final-screen.png" ]]; then
  echo 'Instrumented tests passed, but required Android visual evidence could not be collected' >&2
  exit 65
fi
for required in native-playing native-background native-recreated native-recovery-stopped \
  offline-import-complete offline-library-after-logout offline-playing-after-folder-move \
  offline-current-delete-deferred offline-restored-without-server-login \
  offline-cold-process-airplane-playback \
  offline-player-mini offline-player-expanded offline-player-landscape; do
  if [[ ! -s "${evidence}/screenshots/${required}.png" ]]; then
    echo "Missing native playback evidence: ${required}.png" >&2
    exit 65
  fi
done
