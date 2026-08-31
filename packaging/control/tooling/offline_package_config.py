# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
# ─── How to run ───
# python3 packaging/control/tooling/offline_package_config.py <control-root> <pub-cache> <flutter-root>

from __future__ import annotations

import hashlib
import json
import os
import re
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Final, TypedDict, assert_never

HOSTED_URL: Final = "https://pub.dev"


class PackageConfigEntry(TypedDict):
    name: str
    rootUri: str
    packageUri: str
    languageVersion: str


@dataclass(frozen=True, slots=True)
class LockedPackage:
    name: str
    source: str
    version: str
    sha256: str | None


class PackageResolutionError(Exception):
    def __init__(self, code: str) -> None:
        super().__init__(code)
        self.code = code


def scalar(value: str) -> str:
    stripped = value.strip()
    if stripped.startswith('"'):
        parsed = json.loads(stripped)
        if not isinstance(parsed, str):
            raise PackageResolutionError("CONTROL_PACKAGE_LOCK_INVALID")
        return parsed
    return stripped


def parse_lock(path: Path) -> tuple[LockedPackage, ...]:
    packages: list[LockedPackage] = []
    name: str | None = None
    source = ""
    version = ""
    checksum: str | None = None
    for line in path.read_text().splitlines():
        package_match = re.fullmatch(r"  ([a-zA-Z0-9_]+):", line)
        if package_match:
            if name is not None:
                packages.append(LockedPackage(name, source, version, checksum))
            name = package_match.group(1)
            source = ""
            version = ""
            checksum = None
            continue
        if name is None:
            continue
        field_match = re.fullmatch(r"    (source|version): (.+)", line)
        if field_match:
            match field_match.group(1):
                case "source":
                    source = scalar(field_match.group(2))
                case "version":
                    version = scalar(field_match.group(2))
                case unreachable:
                    raise PackageResolutionError(
                        f"CONTROL_PACKAGE_LOCK_FIELD_UNSUPPORTED:{unreachable}"
                    )
                    assert_never(unreachable)
            continue
        checksum_match = re.fullmatch(r"      sha256: (.+)", line)
        if checksum_match:
            checksum = scalar(checksum_match.group(1))
    if name is not None:
        packages.append(LockedPackage(name, source, version, checksum))
    if not packages or any(not item.source or not item.version for item in packages):
        raise PackageResolutionError("CONTROL_PACKAGE_LOCK_INVALID")
    return tuple(packages)


def pubspec_fields(path: Path) -> tuple[str, str, str]:
    name = ""
    version = ""
    sdk = ""
    environment = False
    for line in path.read_text().splitlines():
        if line.startswith("name:"):
            name = scalar(line.split(":", 1)[1])
        elif line.startswith("version:"):
            version = scalar(line.split(":", 1)[1])
        elif line == "environment:":
            environment = True
        elif environment and re.fullmatch(r"  sdk: .+", line):
            sdk = scalar(line.split(":", 1)[1])
            environment = False
        elif environment and line and not line.startswith(" "):
            environment = False
    if not name or not sdk:
        raise PackageResolutionError("CONTROL_PACKAGE_PUBSPEC_INVALID")
    return name, version, sdk


def language_version(constraint: str) -> str:
    match = re.search(r"(?:\^|>=|>|=)?\s*(\d+)\.(\d+)", constraint)
    if match is None:
        raise PackageResolutionError("CONTROL_PACKAGE_LANGUAGE_INVALID")
    return f"{match.group(1)}.{match.group(2)}"


def within(root: Path, candidate: Path) -> bool:
    try:
        candidate.resolve().relative_to(root.resolve())
    except ValueError:
        return False
    return True


def hosted_entry(item: LockedPackage, cache: Path) -> PackageConfigEntry:
    package_root = cache / "hosted" / "pub.dev" / f"{item.name}-{item.version}"
    checksum_path = cache / "hosted-hashes" / "pub.dev" / f"{item.name}-{item.version}.sha256"
    if not within(cache, package_root) or not package_root.is_dir() or not checksum_path.is_file():
        raise PackageResolutionError("CONTROL_PACKAGE_CACHE_MISSING")
    if item.sha256 is None or checksum_path.read_text().strip() != item.sha256:
        raise PackageResolutionError("CONTROL_PACKAGE_CACHE_CHECKSUM_MISMATCH")
    package_name, package_version, sdk = pubspec_fields(package_root / "pubspec.yaml")
    if package_name != item.name or package_version != item.version:
        raise PackageResolutionError("CONTROL_PACKAGE_CACHE_IDENTITY_MISMATCH")
    return {"name": item.name, "rootUri": package_root.as_uri(), "packageUri": "lib/", "languageVersion": language_version(sdk)}


