#!/usr/bin/env sh
set -eu

release_dir=${1:?usage: verify.sh RELEASE_DIRECTORY}
python3 - "$release_dir" <<'PY'
import hashlib
import json
import pathlib
import re
import stat
import sys
import tarfile


def require(condition, message):
    if not condition:
        raise SystemExit(message)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def read_regular(path):
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode), f"not a regular file: {path.name}")
    return path.read_bytes()


def file_digest(path):
    require(stat.S_ISREG(path.lstat().st_mode), f"not a regular file: {path.name}")
    value = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            value.update(chunk)
    return value.hexdigest()


release = pathlib.Path(sys.argv[1])
require(release.is_dir(), f"release directory not found: {release}")
manifest_bytes = read_regular(release / "manifest.json")
manifest = json.loads(manifest_bytes)
require(manifest.get("schema") == 1, "unsupported release manifest schema")
require(manifest.get("product") == "jastreamer-server", "unexpected release product")
version = manifest.get("version")
require(isinstance(version, str) and re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version), "invalid release version")
require(manifest.get("components", {}).get("server", {}).get("version") == version, "Server component version mismatch")
require(manifest.get("components", {}).get("airplay", {}).get("version") == "0.18.0", "unexpected pyatv version")
require(manifest.get("components", {}).get("ffmpeg", {}).get("version") == "8.1.2", "unexpected FFmpeg version")
require(manifest.get("publication") == "none" and manifest.get("production_qualified") is False, "release must remain unpublished and unqualified")

artifact_name = manifest.get("artifact", "").replace("<version>", version)
require(artifact_name == f"jastreamer-server_{version}_linux_amd64-arm64.oci", "unexpected OCI artifact name")
expected = {
    artifact_name,
    "manifest.json",
    "server.json",
    "LICENSE",
    "THIRD-PARTY-NOTICES.txt",
    "SHA256SUMS",
}
declared_files = {name.replace("<version>", version) for name in manifest.get("release_files", [])}
require(declared_files == expected, "release file manifest mismatch")
require({entry.name for entry in release.iterdir()} == expected, "release directory contains missing or unexpected files")

checksum_lines = read_regular(release / "SHA256SUMS").decode("ascii").splitlines()
checksums = {}
for line in checksum_lines:
    match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9._-]+)", line)
    require(match is not None, "malformed SHA256SUMS entry")
    checksum, name = match.groups()
    require(name not in checksums, f"duplicate SHA256SUMS entry: {name}")
    checksums[name] = checksum
require(set(checksums) == expected - {"SHA256SUMS"}, "SHA256SUMS file list mismatch")
for name, expected_digest in checksums.items():
    require(file_digest(release / name) == expected_digest, f"checksum mismatch: {name}")

artifact = release / artifact_name
with tarfile.open(artifact, mode="r:*") as archive:
    members = {member.name.removeprefix("./"): member for member in archive.getmembers()}

    def archived(name):
        member = members.get(name)
        require(member is not None and member.isfile(), f"OCI archive is missing {name}")
        source = archive.extractfile(member)
        require(source is not None, f"cannot read OCI member {name}")
        return source.read()

    def blob(descriptor):
        identifier = descriptor.get("digest", "")
        require(re.fullmatch(r"sha256:[0-9a-f]{64}", identifier) is not None, "invalid OCI blob digest")
        data = archived("blobs/sha256/" + identifier.removeprefix("sha256:"))
        require("sha256:" + digest(data) == identifier, "OCI blob digest mismatch")
        require(len(data) == descriptor.get("size"), "OCI blob size mismatch")
        return data

    layout = json.loads(archived("oci-layout"))
    require(layout.get("imageLayoutVersion") == "1.0.0", "unsupported OCI layout")
    index = json.loads(archived("index.json"))
    descriptors = index.get("manifests")
    while (
        isinstance(descriptors, list)
        and len(descriptors) == 1
        and descriptors[0].get("mediaType") == "application/vnd.oci.image.index.v1+json"
    ):
        descriptors = json.loads(blob(descriptors[0])).get("manifests")
    require(isinstance(descriptors, list) and len(descriptors) == 2, "OCI index must contain exactly two platform manifests")

    platforms = set()
    source_metadata = set()
    for descriptor in descriptors:
        platform = descriptor.get("platform", {})
        target = (platform.get("os"), platform.get("architecture"))
        require(target not in platforms, f"duplicate OCI platform: {target}")
        platforms.add(target)

        image_manifest = json.loads(blob(descriptor))
        config = json.loads(blob(image_manifest.get("config", {})))
        require((config.get("os"), config.get("architecture")) == target, "OCI descriptor and config platforms differ")

        runtime = config.get("config", {})
        require(runtime.get("Entrypoint") == [manifest["components"]["server"]["entrypoint"]], "unexpected OCI entrypoint")
        require(runtime.get("User") == manifest["runtime_user"], "unexpected OCI runtime user")
        labels = runtime.get("Labels", {})
        require(labels.get("org.opencontainers.image.version") == version, "OCI version label mismatch")
        require(labels.get("org.opencontainers.image.licenses") == "Apache-2.0", "OCI license label mismatch")
        revision = labels.get("org.opencontainers.image.revision", "")
        require(re.fullmatch(r"(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})", revision) is not None, "OCI revision label is not a full object ID")
        require(bool(labels.get("org.opencontainers.image.created")), "OCI created label is missing")
        source_metadata.add((revision, labels["org.opencontainers.image.created"]))

require(platforms == {("linux", "amd64"), ("linux", "arm64")}, "OCI platform set must be linux/amd64 and linux/arm64")
require(len(source_metadata) == 1, "OCI platforms were built from different source metadata")
print(f"Verified {artifact_name}: checksums and OCI platform metadata are complete.")
PY
