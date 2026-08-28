import { createHash } from "node:crypto";
import { basename, join, resolve } from "node:path";
import { fileSha256, readExact, rehashPublicationClosure, resolveInside, stableRead } from "./publication-files";
import { PublicationContractError, parsePublicationManifest, parseVerifiedPublication } from "./publication-parse";
import type { PreparedPublication, PublicationRequest, PublicComponent, SelectedArtifact } from "./publication-types";

const artifactKinds = {
  server: [
    "server-linux-amd64-deb", "server-linux-amd64-rpm", "server-linux-arm64-deb", "server-linux-arm64-rpm",
    "server-windows-amd64-exe", "server-windows-amd64-msi", "server-oci",
  ],
  control: ["control-web", "control-windows", "control-android"],
} as const;

const expectedWorkflow = {
  server: ".github/workflows/server-release.yml",
  control: ".github/workflows/control-release.yml",
} as const;

const expectedNames = (component: PublicComponent, version: string): readonly string[] => {
  switch (component) {
    case "server":
      return [
        `jastreamer-server_${version}_linux_amd64.deb`, `jastreamer-server_${version}_linux_amd64.rpm`,
        `jastreamer-server_${version}_linux_arm64.deb`, `jastreamer-server_${version}_linux_arm64.rpm`,
        `jastreamer-server_${version}_windows_amd64.exe`, `jastreamer-server_${version}_windows_amd64.msi`,
        `jastreamer-server_${version}_linux_amd64-arm64.oci`,
      ];
    case "control":
      return [
        `jastreamer-control_${version}_web.zip`, `jastreamer-control_${version}_windows.msix`,
        `jastreamer-control_${version}_android_universal.apk`,
      ];
    default:
      component satisfies never;
      return [];
  }
};

const versionFromTag = (component: PublicComponent, tag: string): string => {
  const match = new RegExp(`^${component}-v(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)$`).exec(tag);
  if (match === null) throw new PublicationContractError("NON_PROMOTABLE_REF");
  return `${match[1]}.${match[2]}.${match[3]}`;
};

const validateEvent = (request: PublicationRequest, tag: string, revision: string): void => {
  const event = request.event;
  if (event.name !== "push" || event.refType !== "tag" || event.refName !== tag || event.ref !== `refs/tags/${tag}`) throw new PublicationContractError("NON_PROMOTABLE_EVENT");
  if (event.sha !== revision || !/^[0-9a-f]{40}$/.test(event.sha)) throw new PublicationContractError("PUBLICATION_REF_DRIFT");
};

const validateStaging = (prepared: Readonly<{ request: PublicationRequest; revision: string }>, component: PublicComponent, staging: PreparedPublication["candidate"]["staging"]): void => {
  if (staging.repository !== prepared.request.repository || staging.workflowPath !== ".github/workflows/product-qualification-dispatch.yml" || staging.calledWorkflowPath !== expectedWorkflow[component] || staging.calledJob !== component || staging.calledJobResult !== "success" || staging.eventName !== "workflow_dispatch" || staging.headSha !== prepared.revision || staging.callerSha !== prepared.revision || staging.callerRunId !== staging.runId || staging.callerRunAttempt !== staging.runAttempt || !/^refs\/heads\/[A-Za-z0-9._/-]{1,200}$/.test(staging.callerRef) || staging.artifactName !== `${component}-publication-stage-${staging.callerRunId}-${staging.callerRunAttempt}` || staging.artifactAttemptProvenance !== "caller-run+upload-output+embedded-manifest" || !/^[0-9a-f]{64}$/.test(staging.artifactManifestSha256) || !/^[0-9a-f]{64}$/.test(staging.artifactContentManifestSha256)) throw new PublicationContractError("PUBLICATION_RUN_BINDING_INVALID");
};

