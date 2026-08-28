import { createHash } from "node:crypto";
import { findUnsafeEvidence } from "./receipt-redaction.mjs";
import { REQUIRED_RECEIPTS } from "./synthetic-bundle.mjs";
import { validatePhysicalQualification } from "./k17/receipt.mjs";
import { compileReceiptSchemas, validateInstalledProductReceipt } from "./task19/product-e2e-receipt.mjs";

const SHA256 = /^[0-9a-f]{64}$/;
const REVISION = /^[0-9a-f]{40}$/;
const RUN_ID = /^[a-z0-9][a-z0-9-]{2,63}$/;
const MAX_AGE_MS = 24 * 60 * 60 * 1_000;
const MAX_FUTURE_MS = 5 * 60 * 1_000;
const RECEIPT_KINDS = new Set(REQUIRED_RECEIPTS);
const EVIDENCE_MODES = new Set(["real", "contract_fixture", "pending"]);
const digest = (value) => createHash("sha256").update(JSON.stringify(value)).digest("hex");
const failure = (code, path) => ({ ok: false, code, path });
const isObject = (value) => value !== null && typeof value === "object" && !Array.isArray(value);

const exactObject = (value, keys, path) => {
  if (!isObject(value)) return failure("FIELD_INVALID", path);
  for (const key of keys) if (!(key in value)) return failure("FIELD_MISSING", `${path}.${key}`);
  const unknown = Object.keys(value).find((key) => !keys.includes(key));
  return unknown === undefined ? undefined : failure("FIELD_UNKNOWN", `${path}.${unknown}`);
};

const validDigest = (value) => typeof value === "string" && SHA256.test(value);
const validDate = (value, now, path) => {
  if (typeof value !== "string") return failure("FIELD_INVALID", path);
  const recorded = Date.parse(value);
  const current = Date.parse(now);
  if (!Number.isFinite(recorded) || !Number.isFinite(current)) return failure("FIELD_INVALID", path);
  if (current - recorded > MAX_AGE_MS || recorded - current > MAX_FUTURE_MS) return failure("RECEIPT_STALE", path);
  return undefined;
};

const validateBinding = (binding, expected, path) => {
  const keys = ["sourceSha256", "artifactSetSha256", "controlContractSha256", "rendererContractSha256", "peerSetSha256"];
  const shape = exactObject(binding, keys, path);
  if (shape !== undefined) return shape;
  for (const key of keys) {
    if (!validDigest(binding[key])) return failure("FIELD_INVALID", `${path}.${key}`);
    if (binding[key] !== expected[key]) return failure("DIGEST_MISMATCH", `${path}.${key}`);
  }
  return undefined;
};

const validateArtifactList = (payload, expected, path) => {
  const shape = exactObject(payload, ["artifactSetSha256", "artifacts"], path);
  if (shape !== undefined) return shape;
  if (!Array.isArray(payload.artifacts) || payload.artifacts.length !== 3) return failure("FIELD_INVALID", `${path}.artifacts`);
  for (const [index, artifact] of payload.artifacts.entries()) {
    const itemPath = `${path}.artifacts[${index}]`;
    const itemShape = exactObject(artifact, ["component", "sha256"], itemPath);
    if (itemShape !== undefined) return itemShape;
    if (!["server", "control", "renderer"].includes(artifact.component) || !validDigest(artifact.sha256)) return failure("FIELD_INVALID", itemPath);
  }
  if (digest(payload.artifacts) !== expected || payload.artifactSetSha256 !== expected) return failure("DIGEST_MISMATCH", `${path}.artifactSetSha256`);
  return undefined;
};

const validateScenarioPayload = (payload, expected, path) => {
  const shape = exactObject(payload, ["artifactSha256", "startupOrders", "scenarios"], path);
  if (shape !== undefined) return shape;
  if (payload.artifactSha256 !== expected) return failure("DIGEST_MISMATCH", `${path}.artifactSha256`);
  if (!Array.isArray(payload.startupOrders) || payload.startupOrders.join(",") !== "server_first,control_first") return failure("FIELD_INVALID", `${path}.startupOrders`);
  if (!Array.isArray(payload.scenarios) || payload.scenarios.length === 0) return failure("FIELD_INVALID", `${path}.scenarios`);
  for (const [index, scenario] of payload.scenarios.entries()) {
    const scenarioPath = `${path}.scenarios[${index}]`;
    const scenarioShape = exactObject(scenario, ["id", "result"], scenarioPath);
    if (scenarioShape !== undefined) return scenarioShape;
    if (!RUN_ID.test(scenario.id) || scenario.result !== "passed") return failure("FIELD_INVALID", scenarioPath);
  }
  return undefined;
};

