#!/usr/bin/env python3
"""Smoke-test the packaged Server on native Windows and issue its verification receipt."""

from __future__ import annotations

import argparse
import http.cookiejar
import json
import os
import pathlib
import platform
import secrets
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
import zipfile
from typing import Any

import windows_package

HTTP_ORIGIN = "http://127.0.0.1:18080"
START_TIMEOUT_SECONDS = 60
STOP_TIMEOUT_SECONDS = 20


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


def request(
    path: str,
    *,
    opener: urllib.request.OpenerDirector | None = None,
    method: str = "GET",
    value: Any = None,
    timeout: float = 3,
) -> tuple[int, bytes, str]:
    body = None if value is None else json.dumps(value).encode("utf-8")
    headers = {}
    if body is not None:
        headers = {"Content-Type": "application/json", "X-Jastreamer-Request": "web"}
    operation = urllib.request.Request(f"{HTTP_ORIGIN}{path}", data=body, headers=headers, method=method)
    client = opener or urllib.request.build_opener()
    try:
        with client.open(operation, timeout=timeout) as response:
            return response.status, response.read(), response.headers.get("Content-Type", "")
    except urllib.error.HTTPError as error:
        return error.code, error.read(), error.headers.get("Content-Type", "")


def wait_ready(process: subprocess.Popen[bytes], log_path: pathlib.Path) -> None:
    deadline = time.monotonic() + START_TIMEOUT_SECONDS
    last_error = "no response"
    while time.monotonic() < deadline:
        code = process.poll()
        if code is not None:
            log = log_path.read_text(encoding="utf-8", errors="replace")
            raise SystemExit(f"packaged launcher exited before HTTP readiness (code {code}):\n{log}")
        try:
            status, body, _ = request("/healthz", timeout=1)
            if status == 200 and json.loads(body) == {"status": "ready"}:
                return
            last_error = f"status {status}: {body[:200]!r}"
        except (OSError, ValueError, urllib.error.URLError) as error:
            last_error = str(error)
        time.sleep(0.25)
    log = log_path.read_text(encoding="utf-8", errors="replace")
    raise SystemExit(f"packaged Server did not become ready: {last_error}\n{log}")


def launch(package_root: pathlib.Path, log_path: pathlib.Path) -> tuple[subprocess.Popen[bytes], Any]:
    log = log_path.open("ab", buffering=0)
    environment = os.environ.copy()
    environment["JASTREAMER_NO_PAUSE"] = "1"
    process = subprocess.Popen(
        [os.environ.get("COMSPEC", "cmd.exe"), "/d", "/c", "start-server.cmd"],
        cwd=package_root,
        env=environment,
        stdin=subprocess.DEVNULL,
        stdout=log,
        stderr=subprocess.STDOUT,
        creationflags=subprocess.CREATE_NEW_PROCESS_GROUP,
    )
    return process, log


def stop(process: subprocess.Popen[bytes], log: Any, *, must_stop: bool) -> None:
    failure: str | None = None
    if process.poll() is None:
        try:
            process.send_signal(signal.CTRL_BREAK_EVENT)
            process.wait(timeout=STOP_TIMEOUT_SECONDS)
        except (OSError, subprocess.TimeoutExpired) as error:
            failure = f"packaged Server did not stop after Ctrl+Break: {error}"
            subprocess.run(
                ["taskkill", "/PID", str(process.pid), "/T", "/F"],
                check=False,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )
            process.wait(timeout=10)
    log.close()
    if must_stop and failure is not None:
        raise SystemExit(failure)


def parse_json(body: bytes, label: str) -> Any:
    try:
        return json.loads(body)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise SystemExit(f"{label} did not return JSON: {error}") from error


def assert_port_unused() -> None:
    try:
        status, _, _ = request("/healthz", timeout=0.5)
    except (OSError, urllib.error.URLError):
        return
    raise SystemExit(f"TCP port 18080 is already serving HTTP status {status}; native smoke requires this package's default port")