def sdk_entry(item: LockedPackage, flutter_root: Path) -> PackageConfigEntry:
    package_root = (
        flutter_root / "bin/cache/pkg/sky_engine"
        if item.name == "sky_engine"
        else flutter_root / "packages" / item.name
    )
    if not within(flutter_root, package_root) or not package_root.is_dir():
        raise PackageResolutionError("CONTROL_PACKAGE_SDK_MISSING")
    package_name, _, sdk = pubspec_fields(package_root / "pubspec.yaml")
    if package_name != item.name:
        raise PackageResolutionError("CONTROL_PACKAGE_SDK_IDENTITY_MISMATCH")
    return {"name": item.name, "rootUri": package_root.as_uri(), "packageUri": "lib/", "languageVersion": language_version(sdk)}


def dependency_names(path: Path, sections: frozenset[str]) -> tuple[str, ...]:
    names: list[str] = []
    section = False
    for line in path.read_text().splitlines():
        if line.rstrip(":") in sections and not line.startswith(" "):
            section = True
            continue
        if section and line and not line.startswith(" "):
            section = False
        match = re.fullmatch(r"  ([a-zA-Z0-9_]+):.*", line) if section else None
        if match:
            names.append(match.group(1))
    return tuple(names)


def generate(control_root: Path, cache: Path, flutter_root: Path) -> None:
    locked = parse_lock(control_root / "pubspec.lock")
    locked_names = frozenset(item.name for item in locked)
    root_pubspec = control_root / "pubspec.yaml"
    root_dependencies = dependency_names(root_pubspec, frozenset({"dependencies"}))
    root_dev_dependencies = dependency_names(root_pubspec, frozenset({"dev_dependencies"}))
    if not frozenset(root_dependencies + root_dev_dependencies).issubset(locked_names):
        raise PackageResolutionError("CONTROL_PACKAGE_LOCK_STALE")
    entries: list[PackageConfigEntry] = []
    graph_packages: list[dict[str, str | list[str]]] = []
    for item in locked:
        match item.source:
            case "hosted":
                entry = hosted_entry(item, cache)
            case "sdk":
                entry = sdk_entry(item, flutter_root)
            case unsupported:
                raise PackageResolutionError(
                    f"CONTROL_PACKAGE_SOURCE_UNSUPPORTED:{unsupported}"
                )
                assert_never(unsupported)
        entries.append(entry)
        package_root = Path(entry["rootUri"].removeprefix("file://"))
        graph_packages.append({"name": item.name, "version": item.version, "dependencies": list(dependency_names(package_root / "pubspec.yaml", frozenset({"dependencies"})))})
    root_name, root_version, root_sdk = pubspec_fields(root_pubspec)
    entries.append({"name": root_name, "rootUri": "../", "packageUri": "lib/", "languageVersion": language_version(root_sdk)})
    graph_packages.insert(0, {"name": root_name, "version": root_version, "dependencies": list(root_dependencies), "devDependencies": list(root_dev_dependencies)})
    output_dir = control_root / ".dart_tool"
    output_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    outputs = {
        output_dir / "package_config.json": {"configVersion": 2, "packages": entries, "generator": "jastreamer-offline-lock-v1"},
        output_dir / "package_graph.json": {"roots": [root_name], "packages": graph_packages, "configVersion": 1},
    }
    for output, content in outputs.items():
        encoded = json.dumps(content, indent=2) + "\n"
        temporary = output.with_name(f"{output.name}.tmp-{os.getpid()}")
        temporary.write_text(encoded)
        temporary.replace(output)


def main() -> int:
    if len(sys.argv) != 4:
        raise PackageResolutionError("CONTROL_PACKAGE_PREFLIGHT_USAGE")
    generate(Path(sys.argv[1]).resolve(), Path(sys.argv[2]).resolve(), Path(sys.argv[3]).resolve())
    print(hashlib.sha256((Path(sys.argv[1]) / ".dart_tool/package_config.json").read_bytes()).hexdigest())
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except PackageResolutionError as error:
        print(error.code, file=sys.stderr)
        raise SystemExit(65) from error
