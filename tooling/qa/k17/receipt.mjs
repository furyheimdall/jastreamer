import { createHash } from "node:crypto";
import { isIP } from "node:net";

const SHA256 = /^[0-9a-f]{64}$/;
const MAX_AGE_MS = 24 * 60 * 60 * 1_000;
const REQUIRED_FORMATS = ["flac", "mp3", "vorbis", "opus", "wav", "l16-fallback"];
const MIMES_BY_FORMAT = new Map([
  ["flac", ["audio/flac"]],
  ["mp3", ["audio/mpeg"]],
  ["vorbis", ["audio/ogg", "audio/vorbis"]],
  ["opus", ["audio/ogg", "audio/opus"]],
  ["wav", ["audio/wav", "audio/x-wav"]],
  ["l16-fallback", ["audio/L16"]],
]);
const digest = (value) => createHash("sha256").update(JSON.stringify(value)).digest("hex");
const fail = (code, path) => ({ ok: false, code, path });
const object = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
const containsIpAddress = (value) => {
  const direct = value.replace(/^\[|\]$/g, "").split("%", 1)[0];
  if (isIP(direct) !== 0) return true;
  for (const match of value.matchAll(/\b(?:\d{1,3}\.){3}\d{1,3}\b/g)) {
    if (isIP(match[0]) === 4) return true;
  }
  for (const match of value.matchAll(/\[([0-9a-f:.]+(?:%[a-z0-9_.-]+)?)\]/gi)) {
    if (isIP(match[1].split("%", 1)[0]) === 6) return true;
  }
  return false;
};
const hasRawIdentity = (value) => {
  if (typeof value === "string") {
    return /\buuid:/i.test(value) || containsIpAddress(value);
  }
  if (Array.isArray(value)) return value.some(hasRawIdentity);
  return object(value) && Object.entries(value).some(([key, item]) => /^(?:udn|ip|address|location)$/i.test(key) || hasRawIdentity(item));
};
const exact = (value, keys, path) => {
  if (!object(value)) return fail("FIELD_INVALID", path);
  const missing = keys.find((key) => !(key in value));
  if (missing !== undefined) return fail("FIELD_MISSING", `${path}.${missing}`);
  const unknown = Object.keys(value).find((key) => !keys.includes(key));
  return unknown === undefined ? undefined : fail("FIELD_UNKNOWN", `${path}.${unknown}`);
};
const protocolSupports = (protocols, mime) => protocols.some((entry) => {
  const parts = entry.split(":");
  if (parts.length < 4 || (parts[0] !== "http-get" && parts[0] !== "*")) return false;
  const advertised = parts[2]?.split(";")[0];
  return advertised === mime || advertised === "audio/*" || advertised === "*";
});

export const validateQualificationReceipt = (receipt, expected) => {
  const baseKeys = ["schema_version", "kind", "recorded_at", "candidate_sha256", "qualification_status"];
  if (!object(receipt)) return fail("FIELD_INVALID", "$");
  if (receipt.schema_version !== 1 || receipt.kind !== "k17_qualification" || receipt.candidate_sha256 !== expected.candidateSha256) return fail("ARTIFACT_MISMATCH", "candidate_sha256");
  const age = Date.parse(expected.now) - Date.parse(receipt.recorded_at);
  if (!Number.isFinite(age) || age < -5 * 60 * 1_000 || age > MAX_AGE_MS) return fail("RECEIPT_STALE", "recorded_at");
  if (receipt.qualification_status === "awaiting_external_authorization") {
    const shape = exact(receipt, [...baseKeys, "network_calls", "audio_mutations", "publication"], "$");
    if (shape !== undefined) return shape;
    const calls = receipt.network_calls;
    if (!object(calls) || calls.ssdpControl !== 0 || calls.soap !== 0 || calls.media !== 0 || Object.keys(calls).length !== 3 || receipt.audio_mutations !== 0) return fail("UNAUTHORIZED_NETWORK_ACTIVITY", "network_calls");
    if (receipt.publication?.code !== "PRODUCT_GATE_REQUIRED" || receipt.publication?.external_writes !== 0 || Object.keys(receipt.publication).length !== 2) return fail("PUBLICATION_DENIAL_UNVERIFIED", "publication");
    return { ok: true, value: receipt };
  }
  if (receipt.qualification_status === "qualified") {
    const shape = exact(receipt, [...baseKeys, "physical"], "$");
    if (shape !== undefined) return shape;
    return validatePhysicalQualification(receipt.physical, { now: expected.now, artifactSha256: expected.candidateSha256, runnerLabel: expected.runnerLabel });
  }
  return fail("FIELD_INVALID", "qualification_status");
};