const validateK17 = (payload, expected, options, path) => {
  const result = validatePhysicalQualification(payload, {
    now: options?.now,
    artifactSha256: expected.artifactSetSha256,
    runnerLabel: options?.k17RunnerLabel ?? payload?.runnerLabel,
  });
  return result.ok ? undefined : failure(result.code, `${path}.${result.path}`);
};

const validateWasapi = (payload, path) => {
  const keys = ["endpointIdentitySha256", "captureSha256", "sampleRateHz", "channels", "toneFrequencyHz", "durationMs", "seekMarkersObserved"];
  const shape = exactObject(payload, keys, path);
  if (shape !== undefined) return shape;
  if (!validDigest(payload.endpointIdentitySha256) || !validDigest(payload.captureSha256)) return failure("FIELD_INVALID", path);
  if (payload.sampleRateHz !== 48_000 || payload.channels !== 2 || payload.toneFrequencyHz !== 1_000 || payload.durationMs !== 2_000 || payload.seekMarkersObserved < 3) return failure("FIELD_INVALID", path);
  return undefined;
};

const validateFfmpeg = (payload, path) => {
  const shape = exactObject(payload, ["executableSha256", "version", "decoders", "encoder", "probeStatus"], path);
  if (shape !== undefined) return shape;
  const versionShape = exactObject(payload.version, ["major", "minor", "patch"], `${path}.version`);
  if (versionShape !== undefined) return versionShape;
  const requiredDecoders = ["flac", "mp3", "vorbis", "opus", "pcm_s16le"];
  if (!validDigest(payload.executableSha256) || !requiredDecoders.every((codec) => payload.decoders?.includes(codec)) || payload.encoder !== "pcm_s16be" || payload.probeStatus !== "passed") return failure("FIELD_INVALID", path);
  return undefined;
};

const validatePending = (payload, path) => {
  const shape = exactObject(payload, ["gate", "qualificationStatus", "networkCalls", "audioMutations", "publicationEligible"], path);
  if (shape !== undefined) return shape;
  if (!["k17", "wasapi"].includes(payload.gate) || payload.qualificationStatus !== "awaiting_external_authorization" || payload.networkCalls !== 0 || payload.audioMutations !== 0 || payload.publicationEligible !== false) return failure("FIELD_INVALID", path);
  return undefined;
};

const validateCleanup = (payload, path) => {
  const shape = exactObject(payload, ["resourcesReleased", "processesTerminated", "temporaryDirectoriesRemoved", "externalWrites"], path);
  if (shape !== undefined) return shape;
  if (payload.resourcesReleased !== true || payload.processesTerminated !== true || payload.temporaryDirectoriesRemoved !== true || payload.externalWrites !== 0) return failure("CLEANUP_INCOMPLETE", path);
  return undefined;
};

const validatePayload = (receipt, expected, options, path) => {
  switch (receipt.kind) {
    case "candidate": return validateArtifactList(receipt.payload, expected.artifactSetSha256, path);
    case "server_control_e2e": return validateScenarioPayload(receipt.payload, expected.artifactSetSha256, path);
    case "k17": return validateK17(receipt.payload, expected, options, path);
    case "wasapi": return validateWasapi(receipt.payload, path);
    case "ffmpeg": return validateFfmpeg(receipt.payload, path);
    case "external_authorization_pending": return validatePending(receipt.payload, path);
    case "cleanup": return validateCleanup(receipt.payload, path);
    default: return failure("RECEIPT_KIND_UNKNOWN", `${path}.kind`);
  }
};

