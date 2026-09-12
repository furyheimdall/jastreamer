#!/usr/bin/env python3
"""Create and verify the release-bearing artifacts produced by CI."""

from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import re
import stat
import tarfile
from typing import Any

VERSION = "0.2.0"
SHA256 = re.compile(r"[0-9a-f]{64}")
REVISION = re.compile(r"[0-9a-f]{40}")
SERVER_TARGETS = {
    "amd64": ("linux/amd64", "jastreamer-server-ci:amd64", "server-image-amd64.tar"),
    "arm64": ("linux/arm64", "jastreamer-server-ci:arm64", "server-image-arm64.tar"),
}
DESKTOP_TARGETS = {
    "windows-x64": {
        "platform": "win32",
        "arch": "x64",
        "archive": f"jastreamer-desktop_{VERSION}_windows-x64.zip",
        "checks": [
            "exact-package-inventory",
            "windows-package-execution",
            "isolated-session-storage",
            "portable-directory-relocation",
        ],
    },
    "linux-amd64": {
        "platform": "linux",
        "arch": "amd64",
        "archive": f"jastreamer-desktop_{VERSION}_linux-amd64.deb",
        "checks": [
            "exact-package-inventory",
            "native-deb-installation",
            "normal-user-execution",
            "chromium-setuid-sandbox-permissions",
            "renderer-os-sandbox",
        ],
    },
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


def regular(path: pathlib.Path) -> None:
    require(path.exists(), f"missing file: {path}")
    require(stat.S_ISREG(path.lstat().st_mode), f"not a regular file: {path}")


def sha256_file(path: pathlib.Path) -> str:
    regular(path)
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def canonical_json(path: pathlib.Path, value: Any) -> None:
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def read_json(path: pathlib.Path) -> Any:
    regular(path)
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise SystemExit(f"invalid JSON in {path}: {error}") from error


def validate_revision(value: str) -> None:
    require(REVISION.fullmatch(value) is not None, "source revision must be a lowercase full Git SHA-1")


def safe_relative(value: Any, label: str) -> str:
    require(isinstance(value, str) and value, f"{label} must be a non-empty string")
    candidate = pathlib.PurePosixPath(value)
    require(not candidate.is_absolute(), f"{label} must be relative")
    require(all(part not in ("", ".", "..") for part in candidate.parts), f"unsafe {label}")
    require("\\" not in value, f"unsafe {label}")
    return value


def tar_bytes(archive: tarfile.TarFile, members: dict[str, tarfile.TarInfo], name: str) -> bytes:
    member = members.get(name.removeprefix("./"))
    require(member is not None and member.isfile(), f"Docker archive is missing regular file {name}")
    source = archive.extractfile(member)
    require(source is not None, f"cannot read Docker archive member {name}")
    return source.read()


def inspect_server_archive(path: pathlib.Path, architecture: str, source_revision: str) -> dict[str, Any]:
    platform, expected_tag, _ = SERVER_TARGETS[architecture]
    regular(path)
    with tarfile.open(path, mode="r:*") as archive:
        member_list = archive.getmembers()
        normalized = [member.name.removeprefix("./") for member in member_list]
        require(len(normalized) == len(set(normalized)), "Docker archive contains duplicate member names")
        members = dict(zip(normalized, member_list, strict=True))
        saved = json.loads(tar_bytes(archive, members, "manifest.json"))
        require(isinstance(saved, list) and len(saved) == 1, "Docker archive must contain exactly one image")
        image = saved[0]
        require(isinstance(image, dict), "invalid Docker save manifest")
        tags = image.get("RepoTags")
        require(tags == [expected_tag], f"Docker archive must contain only {expected_tag}")
        config_name = safe_relative(image.get("Config"), "Docker config path")
        config_bytes = tar_bytes(archive, members, config_name)
        config_digest = hashlib.sha256(config_bytes).hexdigest()
        require(config_name in (f"{config_digest}.json", f"blobs/sha256/{config_digest}"), "Docker config path does not match its SHA-256")
        config = json.loads(config_bytes)
        require((config.get("os"), config.get("architecture")) == tuple(platform.split("/")), "Docker config platform mismatch")
        runtime = config.get("config", {})
        require(runtime.get("Entrypoint") == ["/usr/local/bin/jastreamer-server"], "unexpected Server entrypoint")
        require(runtime.get("User") == "10001:10001", "unexpected Server runtime user")
        labels = runtime.get("Labels", {})
        require(labels.get("org.opencontainers.image.version") == VERSION, "Server version label mismatch")
        require(labels.get("org.opencontainers.image.revision") == source_revision, "Server source revision label mismatch")
        require(labels.get("org.opencontainers.image.licenses") == "Apache-2.0", "Server license label mismatch")
        require(labels.get("org.opencontainers.image.source") == "https://github.com/furyheimdall/jastreamer", "Server source label mismatch")
        require(bool(labels.get("org.opencontainers.image.created")), "Server creation label is missing")
        layer_names = image.get("Layers")
        require(isinstance(layer_names, list) and layer_names, "Docker image has no layers")
        diff_ids = config.get("rootfs", {}).get("diff_ids")
        require(isinstance(diff_ids, list) and len(diff_ids) == len(layer_names), "Docker layer and rootfs inventories differ")
        layers = []
        for index, value in enumerate(layer_names):
            layer_name = safe_relative(value, "Docker layer path")
            layer = tar_bytes(archive, members, layer_name)
            digest = hashlib.sha256(layer).hexdigest()
            require(diff_ids[index] == f"sha256:{digest}", f"Docker layer {index} does not match its rootfs diff ID")
            layers.append({"index": index, "path": layer_name, "bytes": len(layer), "sha256": digest, "diffId": f"sha256:{digest}"})
    return {
        "config": {"path": config_name, "bytes": len(config_bytes), "digest": f"sha256:{config_digest}"},
        "layers": layers,
        "created": labels["org.opencontainers.image.created"],
        "localTag": expected_tag,
    }


def validate_server_manifest(value: Any, directory: pathlib.Path, architecture: str, source_revision: str) -> dict[str, Any]:
    platform, expected_tag, archive_name = SERVER_TARGETS[architecture]
    require(isinstance(value, dict), "Server artifact manifest must be an object")
    require(value.get("schema") == 1, "unsupported Server artifact schema")
    require(value.get("product") == "jastreamer-server" and value.get("version") == VERSION, "unexpected Server artifact identity")
    require(value.get("platform") == platform and value.get("architecture") == architecture, "Server artifact platform mismatch")
    require(value.get("sourceRevision") == source_revision, "Server artifact source revision mismatch")
    require(value.get("signed") is False and value.get("productionQualified") is False, "Server artifact must remain unsigned and unqualified")
    archive = value.get("archive")
    require(isinstance(archive, dict) and archive.get("path") == archive_name, "unexpected Server archive name")
    archive_path = directory / archive_name
    require(archive.get("bytes") == archive_path.stat().st_size, "Server archive size mismatch")
    require(archive.get("sha256") == sha256_file(archive_path), "Server archive SHA-256 mismatch")
    inspected = inspect_server_archive(archive_path, architecture, source_revision)
    require(value.get("image") == inspected, "Server image config/layer inventory mismatch")
    require(value.get("verification") == {
        "airplayHelperTests": True,
        "configurationCheck": True,
        "nativeExecution": True,
        "packagedDependenciesSourcesAndLicenses": True,
        "versionCheck": True,
    }, "Server verification receipt is incomplete")
    require(inspected["localTag"] == expected_tag, "unexpected local image tag")
    return value


def write_sums(directory: pathlib.Path, names: list[str]) -> None:
    lines = [f"{sha256_file(directory / name)}  {name}\n" for name in names]
    (directory / "SHA256SUMS").write_text("".join(lines), encoding="ascii")


def verify_sums(directory: pathlib.Path, expected_names: set[str]) -> None:
    sums = directory / "SHA256SUMS"
    regular(sums)
    parsed: dict[str, str] = {}
    for line in sums.read_text(encoding="ascii").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
        require(match is not None, "malformed SHA256SUMS entry")
        digest, name = match.groups()
        require(name not in parsed, f"duplicate checksum entry: {name}")
        parsed[name] = digest
    require(set(parsed) == expected_names, "SHA256SUMS file inventory mismatch")
    for name, digest in parsed.items():
        require(sha256_file(directory / name) == digest, f"SHA256SUMS mismatch: {name}")


def create_server(args: argparse.Namespace) -> None:
    validate_revision(args.source_revision)
    directory = pathlib.Path(args.output_dir)
    directory.mkdir(parents=True, exist_ok=True)
    archive_name = SERVER_TARGETS[args.architecture][2]
    archive = directory / archive_name
    inspected = inspect_server_archive(archive, args.architecture, args.source_revision)
    manifest = {
        "schema": 1,
        "product": "jastreamer-server",
        "version": VERSION,
        "platform": SERVER_TARGETS[args.architecture][0],
        "architecture": args.architecture,
        "sourceRevision": args.source_revision,
        "archive": {"path": archive_name, "bytes": archive.stat().st_size, "sha256": sha256_file(archive)},
        "image": inspected,
        "verification": {
            "nativeExecution": True,
            "packagedDependenciesSourcesAndLicenses": True,
            "airplayHelperTests": True,
            "versionCheck": True,
            "configurationCheck": True,
        },
        "signed": False,
        "productionQualified": False,
    }
    canonical_json(directory / "manifest.json", manifest)
    write_sums(directory, [archive_name, "manifest.json"])
    verify_server_artifact(directory, args.architecture, args.source_revision)
    print(json.dumps({"artifact": directory.name, "archive": manifest["archive"], "config": inspected["config"], "layers": inspected["layers"], "sourceRevision": args.source_revision}, sort_keys=True))


def verify_server_artifact(directory: pathlib.Path, architecture: str, source_revision: str) -> dict[str, Any]:
    require(directory.is_dir(), f"Server artifact directory not found: {directory}")
    archive_name = SERVER_TARGETS[architecture][2]
    require({entry.name for entry in directory.iterdir()} == {archive_name, "manifest.json", "SHA256SUMS"}, "Server artifact contains missing or unexpected files")
    verify_sums(directory, {archive_name, "manifest.json"})
    return validate_server_manifest(read_json(directory / "manifest.json"), directory, architecture, source_revision)


def validate_desktop_manifest(value: Any, directory: pathlib.Path, target: str, source_revision: str) -> dict[str, Any]:
    expected = DESKTOP_TARGETS[target]
    require(isinstance(value, dict), "desktop manifest must be an object")
    require(value.get("product") == "jastreamer-desktop" and value.get("version") == VERSION, "unexpected desktop artifact identity")
    require(value.get("platform") == expected["platform"] and value.get("arch") == expected["arch"], "desktop platform mismatch")
    require(value.get("sourceRevision") == source_revision, "desktop source revision mismatch")
    require(value.get("electron") == "44.3.0", "unexpected Electron version")
    require(value.get("signed") is False and value.get("productionQualified") is False, "desktop artifact must remain unsigned and unqualified")
    archive = value.get("archive")
    require(isinstance(archive, dict) and archive.get("path") == expected["archive"], "unexpected desktop archive name")
    archive_path = directory / expected["archive"]
    require(archive.get("bytes") == archive_path.stat().st_size, "desktop archive size mismatch")
    require(archive.get("sha256") == sha256_file(archive_path), "desktop archive SHA-256 mismatch")
    inventory = value.get("files")
    require(isinstance(inventory, list) and inventory, "desktop exact file inventory is missing")
    paths: set[str] = set()
    entries: dict[str, dict[str, Any]] = {}
    for entry in inventory:
        require(isinstance(entry, dict), "invalid desktop inventory entry")
        name = safe_relative(entry.get("path"), "desktop inventory path")
        require(name not in paths, f"duplicate desktop inventory path: {name}")
        paths.add(name)
        entries[name] = entry
        if target == "windows-x64":
            require(isinstance(entry.get("bytes"), int) and entry["bytes"] >= 0, f"invalid desktop inventory size: {name}")
            require(isinstance(entry.get("sha256"), str) and SHA256.fullmatch(entry["sha256"]) is not None, f"invalid desktop inventory digest: {name}")
            continue
        require(entry.get("type") in ("file", "symlink"), f"invalid Linux inventory type: {name}")
        require(isinstance(entry.get("mode"), str) and re.fullmatch(r"[0-7]{4}", entry["mode"]) is not None, f"invalid Linux inventory mode: {name}")
        require(entry.get("uid") == 0 and entry.get("gid") == 0, f"Linux package entry is not root-owned: {name}")
        if entry["type"] == "file":
            require(isinstance(entry.get("bytes"), int) and entry["bytes"] >= 0, f"invalid desktop inventory size: {name}")
            require(isinstance(entry.get("sha256"), str) and SHA256.fullmatch(entry["sha256"]) is not None, f"invalid desktop inventory digest: {name}")
            require("target" not in entry, f"regular Linux file has a symlink target: {name}")
        else:
            require(isinstance(entry.get("target"), str) and entry["target"], f"Linux symlink target is missing: {name}")
            require("bytes" not in entry and "sha256" not in entry, f"Linux symlink has regular-file metadata: {name}")
    if target == "linux-amd64":
        sandbox = entries.get("usr/lib/jastreamer-desktop/chrome-sandbox", {})
        executable = entries.get("usr/lib/jastreamer-desktop/jastreamer-desktop", {})
        require(sandbox.get("type") == "file" and sandbox.get("mode") == "4755", "Linux Chromium setuid sandbox mode is not 4755")
        require(executable.get("type") == "file" and executable.get("mode") == "0755", "Linux desktop executable mode is not 0755")
    sidecar = directory / f"{expected['archive']}.sha256"
    regular(sidecar)
    require(sidecar.read_text(encoding="ascii") == f"{archive['sha256']}  {expected['archive']}\n", "desktop checksum sidecar mismatch")
    return value


def create_desktop(args: argparse.Namespace) -> None:
    validate_revision(args.source_revision)
    directory = pathlib.Path(args.dist)
    manifest_path = directory / "manifest.json"
    manifest = validate_desktop_manifest(read_json(manifest_path), directory, args.target, args.source_revision)
    receipt = {
        "schema": 1,
        "product": "jastreamer-desktop",
        "version": VERSION,
        "target": args.target,
        "platform": manifest["platform"],
        "arch": manifest["arch"],
        "sourceRevision": args.source_revision,
        "packageManifest": {"path": "manifest.json", "bytes": manifest_path.stat().st_size, "sha256": sha256_file(manifest_path)},
        "archive": manifest["archive"],
        "checks": DESKTOP_TARGETS[args.target]["checks"],
        "signed": False,
        "productionQualified": False,
    }
    canonical_json(directory / "verification.json", receipt)
    verify_desktop_artifact(directory, args.target, args.source_revision, subset=True)
    print(json.dumps(receipt, sort_keys=True))


def verify_desktop_artifact(directory: pathlib.Path, target: str, source_revision: str, subset: bool = False) -> dict[str, Any]:
    expected = DESKTOP_TARGETS[target]
    expected_files = {expected["archive"], f"{expected['archive']}.sha256", "manifest.json", "verification.json"}
    require(directory.is_dir(), f"desktop artifact directory not found: {directory}")
    if not subset:
        require({entry.name for entry in directory.iterdir()} == expected_files, "desktop artifact contains missing or unexpected files")
    else:
        require(expected_files <= {entry.name for entry in directory.iterdir()}, "desktop release files are missing")
    manifest_path = directory / "manifest.json"
    manifest = validate_desktop_manifest(read_json(manifest_path), directory, target, source_revision)
    receipt = read_json(directory / "verification.json")
    require(isinstance(receipt, dict) and receipt.get("schema") == 1, "unsupported desktop verification receipt")
    require(receipt.get("product") == "jastreamer-desktop" and receipt.get("version") == VERSION, "unexpected desktop verification identity")
    require(receipt.get("target") == target and receipt.get("platform") == expected["platform"] and receipt.get("arch") == expected["arch"], "desktop verification target mismatch")
    require(receipt.get("sourceRevision") == source_revision, "desktop verification source revision mismatch")
    require(receipt.get("archive") == manifest["archive"], "desktop receipt archive mismatch")
    require(receipt.get("packageManifest") == {"path": "manifest.json", "bytes": manifest_path.stat().st_size, "sha256": sha256_file(manifest_path)}, "desktop receipt manifest mismatch")
    require(receipt.get("checks") == expected["checks"], "desktop verification checks are incomplete")
    require(receipt.get("signed") is False and receipt.get("productionQualified") is False, "desktop receipt must remain unsigned and unqualified")
    return receipt


def main() -> None:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    create_server_parser = subparsers.add_parser("create-server")
    create_server_parser.add_argument("--output-dir", required=True)
    create_server_parser.add_argument("--architecture", choices=sorted(SERVER_TARGETS), required=True)
    create_server_parser.add_argument("--source-revision", required=True)
    create_server_parser.set_defaults(function=create_server)

    verify_server_parser = subparsers.add_parser("verify-server")
    verify_server_parser.add_argument("--artifact-dir", required=True)
    verify_server_parser.add_argument("--architecture", choices=sorted(SERVER_TARGETS), required=True)
    verify_server_parser.add_argument("--source-revision", required=True)
    verify_server_parser.set_defaults(function=lambda args: print(json.dumps(verify_server_artifact(pathlib.Path(args.artifact_dir), args.architecture, args.source_revision), sort_keys=True)))

    create_desktop_parser = subparsers.add_parser("create-desktop")
    create_desktop_parser.add_argument("--dist", required=True)
    create_desktop_parser.add_argument("--target", choices=sorted(DESKTOP_TARGETS), required=True)
    create_desktop_parser.add_argument("--source-revision", required=True)
    create_desktop_parser.set_defaults(function=create_desktop)

    verify_desktop_parser = subparsers.add_parser("verify-desktop")
    verify_desktop_parser.add_argument("--artifact-dir", required=True)
    verify_desktop_parser.add_argument("--target", choices=sorted(DESKTOP_TARGETS), required=True)
    verify_desktop_parser.add_argument("--source-revision", required=True)
    verify_desktop_parser.set_defaults(function=lambda args: print(json.dumps(verify_desktop_artifact(pathlib.Path(args.artifact_dir), args.target, args.source_revision), sort_keys=True)))

    args = parser.parse_args()
    if hasattr(args, "source_revision"):
        validate_revision(args.source_revision)
    args.function(args)


if __name__ == "__main__":
    main()
