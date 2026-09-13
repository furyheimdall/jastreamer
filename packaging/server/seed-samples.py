#!/usr/bin/env python3
"""Copy the bundled jastreamer sample music into an approved music directory."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import re
import shutil
import sys
from typing import Any

SOURCE = pathlib.Path(__file__).resolve().with_name("samples")
MANIFEST_NAME = "manifest.json"
NOTICE_NAME = "THIRD-PARTY-NOTICES.txt"
TRACK_FILES = ("sample-01.mp3", "sample-02.mp3", "sample-03.mp3")
PAYLOAD_FILES = (*TRACK_FILES, MANIFEST_NAME, NOTICE_NAME)
SHA256 = re.compile(r"[0-9a-f]{64}")
LICENSE = "CC0 1.0 Universal"
LICENSE_URL = "https://creativecommons.org/publicdomain/zero/1.0/"
SOURCE_URL_PREFIX = "https://freemusicarchive.org/music/"


def fail(message: str) -> None:
    raise SystemExit(f"sample seeding failed: {message}")


def regular_file(path: pathlib.Path, label: str) -> None:
    if path.is_symlink() or not path.is_file():
        fail(f"{label} is not a regular file: {path}")


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_manifest() -> dict[str, Any]:
    path = SOURCE / MANIFEST_NAME
    regular_file(path, "bundled manifest")
    try:
        value = json.loads(path.read_bytes())
    except (OSError, UnicodeDecodeError, json.JSONDecodeError) as error:
        fail(f"invalid bundled manifest: {error}")

    if not isinstance(value, dict) or set(value) != {"schema", "tracks"}:
        fail("bundled manifest has missing or unexpected fields")
    if type(value["schema"]) is not int or value["schema"] != 1:
        fail("unsupported bundled manifest schema")
    tracks = value["tracks"]
    if not isinstance(tracks, list) or len(tracks) != len(TRACK_FILES):
        fail("bundled manifest must contain exactly three tracks")

    fields = {
        "file",
        "title",
        "artist",
        "source_url",
        "license",
        "license_url",
        "sha256",
        "bytes",
    }
    for expected_file, track in zip(TRACK_FILES, tracks, strict=True):
        if not isinstance(track, dict) or set(track) != fields:
            fail(f"invalid manifest entry for {expected_file}")
        if track["file"] != expected_file:
            fail(f"manifest track order or filename is invalid: {track['file']!r}")
        for field in ("title", "artist", "source_url", "license", "license_url", "sha256"):
            if not isinstance(track[field], str) or not track[field]:
                fail(f"manifest {expected_file} has invalid {field}")
        if not track["source_url"].startswith(SOURCE_URL_PREFIX):
            fail(f"manifest {expected_file} has an unexpected source URL")
        if track["license"] != LICENSE or track["license_url"] != LICENSE_URL:
            fail(f"manifest {expected_file} has unexpected redistribution terms")
        if SHA256.fullmatch(track["sha256"]) is None:
            fail(f"manifest {expected_file} has an invalid SHA-256")
        if type(track["bytes"]) is not int or track["bytes"] <= 0:
            fail(f"manifest {expected_file} has an invalid byte length")

        source = SOURCE / expected_file
        regular_file(source, "bundled sample")
        if source.stat().st_size != track["bytes"] or sha256(source) != track["sha256"]:
            fail(f"bundled sample does not match manifest: {expected_file}")
        with source.open("rb") as stream:
            header = stream.read(3)
        if not (header == b"ID3" or (len(header) >= 2 and header[0] == 0xFF and header[1] & 0xE0 == 0xE0)):
            fail(f"bundled sample is not an MP3: {expected_file}")

    notice = SOURCE / NOTICE_NAME
    regular_file(notice, "bundled sample notice")
    if notice.stat().st_size == 0:
        fail("bundled sample notice is empty")
    return value


def ensure_destination(path: pathlib.Path) -> None:
    try:
        os.mkdir(path, 0o755)
    except FileExistsError:
        if path.is_symlink() or not path.is_dir():
            fail(f"destination is not a directory: {path}")
    except OSError as error:
        fail(f"cannot create destination {path}: {error}")
    else:
        try:
            os.chmod(path, 0o755, follow_symlinks=False)
        except OSError as error:
            fail(f"cannot set public-sample directory permissions on {path}: {error}")


def identical(left: pathlib.Path, right: pathlib.Path) -> bool:
    return left.stat().st_size == right.stat().st_size and sha256(left) == sha256(right)


def copy_new_file(source: pathlib.Path, destination: pathlib.Path) -> bool:
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0)
    try:
        descriptor = os.open(destination, flags, 0o644)
    except FileExistsError:
        regular_file(destination, "destination entry")
        if identical(source, destination):
            return False
        fail(f"destination file appeared with different contents: {destination}")
    except OSError as error:
        fail(f"cannot create {destination}: {error}")

    # A failed write may leave a new partial file for operator inspection.
    # Never unlink by path: another writer may have replaced our open file.
    with os.fdopen(descriptor, "wb") as output, source.open("rb") as input_stream:
        os.fchmod(output.fileno(), 0o644)
        shutil.copyfileobj(input_stream, output)
        output.flush()
        os.fsync(output.fileno())
    return True


def seed(destination: pathlib.Path) -> tuple[int, int]:
    read_manifest()
    if not destination.is_absolute():
        fail("DESTINATION must be an absolute path")
    destination = destination.parent.resolve(strict=True) / destination.name
    ensure_destination(destination)

    conflicts: list[pathlib.Path] = []
    retained: set[str] = set()
    for name in PAYLOAD_FILES:
        source = SOURCE / name
        regular_file(source, "bundled payload")
        target = destination / name
        if target.exists() or target.is_symlink():
            regular_file(target, "destination entry")
            if identical(source, target):
                retained.add(name)
            else:
                conflicts.append(target)
    if conflicts:
        fail("refusing to overwrite different existing file(s): " + ", ".join(str(path) for path in conflicts))

    copied = 0
    for name in PAYLOAD_FILES:
        source = SOURCE / name
        target = destination / name
        if target.exists() or target.is_symlink():
            regular_file(target, "destination entry")
            if not identical(source, target):
                fail(f"destination file changed during seeding: {target}")
            retained.add(name)
            continue
        if copy_new_file(source, target):
            copied += 1
        else:
            retained.add(name)
    return copied, len(retained)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=pathlib.Path, metavar="DESTINATION")
    args = parser.parse_args()
    copied, retained = seed(args.destination)
    print(f"Seeded {copied} file(s); retained {retained} identical file(s) in {args.destination.resolve(strict=False)}")


if __name__ == "__main__":
    try:
        main()
    except BrokenPipeError:
        sys.exit(1)
