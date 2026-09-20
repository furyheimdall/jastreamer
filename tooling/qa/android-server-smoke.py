#!/usr/bin/env python3
"""Run Android instrumentation against an isolated, real Server and embedded Web UI."""

import json
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import wave
from pathlib import Path


class Interrupted(Exception):
    def __init__(self, signum):
        self.signum = signum


def main():
    if len(sys.argv) < 2:
        print("usage: android-server-smoke.py COMMAND [ARG ...]", file=sys.stderr)
        return 64
    root = Path(__file__).resolve().parents[2]
    binary = root / "apps/server/dist/jastreamer-server"
    if not binary.is_file():
        print("Build the actual Server and embedded Web first: make build", file=sys.stderr)
        return 65
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 18080))
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 0))
        upstream_port = reservation.getsockname()[1]

    def interrupted(signum, _frame):
        raise Interrupted(signum)

    signal.signal(signal.SIGINT, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    result = 65
    command = None
    proxy = None
    with tempfile.TemporaryDirectory(prefix="jastreamer-android-smoke-") as temporary:
        work = Path(temporary)
        data = work / "data"
        data.mkdir()
        music = work / "music"
        music.mkdir()
        silence = bytes(44100 * 2)
        for name, seconds in (("short.wav", 3), ("long.wav", 600)):
            with wave.open(str(music / name), "wb") as audio:
                audio.setnchannels(1)
                audio.setsampwidth(2)
                audio.setframerate(44100)
                for _ in range(seconds):
                    audio.writeframesraw(silence)
        config = work / "server.json"
        config.write_text(json.dumps({
            "version": 1,
            "data_dir": str(data),
            "http": {"enabled": True, "address": f"127.0.0.1:{upstream_port}"},
            "https": {"enabled": False, "address": ":8443", "certificate_file": "", "private_key_file": ""},
            "library_roots": [{"id": "android-smoke", "name": "Android smoke", "path": str(music)}],
            "network": {"interfaces": [], "discovery_interval_seconds": 30, "poll_interval_seconds": 1, "allowed_cidrs": []},
            "media": {"base_url": "", "ffmpeg_path": "", "transcode": False},
        }), encoding="utf-8")
        log_path = work / "server.log"
        with log_path.open("w", encoding="utf-8") as log:
            server = subprocess.Popen([str(binary), "--config", str(config)], cwd=root, stdout=log, stderr=log)
            try:
                proxy = subprocess.Popen(
                    ["node", str(root / "tooling/qa/android-smoke-proxy.mjs"), str(upstream_port), "18080"],
                    cwd=root, stdout=log, stderr=log,
                )
                deadline = time.monotonic() + 20
                while True:
                    if server.poll() is not None:
                        raise RuntimeError(f"Fixture Server exited before readiness: {server.returncode}")
                    if proxy.poll() is not None:
                        raise RuntimeError(f"Fixture proxy exited before readiness: {proxy.returncode}")
                    try:
                        with urllib.request.urlopen("http://127.0.0.1:18080/healthz", timeout=1) as response:
                            if response.status == 200:
                                break
                    except (urllib.error.URLError, TimeoutError):
                        pass
                    if time.monotonic() >= deadline:
                        raise RuntimeError("Fixture Server did not become healthy within 20 seconds")
                    time.sleep(0.1)
                print("Android smoke Server ready at host loopback TCP 18080", flush=True)
                command = subprocess.Popen(sys.argv[1:], cwd=root, start_new_session=True)
                result = command.wait()
                if result < 0:
                    result = 128 - result
                if server.poll() is not None:
                    raise RuntimeError(f"Fixture Server exited during instrumentation: {server.returncode}")
                if proxy.poll() is not None:
                    raise RuntimeError(f"Fixture proxy exited during instrumentation: {proxy.returncode}")
            except Interrupted as failure:
                result = 128 + failure.signum
            except (OSError, RuntimeError) as failure:
                print(failure, file=sys.stderr)
                result = 65
            finally:
                # A supervisor may signal the process tree again while children are being reaped.
                signal.signal(signal.SIGINT, signal.SIG_IGN)
                signal.signal(signal.SIGTERM, signal.SIG_IGN)
                if command is not None and command.poll() is None:
                    # The child session is this helper's command, never a host service.
                    import os
                    try:
                        os.killpg(command.pid, signal.SIGTERM)
                    except ProcessLookupError:
                        pass
                    try:
                        command.wait(timeout=10)
                    except subprocess.TimeoutExpired:
                        try:
                            os.killpg(command.pid, signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                        command.wait()
                for label, process in (("proxy", proxy), ("Server", server)):
                    if process is not None and process.poll() is None:
                        process.terminate()
                        try:
                            process.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            process.kill()
                            process.wait()
                            if result == 0:
                                result = 65
                            print(f"Fixture {label} did not stop gracefully", file=sys.stderr)
        if result:
            print(log_path.read_text(encoding="utf-8"), file=sys.stderr)
    return result


if __name__ == "__main__":
    sys.exit(main())
