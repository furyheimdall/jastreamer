import type { PreparedPublication, ProviderCommand } from "./publication-types";

const skopeoImage = "quay.io/skopeo/stable@sha256:47853bb9fb24202af9110531ebd6e43c5f97701254ca290596640290d17942f4";
const serverImage = "ghcr.io/furyheimdall/jastreamer-server";

const command = (value: Omit<ProviderCommand, "stdin"> & { readonly stdin?: ProviderCommand["stdin"] }): ProviderCommand => ({
  ...value,
  stdin: value.stdin ?? "none",
});

export const publicationCommands = (prepared: PreparedPublication) => {
  const candidate = prepared.candidate.staging;
  const repository = prepared.request.repository;
  const tag = prepared.candidate.releaseTag;
  const titleComponent = prepared.request.component === "server" ? "Server" : "Control";
  const run = command({ id: "candidate-run", phase: "read", argv: ["gh", "api", "--method", "GET", `repos/${repository}/actions/runs/${candidate.runId}`] });
  const artifact = command({ id: "candidate-artifact", phase: "read", argv: ["gh", "api", "--method", "GET", `repos/${repository}/actions/artifacts/${candidate.artifactId}`] });
  const release = command({ id: "release-preflight", phase: "read", argv: ["gh", "api", "--method", "GET", `repos/${repository}/releases/tags/${tag}`] });
  const releaseProbe = command({ id: "release-ownership-probe", phase: "read", argv: ["gh", "api", "--method", "GET", `repos/${repository}/releases/tags/${tag}`] });
  const releaseAssetsProbe = command({ id: "release-assets-probe", phase: "read", argv: ["gh", "api", "--method", "GET", `repos/${repository}/releases/tags/${tag}`] });
  const releaseMarker = `publication-run:${prepared.request.publisherRun.id}:${prepared.request.publisherRun.attempt}`;
  const createRelease = command({ id: "release-create-draft", phase: "write", argv: ["gh", "release", "create", tag, "--repo", repository, "--title", `jastreamer ${titleComponent} ${prepared.version}`, "--notes", releaseMarker, "--draft", "--verify-tag"] });
  const uploadRelease = command({ id: "release-upload-assets", phase: "write", argv: ["gh", "release", "upload", tag, ...prepared.artifacts.map((item) => item.absolutePath), "--repo", repository] });
  const publishRelease = command({ id: "release-publish", phase: "write", argv: ["gh", "release", "edit", tag, "--repo", repository, "--draft=false"] });
  const deleteRelease = command({ id: "release-cleanup", phase: "cleanup", mutates: true, argv: ["gh", "release", "delete", tag, "--repo", repository, "--yes"] });
  return { run, artifact, release, releaseProbe, releaseAssetsProbe, createRelease, uploadRelease, publishRelease, deleteRelease };
};

const skopeo = (prepared: PreparedPublication, value: Readonly<{ readonly id: string; readonly phase: ProviderCommand["phase"]; readonly arguments: readonly string[] }>): ProviderCommand => {
  const dockerRoot = prepared.request.dockerConfigRoot;
  if (dockerRoot === undefined) throw new TypeError("REGISTRY_AUTH_ROOT_REQUIRED");
  return command({
    id: value.id,
    phase: value.phase,
    argv: [
      "docker", "run", "--rm", "--network", "host",
      "-v", `${dockerRoot}/config.json:/auth.json:ro`, "-v", `${prepared.request.stageRoot}:/stage:ro`,
      skopeoImage, ...value.arguments, "--authfile", "/auth.json",
    ],
  });
};

export const registryReferences = (prepared: PreparedPublication): Readonly<{ final: string; temporary: string }> => {
  const run = prepared.request.publisherRun;
  const archiveDigest = prepared.verified.serverOci.artifactSha256;
  return {
    final: `${serverImage}:${prepared.version}`,
    temporary: `${serverImage}-publication/run-${run.id}-${run.attempt}:sha256-${archiveDigest}`,
  };
};

export const registryCommands = (prepared: PreparedPublication) => {
  const references = registryReferences(prepared);
  const oci = prepared.artifacts.find((item) => item.kind === "server-oci");
  if (oci === undefined) throw new TypeError("OCI_PUBLICATION_INVALID");
  const login = command({ id: "registry-login", phase: "read", mutates: true, argv: ["docker", "login", "ghcr.io", "--username", prepared.request.publisherRun.actor, "--password-stdin"], stdin: "github-token" });
  const logout = command({ id: "registry-logout", phase: "read", mutates: true, argv: ["docker", "logout", "ghcr.io"] });
  const listTags = skopeo(prepared, { id: "registry-preflight", phase: "read", arguments: ["list-tags", `docker://${serverImage}`] });
  const inspectTag = (tag: string): ProviderCommand => skopeo(prepared, { id: `registry-prior-${tag}`, phase: "read", arguments: ["inspect", "--format", "{{.Digest}}", `docker://${serverImage}:${tag}`] });
  const copyTemporary = skopeo(prepared, { id: "registry-copy-temporary", phase: "write", arguments: ["copy", "--all", `oci-archive:/stage/${oci.name}`, `docker://${references.temporary}`] });
  const inspectTemporary = skopeo(prepared, { id: "registry-inspect-temporary", phase: "read", arguments: ["inspect", "--format", "{{.Digest}}", `docker://${references.temporary}`] });
  const copyFinal = skopeo(prepared, { id: "registry-copy-final", phase: "write", arguments: ["copy", "--all", `docker://${references.temporary}`, `docker://${references.final}`] });
  const inspectFinal = skopeo(prepared, { id: "registry-inspect-final", phase: "read", arguments: ["inspect", "--format", "{{.Digest}}", `docker://${references.final}`] });
  const deleteTemporary = { ...skopeo(prepared, { id: "registry-cleanup-temporary", phase: "cleanup", arguments: ["delete", `docker://${references.temporary}`] }), mutates: true as const };
  const deleteFinal = { ...skopeo(prepared, { id: "registry-cleanup-final", phase: "cleanup", arguments: ["delete", `docker://${references.final}`] }), mutates: true as const };
  return { references, login, logout, listTags, inspectTag, copyTemporary, inspectTemporary, copyFinal, inspectFinal, deleteTemporary, deleteFinal };
};
