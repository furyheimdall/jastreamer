import { describe, expect, test } from "bun:test";
import { symlinkSync, unlinkSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { preparePublication } from "./publication-contract";
import { createPublicationFixture, type PublicationFixture } from "./publication-test-fixture";
import type { PublicationRequest } from "./publication-types";

type OwnedFixture = PublicationFixture & { [Symbol.dispose](): void };
const fixture = async (component: "server" | "control" = "server"): Promise<OwnedFixture> => {
  const created = await createPublicationFixture(component);
  return Object.assign(created, { [Symbol.dispose]: () => created.cleanup() });
};

describe("publication contract boundary", () => {
  test.each([
    ["workflow_dispatch", "tag", "refs/tags/server-v1.2.3"],
    ["pull_request", "branch", "refs/pull/7/merge"],
    ["push", "branch", "refs/heads/main"],
  ])("rejects %s from %s before provider access", async (name, refType, ref) => {
    // Given: exact certified bytes under a non-release event.
    using item = await fixture();
    const request: PublicationRequest = { ...item.request, event: { ...item.request.event, name, refType, ref } };

    // When / Then: immutable preparation denies the event locally.
    expect(() => preparePublication(request, item.key)).toThrow("NON_PROMOTABLE_EVENT");
  });

  test("rejects tag, source revision, protected environment, and fixture-trust drift independently", async () => {
    // Given: a complete qualified publication fixture.
    using item = await fixture();
    const tagRequest: PublicationRequest = { ...item.request, event: { ...item.request.event, refName: "server-v1.2.4", ref: "refs/tags/server-v1.2.4" } };
    const revisionRequest: PublicationRequest = { ...item.request, event: { ...item.request.event, sha: "0".repeat(40) } };
    const productionRequest: PublicationRequest = { ...item.request, mode: "production" };

    // When / Then: each independent identity mismatch fails closed.
    expect(() => preparePublication(tagRequest, item.key)).toThrow("NON_PROMOTABLE_EVENT");
    expect(() => preparePublication(revisionRequest, item.key)).toThrow("PUBLICATION_REF_DRIFT");
    expect(() => preparePublication(productionRequest, item.key)).toThrow("PRODUCTION_TRUST_REQUIRED");
    expect(() => preparePublication(item.request, Buffer.alloc(32, 7))).toThrow("PUBLICATION_RECEIPT_KEY_MISMATCH");
  });

  test.each(["missing", "altered", "symlink", "extra", "renderer"])("rejects %s staged bytes before provider access", async (mode) => {
    // Given: one isolated exact publication stage.
    using item = await fixture();
    const selected = item.prepared.artifacts[0];
    if (selected === undefined) throw new Error("fixture artifact missing");
    switch (mode) {
      case "missing":
        unlinkSync(selected.absolutePath);
        break;
      case "altered":
        writeFileSync(selected.absolutePath, "altered bytes");
        break;
      case "symlink":
        unlinkSync(selected.absolutePath);
        symlinkSync(join(item.gateRoot, "product-gate.json"), selected.absolutePath);
        break;
      case "extra":
        writeFileSync(join(item.stageRoot, "extra.bin"), "extra");
        break;
      case "renderer":
        writeFileSync(join(item.stageRoot, "jastreamer-renderer_1.2.3_windows_amd64.msi"), "renderer");
        break;
      default:
        throw new Error(`unsupported fixture mode: ${mode}`);
    }

    // When / Then: no missing, changed, linked, extra, or Renderer byte is selectable.
    expect(() => preparePublication(item.request, item.key)).toThrow();
  });

  test("rejects OCI platform and attestation drift before provider access", async () => {
    // Given: a verified output whose machine fields are independently altered.
    using platform = await fixture();
    const platformPath = join(platform.gateRoot, "verified.json");
    const platformBytes = await Bun.file(platformPath).text();
    writeFileSync(platformPath, platformBytes.replace('"linux/arm64"', '"linux/s390x"'));
    using attestation = await fixture();
    const attestationPath = join(attestation.gateRoot, "verified.json");
    const attestationBytes = await Bun.file(attestationPath).text();
    const secondDigest = attestation.prepared.verified.serverOci.attestations[1];
    writeFileSync(attestationPath, attestationBytes.replace(`,\n      "${secondDigest}"`, ""));

    // When / Then: the two-platform/two-attestation contract is exact.
    expect(() => preparePublication(platform.request, platform.key)).toThrow("OCI_PUBLICATION_INVALID");
    expect(() => preparePublication(attestation.request, attestation.key)).toThrow("OCI_PUBLICATION_INVALID");
  });

  test("the Control allowlist contains only Web, Windows, and universal Android assets", async () => {
    // Given / When: a complete Control publication selection.
    using item = await fixture("control");
    const names = item.prepared.artifacts.map((artifact) => basename(artifact.absolutePath));

    // Then: stores, AAB, Renderer, and Server bytes are absent.
    expect(names).toEqual([
      "jastreamer-control_1.2.3_web.zip",
      "jastreamer-control_1.2.3_windows.msix",
      "jastreamer-control_1.2.3_android_universal.apk",
    ]);
    expect(names.some((name) => name.endsWith(".aab") || name.includes("renderer") || name.includes("server"))).toBe(false);
  });
});
