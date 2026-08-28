import { findUnsafeEvidence } from "../receipt-redaction.mjs";
import { REQUIRED_RUNNER_LABELS } from "./authorization.mjs";

const SHA256 = /^[0-9a-f]{64}$/;
const MAX_AGE_MS = 24 * 60 * 60 * 1000;
const MAX_FUTURE_MS = 5 * 60 * 1000;
export const TONE_DURATION_MIN_MS = 1_900;
export const TONE_DURATION_MAX_MS = 2_100;
const BINDING_KEYS = ["renderer_executable_sha256", "probe_executable_sha256", "scenario_driver_sha256", "server_peer_sha256", "server_peer_input_sha256", "media_fixture_archive_sha256", "media_fixture_manifest_sha256", "source_sha256", "renderer_contract_sha256", "peer_set_sha256", "candidate_sha256", "endpoint_identity_sha256"];
const FORMATS = ["flac", "mp3", "vorbis", "opus", "wav"];
const SCENARIOS = [
  "play-pause-resume-seek-stop", "endpoint-absent", "endpoint-busy",
  "endpoint-invalidated-restored", "duplicate-conflict", "revocation",
  "server-restart", "renderer-restart", "disconnect-before-ack",
  "disconnect-after-ack", "disconnect-before-result", "disconnect-after-result",
  "corrupted-media", "truncated-media", "cleanup",
];
const fail = (code, path) => ({ ok: false, code, path });
const object = (value) => value !== null && typeof value === "object" && !Array.isArray(value);
const digest = (value) => typeof value === "string" && SHA256.test(value);
const exact = (value, keys, path) => {
  if (!object(value)) return fail("FIELD_INVALID", path);
  const missing = keys.find((key) => !(key in value));
  if (missing !== undefined) return fail("FIELD_MISSING", `${path}.${missing}`);
  const unknown = Object.keys(value).find((key) => !keys.includes(key));
  return unknown === undefined ? undefined : fail("FIELD_UNKNOWN", `${path}.${unknown}`);
};
const stale = (recordedAt, now) => {
  const recorded = Date.parse(recordedAt);
  const current = Date.parse(now);
  return !Number.isFinite(recorded) || !Number.isFinite(current) || current - recorded > MAX_AGE_MS || recorded - current > MAX_FUTURE_MS;
};

const validateBinding = (actual, expected, pending) => {
  const shape = exact(actual, BINDING_KEYS, "binding");
  if (shape !== undefined) return shape;
  for (const key of BINDING_KEYS) {
    const endpointPending = pending && key === "endpoint_identity_sha256";
    if (!(endpointPending ? actual[key] === null : digest(actual[key]))) return fail("FIELD_INVALID", `binding.${key}`);
    if (actual[key] !== expected[key]) return fail("DIGEST_MISMATCH", `binding.${key}`);
  }
  return undefined;
};

const validatePending = (receipt) => {
  const keys = ["schema_version", "kind", "recorded_at", "qualification_status", "binding", "network_calls", "audio_mutations", "external_writes", "publication"];
  const shape = exact(receipt, keys, "$");
  if (shape !== undefined) return shape;
  const publication = exact(receipt.publication, ["code", "external_writes"], "publication");
  if (publication !== undefined) return publication;
  if (receipt.network_calls !== 0 || receipt.audio_mutations !== 0 || receipt.external_writes !== 0 || receipt.publication.code !== "PRODUCT_GATE_REQUIRED" || receipt.publication.external_writes !== 0) {
    return fail("PENDING_GATE_MUTATED", "$");
  }
  return undefined;
};

const validateEndpoint = (receipt) => {
  const shape = exact(receipt.endpoint, ["identity_sha256", "data_flow", "capture_mode"], "endpoint");
  if (shape !== undefined) return shape;
  if (receipt.endpoint.capture_mode !== "wasapi_loopback") return fail("ENDPOINT_NOT_LOOPBACK", "endpoint.capture_mode");
  if (receipt.endpoint.data_flow !== "render") return fail("ENDPOINT_NOT_RENDER", "endpoint.data_flow");
  if (!digest(receipt.endpoint.identity_sha256) || receipt.endpoint.identity_sha256 !== receipt.binding.endpoint_identity_sha256) return fail("ENDPOINT_IDENTITY_CHANGED", "endpoint.identity_sha256");
  return undefined;
};