export const validateProductBundle = (input, options) => {
  const schemaIssues = compileReceiptSchemas().product(input);
  options?.schemaObserver?.(schemaIssues);
  if (input?.purpose === "qualification" && input?.installedProductReceipt === undefined) return failure("INSTALLED_PRODUCT_RECEIPT_REQUIRED", "installedProductReceipt");
  const unsafe = findUnsafeEvidence(input);
  if (unsafe !== undefined) return { ok: false, ...unsafe };
  const bundleKeys = ["schemaVersion", "kind", "purpose", "recordedAt", "runId", "source", "contracts", "peers", "requiredReceipts", "receipts"];
  if (isObject(input) && "installedProductReceipt" in input) bundleKeys.push("installedProductReceipt");
  const shape = exactObject(input, bundleKeys, "$" );
  if (shape !== undefined) return shape;
  if (input.schemaVersion !== 1 || input.kind !== "product_qa_bundle" || !["validator_fixture", "qualification"].includes(input.purpose) || !RUN_ID.test(input.runId)) return failure("FIELD_INVALID", "$");
  if (options?.profile === "qualification" && input.purpose !== "qualification") return failure("NON_REAL_EVIDENCE", "purpose");
  if (options?.profile === "qualification" && (typeof options.k17RunnerLabel !== "string" || options.k17RunnerLabel === "")) return failure("K17_RUNNER_LABEL_REQUIRED", "k17RunnerLabel");
  if (input.purpose === "qualification" && input.installedProductReceipt === undefined) return failure("INSTALLED_PRODUCT_RECEIPT_REQUIRED", "installedProductReceipt");
  const dateIssue = validDate(input.recordedAt, options?.now, "recordedAt");
  if (dateIssue !== undefined) return dateIssue;
  const sourceShape = exactObject(input.source, ["revision", "sha256"], "source");
  if (sourceShape !== undefined) return sourceShape;
  const contractsShape = exactObject(input.contracts, ["controlSha256", "rendererSha256"], "contracts");
  if (contractsShape !== undefined) return contractsShape;
  if (!REVISION.test(input.source.revision) || !validDigest(input.source.sha256) || !validDigest(input.contracts.controlSha256) || !validDigest(input.contracts.rendererSha256)) return failure("FIELD_INVALID", "source");
  if (!Array.isArray(input.peers) || input.peers.length === 0) return failure("FIELD_INVALID", "peers");
  for (const [index, peer] of input.peers.entries()) {
    const peerShape = exactObject(peer, ["component", "sha256"], `peers[${index}]`);
    if (peerShape !== undefined) return peerShape;
    if (typeof peer.component !== "string" || !validDigest(peer.sha256)) return failure("FIELD_INVALID", `peers[${index}]`);
  }
  if (!Array.isArray(input.requiredReceipts)) return failure("FIELD_INVALID", "requiredReceipts");
  const unknownRequired = input.requiredReceipts.find((kind) => !RECEIPT_KINDS.has(kind));
  if (unknownRequired !== undefined) return failure("RECEIPT_KIND_UNKNOWN", "requiredReceipts");
  if (!Array.isArray(input.receipts)) return failure("FIELD_INVALID", "receipts");
  const kinds = input.receipts.map((receipt) => receipt?.kind);
  if (REQUIRED_RECEIPTS.some((kind) => !kinds.includes(kind)) || REQUIRED_RECEIPTS.some((kind) => !input.requiredReceipts.includes(kind))) return failure("RECEIPT_MISSING", "receipts");
  if (new Set(kinds).size !== kinds.length || new Set(input.requiredReceipts).size !== input.requiredReceipts.length) return failure("FIELD_INVALID", "receipts");
  const candidate = input.receipts.find((receipt) => receipt?.kind === "candidate");
  const artifactSetSha256 = candidate?.payload?.artifactSetSha256;
  const expected = {
    sourceSha256: input.source.sha256,
    artifactSetSha256,
    controlContractSha256: input.contracts.controlSha256,
    rendererContractSha256: input.contracts.rendererSha256,
    peerSetSha256: digest(input.peers),
  };
  if (!validDigest(artifactSetSha256)) return failure("FIELD_INVALID", "receipts.candidate.payload.artifactSetSha256");
  for (const [index, receipt] of input.receipts.entries()) {
    const path = `receipts[${index}]`;
    const receiptShape = exactObject(receipt, ["schemaVersion", "kind", "recordedAt", "runId", "binding", "evidenceMode", "payload"], path);
    if (receiptShape !== undefined) return receiptShape;
    if (receipt.schemaVersion !== 1 || !RECEIPT_KINDS.has(receipt.kind) || receipt.runId !== input.runId || !EVIDENCE_MODES.has(receipt.evidenceMode)) return failure("FIELD_INVALID", path);
    const pendingLane = receipt.kind === "external_authorization_pending";
    if (pendingLane !== (receipt.evidenceMode === "pending")) return failure("FIELD_INVALID", `${path}.evidenceMode`);
    if (options?.profile === "qualification" && receipt.evidenceMode !== "real") return failure("NON_REAL_EVIDENCE", `${path}.evidenceMode`);
    const receiptDate = validDate(receipt.recordedAt, options?.now, `${path}.recordedAt`);
    if (receiptDate !== undefined) return receiptDate;
    const bindingIssue = validateBinding(receipt.binding, expected, `${path}.binding`);
    if (bindingIssue !== undefined) return bindingIssue;
    const payloadIssue = validatePayload(receipt, expected, options, `${path}.payload`);
    if (payloadIssue !== undefined) return payloadIssue;
  }
  if (input.installedProductReceipt !== undefined) {
    const installed = validateInstalledProductReceipt(input.installedProductReceipt, {
      now: options?.now,
      root: options?.installedProductRoot,
      trustedCandidates: options?.trustedCandidates,
      harnessTrust: options?.harnessTrust,
      readObserver: options?.readObserver,
      expectedBindings: expected,
    });
    if (!installed.ok) return failure(installed.code, `installedProductReceipt.${installed.path}`);
  }
  if (schemaIssues.length !== 0) return failure("SCHEMA_INVALID", schemaIssues[0].path);
  return { ok: true, value: input };
};
