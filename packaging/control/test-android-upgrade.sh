#!/bin/sh
set -eu

baseline_apk=${1:?baseline APK path required}
update_apk=${2:?update APK path required}
receipt=${3:?receipt path required}
application_id=io.jastreamer.control

adb install "$baseline_apk"
before=$(adb shell dumpsys package "$application_id")
uid_before=$(printf '%s\n' "$before" | tr -d '\r' | sed -n 's/.*userId=\([0-9][0-9]*\).*/\1/p' | head -n 1)
test -n "$uid_before"

adb install -r "$update_apk"
after=$(adb shell dumpsys package "$application_id")
uid_after=$(printf '%s\n' "$after" | tr -d '\r' | sed -n 's/.*userId=\([0-9][0-9]*\).*/\1/p' | head -n 1)
version_after=$(printf '%s\n' "$after" | tr -d '\r' | sed -n 's/.*versionCode=\([0-9][0-9]*\).*/\1/p' | head -n 1)
test -n "$uid_after"
test -n "$version_after"
test "$uid_before" = "$uid_after"
test "$version_after" = 1002003

printf '{"applicationId":"%s","sameSigningKey":true,"updateAccepted":true,"installedVersionCode":%s}\n' \
  "$application_id" "$version_after" > "$receipt"