const validateCapture = (capture) => {
  const shape = exact(capture, ["sha256", "encoding", "sample_rate_hz", "channels", "tone", "pause_rms", "post_stop_500ms_rms", "seek"], "capture");
  if (shape !== undefined) return shape;
  if (!digest(capture.sha256)) return fail("CAPTURE_MISSING", "capture.sha256");
  if (capture.encoding !== "normalized_f32le" || capture.sample_rate_hz !== 48_000 || capture.channels !== 2) return fail("CAPTURE_FORMAT_FAILED", "capture");
  const toneShape = exact(capture.tone, ["peak_frequency_hz", "steady_rms", "absolute_max", "duration_ms"], "capture.tone");
  if (toneShape !== undefined) return toneShape;
  if (capture.tone.peak_frequency_hz < 990 || capture.tone.peak_frequency_hz > 1010) return fail("TONE_FREQUENCY_FAILED", "capture.tone.peak_frequency_hz");
  if (capture.tone.steady_rms < 0.17 || capture.tone.steady_rms > 0.19) return fail("TONE_RMS_FAILED", "capture.tone.steady_rms");
  if (capture.tone.absolute_max >= 0.30) return fail("TONE_CLIPPING", "capture.tone.absolute_max");
  if (capture.tone.duration_ms < TONE_DURATION_MIN_MS || capture.tone.duration_ms > TONE_DURATION_MAX_MS) return fail("TONE_DURATION_FAILED", "capture.tone.duration_ms");
  if (capture.pause_rms > 0.001) return fail("PAUSE_SILENCE_FAILED", "capture.pause_rms");
  if (capture.post_stop_500ms_rms > 0.001) return fail("STOP_SILENCE_FAILED", "capture.post_stop_500ms_rms");
  const seekShape = exact(capture.seek, ["requested_position_ms", "dominance_latency_ms", "frequency_hz", "rejected_frequency_hz", "rejection_db"], "capture.seek");
  if (seekShape !== undefined) return seekShape;
  if (capture.seek.requested_position_ms !== 1000 || capture.seek.dominance_latency_ms > 200 || capture.seek.frequency_hz !== 1000 || capture.seek.rejected_frequency_hz !== 440 || capture.seek.rejection_db < 40) return fail("SEEK_FAILED", "capture.seek");
  return undefined;
};

const validateMatrix = (items, expected, path) => {
  if (!Array.isArray(items) || items.length !== expected.length) return fail(path === "formats" ? "FORMAT_MATRIX_INCOMPLETE" : "SCENARIO_MATRIX_INCOMPLETE", path);
  for (const [index, id] of expected.entries()) {
    const item = items[index];
    const keys = path === "formats" ? ["format", "result", "capture_sha256"] : ["id", "result"];
    const shape = exact(item, keys, `${path}[${index}]`);
    if (shape !== undefined) return shape;
    if (item[keys[0]] !== id || item.result !== "passed" || (path === "formats" && !digest(item.capture_sha256))) return fail(path === "formats" ? "FORMAT_MATRIX_INCOMPLETE" : "SCENARIO_MATRIX_INCOMPLETE", `${path}[${index}]`);
  }
  return undefined;
};

const validateQualified = (receipt) => {
  const keys = ["schema_version", "kind", "recorded_at", "qualification_status", "runner_labels", "binding", "endpoint", "capture", "formats", "scenarios", "cleanup"];
  const shape = exact(receipt, keys, "$");
  if (shape !== undefined) return shape;
  if (!Array.isArray(receipt.runner_labels) || receipt.runner_labels.some((label) => typeof label !== "string" || label.trim().length === 0) || new Set(receipt.runner_labels).size !== receipt.runner_labels.length || REQUIRED_RUNNER_LABELS.some((required) => !receipt.runner_labels.includes(required))) return fail("RUNNER_LABELS_MISMATCH", "runner_labels");
  const checks = [validateEndpoint(receipt), validateCapture(receipt.capture), validateMatrix(receipt.formats, FORMATS, "formats"), validateMatrix(receipt.scenarios, SCENARIOS, "scenarios")];
  const issue = checks.find((value) => value !== undefined);
  if (issue !== undefined) return issue;
  const cleanupShape = exact(receipt.cleanup, ["resources_released", "processes_terminated", "temporary_files_removed", "raw_endpoint_retained", "external_writes"], "cleanup");
  if (cleanupShape !== undefined) return cleanupShape;
  if (!receipt.cleanup.resources_released || !receipt.cleanup.processes_terminated || !receipt.cleanup.temporary_files_removed || receipt.cleanup.raw_endpoint_retained || receipt.cleanup.external_writes !== 0) return fail("CLEANUP_INCOMPLETE", "cleanup");
  return undefined;
};

export const validateWindowsAudioReceipt = (receipt, options) => {
  if (!object(receipt) || receipt.schema_version !== 1 || receipt.kind !== "windows_wasapi_qualification") return fail("FIELD_INVALID", "$");
  if (receipt.qualification_status === "qualified" && receipt.endpoint?.capture_mode !== "wasapi_loopback") return fail("ENDPOINT_NOT_LOOPBACK", "endpoint.capture_mode");
  const unsafe = findUnsafeEvidence(receipt);
  if (unsafe !== undefined) return { ok: false, ...unsafe };
  if (stale(receipt.recorded_at, options?.now)) return fail("RECEIPT_STALE", "recorded_at");
  const pending = receipt.qualification_status === "awaiting_external_authorization";
  if (!pending && receipt.qualification_status !== "qualified") return fail("FIELD_INVALID", "qualification_status");
  const shape = pending ? validatePending(receipt) : validateQualified(receipt);
  if (shape !== undefined) return shape;
  const binding = validateBinding(receipt.binding, options?.expectedBinding, pending);
  return binding ?? { ok: true, value: receipt };
};