def smoke(package_dir: pathlib.Path, source_revision: str) -> dict[str, Any]:
    require(sys.platform == "win32", "Windows Server native verifier must run on Windows")
    require(platform.machine().lower() in {"amd64", "x86_64"}, "Windows Server native verifier must run on x64 Windows")
    manifest = windows_package.verify_windows_package(package_dir, source_revision)
    archive_path = package_dir / windows_package.ARCHIVE_NAME
    assert_port_unused()

    temporary = pathlib.Path(tempfile.mkdtemp(prefix="jastreamer smoke 공간 é "))
    try:
        with zipfile.ZipFile(archive_path, "r") as archive:
            archive.extractall(temporary)
        package_root = temporary / windows_package.PACKAGE_ROOT
        executable = package_root / windows_package.BINARY_NAME
        launcher = package_root / "start-server.cmd"
        require(executable.is_file() and launcher.is_file(), "extracted package is missing its executable or launcher")

        version = subprocess.run(
            [executable, "--version"],
            cwd=package_root,
            check=True,
            capture_output=True,
            timeout=20,
            text=True,
            encoding="utf-8",
        )
        expected_version = f"jastreamer-server {windows_package.VERSION} ({source_revision})"
        require(version.stdout.strip() == expected_version and version.stderr == "", f"unexpected packaged Server version: {version.stdout!r} {version.stderr!r}")

        log_path = temporary / "server.log"
        process, log = launch(package_root, log_path)
        try:
            wait_ready(process, log_path)
            config_path = package_root / "server.json"
            data_path = package_root / "data"
            music_path = package_root / "music"
            require(config_path.is_file() and data_path.is_dir() and music_path.is_dir(), "first launch did not create adjacent config/data/music")
            config = parse_json(config_path.read_bytes(), "first-launch config")
            require(pathlib.Path(config.get("data_dir", "")) == data_path, "first-launch data path is not executable-adjacent")
            roots = config.get("library_roots")
            require(isinstance(roots, list) and len(roots) == 1 and pathlib.Path(roots[0].get("path", "")) == music_path, "first-launch music path is not executable-adjacent")
            require(config.get("media") == {"base_url": "", "ffmpeg_path": "", "transcode": False}, "first-launch config unexpectedly enables or selects FFmpeg")
            require(config.get("airplay") == {"enabled": False, "helper_path": ""}, "first-launch config unexpectedly enables or selects AirPlay")

            status, ui, content_type = request("/")
            require(status == 200 and "text/html" in content_type.lower(), "packaged embedded Web UI did not return HTML")
            lowered_ui = ui.lower()
            require(b"<!doctype html" in lowered_ui and b"<script" in lowered_ui, "packaged embedded Web UI is missing its application document")
            status, setup_body, _ = request("/api/v1/setup")
            require(status == 200 and parse_json(setup_body, "initial setup state") == {"required": True}, "fresh package did not require initial administrator setup")

            cookies = http.cookiejar.CookieJar()
            opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cookies))
            username = "native-smoke-admin"
            password = f"Native-Smoke-{secrets.token_urlsafe(24)}"
            status, _, _ = request("/api/v1/setup", opener=opener, method="POST", value={"username": username, "password": password})
            require(status == 201 and any(cookie.name == "jastreamer_session" for cookie in cookies), "administrator setup did not persist a session")
        finally:
            stop(process, log, must_stop=sys.exc_info()[0] is None)

        config = parse_json(config_path.read_bytes(), "preserved config")
        custom_ffmpeg = package_root / "tools with spaces" / "사용자 ffmpeg.exe"
        custom_helper = package_root / "tools with spaces" / "사용자 airplay.py"
        config["server_name"] = "Native smoke preserved 이름"
        config["media"]["ffmpeg_path"] = str(custom_ffmpeg)
        config["airplay"]["helper_path"] = str(custom_helper)
        preserved = (json.dumps(config, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        config_path.write_bytes(preserved)
        database_path = data_path / "server.sqlite"
        require(database_path.is_file() and database_path.stat().st_size > 0, "first run did not create persistent Server state")

        process, log = launch(package_root, log_path)
        try:
            wait_ready(process, log_path)
            require(config_path.read_bytes() == preserved, "launcher or Server overwrote the existing configuration")
            status, setup_body, _ = request("/api/v1/setup")
            require(status == 200 and parse_json(setup_body, "restart setup state") == {"required": False}, "restart lost administrator setup state")
            restart_cookies = http.cookiejar.CookieJar()
            restart_opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(restart_cookies))
            status, _, _ = request("/api/v1/login", opener=restart_opener, method="POST", value={"username": username, "password": password})
            require(status == 200 and any(cookie.name == "jastreamer_session" for cookie in restart_cookies), "restart did not preserve administrator login state")
            status, session_body, _ = request("/api/v1/session", opener=restart_opener)
            session = parse_json(session_body, "restart session")
            require(status == 200 and session.get("authenticated") is True and session.get("user", {}).get("username") == username, "restart session is not authenticated as the persisted administrator")
        finally:
            stop(process, log, must_stop=sys.exc_info()[0] is None)
    finally:
        shutil.rmtree(temporary, ignore_errors=True)

    return {
        "schema": 1,
        "product": windows_package.PRODUCT,
        "version": windows_package.VERSION,
        "platform": windows_package.PLATFORM,
        "arch": windows_package.ARCH,
        "sourceRevision": source_revision,
        "archiveSha256": manifest["archive"]["sha256"],
        "checks": windows_package.RECEIPT_CHECKS,
        "productionQualified": False,
    }


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--package-dir", required=True, type=pathlib.Path)
    parser.add_argument("--source-revision", required=True)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    package_dir = args.package_dir.resolve()
    output = args.output.resolve()
    require(output.parent == package_dir and output.name == "verification.json", "verification receipt must be PACKAGE_DIR/verification.json")
    receipt = smoke(package_dir, args.source_revision)
    windows_package.atomic_write(output, windows_package.canonical_json_bytes(receipt))
    windows_package.verify_windows_package(package_dir, args.source_revision)
    print(json.dumps(receipt, sort_keys=True))


if __name__ == "__main__":
    main()
