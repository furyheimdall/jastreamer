import { describe, expect, test } from "bun:test";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createPromotionFixture } from "./product-gate-fixture.mjs";
import { verifyProductGate } from "./product-gate.mjs";

const NOW = "2026-08-26T12:00:00.000Z";

const runCase = async (mutation) => {
  const root = mkdtempSync(join(tmpdir(), "product-gate-negative-"));
  try {
    const fixture = await createPromotionFixture(root, NOW);
    mutation(fixture, root);
    return verifyProductGate(fixture.receiptPath, {
      root,
      now: NOW,
      profile: "fixture", trustConfigPath: fixture.trustConfigPath,
      mutationLedgerPath: fixture.mutationLedgerPath,
    });
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
};

const expectDenied = async (mutation, code) => {
  const result = await runCase(mutation);
  expect(result.ok).toBe(false);
  if (!result.ok) {
    expect(result.code).toBe(code);
    expect(result.externalMutations).toBe(0);
  }
};

const resign = (fixture) => fixture.resignReceipt();

describe("immutable product promotion gate", () => {
  test("rejects altered parent reducer bytes before qualification gates", async () => expectDenied((fixture, root) => { writeFileSync(join(root, fixture.receipt.authoritativeReducer.path), '{"altered":true}\n'); }, "AUTHORITATIVE_REDUCER_DIGEST_MISMATCH"));
  test("selects exact staged Server and Control bytes in the local authorized simulation", async () => {
    const result = await runCase(() => {});
    expect(result.ok).toBe(true);
    if (result.ok) {
      expect(result.selection.map((item) => item.kind)).toEqual([
        "server-linux-amd64-deb", "server-linux-amd64-rpm", "server-linux-arm64-deb",
        "server-linux-arm64-rpm", "server-windows-amd64-exe", "server-windows-amd64-msi",
        "server-oci", "control-web", "control-windows", "control-android",
      ]);
      expect(result.selection.some((item) => item.kind.includes("renderer"))).toBe(false);
      expect(result.authoritativeReducer).toEqual({ sha256: expect.stringMatching(/^[0-9a-f]{64}$/), result: "success" });
      expect(result.publication).toEqual(expect.objectContaining({
        repository: "furyheimdall/jastreamer",
        environment: "product-promotion",
        candidates: {
          server: expect.objectContaining({ releaseTag: "server-v1.2.3", staging: expect.objectContaining({ eventName: "workflow_dispatch", calledJob: "server", artifactName: "server-publication-stage-1001-1", artifactAttemptProvenance: "caller-run+upload-output+embedded-manifest" }) }),
          control: expect.objectContaining({ releaseTag: "control-v1.2.3", staging: expect.objectContaining({ eventName: "workflow_dispatch", calledJob: "control", artifactName: "control-publication-stage-1001-1", artifactAttemptProvenance: "caller-run+upload-output+embedded-manifest" }) }),
        },
      }));
      expect(result.trust).toEqual(expect.objectContaining({ profile: "fixture", trustPolicyVersion: "product-gate-trust-v1", rotationEpoch: 1 }));
      expect(result.serverOci).toEqual(expect.objectContaining({ platforms: ["linux/amd64", "linux/arm64"], attestations: expect.any(Array) }));
      expect(result.rebuild).toBe(false);
      expect(result.externalMutations).toBe(0);
    }
  });

  test.each([
    ["missing Todo19", (f) => { delete f.receipt.qualifications.todo19; resign(f); }, "SCHEMA_INVALID"],
    ["pending Todo19", (f) => { f.receipt.qualifications.todo19.status = "pending"; resign(f); }, "QUALIFICATION_PENDING"],
    ["Todo19 platform", (f) => { f.mutateQualification("todo19", (value) => { value.runs = value.runs.filter((run) => run.platform !== "android"); }); }, "TODO19_SCHEMA_INVALID"],
    ["Todo19 startup order", (f) => { f.mutateQualification("todo19", (value) => { value.runs[0].startupOrder = "invalid"; }); }, "TODO19_SCHEMA_INVALID"],
    ["missing K17", (f) => { delete f.receipt.qualifications.k17; resign(f); }, "SCHEMA_INVALID"],
    ["pending K17", (f) => { f.receipt.qualifications.k17.status = "pending"; resign(f); }, "QUALIFICATION_PENDING"],
    ["emulator K17", (f) => { f.mutateQualification("k17", (value) => { value.physical.evidenceSource = "emulator"; }); }, "K17_EMULATOR_ONLY"],
    ["K17 physical transport", (f) => { f.mutateQualification("k17", (value) => { value.physical.transport.seek = "failed"; }); }, "K17_TRANSPORT_PROOF_MISSING"],
    ["missing WASAPI", (f) => { delete f.receipt.qualifications.wasapi; resign(f); }, "SCHEMA_INVALID"],
    ["pending WASAPI", (f) => { f.receipt.qualifications.wasapi.status = "pending"; resign(f); }, "QUALIFICATION_PENDING"],
    ["mock WASAPI", (f) => { f.mutateQualification("wasapi", (value) => { value.endpoint.capture_mode = "mock"; }); }, "WASAPI_SCHEMA_INVALID"],
    ["WASAPI numeric threshold", (f) => { f.mutateQualification("wasapi", (value) => { value.capture.tone.steady_rms = 0; }); }, "WASAPI_TONE_RMS_FAILED"],
    ["stale", (f) => { f.receipt.recordedAt = "2026-08-24T12:00:00.000Z"; resign(f); }, "RECEIPT_STALE"],
    ["dirty source digest", (f) => { f.receipt.source.dirtySha256 = "0".repeat(64); resign(f); }, "SOURCE_BINDING_MISMATCH"],
    ["source revision", (f) => { f.receipt.source.revision = "0".repeat(40); resign(f); }, "SOURCE_BINDING_MISMATCH"],
    ["checkout bytes", (_f, root) => { writeFileSync(join(root, "installed/source.txt"), "changed checkout\n"); }, "SOURCE_BINDING_MISMATCH"],
    ["artifact-set digest", (f) => { f.receipt.bindings.artifactSetSha256 = "0".repeat(64); resign(f); }, "EVIDENCE_BINDING_MISMATCH"],
    ["peer digest", (f) => { f.receipt.bindings.peerSetSha256 = "0".repeat(64); resign(f); }, "PEER_RECOMPUTATION_MISMATCH"],
    ["control contract digest", (f) => { f.receipt.bindings.controlContractSha256 = "0".repeat(64); resign(f); }, "CONTRACT_RECOMPUTATION_MISMATCH"],
    ["renderer contract digest", (f) => { f.receipt.bindings.rendererContractSha256 = "0".repeat(64); resign(f); }, "CONTRACT_RECOMPUTATION_MISMATCH"],
    ["canonical contract bytes", (_f, root) => { writeFileSync(join(root, "installed/bound/control.contract.json"), "{}\n"); }, "CONTRACT_RECOMPUTATION_MISMATCH"],
    ["canonical peer bytes", (_f, root) => { writeFileSync(join(root, "installed/bound/server.peer.json"), "{}\n"); }, "PEER_RECOMPUTATION_MISMATCH"],
    ["signature", (f) => { f.receipt.signature.value = "AAAA"; f.writeReceipt(); }, "SIGNATURE_INVALID"],
    ["SBOM digest", (f) => { f.receipt.supplyChain.sbom[0].sha256 = "0".repeat(64); resign(f); }, "DIGEST_MISMATCH"],
    ["SPDX semantics", (f) => { f.mutateSupply("sbom", 0, (value) => { value.spdxVersion = "SPDX-1.0"; }); }, "SPDX_INVALID"],
    ["SPDX required creation info", (f) => { f.mutateSupply("sbom", 0, (value) => { delete value.creationInfo; }); }, "SPDX_INVALID"],
    ["SPDX relationship closure", (f) => { f.mutateSupply("sbom", 0, (value) => { value.relationships.pop(); }); }, "SPDX_INVALID"],
    ["provenance digest", (f) => { f.receipt.supplyChain.provenance[0].sha256 = "0".repeat(64); resign(f); }, "DIGEST_MISMATCH"],
    ["DSSE signature", (f) => { f.mutateSupply("provenance", 0, (value) => { value.signatures[0].sig = "AAAA"; }); }, "ATTESTATION_SIGNATURE_INVALID"],
    ["SLSA predicate", (f) => { f.mutateDsse("provenance", 0, (value) => { value.predicateType = "https://example.invalid/fake"; }); }, "ATTESTATION_SEMANTICS_INVALID"],
    ["SLSA source material drift", (f) => { f.mutateDsse("provenance", 0, (value) => { value.predicate.buildDefinition.resolvedDependencies[0].digest.gitCommit = "0".repeat(40); }); }, "SLSA_INVALID"],
    ["SLSA extra dependency", (f) => { f.mutateDsse("provenance", 0, (value) => { value.predicate.buildDefinition.resolvedDependencies.push({ uri: "extra", digest: { gitCommit: "0".repeat(40) } }); }); }, "SLSA_INVALID"],
    ["SLSA arbitrary builder", (f) => { f.mutateDsse("provenance", 0, (value) => { value.predicate.runDetails.builder.id = "arbitrary"; }); }, "SLSA_INVALID"],
    ["SLSA subject drift", (f) => { f.mutateDsse("provenance", 0, (value) => { value.subject[0].name = "other"; }); }, "ATTESTATION_SEMANTICS_INVALID"],
    ["SLSA platform drift", (f) => { f.mutateDsse("provenance", 0, (value) => { value.predicate.buildDefinition.externalParameters.platforms = ["linux/other"]; }); }, "SLSA_INVALID"],
    ["SLSA build type", (f) => { f.mutateDsse("provenance", 0, (value) => { value.predicate.buildDefinition.buildType = "other"; }); }, "SLSA_INVALID"],
    ["signing digest", (f) => { f.receipt.supplyChain.signing[0].sha256 = "0".repeat(64); resign(f); }, "DIGEST_MISMATCH"],
    ["artifact signing identity", (f) => { f.mutateSupply("signing", 0, (value) => { value.keyId = "0".repeat(64); }); }, "ARTIFACT_SIGNATURE_INVALID"],
    ["security failure", (f) => { delete f.receipt.supplyChain.security; resign(f); }, "SCHEMA_INVALID"],
    ["incomplete cleanup", (f) => { f.receipt.cleanup.stagingTemporaryRemoved = false; resign(f); }, "CLEANUP_INCOMPLETE"],
    ["forged ledger chain", (f) => { f.mutateCleanup("mutationLedger", (value) => { value[1].previousSha256 = "0".repeat(64); }); }, "MUTATION_LEDGER_INVALID"],
    ["extra artifact", (f) => { f.receipt.candidates.control.artifacts.push({ kind: "control-extra", path: "stage/control/extra.bin", sha256: "0".repeat(64) }); resign(f); }, "ARTIFACT_ALLOWLIST_MISMATCH"],
    ["floating artifact", (f) => { f.receipt.candidates.server.floating = true; resign(f); }, "FLOATING_ARTIFACT"],
    ["OCI platform", (f) => { f.receipt.candidates.server.artifacts.find((a) => a.kind === "server-oci").platforms = ["linux/amd64"]; resign(f); }, "OCI_PLATFORM_MISMATCH"],
    ["OCI attestation", (f) => { f.receipt.candidates.server.artifacts.find((a) => a.kind === "server-oci").attestations.pop(); resign(f); }, "SCHEMA_INVALID"],
    ["OCI referrer media type", (f) => { f.mutateOciReferrers((value) => { value.manifests[0].mediaType = "application/json"; }); }, "OCI_REFERRERS_INVALID"],
    ["prior published bytes", (_f, root) => { writeFileSync(join(root, "prior-published/server-v0.0.9.bin"), "changed\n"); }, "PRIOR_PUBLISHED_CHANGED"],
    ["publication environment", (f) => { f.receipt.publication.environment = "unprotected"; resign(f); }, "SCHEMA_INVALID"],
    ["publication receipt key", (f) => { f.receipt.publication.receiptKeyId = "0".repeat(64); resign(f); }, "PUBLICATION_BINDING_MISMATCH"],
    ["candidate staging event", (f) => { f.receipt.candidates.server.staging.eventName = "push"; resign(f); }, "SCHEMA_INVALID"],
    ["candidate staging workflow", (f) => { f.receipt.candidates.control.staging.calledWorkflowPath = ".github/workflows/server-release.yml"; resign(f); }, "AUTHORITATIVE_REDUCER_INVALID"],
    ["candidate staging revision", (f) => { f.receipt.candidates.server.staging.headSha = "0".repeat(40); resign(f); }, "PUBLICATION_STAGE_INVALID"],
    ["candidate caller identity removal", (f) => { delete f.receipt.candidates.server.staging.callerRunId; resign(f); }, "SCHEMA_INVALID"],
    ["candidate called job drift", (f) => { f.receipt.candidates.control.staging.calledJob = "server"; resign(f); }, "AUTHORITATIVE_REDUCER_INVALID"],
    ["candidate called result drift", (f) => { f.receipt.candidates.server.staging.calledJobResult = "skipped"; resign(f); }, "SCHEMA_INVALID"],
    ["candidate artifact manifest removal", (f) => { delete f.receipt.candidates.server.staging.artifactManifestSha256; resign(f); }, "SCHEMA_INVALID"],
    ["candidate content manifest removal", (f) => { delete f.receipt.candidates.control.staging.artifactContentManifestSha256; resign(f); }, "SCHEMA_INVALID"],
    ["candidate artifact attempt provenance drift", (f) => { f.receipt.candidates.control.staging.artifactAttemptProvenance = "run-api"; resign(f); }, "SCHEMA_INVALID"],
    ["candidate artifact earlier-attempt name", (f) => { f.receipt.candidates.server.staging.artifactName = "server-publication-stage-1001-2"; resign(f); }, "PUBLICATION_STAGE_INVALID"],
    ["candidate release tag", (f) => { f.receipt.candidates.control.releaseTag = "control-v1.2.4"; resign(f); }, "PUBLICATION_MANIFEST_MISMATCH"],
  ])("rejects %s with zero externally observed mutation", (_name, mutation, code) => expectDenied(mutation, code));

  test("rejects rebuilt or drifted candidate bytes", () => expectDenied((fixture, root) => {
    const path = fixture.receipt.candidates.control.artifacts[0].path;
    writeFileSync(join(root, path), "rebuilt bytes");
  }, "DIGEST_MISMATCH"));

  test("rejects a replaced staged file through a symlink", () => expectDenied((fixture, root) => {
    const artifact = fixture.receipt.candidates.control.artifacts[0];
    fixture.replaceWithSymlink(artifact.path, join(root, fixture.receipt.candidates.control.artifacts[1].path));
  }, "SYMLINK_REJECTED"));

  test("requires authenticated qualification transcripts", () => expectDenied((fixture) => {
    fixture.receipt.qualifications.todo19.signature = "AAAA";
    resign(fixture);
  }, "TRANSCRIPT_AUTHENTICATION_FAILED"));

  test("derives signed inventory drift as an external mutation", async () => {
    const result = await runCase((fixture) => { fixture.mutateCleanup("inventoryAfter", (value) => { value[0].ids.push("new-process"); }); });
    expect(result).toEqual(expect.objectContaining({ ok: false, code: "CLEANUP_INCOMPLETE", externalMutations: 1 }));
  });

  test("derives externally observed mutation instead of hardcoding zero", async () => {
    const result = await runCase((fixture) => { writeFileSync(fixture.mutationLedgerPath, '{"sequence":1,"operation":"gh-release-create","externallyObserved":true}\n'); });
    expect(result).toEqual(expect.objectContaining({ ok: false, code: "EXTERNAL_MUTATION_OBSERVED", externalMutations: 1 }));
  });

  test("current repository production trust is typed default-deny when qualification/signing trust is incomplete", async () => {
    const root = mkdtempSync(join(tmpdir(), "product-gate-production-incomplete-"));
    try { const fixture = await createPromotionFixture(root, NOW); const result = verifyProductGate(fixture.receiptPath, { root, now: NOW, profile: "production", repositoryRoot: process.cwd(), trustConfigPath: "tooling/release/product-gate-production-trust-v1.json", mutationLedgerPath: fixture.mutationLedgerPath }); expect(result).toEqual(expect.objectContaining({ ok: false, code: "PRODUCTION_TRUST_INCOMPLETE" })); } finally { rmSync(root, { recursive: true, force: true }); }
  });

  test("production mode rejects a caller-selected fixture trust root", async () => {
    const root = mkdtempSync(join(tmpdir(), "product-gate-production-trust-"));
    try {
      const fixture = await createPromotionFixture(root, NOW);
      expect(verifyProductGate(fixture.receiptPath, { root, now: NOW, profile: "production", repositoryRoot: process.cwd(), trustConfigPath: fixture.trustConfigPath, mutationLedgerPath: fixture.mutationLedgerPath })).toEqual(expect.objectContaining({ ok: false, code: "TRUST_CONFIG_REJECTED" }));
    } finally { rmSync(root, { recursive: true, force: true }); }
  });

  test.each([["older", 0], ["newer", 2]])("rejects %s receipt rotation epoch with the same valid key", (_name, epoch) => expectDenied((fixture) => { fixture.receipt.rotationEpoch = epoch; fixture.resignReceipt(); }, epoch === 0 ? "SCHEMA_INVALID" : "TRUST_POLICY_MISMATCH"));

  test("rejects omitted receipt trust policy even when re-signed", () => expectDenied((fixture) => { delete fixture.receipt.trustPolicyVersion; fixture.resignReceipt(); }, "SCHEMA_INVALID"));

  test("rejects transcript rotation drift even when gate receipt is re-signed", () => expectDenied((fixture) => { fixture.receipt.qualifications.k17.rotationEpoch = 2; fixture.resignReceipt(); }, "TRUST_POLICY_MISMATCH"));

  test("rejects untyped trust rotation epochs", () => expectDenied((fixture) => {
    const trust = JSON.parse(readFileSync(fixture.trustConfigPath, "utf8")); trust.rotationEpoch = "1"; writeFileSync(fixture.trustConfigPath, JSON.stringify(trust));
  }, "TRUST_CONFIG_INVALID"));

  test("emits an immutable authorization receipt bound to the gate and selected bytes", async () => {
    const root = mkdtempSync(join(tmpdir(), "product-gate-output-"));
    try {
      const fixture = await createPromotionFixture(root, NOW);
      const result = verifyProductGate(fixture.receiptPath, { root, now: NOW, profile: "fixture", trustConfigPath: fixture.trustConfigPath, mutationLedgerPath: fixture.mutationLedgerPath });
      expect(result.ok).toBe(true);
      if (result.ok) {
        const bytes = readFileSync(fixture.receiptPath);
        expect(result.productGateSha256).toBe(fixture.sha256(bytes));
        expect(result.selection.every((item) => fixture.sha256(readFileSync(join(root, item.path))) === item.sha256)).toBe(true);
      }
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });
});
