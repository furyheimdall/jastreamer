#!/usr/bin/env python3
"""Verify the native jastreamer Server image contents from inside the image."""

from __future__ import annotations

import hashlib
import importlib.metadata
import json
import pathlib
import platform
import re
import runpy
import shlex
import stat
import subprocess
import sys
import tempfile
import urllib.parse

FFMPEG_VERSION = "8.1.2"
FFMPEG_SOURCE_SHA256 = "464beb5e7bf0c311e68b45ae2f04e9cc2af88851abb4082231742a74d97b524c"
ARCHITECTURES = {"amd64": "x86_64", "arm64": "aarch64"}
ROOT = pathlib.Path("/")
REQUIREMENTS = ROOT / "usr/share/jastreamer/requirements-airplay.txt"
PYTHON_SOURCES = ROOT / "usr/share/jastreamer/sources/python"
SAMPLES = ROOT / "usr/share/jastreamer/samples"
SAMPLE_FILES = ("sample-01.mp3", "sample-02.mp3", "sample-03.mp3")
SAMPLE_FIELDS = {
    "file",
    "title",
    "artist",
    "source_url",
    "license",
    "license_url",
    "sha256",
    "bytes",
}
SHA256 = re.compile(r"[0-9a-f]{64}")


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
        ROOT / "usr/share/jastreamer/seed-samples.py",
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

    encoders = subprocess.run(
        ["/usr/local/bin/ffmpeg", "-hide_banner", "-encoders"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    muxers = subprocess.run(
        ["/usr/local/bin/ffmpeg", "-hide_banner", "-muxers"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    filters = subprocess.run(
        ["/usr/local/bin/ffmpeg", "-hide_banner", "-filters"],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    require(re.search(r"(?m)^\s*A\S*\s+aac\s", encoders) is not None, "native AAC encoder is missing")
    require(re.search(r"(?m)^\s*E\S*\s+ipod\s", muxers) is not None, "M4A/iPod muxer is missing")
    require(re.search(r"(?m)^\s*\S+\s+aformat\s", filters) is not None, "audio format/channel-layout filter is missing")


def verify_samples() -> None:
    expected_entries = {*SAMPLE_FILES, "manifest.json", "THIRD-PARTY-NOTICES.txt"}
    actual_entries = {entry.name for entry in SAMPLES.iterdir()}
    require(actual_entries == expected_entries, "sample bundle contains missing or unexpected files")
    for name in expected_entries:
        info = (SAMPLES / name).lstat()
        require(stat.S_ISREG(info.st_mode), f"sample bundle entry is not a regular file: {name}")

    manifest_path = SAMPLES / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    require(isinstance(manifest, dict) and set(manifest) == {"schema", "tracks"}, "invalid sample manifest")
    require(type(manifest["schema"]) is int and manifest["schema"] == 1, "unsupported sample manifest schema")
    tracks = manifest["tracks"]
    require(isinstance(tracks, list) and len(tracks) == len(SAMPLE_FILES), "sample manifest must contain exactly three tracks")

    for expected_file, track in zip(SAMPLE_FILES, tracks, strict=True):
        require(isinstance(track, dict) and set(track) == SAMPLE_FIELDS, f"invalid sample manifest entry: {expected_file}")
        require(track["file"] == expected_file, f"unexpected sample filename or order: {track.get('file')!r}")
        for field in ("title", "artist", "source_url", "license", "license_url", "sha256"):
            require(isinstance(track[field], str) and bool(track[field]), f"{expected_file}: invalid {field}")
        source_url = urllib.parse.urlsplit(track["source_url"])
        license_url = urllib.parse.urlsplit(track["license_url"])
        require(source_url.scheme == "https" and source_url.hostname == "freemusicarchive.org", f"{expected_file}: untrusted source URL")
        require(track["license"] == "CC0 1.0 Universal", f"{expected_file}: unexpected sample license")
        require(
            license_url.scheme == "https"
            and license_url.hostname == "creativecommons.org"
            and license_url.path == "/publicdomain/zero/1.0/",
            f"{expected_file}: unexpected sample license URL",
        )
        require(isinstance(track["sha256"], str) and SHA256.fullmatch(track["sha256"]) is not None, f"{expected_file}: invalid SHA-256")
        require(type(track["bytes"]) is int and track["bytes"] > 0, f"{expected_file}: invalid byte length")

        sample = SAMPLES / expected_file
        require(sample.stat().st_size == track["bytes"], f"{expected_file}: byte length mismatch")
        require(sha256(sample) == track["sha256"], f"{expected_file}: checksum mismatch")
        with sample.open("rb") as stream:
            header = stream.read(3)
        require(
            header == b"ID3" or (len(header) >= 2 and header[0] == 0xFF and header[1] & 0xE0 == 0xE0),
            f"{expected_file}: not an MP3",
        )

    require((SAMPLES / "THIRD-PARTY-NOTICES.txt").stat().st_size > 0, "sample source and license notice is empty")


def verify_sample_seeding() -> None:
    helper = ROOT / "usr/share/jastreamer/seed-samples.py"
    with tempfile.TemporaryDirectory(prefix="jastreamer-sample-verification-") as temporary:
        parent = pathlib.Path(temporary)
        outside = parent / "outside"
        outside.mkdir()
        destination = parent / "jastreamer-samples"
        destination.symlink_to(outside, target_is_directory=True)
        result = subprocess.run(
            [sys.executable, helper, destination], capture_output=True, text=True, timeout=20,
        )
        require(result.returncode != 0 and not any(outside.iterdir()), "sample seeder followed a destination symlink")
        destination.unlink()

        subprocess.run(
            [sys.executable, helper, destination],
            check=True, capture_output=True, text=True, timeout=20, umask=0o077,
        )
        require(stat.S_IMODE(destination.stat().st_mode) == 0o755, "new sample directory is not traversable")
        for source in SAMPLES.iterdir():
            target = destination / source.name
            require(target.read_bytes() == source.read_bytes(), f"seeded sample payload differs: {source.name}")
            require(stat.S_IMODE(target.stat().st_mode) == 0o644, f"seeded sample is not readable: {source.name}")

        destination.chmod(0o750)
        retained_track = destination / SAMPLE_FILES[0]
        retained_track.chmod(0o600)
        subprocess.run(
            [sys.executable, helper, destination], check=True, capture_output=True, text=True, timeout=20,
        )
        require(stat.S_IMODE(destination.stat().st_mode) == 0o750, "sample seeder changed existing directory permissions")
        require(stat.S_IMODE(retained_track.stat().st_mode) == 0o600, "sample seeder changed existing music permissions")

        seeded = {path.name: path.read_bytes() for path in destination.iterdir()}
        conflict = destination / SAMPLE_FILES[0]
        conflict.write_bytes(b"existing user music")
        result = subprocess.run(
            [sys.executable, helper, destination], capture_output=True, text=True, timeout=20,
        )
        seeded[SAMPLE_FILES[0]] = b"existing user music"
        require(result.returncode != 0, "sample seeder accepted conflicting user music")
        require(
            {path.name: path.read_bytes() for path in destination.iterdir()} == seeded,
            "sample seeder changed files after detecting a conflict",
        )

        seeder = runpy.run_path(str(helper))
        replacement = parent / "concurrent-user.mp3"
        original_copy = seeder["shutil"].copyfileobj

        def replace_during_failed_write(input_stream, output_stream):
            replacement.unlink()
            replacement.write_bytes(b"concurrent user replacement")
            raise OSError("simulated write failure")

        failed = False
        try:
            seeder["shutil"].copyfileobj = replace_during_failed_write
            try:
                seeder["copy_new_file"](SAMPLES / SAMPLE_FILES[0], replacement)
            except OSError:
                failed = True
        finally:
            seeder["shutil"].copyfileobj = original_copy
        require(failed, "sample copy did not report a write failure")
        require(
            replacement.is_file() and replacement.read_bytes() == b"concurrent user replacement",
            "sample copy cleanup removed a concurrent user replacement",
        )


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
    verify_samples()
    verify_sample_seeding()
    lock = locked_requirements()
    verify_python(lock)
    verify_ffmpeg()
    print(f"Verified native linux/{sys.argv[1]} Server image, sample music, locked Python sources, and FFmpeg corresponding source.")
