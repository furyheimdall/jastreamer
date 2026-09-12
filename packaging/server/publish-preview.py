#!/usr/bin/env python3
"""Fail-closed validation and staging for the public preview release workflow."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import pathlib
import re
import shutil
import ssl
import stat
import sys
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from typing import Any

import ci_artifact

REPOSITORY = "furyheimdall/jastreamer"
REGISTRY_REPOSITORY = "furyheimdall/jastreamer-server"
CI_WORKFLOW = ".github/workflows/ci.yml"
CI_NAME = "Server, Web, and Desktop CI"
VERSION = "0.2.0"
SHA = re.compile(r"[0-9a-f]{40}")
PREVIEW_TAG = re.compile(r"v0\.2\.0-preview\.([1-9][0-9]*)")
IMAGE_TAG = re.compile(r"0\.2\.0-preview\.([1-9][0-9]*)")
DIGEST = re.compile(r"sha256:[0-9a-f]{64}")
EXPECTED_JOBS = {
    "Validate Server and Web",
    "Native Server container (linux/amd64)",
    "Native Server container (linux/arm64)",
    "Windows x64 portable desktop",
    "Linux amd64 DEB desktop",
}
EXPECTED_ARTIFACTS = {
    "server-image-amd64",
    "server-image-arm64",
    "jastreamer-desktop-windows-x64",
    "jastreamer-desktop-linux-amd64",
}
MANIFEST_ACCEPT = ", ".join((
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
))


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


def regular(path: pathlib.Path) -> None:
    require(path.exists(), f"missing file: {path}")
    require(stat.S_ISREG(path.lstat().st_mode), f"not a regular file: {path}")


def load_json(path: pathlib.Path) -> Any:
    regular(path)
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise SystemExit(f"invalid JSON in {path}: {error}") from error


def canonical_json(path: pathlib.Path, value: Any) -> None:
    path.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def sha256_file(path: pathlib.Path) -> str:
    regular(path)
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def validate_identity(source_revision: str, release_tag: str, image_tag: str) -> None:
    require(SHA.fullmatch(source_revision) is not None, "source revision must be a lowercase full Git SHA-1")
    release_match = PREVIEW_TAG.fullmatch(release_tag)
    image_match = IMAGE_TAG.fullmatch(image_tag)
    require(release_match is not None, "release tag must be v0.2.0-preview.N with N greater than zero")
    require(image_match is not None, "image tag must be 0.2.0-preview.N with N greater than zero")
    require(release_match.group(1) == image_match.group(1), "release and image preview numbers differ")


def validate_run(args: argparse.Namespace) -> None:
    require(args.repository == REPOSITORY, f"release workflow is restricted to {REPOSITORY}")
    require(re.fullmatch(r"[1-9][0-9]*", args.run_id) is not None, "CI run ID must be a positive decimal integer")
    run_id = int(args.run_id)
    run = load_json(pathlib.Path(args.run))
    workflow = load_json(pathlib.Path(args.workflow))
    branch = load_json(pathlib.Path(args.branch))
    comparison = load_json(pathlib.Path(args.comparison))
    jobs_response = load_json(pathlib.Path(args.jobs))
    artifacts_response = load_json(pathlib.Path(args.artifacts))

    require(run.get("id") == run_id, "GitHub returned a different workflow run")
    require(run.get("name") == CI_NAME and run.get("path") == CI_WORKFLOW, "run did not execute the protected CI workflow")
    require(run.get("workflow_id") == workflow.get("id"), "workflow identity mismatch")
    require(workflow.get("path") == CI_WORKFLOW and workflow.get("name") == CI_NAME and workflow.get("state") == "active", "unexpected or inactive CI workflow")
    require(run.get("repository", {}).get("full_name") == REPOSITORY, "run repository mismatch")
    require(run.get("head_repository", {}).get("full_name") == REPOSITORY, "run originated from a fork")
    require(run.get("event") == "push" and run.get("head_branch") == "main", "release source must be a main push run")
    require(run.get("status") == "completed" and run.get("conclusion") == "success", "CI run is not successfully completed")
    source_revision = run.get("head_sha")
    require(isinstance(source_revision, str) and SHA.fullmatch(source_revision) is not None, "run head SHA is invalid")
    require(run.get("head_commit", {}).get("id") == source_revision, "run head commit and SHA differ")
    require(branch.get("name") == "main" and branch.get("protected") is True, "main is not reported as protected")
    require(comparison.get("status") in ("ahead", "identical"), "CI source is not an ancestor of protected main")
    require(comparison.get("base_commit", {}).get("sha") == source_revision, "comparison base is not the CI source")

    require(jobs_response.get("total_count") == len(EXPECTED_JOBS), "CI job count differs from the protected release contract")
    jobs = jobs_response.get("jobs")
    require(isinstance(jobs, list) and len(jobs) == len(EXPECTED_JOBS), "CI jobs response is incomplete")
    names: set[str] = set()
    for job in jobs:
        require(isinstance(job, dict), "invalid CI job record")
        name = job.get("name")
        require(isinstance(name, str) and name not in names, "duplicate or invalid CI job name")
        names.add(name)
        require(job.get("status") == "completed" and job.get("conclusion") == "success", f"CI job was not successful: {name}")
        require(job.get("head_sha") == source_revision, f"CI job source mismatch: {name}")
    require(names == EXPECTED_JOBS, "required CI jobs are missing or renamed")

    require(artifacts_response.get("total_count") == len(EXPECTED_ARTIFACTS), "CI artifact count differs from the release contract")
    artifacts = artifacts_response.get("artifacts")
    require(isinstance(artifacts, list) and len(artifacts) == len(EXPECTED_ARTIFACTS), "CI artifacts response is incomplete")
    artifact_names: set[str] = set()
    artifact_records = []
    for artifact in artifacts:
        require(isinstance(artifact, dict), "invalid CI artifact record")
        name = artifact.get("name")
        require(isinstance(name, str) and name not in artifact_names, "duplicate or invalid CI artifact name")
        artifact_names.add(name)
        require(artifact.get("expired") is False, f"CI artifact has expired: {name}")
        require(isinstance(artifact.get("id"), int) and artifact["id"] > 0, f"invalid CI artifact ID: {name}")
        require(isinstance(artifact.get("size_in_bytes"), int) and artifact["size_in_bytes"] > 0, f"empty CI artifact: {name}")
        artifact_digest = artifact.get("digest")
        require(isinstance(artifact_digest, str) and DIGEST.fullmatch(artifact_digest) is not None, f"CI artifact wrapper digest is missing or invalid: {name}")
        workflow_run = artifact.get("workflow_run", {})
        require(workflow_run.get("id") == run_id and workflow_run.get("head_sha") == source_revision, f"CI artifact provenance mismatch: {name}")
        artifact_records.append({"name": name, "id": artifact["id"], "sizeInBytes": artifact["size_in_bytes"], "digest": artifact_digest})
    require(artifact_names == EXPECTED_ARTIFACTS, "required CI artifacts are missing or renamed")

    provenance = {
        "schema": 1,
        "repository": REPOSITORY,
        "workflow": CI_WORKFLOW,
        "workflowId": workflow["id"],
        "runId": run_id,
        "runAttempt": run.get("run_attempt"),
        "event": "push",
        "branch": "main",
        "sourceRevision": source_revision,
        "artifacts": sorted(artifact_records, key=lambda record: record["name"]),
    }
    canonical_json(pathlib.Path(args.output), provenance)
    if args.github_output:
        with pathlib.Path(args.github_output).open("a", encoding="utf-8") as output:
            output.write(f"source_sha={source_revision}\n")
    print(json.dumps(provenance, sort_keys=True))


def download_artifacts(args: argparse.Namespace) -> None:
    provenance = load_json(pathlib.Path(args.run_provenance))
    require(provenance.get("repository") == REPOSITORY, "artifact repository mismatch")
    records = provenance.get("artifacts", [])
    require(len(records) == len(EXPECTED_ARTIFACTS) and {item["name"] for item in records} == EXPECTED_ARTIFACTS, "artifact download inventory mismatch")
    root = pathlib.Path(args.output_dir)
    root.mkdir(parents=True)
    for record in records:
        require(isinstance(record["id"], int) and record["id"] > 0, "invalid artifact ID")
        with tempfile.TemporaryDirectory(prefix="jastreamer-artifact-") as temporary:
            archive = pathlib.Path(temporary) / "artifact.zip"
            with archive.open("wb") as output:
                subprocess.run(["gh", "api", f"repos/{REPOSITORY}/actions/artifacts/{record['id']}/zip"], stdout=output, check=True)
            require(archive.stat().st_size == record["sizeInBytes"], "artifact ZIP size differs from GitHub record")
            require(f"sha256:{sha256_file(archive)}" == record["digest"], "artifact ZIP digest differs from GitHub record")
            destination = root / record["name"]
            destination.mkdir()
            with zipfile.ZipFile(archive) as bundle:
                for member in bundle.infolist():
                    require(member.filename == pathlib.PurePosixPath(member.filename).name and "\\" not in member.filename, "artifact ZIP contains a non-flat path")
                    require(not member.is_dir() and stat.S_IFMT(member.external_attr >> 16) in (0, stat.S_IFREG), "artifact ZIP contains a non-regular entry")
                    with bundle.open(member) as source, (destination / member.filename).open("xb") as output:
                        shutil.copyfileobj(source, output)
            print(f"Verified GitHub artifact {record['id']}: {record['name']}")


def raw_payload(path: pathlib.Path) -> tuple[bytes, dict[str, Any]]:
    regular(path)
    payload = path.read_bytes()
    require(payload, f"empty registry manifest: {path}")
    try:
        value = json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise SystemExit(f"invalid registry manifest in {path}: {error}") from error
    require(isinstance(value, dict), "registry manifest must be an object")
    return payload, value


def validate_descriptor(value: Any, label: str) -> None:
    require(isinstance(value, dict), f"invalid {label} descriptor")
    require(isinstance(value.get("digest"), str) and DIGEST.fullmatch(value["digest"]) is not None, f"invalid {label} digest")
    require(isinstance(value.get("size"), int) and value["size"] >= 0, f"invalid {label} size")


def validate_registry_image(path: pathlib.Path, server_manifest: dict[str, Any], architecture: str) -> dict[str, Any]:
    payload, value = raw_payload(path)
    require(value.get("schemaVersion") == 2, "unsupported registry image manifest schema")
    require(value.get("mediaType") in ("application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json"), "reference is not a single-platform image manifest")
    config = value.get("config")
    validate_descriptor(config, "config")
    require(config["digest"] == server_manifest["image"]["config"]["digest"], f"published {architecture} config digest differs from verified CI image")
    layers = value.get("layers")
    require(isinstance(layers, list) and len(layers) == len(server_manifest["image"]["layers"]), f"published {architecture} layer count differs from verified CI image")
    for index, layer in enumerate(layers):
        validate_descriptor(layer, f"layer {index}")
    return {
        "architecture": architecture,
        "manifestDigest": f"sha256:{hashlib.sha256(payload).hexdigest()}",
        "manifestBytes": len(payload),
        "configDigest": config["digest"],
        "compressedLayers": [{"digest": layer["digest"], "bytes": layer["size"]} for layer in layers],
    }


def validate_index(index_path: pathlib.Path, image_paths: dict[str, pathlib.Path]) -> dict[str, Any]:
    payload, value = raw_payload(index_path)
    require(value.get("schemaVersion") == 2, "unsupported registry index schema")
    require(value.get("mediaType") in ("application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json"), "preview reference is not a multi-platform index")
    manifests = value.get("manifests")
    require(isinstance(manifests, list) and len(manifests) == 2, "published index must contain exactly two manifests")
    found: dict[str, dict[str, Any]] = {}
    for descriptor in manifests:
        validate_descriptor(descriptor, "platform manifest")
        platform = descriptor.get("platform", {})
        require(platform.get("os") == "linux" and platform.get("architecture") in ("amd64", "arm64"), "published index contains an unexpected platform")
        architecture = platform["architecture"]
        require(architecture not in found, f"duplicate published platform: {architecture}")
        child_payload, _ = raw_payload(image_paths[architecture])
        expected_digest = f"sha256:{hashlib.sha256(child_payload).hexdigest()}"
        require(descriptor["digest"] == expected_digest and descriptor["size"] == len(child_payload), f"index descriptor does not identify verified {architecture} manifest bytes")
        found[architecture] = {"digest": expected_digest, "bytes": len(child_payload)}
    require(set(found) == {"amd64", "arm64"}, "published index platform set is incomplete")
    return {"digest": f"sha256:{hashlib.sha256(payload).hexdigest()}", "bytes": len(payload), "platforms": found}


def check_registry_image(args: argparse.Namespace) -> None:
    manifest = ci_artifact.verify_server_artifact(pathlib.Path(args.artifact_dir), args.architecture, args.source_revision)
    result = validate_registry_image(pathlib.Path(args.raw), manifest, args.architecture)
    if args.output:
        canonical_json(pathlib.Path(args.output), result)
    print(json.dumps(result, sort_keys=True))


def check_registry_index(args: argparse.Namespace) -> None:
    result = validate_index(pathlib.Path(args.raw), {"amd64": pathlib.Path(args.amd64_raw), "arm64": pathlib.Path(args.arm64_raw)})
    if args.output:
        canonical_json(pathlib.Path(args.output), result)
    print(json.dumps(result, sort_keys=True))


def registry_missing(args: argparse.Namespace) -> None:
    regular(pathlib.Path(args.error))
    message = pathlib.Path(args.error).read_text(encoding="utf-8", errors="replace").lower()
    denied = ("unauthorized", "forbidden", "denied", "timeout", "connection refused", "tls handshake", "too many requests")
    require(not any(fragment in message for fragment in denied), "registry lookup failed for a reason other than a missing reference")
    require("manifest unknown" in message or "not found" in message, "registry did not explicitly report a missing manifest")
    print("Registry explicitly reported a missing manifest.")


def copy_regular(source: pathlib.Path, destination: pathlib.Path) -> None:
    regular(source)
    require(not destination.exists(), f"duplicate staged release asset: {destination.name}")
    shutil.copyfile(source, destination)


def stage_release(args: argparse.Namespace) -> None:
    validate_identity(args.source_revision, args.release_tag, args.image_tag)
    root = pathlib.Path(args.artifact_root)
    provenance = load_json(pathlib.Path(args.run_provenance))
    require(provenance.get("repository") == REPOSITORY and provenance.get("workflow") == CI_WORKFLOW, "run provenance identity mismatch")
    require(provenance.get("runId") == int(args.run_id) and provenance.get("sourceRevision") == args.source_revision, "run provenance source mismatch")
    require({record.get("name") for record in provenance.get("artifacts", [])} == EXPECTED_ARTIFACTS, "run provenance artifact set mismatch")

    server_manifests = {
        architecture: ci_artifact.verify_server_artifact(root / f"server-image-{architecture}", architecture, args.source_revision)
        for architecture in ("amd64", "arm64")
    }
    desktop_receipts = {
        "windows-x64": ci_artifact.verify_desktop_artifact(root / "jastreamer-desktop-windows-x64", "windows-x64", args.source_revision),
        "linux-amd64": ci_artifact.verify_desktop_artifact(root / "jastreamer-desktop-linux-amd64", "linux-amd64", args.source_revision),
    }
    image_paths = {"amd64": pathlib.Path(args.amd64_raw), "arm64": pathlib.Path(args.arm64_raw)}
    published_images = {
        architecture: validate_registry_image(image_paths[architecture], server_manifests[architecture], architecture)
        for architecture in ("amd64", "arm64")
    }
    index = validate_index(pathlib.Path(args.index_raw), image_paths)

    output = pathlib.Path(args.output_dir)
    require(not output.exists() or not any(output.iterdir()), "release staging directory must be empty")
    output.mkdir(parents=True, exist_ok=True)
    for target in ("windows-x64", "linux-amd64"):
        source = root / f"jastreamer-desktop-{target}"
        archive_name = ci_artifact.DESKTOP_TARGETS[target]["archive"]
        copy_regular(source / archive_name, output / archive_name)
        copy_regular(source / f"{archive_name}.sha256", output / f"{archive_name}.sha256")
        prefix = f"jastreamer-desktop_{VERSION}_{target}"
        copy_regular(source / "manifest.json", output / f"{prefix}.manifest.json")
        copy_regular(source / "verification.json", output / f"{prefix}.verification.json")
    for architecture in ("amd64", "arm64"):
        copy_regular(root / f"server-image-{architecture}" / "manifest.json", output / f"jastreamer-server_{VERSION}_linux-{architecture}.manifest.json")

    server_publication = {
        "schema": 1,
        "product": "jastreamer-server",
        "version": VERSION,
        "releaseTag": args.release_tag,
        "imageTag": args.image_tag,
        "sourceRevision": args.source_revision,
        "registry": f"ghcr.io/{REGISTRY_REPOSITORY}",
        "index": {"reference": f"ghcr.io/{REGISTRY_REPOSITORY}:{args.image_tag}", **index},
        "images": [
            {
                **published_images[architecture],
                "platform": f"linux/{architecture}",
                "reference": f"ghcr.io/{REGISTRY_REPOSITORY}:{args.image_tag}-{architecture}",
                "digestReference": f"ghcr.io/{REGISTRY_REPOSITORY}@{published_images[architecture]['manifestDigest']}",
                "verifiedArchive": server_manifests[architecture]["archive"],
                "uncompressedLayers": server_manifests[architecture]["image"]["layers"],
            }
            for architecture in ("amd64", "arm64")
        ],
        "signed": False,
        "productionQualified": False,
    }
    publication_name = f"jastreamer-server_{args.image_tag}.manifest.json"
    canonical_json(output / publication_name, server_publication)

    release_provenance = {
        "schema": 1,
        "repository": REPOSITORY,
        "releaseTag": args.release_tag,
        "imageTag": args.image_tag,
        "productVersion": VERSION,
        "sourceRevision": args.source_revision,
        "ciRunId": int(args.run_id),
        "ciWorkflow": CI_WORKFLOW,
        "ciArtifacts": provenance["artifacts"],
        "serverPublicationManifest": {"path": publication_name, "sha256": sha256_file(output / publication_name)},
        "desktop": desktop_receipts,
        "prerelease": True,
        "latest": False,
        "productionQualified": False,
    }
    canonical_json(output / "release-provenance.json", release_provenance)
    asset_names = sorted(entry.name for entry in output.iterdir())
    require("SHA256SUMS" not in asset_names, "unexpected checksum file before staging")
    (output / "SHA256SUMS").write_text("".join(f"{sha256_file(output / name)}  {name}\n" for name in asset_names), encoding="ascii")
    print(json.dumps({"assets": asset_names + ["SHA256SUMS"], "index": index, "images": published_images}, sort_keys=True))


def request(url: str, *, token: str | None = None, method: str = "GET", accept: str | None = None) -> tuple[bytes, dict[str, str]]:
    headers = {"User-Agent": "jastreamer-public-release-verifier"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    if accept:
        headers["Accept"] = accept
    try:
        with urllib.request.urlopen(urllib.request.Request(url, headers=headers, method=method), context=ssl.create_default_context(), timeout=30) as response:
            return response.read(), {key.lower(): value for key, value in response.headers.items()}
    except urllib.error.HTTPError as error:
        detail = error.read(512).decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {error.code} from {url}: {detail}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"request failed for {url}: {error.reason}") from error


def hash_public_url(url: str) -> tuple[int, str]:
    request_value = urllib.request.Request(url, headers={"User-Agent": "jastreamer-public-release-verifier"}, method="GET")
    digest = hashlib.sha256()
    size = 0
    try:
        with urllib.request.urlopen(request_value, context=ssl.create_default_context(), timeout=30) as response:
            for chunk in iter(lambda: response.read(1024 * 1024), b""):
                size += len(chunk)
                digest.update(chunk)
    except urllib.error.HTTPError as error:
        detail = error.read(512).decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {error.code} from {url}: {detail}") from error
    except urllib.error.URLError as error:
        raise RuntimeError(f"request failed for {url}: {error.reason}") from error
    return size, digest.hexdigest()


def verify_registry_blob(url: str, token: str, descriptor: dict[str, Any], layer: dict[str, Any] | None = None) -> None:
    headers = {"User-Agent": "jastreamer-public-release-verifier", "Authorization": f"Bearer {token}"}
    digest = hashlib.sha256()
    size = 0
    with tempfile.TemporaryFile() as downloaded:
        with urllib.request.urlopen(urllib.request.Request(url, headers=headers), context=ssl.create_default_context(), timeout=60) as response:
            for chunk in iter(lambda: response.read(1024 * 1024), b""):
                size += len(chunk)
                require(size <= descriptor["size"], "registry blob exceeds declared size")
                digest.update(chunk)
                downloaded.write(chunk)
        require(size == descriptor["size"] and f"sha256:{digest.hexdigest()}" == descriptor["digest"], "registry blob content differs from its descriptor")
        if layer is not None:
            downloaded.seek(0)
            media_type = descriptor.get("mediaType")
            if media_type in ("application/vnd.docker.image.rootfs.diff.tar.gzip", "application/vnd.oci.image.layer.v1.tar+gzip"):
                source = gzip.GzipFile(fileobj=downloaded)
            else:
                require(media_type == "application/vnd.oci.image.layer.v1.tar", f"unsupported registry layer media type: {media_type}")
                source = downloaded
            uncompressed = hashlib.sha256()
            uncompressed_size = 0
            for chunk in iter(lambda: source.read(1024 * 1024), b""):
                uncompressed_size += len(chunk)
                require(uncompressed_size <= layer["bytes"], "registry layer exceeds CI-verified uncompressed size")
                uncompressed.update(chunk)
            require(uncompressed_size == layer["bytes"] and f"sha256:{uncompressed.hexdigest()}" == layer["diffId"], "registry layer differs from CI-verified uncompressed bytes")


def verify_anonymous(args: argparse.Namespace) -> None:
    validate_identity(args.source_revision, args.release_tag, args.image_tag)
    root = pathlib.Path(args.artifact_root)
    server_manifests = {
        architecture: ci_artifact.verify_server_artifact(root / f"server-image-{architecture}", architecture, args.source_revision)
        for architecture in ("amd64", "arm64")
    }
    token_url = "https://ghcr.io/token?" + urllib.parse.urlencode({"scope": f"repository:{REGISTRY_REPOSITORY}:pull", "service": "ghcr.io"})
    try:
        token_value = json.loads(request(token_url)[0]).get("token")
        require(isinstance(token_value, str) and token_value, "GHCR did not issue an anonymous pull token")
        base = f"https://ghcr.io/v2/{REGISTRY_REPOSITORY}"
        index_payload, _ = request(f"{base}/manifests/{urllib.parse.quote(args.image_tag, safe='')}", token=token_value, accept=MANIFEST_ACCEPT)
        index_file = pathlib.Path(args.output_dir) / "anonymous-index.json"
        pathlib.Path(args.output_dir).mkdir(parents=True, exist_ok=True)
        index_file.write_bytes(index_payload)
        image_files: dict[str, pathlib.Path] = {}
        _, index_value = raw_payload(index_file)
        descriptors = index_value.get("manifests")
        require(isinstance(descriptors, list), "anonymous GHCR response is not an image index")
        for descriptor in descriptors:
            validate_descriptor(descriptor, "anonymous platform manifest")
            architecture = descriptor.get("platform", {}).get("architecture")
            require(architecture in ("amd64", "arm64") and architecture not in image_files, "anonymous index platform is invalid")
            image_payload, _ = request(f"{base}/manifests/{descriptor['digest']}", token=token_value, accept=MANIFEST_ACCEPT)
            image_file = pathlib.Path(args.output_dir) / f"anonymous-{architecture}.json"
            image_file.write_bytes(image_payload)
            result = validate_registry_image(image_file, server_manifests[architecture], architecture)
            require(result["manifestDigest"] == descriptor["digest"], f"anonymous {architecture} manifest bytes differ from published index")
            _, image_value = raw_payload(image_file)
            config_descriptor = image_value["config"]
            require(config_descriptor["size"] == server_manifests[architecture]["image"]["config"]["bytes"], "registry config size differs from CI")
            verify_registry_blob(f"{base}/blobs/{config_descriptor['digest']}", token_value, config_descriptor)
            for blob, layer in zip(image_value["layers"], server_manifests[architecture]["image"]["layers"], strict=True):
                verify_registry_blob(f"{base}/blobs/{blob['digest']}", token_value, blob, layer)
            image_files[architecture] = image_file
        require(set(image_files) == {"amd64", "arm64"}, "anonymous GHCR platform set is incomplete")
        result = validate_index(index_file, image_files)
    except (RuntimeError, ValueError, KeyError, json.JSONDecodeError) as error:
        raise SystemExit(
            "Anonymous GHCR verification failed. Newly created GHCR packages default to private and GitHub has no supported package-visibility write API; "
            "the owner must set jastreamer-server to Public in GitHub package settings, then rerun this workflow. "
            f"Underlying error: {error}"
        ) from error
    print(json.dumps({"anonymous": True, "index": result, "platforms": ["linux/amd64", "linux/arm64"]}, sort_keys=True))


def verify_public_release(args: argparse.Namespace) -> None:
    validate_identity(args.source_revision, args.release_tag, args.image_tag)
    release = load_json(pathlib.Path(args.release_json))
    require(release.get("tag_name") == args.release_tag, "public GitHub release tag mismatch")
    require(release.get("target_commitish") == args.source_revision, "public GitHub release source mismatch")
    require(release.get("draft") is False and release.get("prerelease") is True, "GitHub release is not a published prerelease")
    require(release.get("url") == f"https://api.github.com/repos/{REPOSITORY}/releases/{release.get('id')}", "public GitHub release repository mismatch")
    if args.latest_json:
        latest = load_json(pathlib.Path(args.latest_json))
        require(latest.get("id") != release.get("id"), "preview release was incorrectly marked latest")
    staged = pathlib.Path(args.staged)
    expected = {entry.name: entry for entry in staged.iterdir() if entry.is_file()}
    assets = release.get("assets")
    require(isinstance(assets, list) and len(assets) == len(expected), "public GitHub release asset count mismatch")
    seen: set[str] = set()
    for asset in assets:
        name = asset.get("name")
        require(name in expected and name not in seen, "public GitHub release contains an unexpected or duplicate asset")
        seen.add(name)
        require(asset.get("state") == "uploaded" and asset.get("size") == expected[name].stat().st_size, f"public release asset metadata mismatch: {name}")
        url = asset.get("browser_download_url")
        require(isinstance(url, str) and url.startswith(f"https://github.com/{REPOSITORY}/releases/download/{args.release_tag}/"), f"unexpected public asset URL: {name}")
        try:
            downloaded_size, downloaded_digest = hash_public_url(url)
        except RuntimeError as error:
            raise SystemExit(f"public release asset is not anonymously downloadable: {name}: {error}") from error
        require(downloaded_size == expected[name].stat().st_size, f"public release asset download size differs from CI-verified bytes: {name}")
        require(downloaded_digest == sha256_file(expected[name]), f"public release asset bytes differ from CI-verified bytes: {name}")
    require(seen == set(expected), "public GitHub release assets are incomplete")
    print(json.dumps({"publicPrerelease": True, "latest": False, "assets": sorted(seen)}, sort_keys=True))


def check_identity(args: argparse.Namespace) -> None:
    validate_identity(args.source_revision, args.release_tag, args.image_tag)
    print(json.dumps({"sourceRevision": args.source_revision, "releaseTag": args.release_tag, "imageTag": args.image_tag}, sort_keys=True))


def main() -> None:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)

    identity_parser = subparsers.add_parser("validate-identity")
    identity_parser.add_argument("--source-revision", required=True)
    identity_parser.add_argument("--release-tag", required=True)
    identity_parser.add_argument("--image-tag", required=True)
    identity_parser.set_defaults(function=check_identity)

    run_parser = subparsers.add_parser("validate-run")
    for option in ("run", "workflow", "branch", "comparison", "jobs", "artifacts", "repository", "run-id", "output"):
        run_parser.add_argument(f"--{option}", required=True)
    run_parser.add_argument("--github-output")
    run_parser.set_defaults(function=validate_run)

    download_parser = subparsers.add_parser("download-artifacts")
    download_parser.add_argument("--run-provenance", required=True)
    download_parser.add_argument("--output-dir", required=True)
    download_parser.set_defaults(function=download_artifacts)

    image_parser = subparsers.add_parser("validate-registry-image")
    image_parser.add_argument("--raw", required=True)
    image_parser.add_argument("--artifact-dir", required=True)
    image_parser.add_argument("--architecture", choices=("amd64", "arm64"), required=True)
    image_parser.add_argument("--source-revision", required=True)
    image_parser.add_argument("--output")
    image_parser.set_defaults(function=check_registry_image)

    index_parser = subparsers.add_parser("validate-registry-index")
    index_parser.add_argument("--raw", required=True)
    index_parser.add_argument("--amd64-raw", required=True)
    index_parser.add_argument("--arm64-raw", required=True)
    index_parser.add_argument("--output")
    index_parser.set_defaults(function=check_registry_index)

    missing_parser = subparsers.add_parser("require-missing-reference")
    missing_parser.add_argument("--error", required=True)
    missing_parser.set_defaults(function=registry_missing)

    stage_parser = subparsers.add_parser("stage-release")
    for option in ("artifact-root", "run-provenance", "run-id", "source-revision", "release-tag", "image-tag", "amd64-raw", "arm64-raw", "index-raw", "output-dir"):
        stage_parser.add_argument(f"--{option}", required=True)
    stage_parser.set_defaults(function=stage_release)

    anonymous_parser = subparsers.add_parser("verify-anonymous-registry")
    for option in ("artifact-root", "source-revision", "release-tag", "image-tag", "output-dir"):
        anonymous_parser.add_argument(f"--{option}", required=True)
    anonymous_parser.set_defaults(function=verify_anonymous)

    public_parser = subparsers.add_parser("verify-public-release")
    for option in ("release-json", "staged", "source-revision", "release-tag", "image-tag"):
        public_parser.add_argument(f"--{option}", required=True)
    public_parser.add_argument("--latest-json")
    public_parser.set_defaults(function=verify_public_release)

    args = parser.parse_args()
    args.function(args)


if __name__ == "__main__":
    main()