export const validatePhysicalQualification = (receipt, expected) => {
  if (hasRawIdentity(receipt)) return fail("IDENTITY_UNREDACTED", "$");
  const keys = ["evidenceSource", "artifactSha256", "identitySha256", "model", "firmware", "runnerLabel", "protocolInfo", "protocolInfoSha256", "representations", "transport", "lifecycle", "externalOverride", "network", "audioProof", "cleanup", "recordedAt"];
  const shape = exact(receipt, keys, "$" );
  if (shape !== undefined) return shape;
  if (receipt.evidenceSource !== "physical") return fail("EMULATOR_ONLY", "evidenceSource");
  if (receipt.artifactSha256 !== expected.artifactSha256) return fail("ARTIFACT_MISMATCH", "artifactSha256");
  if (!SHA256.test(receipt.identitySha256) || receipt.model !== "FiiO K17") return fail("IDENTITY_REJECTED", "model");
  if (!Number.isInteger(receipt.firmware) || receipt.firmware < 261) return fail("FIRMWARE_REJECTED", "firmware");
  if (receipt.runnerLabel !== expected.runnerLabel) return fail("RUNNER_LABEL_MISMATCH", "runnerLabel");
  const age = Date.parse(expected.now) - Date.parse(receipt.recordedAt);
  if (!Number.isFinite(age) || age < -5 * 60 * 1_000 || age > MAX_AGE_MS) return fail("RECEIPT_STALE", "recordedAt");
  if (!Array.isArray(receipt.protocolInfo) || receipt.protocolInfo.length === 0 || receipt.protocolInfoSha256 !== digest(receipt.protocolInfo)) return fail("PROTOCOL_INFO_INCOMPATIBLE", "protocolInfo");
  if (!Array.isArray(receipt.representations) || REQUIRED_FORMATS.some((format) => !receipt.representations.some((item) => item?.format === format))) return fail("REPRESENTATION_PROOF_MISSING", "representations");
  for (const item of receipt.representations) {
    const representationShape = exact(item, ["format", "advertised", "selected", "audioProofSha256"], "representations[]");
    if (representationShape !== undefined || !REQUIRED_FORMATS.includes(item.format) || !SHA256.test(item.audioProofSha256)) return fail("REPRESENTATION_PROOF_MISSING", "representations");
    const mimes = MIMES_BY_FORMAT.get(item.format);
    if (mimes === undefined) return fail("PROTOCOL_INFO_INCOMPATIBLE", "protocolInfo");
    if (item.format === "l16-fallback") {
      if (item.selected !== "l16" || !protocolSupports(receipt.protocolInfo, "audio/L16")) return fail("REPRESENTATION_PROOF_MISSING", "representations");
    } else if (item.advertised) {
      if (item.selected !== "original" || !mimes.some((mime) => protocolSupports(receipt.protocolInfo, mime))) return fail("PROTOCOL_INFO_INCOMPATIBLE", "protocolInfo");
    } else if (item.selected !== "l16" || !protocolSupports(receipt.protocolInfo, "audio/L16")) {
      return fail("REPRESENTATION_PROOF_MISSING", "representations");
    }
  }
  if (!SHA256.test(receipt.audioProof?.captureSha256) || receipt.audioProof?.method !== "automated_capture" || receipt.audioProof?.manualListening !== false) return fail("AUDIO_PROOF_MISSING", "audioProof");
  if (receipt.transport?.pause !== "passed" || receipt.transport?.seek !== "passed" || receipt.transport?.stop !== "passed" || receipt.transport?.naturalEndCount !== 1) return fail("TRANSPORT_PROOF_MISSING", "transport");
  if (receipt.lifecycle?.disappearance !== "passed" || receipt.lifecycle?.reappearance !== "passed") return fail("LIFECYCLE_PROOF_MISSING", "lifecycle");
  if (receipt.externalOverride?.observed !== true || receipt.externalOverride?.adopted !== false) return fail("EXTERNAL_OVERRIDE_VIOLATION", "externalOverride");
  if (receipt.network?.expiredUrlRejected !== true) return fail("EXPIRED_URL_ACCEPTED", "network.expiredUrlRejected");
  if (receipt.network?.https !== "passed" || receipt.network?.explicitMediaOnlyHttp !== "passed" || receipt.network?.privateNetworkOnly !== true || receipt.network?.hostileLocationRejected !== true || receipt.network?.redirectsRejected !== true) return fail("NETWORK_RESTRICTION_FAILED", "network");
  if (receipt.cleanup?.rawIdentityRetained !== false || receipt.cleanup?.firmwareMutated !== false || receipt.cleanup?.resourcesReleased !== true || receipt.cleanup?.processesTerminated !== true) return fail("CLEANUP_INCOMPLETE", "cleanup");
  return { ok: true, value: receipt };
};
