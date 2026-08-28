import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { parseSupportMatrix, runAuthorizationGate } from "./authorization.mjs";
import { validatePhysicalQualification, validateQualificationReceipt } from "./receipt.mjs";
import {
  EMULATOR_SCENARIOS,
  EMULATOR_SCENARIO_TESTS,
  runEmulatorMatrix,
} from "./emulator.mjs";

const NOW = "2026-08-26T12:00:00.000Z";
const sha = (value) => createHash("sha256").update(JSON.stringify(value)).digest("hex");
const protocols = [
  "http-get:*:audio/flac:*",
  "http-get:*:audio/mpeg:*",
  "http-get:*:audio/ogg:*",
  "http-get:*:audio/wav:*",
  "http-get:*:audio/L16;rate=44100;channels=2:*",
];
const representations = [
  ["flac", "original"], ["mp3", "original"], ["vorbis", "original"],
  ["opus", "original"], ["wav", "original"], ["l16-fallback", "l16"],
].map(([format, selected], index) => ({ format, advertised: true, selected, audioProofSha256: String(index + 1).repeat(64) }));

const physicalReceipt = () => ({
  evidenceSource: "physical",
  artifactSha256: "a".repeat(64),
  identitySha256: "b".repeat(64),
  model: "FiiO K17",
  firmware: 261,
  runnerLabel: "jastreamer-k17-lab-a",
  protocolInfo: [...protocols],
  protocolInfoSha256: sha(protocols),
  representations: representations.map((item) => ({ ...item })),
  transport: { pause: "passed", seek: "passed", stop: "passed", naturalEndCount: 1 },
  lifecycle: { disappearance: "passed", reappearance: "passed" },
  externalOverride: { observed: true, adopted: false },
  network: { https: "passed", explicitMediaOnlyHttp: "passed", privateNetworkOnly: true, hostileLocationRejected: true, redirectsRejected: true, expiredUrlRejected: true },
  audioProof: { captureSha256: "c".repeat(64), method: "automated_capture", manualListening: false },
  cleanup: { rawIdentityRetained: false, firmwareMutated: false, resourcesReleased: true, processesTerminated: true },
  recordedAt: NOW,
});

const rejectedAs = (mutate, code) => {
  const receipt = physicalReceipt();
  mutate(receipt);
  expect(validatePhysicalQualification(receipt, { now: NOW, artifactSha256: "a".repeat(64), runnerLabel: "jastreamer-k17-lab-a" })).toEqual(expect.objectContaining({ ok: false, code }));
};

describe("K17 emulator qualification", () => {
  test("reports the deterministic candidate matrix after the exact Go test process exits", async () => {
    // Given
    const stdout = Object.values(EMULATOR_SCENARIO_TESTS)
      .flat()
      .map((name) => JSON.stringify({ Action: "pass", Test: name }))
      .join("\n");
    const exited = Promise.resolve(0);
    let seenCommand;
    const spawn = (command, options) => {
      seenCommand = command;
      return {
        exited,
        stdout: new Response(`${stdout}\n`).body,
        stderr: new Response("").body,
        command,
        options,
      };
    };

    // When
    const result = await runEmulatorMatrix({ spawn, serverDirectory: "apps/server", candidateSha256: "a".repeat(64) });

    // Then
    expect(result).toEqual({ ok: true, receipt: {
      schema_version: 1,
      kind: "k17_emulator_matrix",
      candidate_sha256: "a".repeat(64),
      status: "passed",
      scenarios: EMULATOR_SCENARIOS.map((id) => ({
        id,
        result: "passed",
        tests: EMULATOR_SCENARIO_TESTS[id],
      })),
      external_device_calls: 0,
    } });
    expect(seenCommand).toEqual([
      "go", "test", "-json", "-race", "-shuffle=on", "-count=1",
      "./internal/upnp", "./internal/media", "./internal/playback", "./internal/api",
    ]);
  });

  test("rejects a green Go process when any mapped scenario test is absent", async () => {
    // Given
    const spawn = () => ({
      exited: Promise.resolve(0),
      stdout: new Response(
        `${JSON.stringify({ Action: "pass", Test: "TestInspector_rejects_wrong_identity_firmware_and_protocolInfo" })}\n`,
      ).body,
      stderr: new Response("").body,
    });

    // When
    const result = await runEmulatorMatrix({
      spawn,
      serverDirectory: "apps/server",
      candidateSha256: "a".repeat(64),
    });

    // Then
    expect(result).toEqual(expect.objectContaining({
      ok: false,
      code: "EMULATOR_SCENARIO_MISSING",
    }));
  });
});

