#!/usr/bin/env python3
"""Build and verify the unsigned Windows x64 portable Server archive."""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import pathlib
import re
import stat
import struct
import tempfile
import zipfile
from typing import Any

VERSION = "0.2.0"
PRODUCT = "jastreamer-server"
PLATFORM = "win32"
ARCH = "x64"
ARCHIVE_NAME = f"{PRODUCT}_{VERSION}_windows-x64.zip"
CHECKSUM_NAME = f"{ARCHIVE_NAME}.sha256"
PACKAGE_ROOT = "jastreamer-server-windows-x64"
BINARY_NAME = "jastreamer-server.exe"
REVISION = re.compile(r"[0-9a-f]{40}")
SHA256 = re.compile(r"[0-9a-f]{64}")
ASSET_ROOT = pathlib.Path(__file__).with_name("windows")
ASSET_FILES = (
    "LICENSE",
    "START-HERE.txt",
    "THIRD-PARTY-NOTICES.txt",
    "UPDATE.txt",
    "server.template.json",
    "start-server.cmd",
    "third-party-licenses/go/dustin-go-humanize.txt",
    "third-party-licenses/go/enbility-zeroconf.txt",
    "third-party-licenses/go/go-runtime.txt",
    "third-party-licenses/go/golang-x.txt",
    "third-party-licenses/go/google-uuid.txt",
    "third-party-licenses/go/mattn-go-isatty.txt",
    "third-party-licenses/go/miekg-dns.txt",
    "third-party-licenses/go/modernc-libc-third-party.txt",
    "third-party-licenses/go/modernc-libc.txt",
    "third-party-licenses/go/modernc-mathutil.txt",
    "third-party-licenses/go/modernc-memory-go.txt",
    "third-party-licenses/go/modernc-memory-mmap-go.txt",
    "third-party-licenses/go/modernc-memory.txt",
    "third-party-licenses/go/modernc-sqlite.txt",
    "third-party-licenses/go/ncruces-go-strftime.txt",
    "third-party-licenses/go/remyoudompheng-bigfft.txt",
    "third-party-licenses/go/sqlite-public-domain.txt",
    "third-party-licenses/go/sqlite-vec.txt",
    "third-party-licenses/web/react-react-dom-scheduler.txt",
)
RECEIPT_CHECKS = {
    "nativeExecution": True,
    "versionCheck": True,
    "freshInstall": True,
    "existingConfigPreserved": True,
    "httpUI": True,
    "restartPreservesState": True,
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def sha256_file(path: pathlib.Path) -> str:
    require(path.is_file() and not path.is_symlink(), f"missing regular file: {path}")
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def canonical_json_bytes(value: Any) -> bytes:
    return (json.dumps(value, indent=2, sort_keys=True) + "\n").encode("utf-8")


def read_json(path: pathlib.Path) -> Any:
    require(path.is_file() and not path.is_symlink(), f"missing regular file: {path}")
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise SystemExit(f"invalid JSON in {path}: {error}") from error


def atomic_write(path: pathlib.Path, value: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as target:
            target.write(value)
            target.flush()
            os.fsync(target.fileno())
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def inspect_pe(value: bytes) -> dict[str, Any]:
    require(len(value) >= 0x100 and value[:2] == b"MZ", "Server binary is not a DOS/PE executable")
    pe_offset = struct.unpack_from("<I", value, 0x3C)[0]
    require(0x40 <= pe_offset <= len(value) - 24, "Server binary has an invalid PE header offset")
    require(value[pe_offset : pe_offset + 4] == b"PE\0\0", "Server binary has no PE signature")
    machine, sections, _, _, _, optional_size, characteristics = struct.unpack_from("<HHIIIHH", value, pe_offset + 4)
    require(machine == 0x8664, "Server binary is not Windows AMD64")
    require(1 <= sections <= 96, "Server binary has an invalid PE section count")
    require(optional_size >= 160 and pe_offset + 24 + optional_size <= len(value), "Server binary has a truncated PE32+ optional header")
    optional = pe_offset + 24
    require(struct.unpack_from("<H", value, optional)[0] == 0x20B, "Server binary is not PE32+")
    require(characteristics & 0x0002 != 0 and characteristics & 0x2000 == 0, "Server binary is not an executable image")
    entry_point = struct.unpack_from("<I", value, optional + 16)[0]
    image_size = struct.unpack_from("<I", value, optional + 56)[0]
    subsystem = struct.unpack_from("<H", value, optional + 68)[0]
    require(entry_point != 0 and image_size != 0, "Server binary has an invalid PE image layout")
    require(subsystem == 3, "Server binary is not a Windows console executable")
    certificate_offset, certificate_size = struct.unpack_from("<II", value, optional + 112 + 8 * 4)
    require(certificate_offset == 0 and certificate_size == 0, "Server binary unexpectedly contains an Authenticode certificate table")
    return {
        "format": "PE32+",
        "machine": "AMD64",
        "subsystem": "Windows CUI",
        "signed": False,
    }


def zip_timestamp() -> tuple[int, int, int, int, int, int]:
    raw = os.environ.get("SOURCE_DATE_EPOCH", "315532800")
    try:
        epoch = int(raw)
    except ValueError as error:
        raise SystemExit("SOURCE_DATE_EPOCH must be an integer") from error
    epoch = max(315532800, min(epoch, 4354819198))
    value = dt.datetime.fromtimestamp(epoch, tz=dt.timezone.utc)
    return value.year, value.month, value.day, value.hour, value.minute, value.second


def zip_info(name: str, executable: bool) -> zipfile.ZipInfo:
    info = zipfile.ZipInfo(name, zip_timestamp())
    info.compress_type = zipfile.ZIP_DEFLATED
    info.create_system = 3
    mode = 0o755 if executable else 0o644
    info.external_attr = (stat.S_IFREG | mode) << 16
    info.flag_bits |= 0x800
    return info


def package_files(binary: pathlib.Path) -> dict[str, bytes]:
    require(binary.is_file() and not binary.is_symlink(), f"binary is not a regular file: {binary}")
    files = {f"{PACKAGE_ROOT}/{BINARY_NAME}": binary.read_bytes()}
    inspect_pe(files[f"{PACKAGE_ROOT}/{BINARY_NAME}"])
    for relative in ASSET_FILES:
        source = pathlib.Path(__file__).resolve().parents[2] / "LICENSE" if relative == "LICENSE" else ASSET_ROOT / pathlib.PurePosixPath(relative)
        require(source.is_file() and not source.is_symlink(), f"missing Windows package asset: {source}")
        files[f"{PACKAGE_ROOT}/{relative}"] = source.read_bytes()
    return dict(sorted(files.items()))


def create_package(binary: pathlib.Path, output_dir: pathlib.Path, source_revision: str) -> dict[str, Any]:
    require(REVISION.fullmatch(source_revision) is not None, "source revision must be a lowercase full Git SHA-1")
    files = package_files(binary)
    output_dir.mkdir(parents=True, exist_ok=True)
    allowed = {ARCHIVE_NAME, CHECKSUM_NAME, "manifest.json", "verification.json"}
    unexpected = {entry.name for entry in output_dir.iterdir()} - allowed
    require(not unexpected, f"output directory contains unexpected entries: {sorted(unexpected)}")
    for name in allowed:
        path = output_dir / name
        if path.exists() or path.is_symlink():
            require(path.is_file() and not path.is_symlink(), f"refusing to replace non-regular output: {path}")
            path.unlink()

    archive_path = output_dir / ARCHIVE_NAME
    descriptor, temporary = tempfile.mkstemp(prefix=f".{ARCHIVE_NAME}.", dir=output_dir)
    os.close(descriptor)
    try:
        with zipfile.ZipFile(temporary, "w", allowZip64=True) as archive:
            for name, value in files.items():
                executable = name.endswith((".exe", ".cmd"))
                archive.writestr(zip_info(name, executable), value, compresslevel=9)
        require(zipfile.is_zipfile(temporary), "failed to create Windows Server ZIP")
        os.replace(temporary, archive_path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass

    binary_path = f"{PACKAGE_ROOT}/{BINARY_NAME}"
    binary_bytes = files[binary_path]
    pe = inspect_pe(binary_bytes)
    inventory = [
        {"path": name, "bytes": len(value), "sha256": sha256_bytes(value)}
        for name, value in files.items()
    ]
    manifest = {
        "schema": 1,
        "product": PRODUCT,
        "version": VERSION,
        "platform": PLATFORM,
        "arch": ARCH,
        "sourceRevision": source_revision,
        "signed": False,
        "productionQualified": False,
        "archive": {
            "path": ARCHIVE_NAME,
            "bytes": archive_path.stat().st_size,
            "sha256": sha256_file(archive_path),
        },
        "binary": {
            "path": binary_path,
            "bytes": len(binary_bytes),
            "sha256": sha256_bytes(binary_bytes),
            **pe,
        },
        "inventory": inventory,
    }
    atomic_write(output_dir / "manifest.json", canonical_json_bytes(manifest))
    atomic_write(output_dir / CHECKSUM_NAME, f"{manifest['archive']['sha256']}  {ARCHIVE_NAME}\n".encode("ascii"))
    return verify_windows_package(output_dir, source_revision)


def validate_receipt(value: Any, archive_sha256: str, source_revision: str) -> None:
    expected = {
        "schema": 1,
        "product": PRODUCT,
        "version": VERSION,
        "platform": PLATFORM,
        "arch": ARCH,
        "sourceRevision": source_revision,
        "archiveSha256": archive_sha256,
        "checks": RECEIPT_CHECKS,
        "productionQualified": False,
    }
    require(
        value == expected
        and type(value["schema"]) is int
        and value["productionQualified"] is False
        and all(value["checks"][name] is True for name in RECEIPT_CHECKS),
        "Windows native verification receipt is incomplete or mismatched",
    )


def verify_windows_package(directory: pathlib.Path | str, source_revision: str) -> dict[str, Any]:
    directory = pathlib.Path(directory)
    require(REVISION.fullmatch(source_revision) is not None, "source revision must be a lowercase full Git SHA-1")
    require(directory.is_dir(), f"Windows package directory not found: {directory}")
    expected_files = {ARCHIVE_NAME, CHECKSUM_NAME, "manifest.json"}
    actual_files = {entry.name for entry in directory.iterdir()}
    require(actual_files in (expected_files, expected_files | {"verification.json"}), "Windows package directory contains missing or unexpected entries")
    for entry in directory.iterdir():
        require(entry.is_file() and not entry.is_symlink(), f"Windows package output is not a regular file: {entry}")

    manifest_path = directory / "manifest.json"
    manifest = read_json(manifest_path)
    require(manifest_path.read_bytes() == canonical_json_bytes(manifest), "Windows manifest is not canonical JSON")
    require(isinstance(manifest, dict), "Windows manifest must be an object")
    require(set(manifest) == {
        "schema",
        "product",
        "version",
        "platform",
        "arch",
        "sourceRevision",
        "signed",
        "productionQualified",
        "archive",
        "binary",
        "inventory",
    }, "Windows manifest has missing or unexpected fields")
    require(manifest.get("schema") == 1 and manifest.get("product") == PRODUCT and manifest.get("version") == VERSION, "Windows manifest product identity mismatch")
    require(manifest.get("platform") == PLATFORM and manifest.get("arch") == ARCH, "Windows manifest platform mismatch")
    require(manifest.get("sourceRevision") == source_revision, "Windows manifest source revision mismatch")
    require(manifest.get("signed") is False and manifest.get("productionQualified") is False, "Windows package must remain unsigned and unqualified")

    archive_path = directory / ARCHIVE_NAME
    archive = manifest.get("archive")
    require(isinstance(archive, dict) and set(archive) == {"path", "bytes", "sha256"}, "invalid Windows archive manifest entry")
    require(archive.get("path") == ARCHIVE_NAME and archive.get("bytes") == archive_path.stat().st_size, "Windows archive name or size mismatch")
    archive_digest = sha256_file(archive_path)
    require(archive.get("sha256") == archive_digest and SHA256.fullmatch(archive_digest) is not None, "Windows archive digest mismatch")
    checksum = (directory / CHECKSUM_NAME).read_bytes()
    require(checksum == f"{archive_digest}  {ARCHIVE_NAME}\n".encode("ascii"), "Windows archive checksum sidecar mismatch")

    inventory = manifest.get("inventory")
    require(isinstance(inventory, list), "Windows manifest inventory must be a list")
    expected_names = sorted([f"{PACKAGE_ROOT}/{BINARY_NAME}", *(f"{PACKAGE_ROOT}/{name}" for name in ASSET_FILES)])
    require([entry.get("path") if isinstance(entry, dict) else None for entry in inventory] == expected_names, "Windows archive inventory is incomplete or out of order")
    inventory_by_name: dict[str, dict[str, Any]] = {}
    for entry in inventory:
        require(isinstance(entry, dict) and set(entry) == {"path", "bytes", "sha256"}, "invalid Windows inventory entry")
        require(isinstance(entry["bytes"], int) and entry["bytes"] >= 0, "invalid Windows inventory size")
        require(isinstance(entry["sha256"], str) and SHA256.fullmatch(entry["sha256"]) is not None, "invalid Windows inventory digest")
        inventory_by_name[entry["path"]] = entry

    try:
        with zipfile.ZipFile(archive_path, "r") as package:
            infos = package.infolist()
            names = [info.filename for info in infos]
            require(names == expected_names and len(set(names)) == len(names), "Windows ZIP contains missing, reordered, or duplicate members")
            require(package.comment == b"", "Windows ZIP has an unexpected comment")
            binary_bytes = b""
            for info in infos:
                require(not info.is_dir() and not (info.flag_bits & 1), f"invalid Windows ZIP member: {info.filename}")
                require(pathlib.PurePosixPath(info.filename).parts[0] == PACKAGE_ROOT and all(part not in ("", ".", "..") for part in pathlib.PurePosixPath(info.filename).parts), f"unsafe Windows ZIP member: {info.filename}")
                mode = info.external_attr >> 16
                require(stat.S_ISREG(mode), f"Windows ZIP member is not a regular file: {info.filename}")
                value = package.read(info)
                entry = inventory_by_name[info.filename]
                require(len(value) == entry["bytes"] and sha256_bytes(value) == entry["sha256"], f"Windows ZIP member differs from manifest: {info.filename}")
                if info.filename == f"{PACKAGE_ROOT}/{BINARY_NAME}":
                    binary_bytes = value
    except (OSError, zipfile.BadZipFile, RuntimeError) as error:
        raise SystemExit(f"invalid Windows Server ZIP: {error}") from error

    binary = manifest.get("binary")
    pe = inspect_pe(binary_bytes)
    expected_binary = {
        "path": f"{PACKAGE_ROOT}/{BINARY_NAME}",
        "bytes": len(binary_bytes),
        "sha256": sha256_bytes(binary_bytes),
        **pe,
    }
    require(binary == expected_binary, "Windows binary identity differs from manifest")
    if "verification.json" in actual_files:
        receipt_path = directory / "verification.json"
        receipt = read_json(receipt_path)
        require(receipt_path.read_bytes() == canonical_json_bytes(receipt), "Windows verification receipt is not canonical JSON")
        validate_receipt(receipt, archive_digest, source_revision)
    return manifest


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    package = subparsers.add_parser("package", help="Package an existing Windows x64 Server executable")
    package.add_argument("--binary", required=True, type=pathlib.Path)
    package.add_argument("--output-dir", required=True, type=pathlib.Path)
    package.add_argument("--source-revision", required=True)
    verify = subparsers.add_parser("verify", help="Verify a Windows x64 Server package")
    verify.add_argument("--directory", required=True, type=pathlib.Path)
    verify.add_argument("--source-revision", required=True)
    args = parser.parse_args()
    if args.command == "package":
        manifest = create_package(args.binary, args.output_dir, args.source_revision)
    else:
        manifest = verify_windows_package(args.directory, args.source_revision)
    print(json.dumps({"archive": manifest["archive"], "binary": manifest["binary"], "sourceRevision": args.source_revision}, sort_keys=True))


if __name__ == "__main__":
    main()
