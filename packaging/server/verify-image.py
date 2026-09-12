#!/usr/bin/env python3
"""Verify the native jastreamer Server image contents from inside the image."""

from __future__ import annotations

import hashlib
import importlib.metadata
import json
import pathlib
import platform
import shlex
import stat
import subprocess
import sys
import urllib.parse

FFMPEG_VERSION = "8.1.2"
FFMPEG_SOURCE_SHA256 = "464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c"
ARCHITECTURES = {"amd64": "x86_64", "arm64": "aarch64"}
ROOT = pathlib.Path("/")
REQUIREMENTS = ROOT / "usr/share/jastreamer/requirements-airplay.txt"
PYTHON_SOURCES = ROOT / "usr/share/jastreamer/sources/python"


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(128 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def locked_requirements() -> dict[str, tuple[str, set[str]]]:
    locked: dict[str, tuple[str, set[str]]] = {}
    for line in REQUIREMENTS.read_text().replace("\\\n", " ").splitlines():
        fields = shlex.split(line, comments=True)
        if not fields:
            continue
        name, version = fields[0].split("==", 1)
        hashes = {
            field.removeprefix("--hash=sha256:")
            for field in fields[1:]
            if field.startswith("--hash=sha256:")
        }
        require(name not in locked, f"duplicate locked Python package: {name}")
        require(bool(hashes), f"locked Python package has no hashes: {name}")
        locked[name] = (version, hashes)
    return locked


def verify_executables() -> None:
    for path in (
        ROOT / "usr/local/bin/jastreamer-server",
        ROOT / "usr/local/bin/jastreamer-airplay",
        ROOT / "usr/local/bin/ffmpeg",
    ):
        info = path.lstat()
        require(stat.S_ISREG(info.st_mode), f"not a regular executable: {path}")
        require(bool(info.st_mode & 0o111), f"not executable: {path}")


def verify_python(locked: dict[str, tuple[str, set[str]]]) -> None:
    for name, (expected, _) in locked.items():
        actual = importlib.metadata.version(name)
        require(actual == expected, f"{name}: installed {actual}, expected {expected}")
    require(locked.get("pyatv", (None,))[0] == "0.18.0", "pyatv 0.18.0 is not locked")

    entries = json.loads((PYTHON_SOURCES / "manifest.json").read_text())
    require(isinstance(entries, list), "Python source manifest must be an array")
    sources: dict[str, dict[str, str]] = {}
    for entry in entries:
        require(isinstance(entry, dict), "invalid Python source manifest entry")
        name = entry.get("name")
        require(isinstance(name, str) and name not in sources, "duplicate or invalid Python source entry")
        sources[name] = entry
    require(set(sources) == set(locked), "Python source manifest does not match the runtime lock")

    for name, (version, accepted_hashes) in locked.items():
        entry = sources[name]
        filename = entry.get("file")
        expected_hash = entry.get("sha256")
        url = urllib.parse.urlsplit(str(entry.get("url", "")))
        require(entry.get("version") == version, f"{name}: source version does not match lock")
        require(isinstance(filename, str) and pathlib.Path(filename).name == filename, f"{name}: invalid source filename")
        require(expected_hash in accepted_hashes, f"{name}: source hash is not accepted by lock")
        require(url.scheme == "https" and url.hostname == "files.pythonhosted.org", f"{name}: untrusted source URL")
        source = PYTHON_SOURCES / filename
        require(source.is_file(), f"{name}: source archive is missing")
        require(sha256(source) == expected_hash, f"{name}: source archive checksum mismatch")


def verify_ffmpeg() -> None:
    source = ROOT / f"usr/share/jastreamer/sources/ffmpeg-{FFMPEG_VERSION}.tar.xz"
    require(source.is_file(), "FFmpeg corresponding source is missing")
    require(sha256(source) == FFMPEG_SOURCE_SHA256, "FFmpeg source checksum mismatch")
    require((ROOT / "usr/share/jastreamer/sources/build-ffmpeg.sh").is_file(), "FFmpeg build script is missing")
    require((ROOT / "usr/share/licenses/ffmpeg/COPYING.LGPLv2.1").is_file(), "FFmpeg LGPL license is missing")

    result = subprocess.run(
        ["/usr/local/bin/ffmpeg", "-version"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    require(result.startswith(f"ffmpeg version {FFMPEG_VERSION}"), "unexpected FFmpeg version")
    require("configuration:" in result, "FFmpeg configure flags are missing")
    for option in ("--disable-everything", "--disable-network", "--enable-ffmpeg", "--disable-shared"):
        require(option in result, f"FFmpeg was not built with {option}")
    for option in ("--enable-gpl", "--enable-version3", "--enable-nonfree"):
        require(option not in result, f"FFmpeg unexpectedly enables {option}")


def verify_metadata(expected_arch: str) -> None:
    require(sys.platform == "linux", "Server image verifier must run on Linux")
    require(expected_arch in ARCHITECTURES, f"unsupported expected architecture: {expected_arch}")
    require(platform.machine() == ARCHITECTURES[expected_arch], "image is not running on the expected native architecture")

    config = json.loads((ROOT / "etc/jastreamer/server.json").read_text())
    require(config.get("version") == 1, "unexpected Server config schema")
    require(config.get("data_dir") == "/var/lib/jastreamer", "unexpected Server data directory")
    require(config.get("media", {}).get("ffmpeg_path") == "/usr/local/bin/ffmpeg", "Server config does not select packaged FFmpeg")
    require(config.get("airplay", {}).get("enabled") is True, "Server config does not enable AirPlay")
    require(config.get("airplay", {}).get("helper_path") == "/usr/local/bin/jastreamer-airplay", "Server config does not select packaged AirPlay helper")

    for path in (
        ROOT / "usr/local/lib/jastreamer/airplay-helper.py",
        ROOT / "usr/share/jastreamer/THIRD-PARTY-NOTICES.txt",
        ROOT / "usr/share/jastreamer/sources/fetch-python-sources.py",
        ROOT / "usr/share/jastreamer/sources/requirements-airplay-build.txt",
        ROOT / "usr/share/licenses/jastreamer/LICENSE",
    ):
        require(path.is_file(), f"required packaged file is missing: {path}")

    license_root = ROOT / "usr/share/licenses/jastreamer-third-party"
    expected_licenses = {
        "go/dustin-go-humanize.txt",
        "go/enbility-zeroconf.txt",
        "go/go-runtime.txt",
        "go/golang-x.txt",
        "go/google-uuid.txt",
        "go/mattn-go-isatty.txt",
        "go/miekg-dns.txt",
        "go/modernc-libc-third-party.txt",
        "go/modernc-libc.txt",
        "go/modernc-mathutil.txt",
        "go/modernc-memory-go.txt",
        "go/modernc-memory-mmap-go.txt",
        "go/modernc-memory.txt",
        "go/modernc-sqlite.txt",
        "go/ncruces-go-strftime.txt",
        "go/remyoudompheng-bigfft.txt",
        "go/sqlite-public-domain.txt",
        "go/sqlite-vec.txt",
        "web/react-react-dom-scheduler.txt",
    }
    packaged_licenses = {
        str(path.relative_to(license_root))
        for path in license_root.rglob("*")
        if path.is_file()
    }
    require(packaged_licenses == expected_licenses, "Go/Web third-party license bundle is incomplete")


if __name__ == "__main__":
    require(len(sys.argv) == 2, "usage: verify-image.py amd64|arm64")
    verify_metadata(sys.argv[1])
    verify_executables()
    lock = locked_requirements()
    verify_python(lock)
    verify_ffmpeg()
    print(f"Verified native linux/{sys.argv[1]} Server image, locked Python sources, and FFmpeg corresponding source.")
