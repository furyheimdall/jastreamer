#!/usr/bin/env python3
"""Verify owned music after process stop, package replacement and emulator reboot offline."""

import re
import socket
import subprocess
import time
from pathlib import Path


def run(*arguments, timeout=120):
    result = subprocess.run(arguments, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
    print("+ " + " ".join(arguments), flush=True)
    print(result.stdout, end="", flush=True)
    result.check_returncode()
    return result.stdout.strip()


def instrument(phase, evidence, scenario="process"):
    output = run(
        "adb", "shell", "am", "instrument", "-w", "-r",
        "-e", "class", "io.jastreamer.android.OfflineColdStartSmokeTest",
        "-e", "offlinePhase", phase,
        "io.jastreamer.android.debug.test/androidx.test.runner.AndroidJUnitRunner",
        timeout=180,
    )
    (evidence / f"offline-cold-{scenario}-{phase}.txt").write_text(output + "\n", encoding="utf-8")
    if not re.search(r"(?m)^OK \(1 test\)\s*$", output) or re.search(r"(?m)^INSTRUMENTATION_STATUS_CODE: -[12]$", output):
        raise RuntimeError(f"Cold-{scenario} {phase} instrumentation did not report one successful test")


def wait_for_boot():
    run("adb", "wait-for-device", timeout=180)
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        if run("adb", "shell", "getprop", "sys.boot_completed", timeout=15) == "1":
            return
        time.sleep(2)
    raise RuntimeError("Isolated emulator did not complete its reboot")


def main():
    root = Path(__file__).resolve().parents[2]
    if not run("adb", "get-serialno").startswith("emulator-"):
        raise RuntimeError("Cold-process smoke requires an isolated emulator, never a physical device")
    try:
        connection = socket.create_connection(("127.0.0.1", 18080), timeout=1)
    except ConnectionRefusedError:
        pass
    else:
        connection.close()
        raise RuntimeError("The real Server fixture must be stopped before cold offline verification")
    evidence = root / "apps/android/app/build/androidTest-evidence"
    evidence.mkdir(parents=True, exist_ok=True)
    run("adb", "install", "-r", str(root / "apps/android/app/build/outputs/apk/debug/app-debug.apk"))
    run("adb", "install", "-r", str(root / "apps/android/app/build/outputs/apk/androidTest/debug/app-debug-androidTest.apk"))
    instrument("seed", evidence)
    original_mode = run("adb", "shell", "settings", "get", "global", "airplane_mode_on")
    if original_mode not in ("0", "1"):
        raise RuntimeError("Cannot safely restore the emulator's original airplane-mode state")
    try:
        run("adb", "shell", "am", "force-stop", "io.jastreamer.android.debug")
        run("adb", "shell", "cmd", "connectivity", "airplane-mode", "enable")
        if run("adb", "shell", "settings", "get", "global", "airplane_mode_on") != "1":
            raise RuntimeError("Emulator airplane mode was not enabled")
        instrument("verify", evidence)
        instrument("seed", evidence, "package-replacement")
        run("adb", "shell", "am", "force-stop", "io.jastreamer.android.debug")
        run("adb", "install", "-r", str(root / "apps/android/app/build/outputs/apk/debug/app-debug.apk"))
        instrument("verify", evidence, "package-replacement")
        instrument("seed", evidence, "reboot")
        run("adb", "reboot")
        wait_for_boot()
        if run("adb", "shell", "settings", "get", "global", "airplane_mode_on") != "1":
            raise RuntimeError("Reboot did not preserve the emulator's offline state")
        instrument("verify", evidence, "reboot")
    finally:
        run("adb", "shell", "cmd", "connectivity", "airplane-mode", "enable" if original_mode == "1" else "disable")
    print("Verified local audio and duplicate queue restoration after process stop, same-APK replacement, and emulator reboot without Server/login; not a version upgrade or physical audible evidence.")


if __name__ == "__main__":
    main()
