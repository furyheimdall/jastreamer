#!/usr/bin/env python3
import argparse
import errno
import fcntl
import hashlib
import json
import os
import shutil
import struct
import sys
import time

FICLONE = 0x40049409
DEFAULT_COUNT = 100_000
SAMPLE_RATE = 8_000
SAMPLES = 800


def _chunk(kind: bytes, payload: bytes) -> bytes:
    padded = payload + (b"\0" if len(payload) % 2 else b"")
    return kind + struct.pack("<I", len(payload)) + padded


def deterministic_wav() -> bytes:
    pcm = bytearray()
    for index in range(SAMPLES):
        value = ((index * 257) % 65536) - 32768
        pcm.extend(struct.pack("<h", value))
    info = b"INFO" + _chunk(b"INAM", b"Task19 deterministic audio\0") + _chunk(b"IART", b"jastreamer Task19\0") + _chunk(b"IPRD", b"Task19 qualification\0")
    fmt = struct.pack("<HHIIHH", 1, 1, SAMPLE_RATE, SAMPLE_RATE * 2, 2, 16)
    body = b"WAVE" + _chunk(b"fmt ", fmt) + _chunk(b"LIST", info) + _chunk(b"data", bytes(pcm))
    return b"RIFF" + struct.pack("<I", len(body)) + body


def validate_wav(data: bytes) -> None:
    if len(data) < 44 or data[:4] != b"RIFF" or data[8:12] != b"WAVE" or struct.unpack("<I", data[4:8])[0] != len(data) - 8:
        raise ValueError("TASK19_MEDIA_SEED_INVALID")
    offset, pcm, metadata = 12, 0, False
    while offset + 8 <= len(data):
        kind, size = data[offset:offset + 4], struct.unpack("<I", data[offset + 4:offset + 8])[0]
        start, end = offset + 8, offset + 8 + size
        if end > len(data):
            raise ValueError("TASK19_MEDIA_SEED_INVALID")
        if kind == b"fmt " and (size < 16 or struct.unpack("<H", data[start:start + 2])[0] != 1):
            raise ValueError("TASK19_MEDIA_SEED_INVALID")
        if kind == b"LIST" and data[start:start + 4] == b"INFO" and b"Task19 deterministic audio" in data[start:end]:
            metadata = True
        if kind == b"data":
            pcm = size
        offset = end + size % 2
    if pcm == 0 or not metadata:
        raise ValueError("TASK19_MEDIA_SEED_INVALID")


def path_for(index: int) -> str:
    return f"task19-{index:06d}.wav"


def _clone(source: str, destination: str, strategy: str) -> str:
    force_failure = os.environ.get("TASK19_FORCE_LINK_FAILURE") == "1"
    if strategy in ("auto", "hardlink"):
        try:
            if force_failure:
                raise OSError(errno.EXDEV, "forced hardlink failure")
            os.link(source, destination)
            return "hardlink"
        except OSError:
            if strategy == "hardlink":
                raise
    if strategy in ("auto", "reflink"):
        try:
            with open(source, "rb") as source_file, open(destination, "xb") as destination_file:
                fcntl.ioctl(destination_file.fileno(), FICLONE, source_file.fileno())
            return "reflink"
        except OSError:
            try:
                os.unlink(destination)
            except FileNotFoundError:
                pass
            if strategy == "reflink":
                raise
    shutil.copyfile(source, destination)
    return "copy"


def verify(root: str, count: int) -> dict:
    names = sorted(os.listdir(root))
    expected = [path_for(index) for index in range(count)]
    if names != expected:
        raise ValueError("TASK19_MEDIA_PATH_SET_INVALID")
    seed = os.path.join(root, names[0])
    with open(seed, "rb") as source:
        data = source.read()
    validate_wav(data)
    digest = hashlib.sha256(data).hexdigest()
    unique_storage = {}
    logical_bytes = 0
    for name in names:
        stat = os.stat(os.path.join(root, name), follow_symlinks=False)
        if not os.path.isfile(os.path.join(root, name)) or stat.st_size != len(data):
            raise ValueError("TASK19_MEDIA_PATH_INVALID")
        logical_bytes += stat.st_size
        unique_storage[(stat.st_dev, stat.st_ino)] = stat.st_blocks * 512
    return {
        "count": count,
        "seed_sha256": digest,
        "seed_bytes": len(data),
        "logical_bytes": logical_bytes,
        "physical_bytes": sum(unique_storage.values()),
        "first_path": names[0],
        "middle_path": names[count // 2],
        "last_path": names[-1],
        "unique_inodes": len(unique_storage),
    }


def create(root: str, count: int, strategy: str) -> dict:
    if count < 1 or os.path.exists(root):
        raise ValueError("TASK19_MEDIA_ROOT_NOT_ISOLATED")
    started = time.monotonic()
    os.makedirs(root, mode=0o700)
    try:
        seed_data = deterministic_wav()
        validate_wav(seed_data)
        seed = os.path.join(root, path_for(0))
        with open(seed, "xb") as target:
            target.write(seed_data)
        used = set()
        for index in range(1, count):
            used.add(_clone(seed, os.path.join(root, path_for(index)), strategy))
        result = verify(root, count)
        result["strategies"] = sorted(used or {"seed"})
        result["generation_ms"] = round((time.monotonic() - started) * 1000, 3)
        return result
    except BaseException:
        shutil.rmtree(root, ignore_errors=True)
        raise


def cleanup(root: str) -> dict:
    shutil.rmtree(root, ignore_errors=True)
    if os.path.exists(root):
        raise ValueError("TASK19_MEDIA_CLEANUP_FAILED")
    return {"removed": True}


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("operation", choices=("create", "verify", "validate", "cleanup"))
    parser.add_argument("--root")
    parser.add_argument("--count", type=int, default=DEFAULT_COUNT)
    parser.add_argument("--strategy", choices=("auto", "hardlink", "reflink", "copy"), default="auto")
    parser.add_argument("--path")
    args = parser.parse_args()
    try:
        if args.operation == "create":
            result = create(os.path.abspath(args.root), args.count, args.strategy)
        elif args.operation == "verify":
            result = verify(os.path.abspath(args.root), args.count)
        elif args.operation == "validate":
            with open(args.path, "rb") as source:
                validate_wav(source.read())
            result = {"valid": True}
        else:
            result = cleanup(os.path.abspath(args.root))
        print(json.dumps(result, sort_keys=True, separators=(",", ":")))
        return 0
    except (OSError, ValueError) as error:
        print(str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