const selectArtifacts = (input: Readonly<{ request: PublicationRequest; version: string; manifestArtifacts: PreparedPublication["manifest"]["artifacts"]; selection: PreparedPublication["verified"]["selection"] }>): readonly SelectedArtifact[] => {
  const kinds = artifactKinds[input.request.component];
  const names = expectedNames(input.request.component, input.version);
  if (input.manifestArtifacts.length !== kinds.length) throw new PublicationContractError("PUBLICATION_ALLOWLIST_MISMATCH");
  const selected: SelectedArtifact[] = [];
  for (const [index, kind] of kinds.entries()) {
    const artifact = input.manifestArtifacts[index]; const expectedName = names[index];
    if (artifact === undefined || expectedName === undefined || artifact.kind !== kind || basename(artifact.path) !== expectedName) throw new PublicationContractError("PUBLICATION_ALLOWLIST_MISMATCH");
    const verified = input.selection.find((item) => item.kind === kind);
    if (verified === undefined || verified.path !== artifact.path || verified.sha256 !== artifact.sha256) throw new PublicationContractError("PUBLICATION_SELECTION_DRIFT");
    const absolutePath = join(input.request.stageRoot, expectedName);
    selected.push({ ...artifact, name: expectedName, absolutePath, size: readExact(absolutePath, artifact.sha256).byteLength });
  }
  const componentKinds = new Set<string>(kinds);
  if (input.selection.filter((item) => componentKinds.has(item.kind)).length !== kinds.length) throw new PublicationContractError("PUBLICATION_SELECTION_DRIFT");
  return selected;
};

export const preparePublication = (rawRequest: PublicationRequest, receiptKey: Buffer): PreparedPublication => {
  const request: PublicationRequest = { ...rawRequest, stageRoot: resolve(rawRequest.stageRoot), gate: { ...rawRequest.gate, root: resolve(rawRequest.gate.root) }, receiptPath: resolve(rawRequest.receiptPath), ...(rawRequest.dockerConfigRoot === undefined ? {} : { dockerConfigRoot: resolve(rawRequest.dockerConfigRoot) }) };
  const gateReceiptPath = resolveInside(request.gate.root, request.gate.receiptPath);
  const verifiedPath = resolveInside(request.gate.root, request.gate.verifiedPath);
  readExact(gateReceiptPath, request.gate.expectedReceiptSha256);
  const verifiedBytes = stableRead(verifiedPath);
  const verified = parseVerifiedPublication(verifiedBytes);
  if (verified.productGateSha256 !== request.gate.expectedReceiptSha256) throw new PublicationContractError("PRODUCT_GATE_DIGEST_DRIFT");
  if (request.mode === "production" && verified.trust.profile !== "production") throw new PublicationContractError("PRODUCTION_TRUST_REQUIRED");
  if (verified.publication.repository !== request.repository || verified.publication.environment !== request.environment) throw new PublicationContractError("PROTECTED_ENVIRONMENT_REQUIRED");
  const receiptKeyId = createHash("sha256").update(receiptKey).digest("hex");
  if (receiptKeyId !== verified.publication.receiptKeyId) throw new PublicationContractError("PUBLICATION_RECEIPT_KEY_MISMATCH");
  const candidate = verified.publication.candidates[request.component];
  const manifestPath = resolveInside(request.gate.root, candidate.manifest.path);
  const manifestBytes = readExact(manifestPath, candidate.manifest.sha256);
  const manifest = parsePublicationManifest(manifestBytes);
  const version = versionFromTag(request.component, candidate.releaseTag);
  if (manifest.component !== request.component || manifest.tag !== candidate.releaseTag || manifest.artifactSetSha256 !== verified.publication.artifactSetSha256) throw new PublicationContractError("PUBLICATION_MANIFEST_MISMATCH");
  validateEvent(request, candidate.releaseTag, manifest.sourceRevision);
  validateStaging({ request, revision: manifest.sourceRevision }, request.component, candidate.staging);
  const artifacts = selectArtifacts({ request, version, manifestArtifacts: manifest.artifacts, selection: verified.selection });
  if (request.component === "server") {
    const oci = artifacts.find((artifact) => artifact.kind === "server-oci");
    if (oci === undefined || oci.sha256 !== verified.serverOci.artifactSha256 || JSON.stringify(verified.serverOci.platforms) !== JSON.stringify(["linux/amd64", "linux/arm64"]) || verified.serverOci.attestations.length !== 2) throw new PublicationContractError("OCI_PUBLICATION_INVALID");
    if (request.dockerConfigRoot === undefined) throw new PublicationContractError("REGISTRY_AUTH_ROOT_REQUIRED");
  } else if (request.dockerConfigRoot !== undefined) throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  const authorizationFiles = [
    { path: gateReceiptPath, sha256: request.gate.expectedReceiptSha256 },
    { path: verifiedPath, sha256: fileSha256(verifiedPath) },
    { path: manifestPath, sha256: candidate.manifest.sha256 },
  ];
  const prepared: PreparedPublication = { request, verified, candidate, manifest, artifacts, version, authorizationFiles, immutableFiles: [...authorizationFiles, ...artifacts.map((artifact) => ({ path: artifact.absolutePath, sha256: artifact.sha256 }))] };
  rehashPublicationClosure(prepared);
  return prepared;
};