describe("K17 external authorization gate", () => {
  test("emits awaiting authorization and invokes no device or media operation when authorization is false", async () => {
    // Given
    const matrix = parseSupportMatrix("certification:\n  renderer_control_authorized: false\n");
    const calls = { ssdpControl: 0, soap: 0, media: 0 };

    // When
    const result = await runAuthorizationGate({ matrix, candidateSha256: "a".repeat(64), recordedAt: NOW, runPhysical: async () => {
      calls.ssdpControl += 1; calls.soap += 1; calls.media += 1;
      return physicalReceipt();
    }, verifyPublicationDenied: async () => ({ code: "PRODUCT_GATE_REQUIRED", externalWrites: 0 }) });

    // Then
    expect(calls).toEqual({ ssdpControl: 0, soap: 0, media: 0 });
    expect(result).toEqual(expect.objectContaining({ qualification_status: "awaiting_external_authorization", network_calls: calls, publication: { code: "PRODUCT_GATE_REQUIRED", external_writes: 0 } }));
    expect(validateQualificationReceipt(result, { now: NOW, candidateSha256: "a".repeat(64), runnerLabel: "" }).ok).toBe(true);
  });

  test("requires one exact nonempty runner label whenever authorization is true", () => {
    // Given / When / Then
    expect(() => parseSupportMatrix("certification:\n  renderer_control_authorized: true\n")).toThrow("K17_RUNNER_LABEL_REQUIRED");
    expect(() => parseSupportMatrix("certification:\n  renderer_control_authorized: true\n  k17_runner_label: lab runner\n")).toThrow("K17_RUNNER_LABEL_INVALID");
  });

  test("does not select a differently labeled physical runner", async () => {
    // Given
    const matrix = parseSupportMatrix("certification:\n  renderer_control_authorized: true\n  k17_runner_label: jastreamer-k17-lab-a\n");

    // When / Then
    await expect(runAuthorizationGate({ matrix, actualRunnerLabel: "jastreamer-k17-lab-b", candidateSha256: "a".repeat(64), recordedAt: NOW, runPhysical: async () => physicalReceipt(), verifyPublicationDenied: async () => ({ code: "PRODUCT_GATE_REQUIRED", externalWrites: 0 }) })).rejects.toThrow("K17_RUNNER_LABEL_MISMATCH");
  });
});

describe("K17 physical promotion receipt", () => {
  test("accepts complete physical qualification bound to the exact candidate", () => {
    // Given / When
    const result = validatePhysicalQualification(physicalReceipt(), { now: NOW, artifactSha256: "a".repeat(64), runnerLabel: "jastreamer-k17-lab-a" });

    // Then
    expect(result.ok).toBe(true);
  });

  test("requires L16 when a source representation is not advertised", () => {
    // Given
    const receipt = physicalReceipt();
    receipt.protocolInfo = receipt.protocolInfo.filter((entry) => !entry.includes("audio/flac"));
    receipt.protocolInfoSha256 = sha(receipt.protocolInfo);
    receipt.representations[0].advertised = false;
    receipt.representations[0].selected = "l16";

    // When
    const result = validatePhysicalQualification(receipt, { now: NOW, artifactSha256: "a".repeat(64), runnerLabel: "jastreamer-k17-lab-a" });

    // Then
    expect(result.ok).toBe(true);
  });

  test.each([
    ["emulator-only", (r) => { r.evidenceSource = "emulator"; }, "EMULATOR_ONLY"],
    ["wrong model", (r) => { r.model = "K19"; }, "IDENTITY_REJECTED"],
    ["old firmware", (r) => { r.firmware = 260; }, "FIRMWARE_REJECTED"],
    ["missing representation", (r) => { r.representations = r.representations.filter((item) => item.format !== "opus"); }, "REPRESENTATION_PROOF_MISSING"],
    ["missing audio proof", (r) => { r.audioProof.captureSha256 = ""; }, "AUDIO_PROOF_MISSING"],
    ["stale artifact", (r) => { r.artifactSha256 = "d".repeat(64); }, "ARTIFACT_MISMATCH"],
    ["raw UDN", (r) => { r.udn = "uuid:k17-raw"; }, "IDENTITY_UNREDACTED"],
    ["raw IP", (r) => { r.address = "192.168.1.9"; }, "IDENTITY_UNREDACTED"],
    ["public IPv4 in protocolInfo", (r) => { r.protocolInfo[0] += ":8.8.8.8"; }, "IDENTITY_UNREDACTED"],
    ["IPv6 in protocolInfo", (r) => { r.protocolInfo[0] += ":[fe80::1]"; }, "IDENTITY_UNREDACTED"],
    ["hostile location", (r) => { r.network.hostileLocationRejected = false; }, "NETWORK_RESTRICTION_FAILED"],
    ["incompatible protocolInfo", (r) => { r.protocolInfo = ["http-get:*:video/mp4:*"]; r.protocolInfoSha256 = sha(r.protocolInfo); }, "PROTOCOL_INFO_INCOMPATIBLE"],
    ["expired URL accepted", (r) => { r.network.expiredUrlRejected = false; }, "EXPIRED_URL_ACCEPTED"],
    ["external override adopted", (r) => { r.externalOverride.adopted = true; }, "EXTERNAL_OVERRIDE_VIOLATION"],
  ])("rejects %s", (_name, mutate, code) => rejectedAs(mutate, code));

  test("accepts the frozen Vorbis and Opus protocolInfo MIME aliases", () => {
    const receipt = physicalReceipt();
    receipt.protocolInfo = receipt.protocolInfo.filter((entry) => !entry.includes("audio/ogg"));
    receipt.protocolInfo.push(
      "http-get:*:audio/vorbis:*",
      "http-get:*:audio/opus:*",
    );
    receipt.protocolInfoSha256 = sha(receipt.protocolInfo);

    expect(validatePhysicalQualification(receipt, {
      now: NOW,
      artifactSha256: "a".repeat(64),
      runnerLabel: "jastreamer-k17-lab-a",
    }).ok).toBe(true);
  });
});
