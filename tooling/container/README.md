# Server container QA

`tooling/componentctl container build-qa` builds one OCI index for `linux/amd64,linux/arm64` with BuildKit SBOM and provenance attestations. It verifies the index, image configuration and filesystems; executes arm64 natively and amd64 through QEMU on an arm64 host; and replaces the amd64 service through the Synology Compose contract while checking persisted catalog, configuration, and pairing identity.

All externally visible artifacts are staged until every gate passes. A rejected context exits 65 before Buildx runs and leaves existing outputs unchanged.
