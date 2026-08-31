# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
# ─── How to run ───
# python3 packaging/control/tooling/flutter_web_sdk_preflight.py <sdk-zip> <extracted-sdk> <flutter-root> <stamp>

from __future__ import annotations

import hashlib
import json
import pathlib
import stat
import sys
import zipfile
import zlib
from dataclasses import dataclass
from typing import Final

ENGINE: Final = "1e9a811bf8e70466596bcf0ea3a8b5adb5f17f7f"
FRAMEWORK: Final = "b8962555571d8c170cff8e76023ea7bf60e5ec4b"
SDK_SHA256: Final = "e4b3b674e149286126615768067fbb6ce0e68988b67017df1bb872eacb44b81a"
SDK_SIZE: Final = 27_133_134
REQUIRED: Final = frozenset(
    {
        "canvaskit/canvaskit.wasm",
        "kernel/dart2js_platform.dill",
        "lib/_engine/engine.dart",
        "lib/ui/ui.dart",
    }
)


class WebSdkPreflightError(Exception):
    def __init__(self, code: str) -> None:
        super().__init__(code)
        self.code = code


@dataclass(frozen=True, slots=True)
class Artifact:
    path: str
    size: int
    crc32: int


def zip_artifacts(path: pathlib.Path) -> tuple[Artifact, ...]:
    if path.stat().st_size != SDK_SIZE:
        raise WebSdkPreflightError("CONTROL_WEB_SDK_SIZE_MISMATCH")
    if hashlib.sha256(path.read_bytes()).hexdigest() != SDK_SHA256:
        raise WebSdkPreflightError("CONTROL_WEB_SDK_SHA256_MISMATCH")
    artifacts: list[Artifact] = []
    names: set[str] = set()
    with zipfile.ZipFile(path) as archive:
        for info in archive.infolist():
            name = info.filename
            parts = pathlib.PurePosixPath(name).parts
            mode = (info.external_attr >> 16) & 0o170000
            if (
                not name
                or name in names
                or name.startswith("/")
                or ".." in parts
                or "\\" in name
                or info.flag_bits & 1
                or mode == stat.S_IFLNK
            ):
                raise WebSdkPreflightError("CONTROL_WEB_SDK_ZIP_UNSAFE")
            names.add(name)
            if not info.is_dir():
                artifacts.append(Artifact(name, info.file_size, info.CRC))
        if archive.testzip() is not None:
            raise WebSdkPreflightError("CONTROL_WEB_SDK_ZIP_CRC_MISMATCH")
    if not REQUIRED.issubset(names):
        raise WebSdkPreflightError("CONTROL_WEB_SDK_LAYOUT_MISMATCH")
    return tuple(artifacts)


def verify_extracted(root: pathlib.Path, artifacts: tuple[Artifact, ...]) -> None:
    expected = frozenset(item.path for item in artifacts)
    actual: set[str] = set()
    for path in root.rglob("*"):
        if path.is_symlink():
            raise WebSdkPreflightError("CONTROL_WEB_SDK_EXTRACTED_UNSAFE")
        if path.is_file():
            actual.add(path.relative_to(root).as_posix())
    if frozenset(actual) != expected:
        raise WebSdkPreflightError("CONTROL_WEB_SDK_EXTRACTED_LAYOUT_MISMATCH")
    for item in artifacts:
        path = root / item.path
        if path.stat().st_size != item.size:
            raise WebSdkPreflightError("CONTROL_WEB_SDK_EXTRACTED_SIZE_MISMATCH")
        checksum = 0
        with path.open("rb") as stream:
            while block := stream.read(1024 * 1024):
                checksum = zlib.crc32(block, checksum)
        if checksum != item.crc32:
            raise WebSdkPreflightError("CONTROL_WEB_SDK_EXTRACTED_CRC_MISMATCH")


def verify_toolchain(flutter_root: pathlib.Path, stamp: pathlib.Path) -> None:
    metadata_path = flutter_root / "bin/cache/flutter.version.json"
    metadata = json.loads(metadata_path.read_text())
    if not isinstance(metadata, dict):
        raise WebSdkPreflightError("CONTROL_FLUTTER_IDENTITY_INVALID")
    if metadata.get("engineRevision") != ENGINE or metadata.get("frameworkRevision") != FRAMEWORK:
        raise WebSdkPreflightError("CONTROL_FLUTTER_IDENTITY_MISMATCH")
    if (flutter_root / "bin/internal/engine.version").read_text().strip() != ENGINE:
        raise WebSdkPreflightError("CONTROL_FLUTTER_ENGINE_MISMATCH")
    if stamp.read_text().strip() != ENGINE:
        raise WebSdkPreflightError("CONTROL_WEB_SDK_STAMP_MISMATCH")


def main() -> int:
    if len(sys.argv) != 5:
        raise WebSdkPreflightError("CONTROL_WEB_SDK_PREFLIGHT_USAGE")
    archive = pathlib.Path(sys.argv[1]).resolve()
    extracted = pathlib.Path(sys.argv[2]).resolve()
    flutter_root = pathlib.Path(sys.argv[3]).resolve()
    stamp = pathlib.Path(sys.argv[4]).resolve()
    artifacts = zip_artifacts(archive)
    verify_extracted(extracted, artifacts)
    verify_toolchain(flutter_root, stamp)
    print(f"CONTROL_WEB_SDK_VERIFIED sha256={SDK_SHA256} files={len(artifacts)}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, json.JSONDecodeError, zipfile.BadZipFile) as error:
        print(f"CONTROL_WEB_SDK_IO_INVALID:{type(error).__name__}", file=sys.stderr)
        raise SystemExit(65) from error
    except WebSdkPreflightError as error:
        print(error.code, file=sys.stderr)
        raise SystemExit(65) from error
