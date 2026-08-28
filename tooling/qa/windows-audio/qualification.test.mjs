import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { authorizeWindowsAudio, pendingReceipt } from "./authorization.mjs";
import { TONE_DURATION_MAX_MS, TONE_DURATION_MIN_MS, validateWindowsAudioReceipt } from "./receipt.mjs";
import { CAPTURE_SAMPLE_RATE_HZ, TONE_CAPTURE_FRAMES } from "./windows-audio-native-runtime.mjs";

const NOW = "2026-08-26T12:00:00.000Z";
const H = (value) => createHash("sha256").update(value).digest("hex");
const LABELS = ["self-hosted", "windows", "x64", "jastreamer-audio"];
const binding = {
  renderer_executable_sha256: H("renderer.exe"), probe_executable_sha256: H("probe.exe"),
  scenario_driver_sha256: H("driver.mjs"), server_peer_sha256: H("peer.mjs"),
  server_peer_input_sha256: H("peer.json"), media_fixture_archive_sha256: H("media.zip"),
  media_fixture_manifest_sha256: H("manifest.json"), source_sha256: H("source"), renderer_contract_sha256: H("contract"),
  peer_set_sha256: H("peers"), candidate_sha256: H("candidate"),
  endpoint_identity_sha256: H("endpoint-a"),
};
const scenarios = [
  "play-pause-resume-seek-stop", "endpoint-absent", "endpoint-busy",
  "endpoint-invalidated-restored", "duplicate-conflict", "revocation",
  "server-restart", "renderer-restart", "disconnect-before-ack",
  "disconnect-after-ack", "disconnect-before-result", "disconnect-after-result",
  "corrupted-media", "truncated-media", "cleanup",
].map((id) => ({ id, result: "passed" }));
const qualified = () => ({
  schema_version: 1, kind: "windows_wasapi_qualification", recorded_at: NOW,
  qualification_status: "qualified", runner_labels: [...LABELS], binding: { ...binding },
  endpoint: { identity_sha256: H("endpoint-a"), data_flow: "render", capture_mode: "wasapi_loopback" },
  capture: {
    sha256: H("capture"), encoding: "normalized_f32le", sample_rate_hz: 48_000, channels: 2,
    tone: { peak_frequency_hz: 1000, steady_rms: 0.18, absolute_max: 0.251, duration_ms: 2000 },
    pause_rms: 0.0004, post_stop_500ms_rms: 0.0003,
    seek: { requested_position_ms: 1000, dominance_latency_ms: 120, frequency_hz: 1000, rejected_frequency_hz: 440, rejection_db: 44 },
  },
  formats: ["flac", "mp3", "vorbis", "opus", "wav"].map((format) => ({ format, result: "passed", capture_sha256: H(format) })),
  scenarios, cleanup: { resources_released: true, processes_terminated: true, temporary_files_removed: true, raw_endpoint_retained: false, external_writes: 0 },
});
const rejectedAs = (mutate, code) => {
  const receipt = qualified(); mutate(receipt);
  expect(validateWindowsAudioReceipt(receipt, { now: NOW, expectedBinding: binding })).toEqual(expect.objectContaining({ ok: false, code }));
};

