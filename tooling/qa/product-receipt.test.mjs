import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createSyntheticBundle } from "./synthetic-bundle.mjs";
import { validateProductBundle } from "./product-receipt.mjs";

const NOW = "2026-08-25T12:00:00.000Z";
const rejectedAs = (bundle, code) => {
  const result = validateProductBundle(bundle, { now: NOW, profile: "fixture" });
  expect(result.ok).toBe(false);
  if (!result.ok) expect(result.code).toBe(code);
};

describe("product receipt boundary", () => {
  test("executes product schema for an ordinary fixture and rejects schema-only malformed input", () => {
    let calls = 0; const ordinary = createSyntheticBundle(NOW);
    expect(validateProductBundle(ordinary, { now: NOW, profile: "fixture", schemaObserver: () => { calls += 1; } }).ok).toBe(true);
    expect(calls).toBe(1);
    ordinary.peers[0].component = "";
    const peerSetSha256 = createHash("sha256").update(JSON.stringify(ordinary.peers)).digest("hex");
    for (const receipt of ordinary.receipts) receipt.binding.peerSetSha256 = peerSetSha256;
    expect(validateProductBundle(ordinary, { now: NOW, profile: "fixture", schemaObserver: () => { calls += 1; } })).toEqual(expect.objectContaining({ ok: false, code: "SCHEMA_INVALID" }));
    expect(calls).toBe(2);
  });

  test("validates every receipt lane when a full synthetic bundle is supplied", () => {
    // Given
    const bundle = createSyntheticBundle(NOW);

    // When
    const result = validateProductBundle(bundle, { now: NOW, profile: "fixture" });

    // Then
    expect(result.ok).toBe(true);
    if (result.ok) expect(result.value.receipts.map((receipt) => receipt.kind)).toEqual([
      "candidate", "server_control_e2e", "k17", "wasapi", "ffmpeg", "external_authorization_pending", "cleanup",
    ]);
  });

  test("rejects a missing required receipt", () => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    bundle.receipts = bundle.receipts.filter((receipt) => receipt.kind !== "cleanup");

    // When / Then
    rejectedAs(bundle, "RECEIPT_MISSING");
  });

  test("rejects an unknown required receipt", () => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    bundle.requiredReceipts.push("future_lane");

    // When / Then
    rejectedAs(bundle, "RECEIPT_KIND_UNKNOWN");
  });

  test("rejects stale evidence", () => {
    // Given
    const bundle = createSyntheticBundle("2026-08-23T12:00:00.000Z");

    // When / Then
    rejectedAs(bundle, "RECEIPT_STALE");
  });

  test("rejects unknown fields", () => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    bundle.extra = true;

    // When / Then
    rejectedAs(bundle, "FIELD_UNKNOWN");
  });

  test.each([
    ["secret", (bundle) => { bundle.receipts[0].payload.setupSecret = "not-for-evidence"; }, "SECRET_PRESENT"],
    ["token", (bundle) => { bundle.receipts[1].payload.token = "raw-token-value"; }, "SECRET_PRESENT"],
    ["absolute path", (bundle) => { bundle.receipts[4].payload.executable = "/usr/bin/ffmpeg"; }, "ABSOLUTE_PATH_PRESENT"],
    ["LAN identity", (bundle) => { bundle.receipts[2].payload.address = "192.168.1.7"; }, "LAN_IDENTITY_PRESENT"],
    ["raw UDN", (bundle) => { bundle.receipts[2].payload.device = "uuid:device-identity"; }, "LAN_IDENTITY_PRESENT"],
    ["fake marker", (bundle) => { bundle.receipts[3].payload.captureMode = "fake-audio"; }, "NON_REAL_EVIDENCE"],
    ["mock marker", (bundle) => { bundle.receipts[1].payload.driver = "mock-only"; }, "NON_REAL_EVIDENCE"],
  ])("rejects %s leakage", (_name, mutate, code) => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    mutate(bundle);

    // When / Then
    rejectedAs(bundle, code);
  });

  test("rejects a mismatched artifact digest", () => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    bundle.receipts[1].binding.artifactSetSha256 = "a".repeat(64);

    // When / Then
    rejectedAs(bundle, "DIGEST_MISMATCH");
  });

  test("rejects stale artifact references even when they are valid digests", () => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    bundle.receipts[1].payload.artifactSha256 = "b".repeat(64);

    // When / Then
    rejectedAs(bundle, "DIGEST_MISMATCH");
  });

  test.each([
    ["emulator-only K17", (payload) => { payload.evidenceSource = "emulator"; }, "EMULATOR_ONLY"],
    ["wrong K17 model", (payload) => { payload.model = "K19"; }, "IDENTITY_REJECTED"],
    ["old K17 firmware", (payload) => { payload.firmware = 260; }, "FIRMWARE_REJECTED"],
    ["missing K17 representation proof", (payload) => { payload.representations = []; }, "REPRESENTATION_PROOF_MISSING"],
    ["missing K17 audio proof", (payload) => { payload.audioProof.captureSha256 = ""; }, "AUDIO_PROOF_MISSING"],
    ["stale K17 artifact", (payload) => { payload.artifactSha256 = "e".repeat(64); }, "ARTIFACT_MISMATCH"],
    ["hostile K17 location accepted", (payload) => { payload.network.hostileLocationRejected = false; }, "NETWORK_RESTRICTION_FAILED"],
    ["incompatible K17 protocolInfo", (payload) => { payload.protocolInfo = ["http-get:*:video/mp4:*"]; payload.protocolInfoSha256 = createHash("sha256").update(JSON.stringify(payload.protocolInfo)).digest("hex"); }, "PROTOCOL_INFO_INCOMPATIBLE"],
    ["expired K17 URL accepted", (payload) => { payload.network.expiredUrlRejected = false; }, "EXPIRED_URL_ACCEPTED"],
    ["K17 external override adopted", (payload) => { payload.externalOverride.adopted = true; }, "EXTERNAL_OVERRIDE_VIOLATION"],
  ])("rejects %s at the product receipt boundary", (_name, mutate, code) => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    mutate(bundle.receipts[2].payload);

    // When / Then
    rejectedAs(bundle, code);
  });

  test.each([
    ["resource", (bundle) => { bundle.receipts.at(-1).payload.resourcesReleased = false; }],
    ["process", (bundle) => { bundle.receipts.at(-1).payload.processesTerminated = false; }],
    ["directory", (bundle) => { bundle.receipts.at(-1).payload.temporaryDirectoriesRemoved = false; }],
    ["external write", (bundle) => { bundle.receipts.at(-1).payload.externalWrites = 1; }],
  ])("rejects incomplete cleanup for %s", (_name, mutate) => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    mutate(bundle);

    // When / Then
    rejectedAs(bundle, "CLEANUP_INCOMPLETE");
  });

  test("rejects fixture-only evidence under qualification validation", () => {
    // Given
    const bundle = createSyntheticBundle(NOW);

    // When
    const result = validateProductBundle(bundle, { now: NOW, profile: "qualification" });

    // Then
    expect(result).toEqual({ ok: false, code: "NON_REAL_EVIDENCE", path: "purpose" });
  });

  test("rejects pending external authorization from product promotion", () => {
    // Given
    const bundle = createSyntheticBundle(NOW);
    bundle.purpose = "qualification";
    for (const receipt of bundle.receipts) if (receipt.evidenceMode === "contract_fixture") receipt.evidenceMode = "real";

    // When
    const result = validateProductBundle(bundle, { now: NOW, profile: "qualification", k17RunnerLabel: "jastreamer-k17-lab-a" });

    // Then
    expect(result).toEqual(expect.objectContaining({ ok: false, code: "INSTALLED_PRODUCT_RECEIPT_REQUIRED" }));
  });

  test("ships a strict versioned machine-readable JSON schema", async () => {
    // Given
    const schemaPath = join(import.meta.dir, "product-receipt.schema.json");

    // When
    const schema = JSON.parse(await readFile(schemaPath, "utf8"));

    // Then
    expect(schema.$schema).toBe("https://json-schema.org/draft/2020-12/schema");
    expect(schema.properties.schemaVersion.const).toBe(1);
    expect(schema.additionalProperties).toBe(false);
    expect(Object.keys(schema.$defs.receipt.discriminator.mapping)).toHaveLength(7);
  });
});
