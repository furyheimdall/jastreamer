#!/usr/bin/env bash
# Builds and runs the host tests for the native USB direct driver.
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
source_dir="$root/apps/android/app/src/main/cpp/test"
build_dir="${1:-$root/apps/android/app/build/usb-direct-host-tests}"

mkdir -p "$build_dir"
cmake -S "$source_dir" -B "$build_dir" -DCMAKE_BUILD_TYPE=Debug
cmake --build "$build_dir" --target usb_direct_host_tests

"$build_dir/usb_direct_host_tests"