describe("Windows audio authorization", () => {
  test("authorizes a protected runner when its unordered label set contains every required label", () => {
    // Given / When
    const result = authorizeWindowsAudio({ labels: ["lab-a", "x64", "windows", "jastreamer-audio", "self-hosted", "group-1"], endpointId: "endpoint-a", endpointIdProtected: true });
    // Then
    expect(result).toEqual({ authorized: true, endpointIdentitySha256: H("endpoint-a") });
    expect(JSON.stringify(result)).not.toContain("endpoint-a");
  });

  test.each([
    ["missing label", ["self-hosted", "windows", "x64"]],
    ["wrong architecture", ["self-hosted", "windows", "arm64", "jastreamer-audio"]],
  ])("denies %s without invoking audio", (_name, labels) => {
    // Given / When
    const result = authorizeWindowsAudio({ labels, endpointId: "endpoint-a", endpointIdProtected: true });
    // Then
    expect(result).toEqual({ authorized: false, code: "WINDOWS_AUDIO_RUNNER_UNAUTHORIZED" });
  });

  test("denies empty, duplicate, missing, or unprotected runner identity", () => {
    expect(authorizeWindowsAudio({ labels: [...LABELS, " "], endpointId: "endpoint-a", endpointIdProtected: true })).toEqual({ authorized: false, code: "WINDOWS_AUDIO_RUNNER_UNAUTHORIZED" });
    expect(authorizeWindowsAudio({ labels: [...LABELS, "windows"], endpointId: "endpoint-a", endpointIdProtected: true })).toEqual({ authorized: false, code: "WINDOWS_AUDIO_RUNNER_UNAUTHORIZED" });
    expect(authorizeWindowsAudio({ labels: LABELS, endpointId: "", endpointIdProtected: true })).toEqual({ authorized: false, code: "WINDOWS_AUDIO_ENDPOINT_ID_REQUIRED" });
    expect(authorizeWindowsAudio({ labels: LABELS, endpointId: "endpoint-a", endpointIdProtected: false })).toEqual({ authorized: false, code: "WINDOWS_AUDIO_ENDPOINT_ID_UNPROTECTED" });
  });

  test("emits a valid pending receipt with no mutation and verified publication denial", () => {
    // Given / When
    const receipt = pendingReceipt({ recordedAt: NOW, binding: { ...binding, endpoint_identity_sha256: null }, publication: { code: "PRODUCT_GATE_REQUIRED", externalWrites: 0 } });
    // Then
    expect(receipt).toEqual(expect.objectContaining({ qualification_status: "awaiting_external_authorization", audio_mutations: 0, network_calls: 0, external_writes: 0 }));
    expect(validateWindowsAudioReceipt(receipt, { now: NOW, expectedBinding: { ...binding, endpoint_identity_sha256: null } }).ok).toBe(true);
  });
});

describe("native WASAPI qualification receipt", () => {
  test("couples exact tone capture frames to the accepted receipt duration", () => {
    const durationMs = TONE_CAPTURE_FRAMES * 1000 / CAPTURE_SAMPLE_RATE_HZ;
    expect(TONE_CAPTURE_FRAMES).toBe(96_000);
    expect(durationMs).toBeGreaterThanOrEqual(TONE_DURATION_MIN_MS);
    expect(durationMs).toBeLessThanOrEqual(TONE_DURATION_MAX_MS);
    expect(qualified().capture.tone.duration_ms).toBe(durationMs);
  });

  test("accepts complete loopback qualification bound to the staged candidate", () => {
    expect(validateWindowsAudioReceipt(qualified(), { now: NOW, expectedBinding: binding }).ok).toBe(true);
  });

  test("accepts qualified evidence with unordered required labels and additional runner labels", () => {
    const receipt = qualified();
    receipt.runner_labels = ["lab-a", "jastreamer-audio", "x64", "self-hosted", "windows", "group-1"];
    expect(validateWindowsAudioReceipt(receipt, { now: NOW, expectedBinding: binding }).ok).toBe(true);
  });

  test.each([
    ["fake endpoint", (r) => { r.endpoint.capture_mode = "fake"; }, "ENDPOINT_NOT_LOOPBACK"],
    ["capture endpoint", (r) => { r.endpoint.data_flow = "capture"; }, "ENDPOINT_NOT_RENDER"],
    ["changed endpoint", (r) => { r.endpoint.identity_sha256 = H("endpoint-b"); }, "ENDPOINT_IDENTITY_CHANGED"],
    ["wrong labels", (r) => { r.runner_labels[3] = "hosted"; }, "RUNNER_LABELS_MISMATCH"],
    ["wrong candidate digest", (r) => { r.binding.candidate_sha256 = H("other"); }, "DIGEST_MISMATCH"],
    ["wrong probe digest", (r) => { r.binding.probe_executable_sha256 = H("other"); }, "DIGEST_MISMATCH"],
    ["wrong driver digest", (r) => { r.binding.scenario_driver_sha256 = H("other"); }, "DIGEST_MISMATCH"],
    ["wrong server peer digest", (r) => { r.binding.server_peer_sha256 = H("other"); }, "DIGEST_MISMATCH"],
    ["wrong server peer input digest", (r) => { r.binding.server_peer_input_sha256 = H("other"); }, "DIGEST_MISMATCH"],
    ["wrong media archive digest", (r) => { r.binding.media_fixture_archive_sha256 = H("other"); }, "DIGEST_MISMATCH"],
    ["wrong media manifest digest", (r) => { r.binding.media_fixture_manifest_sha256 = H("other"); }, "DIGEST_MISMATCH"],
    ["missing capture", (r) => { r.capture.sha256 = ""; }, "CAPTURE_MISSING"],
    ["silence", (r) => { r.capture.tone.steady_rms = 0; }, "TONE_RMS_FAILED"],
    ["clipping", (r) => { r.capture.tone.absolute_max = 0.31; }, "TONE_CLIPPING"],
    ["frequency", (r) => { r.capture.tone.peak_frequency_hz = 950; }, "TONE_FREQUENCY_FAILED"],
    ["duration", (r) => { r.capture.tone.duration_ms = 1800; }, "TONE_DURATION_FAILED"],
    ["pause", (r) => { r.capture.pause_rms = 0.002; }, "PAUSE_SILENCE_FAILED"],
    ["stop", (r) => { r.capture.post_stop_500ms_rms = 0.002; }, "STOP_SILENCE_FAILED"],
    ["seek latency", (r) => { r.capture.seek.dominance_latency_ms = 201; }, "SEEK_FAILED"],
    ["seek rejection", (r) => { r.capture.seek.rejection_db = 39.9; }, "SEEK_FAILED"],
    ["missing format", (r) => { r.formats.pop(); }, "FORMAT_MATRIX_INCOMPLETE"],
    ["missing scenario", (r) => { r.scenarios.pop(); }, "SCENARIO_MATRIX_INCOMPLETE"],
    ["secret", (r) => { r.token = "plaintext"; }, "SECRET_PRESENT"],
    ["orphan evidence", (r) => { r.capture.orphan_sha256 = H("orphan"); }, "FIELD_UNKNOWN"],
  ])("rejects %s", (_name, mutate, code) => rejectedAs(mutate, code));

  test("rejects stale evidence", () => {
    rejectedAs((r) => { r.recorded_at = "2026-08-24T00:00:00.000Z"; }, "RECEIPT_STALE");
  });

  test("ships strict runner and receipt schemas", async () => {
    const [runner, receipt] = await Promise.all([
      readFile(join(import.meta.dir, "runner.schema.json"), "utf8").then(JSON.parse),
      readFile(join(import.meta.dir, "qualification-receipt.schema.json"), "utf8").then(JSON.parse),
    ]);
    expect(runner.properties.required_labels.const).toEqual(LABELS);
    expect(runner.properties.endpoint_environment.const).toBe("JASTREAMER_QA_ENDPOINT_ID");
    expect(receipt.additionalProperties).toBe(false);
  });

  test("provisioning executes the bound driver and rejects caller-made evidence inputs", async () => {
    // Given / When
    const script = await readFile(join(import.meta.dir, "provision.ps1"), "utf8");
    const authorization = script.indexOf("if (-not $authorized)");
    const driver = script.indexOf("& bun $ScenarioDriver");

    // Then
    expect(authorization).toBeGreaterThan(0);
    expect(driver).toBeGreaterThan(authorization);
    expect(script).not.toMatch(/\$ScenarioReceipt|\$CaptureFile|Start-Sleep|cargo (?:build|run)|Invoke-WebRequest/);
    expect(script).toContain("SCENARIO_DRIVER_NOT_EXECUTED");
    expect(script).toContain("RENDERER_DIGEST_MISMATCH");
    expect(script).toContain("PROBE_DIGEST_MISMATCH");
    expect(script).toContain("DRIVER_DIGEST_MISMATCH");
    expect(script).toContain("SERVER_PEER_DIGEST_MISMATCH");
    expect(script).toContain("SERVER_PEER_INPUT_DIGEST_MISMATCH");
    expect(script).toContain("MEDIA_FIXTURE_ARCHIVE_DIGEST_MISMATCH");
    expect(script).toContain("MEDIA_FIXTURE_MANIFEST_DIGEST_MISMATCH");
    expect(script).not.toContain("Remove-Item $temp -Recurse -Force -ErrorAction SilentlyContinue");
  });
});
