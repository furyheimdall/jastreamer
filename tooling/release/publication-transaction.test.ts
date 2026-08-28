import { describe, expect, test } from "bun:test";
import { chmodSync, existsSync, mkdirSync, readFileSync, unlinkSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { registryReferences } from "./publication-commands";
import { fileSha256 } from "./publication-files";
import { verifyReceiptAuthentication } from "./publication-receipt";
import { createPublicationFixture, FakePublicationDriver, type PublicationFixture } from "./publication-test-fixture";
import { executePublication, PublicationTransactionError } from "./publication-transaction";
import type { PreparedPublication, PublicationReceipt } from "./publication-types";

type OwnedFixture = PublicationFixture & { [Symbol.dispose](): void };
const fixture = async (component: "server" | "control" = "server"): Promise<OwnedFixture> => {
  const created = await createPublicationFixture(component);
  return Object.assign(created, { [Symbol.dispose]: () => created.cleanup() });
};

const executeFailure = async (item: PublicationFixture, driver: FakePublicationDriver, prepared: PreparedPublication = item.prepared): Promise<PublicationReceipt> => {
  try {
    await executePublication({ prepared, driver, receiptKey: item.key });
  } catch (error) {
    if (error instanceof PublicationTransactionError) return error.receipt;
    throw error;
  }
  throw new Error("expected publication failure");
};

const expectRehashBeforeEveryProviderCommand = (receipt: PublicationReceipt): void => {
  for (const [index, entry] of receipt.providerCommands.entries()) {
    if (entry.kind !== "provider") continue;
    const preceding = receipt.providerCommands[index - 1];
    expect(preceding).toEqual(expect.objectContaining({ kind: "rehash", commandId: entry.commandId }));
  }
};

const expectPossiblyCommittedBeforeWrites = (receipt: PublicationReceipt, driver: FakePublicationDriver): void => {
  const writes = driver.commands.filter((command) => command.phase === "write" || command.mutates === true).map((command) => command.id);
  const intents = receipt.providerCommands.filter((entry) => entry.kind === "write-intent").map((entry) => entry.commandId);
  expect(intents).toEqual(writes);
};

describe("observed publication transaction", () => {
  test("plans and executes the exact Server release and GHCR assets without rebuilding", async () => {
    // Given: complete signed qualification output and a no-network provider boundary.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared);
    const priorDigest = driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0");

    // When: the positive publication transaction is simulated.
    const receipt = await executePublication({ prepared: item.prepared, driver, receiptKey: item.key });

    // Then: one immutable release and image are selected, with no Renderer/floating/rebuild command.
    const release = driver.releases.get("server-v1.2.3");
    const references = registryReferences(item.prepared);
    expect(receipt.status).toBe("published");
    expect(release).toEqual({ draft: false, assets: item.prepared.artifacts.map((artifact) => artifact.name), body: "publication-run:9001:2" });
    expect(driver.registry.get(references.final)).toBe(item.prepared.verified.serverOci.indexDigest);
    expect(driver.registry.has(references.temporary)).toBe(false);
    expect(driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0")).toBe(priorDigest);
    expect(driver.commands.filter((command) => command.phase === "write").at(-1)?.id).toBe("release-publish");
    expect(driver.commands.at(-1)?.id).toBe("registry-logout");
    const plan = driver.commands.flatMap((command) => command.argv);
    expect(plan.some((value) => value === "latest" || value.endsWith(":latest") || /renderer/i.test(value))).toBe(false);
    expect(plan.join(" ")).not.toMatch(/\b(?:build|buildx|git tag|docker push)\b/);
    expect(receipt.providerObservedAssets).toEqual(item.prepared.artifacts.map(({ name, sha256, size }) => ({ name, sha256, size })).sort((left, right) => left.name.localeCompare(right.name)));
    const upload = driver.commands.find((command) => command.id === "release-upload-assets");
    expect(upload?.argv.some((value) => item.prepared.artifacts.some((artifact) => value === artifact.absolutePath))).toBe(false);
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
    expectRehashBeforeEveryProviderCommand(receipt);
    expectPossiblyCommittedBeforeWrites(receipt, driver);
  });

  test("clears successful Server registry auth before writing the authenticated receipt", async () => {
    // Given: the fake registry login creates run-owned Docker credentials and observes receipt state at logout.
    using item = await fixture();
    const authRoot = item.request.dockerConfigRoot;
    if (authRoot === undefined) throw new Error("server auth root missing");
    let receiptPresentAtLogout: boolean | undefined;
    const driver = new FakePublicationDriver(item.prepared, { mutateAfter: { commandId: "registry-logout", mutate: () => { receiptPresentAtLogout = existsSync(item.request.receiptPath); } } });

    // When: successful Server publication reaches its local credential cut point.
    const receipt = await executePublication({ prepared: item.prepared, driver, receiptKey: item.key });
    const ownership = Object.fromEntries(Object.entries(receipt.cleanup.ownership));

    // Then: logout and root absence precede the final authenticated receipt.
    expect(receiptPresentAtLogout).toBe(false);
    expect(driver.commands.filter((command) => command.id === "registry-logout")).toHaveLength(1);
    expect(existsSync(authRoot)).toBe(false);
    expect(ownership["registryAuth"]).toBe("absent");
    expect(receipt.cleanup.residualResources).toEqual([]);
    expect(receipt.cleanup.failures).toEqual([]);
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });

  test("reconciles successful logout that returns an ambiguous error from auth-root absence", async () => {
    // Given: logout removes credentials before returning nonzero.
    using item = await fixture();
    const authRoot = item.request.dockerConfigRoot;
    if (authRoot === undefined) throw new Error("server auth root missing");
    const driver = new FakePublicationDriver(item.prepared, { sideEffectThenErrorAt: "registry-logout" });

    // When: local root removal provides the authoritative absence proof.
    const receipt = await executePublication({ prepared: item.prepared, driver, receiptKey: item.key });

    // Then: approved publication remains successful with no credential residual.
    expect(receipt.status).toBe("published");
    expect(existsSync(authRoot)).toBe(false);
    expect(receipt.cleanup.failures).toEqual([]);
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });

  test("fails publication truthfully when registry auth-root absence cannot be proven", async () => {
    // Given: a run-owned auth root under a parent that cannot remove its directory entry.
    using item = await fixture();
    const parent = join(item.root, "locked-auth-parent");
    const authRoot = join(parent, "docker-auth");
    mkdirSync(authRoot, { recursive: true, mode: 0o700 });
    const prepared: PreparedPublication = { ...item.prepared, request: { ...item.prepared.request, dockerConfigRoot: authRoot } };
    const driver = new FakePublicationDriver(prepared);
    chmodSync(parent, 0o500);
    try {
      // When: post-publication credential cleanup cannot prove root absence.
      const receipt = await executeFailure(item, driver, prepared);
      const ownership = Object.fromEntries(Object.entries(receipt.cleanup.ownership));

      // Then: compensation runs and the authenticated receipt names the credential residual.
      expect(receipt.failure?.code).toBe("REGISTRY_AUTH_CLEANUP_FAILED");
      expect(ownership["registryAuth"]).toBe("indeterminate");
      expect(receipt.cleanup.failures).toContain("registry-auth-root");
      expect(receipt.cleanup.residualResources).toContain("registry-auth-root");
      expect(existsSync(authRoot)).toBe(true);
      expect(driver.releases.has("server-v1.2.3")).toBe(false);
      expect(driver.registry.has(registryReferences(prepared).final)).toBe(false);
      expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
    } finally {
      chmodSync(parent, 0o700);
    }
  });

  test("publishes only the three public Control assets and never touches GHCR", async () => {
    // Given: the independently staged Control candidate.
    using item = await fixture("control");
    const driver = new FakePublicationDriver(item.prepared);

    // When: publication is simulated through the same transaction boundary.
    const receipt = await executePublication({ prepared: item.prepared, driver, receiptKey: item.key });

    // Then: Web, MSIX, and universal APK are the complete provider selection.
    expect(receipt.selectedAssets.map((asset) => asset.name)).toEqual([
      "jastreamer-control_1.2.3_web.zip",
      "jastreamer-control_1.2.3_windows.msix",
      "jastreamer-control_1.2.3_android_universal.apk",
    ]);
    expect(driver.commands.some((command) => command.id.startsWith("registry-"))).toBe(false);
    expect(item.request.dockerConfigRoot).toBeUndefined();
    expect(driver.releases.get("control-v1.2.3")?.draft).toBe(false);
    expectRehashBeforeEveryProviderCommand(receipt);
    expectPossiblyCommittedBeforeWrites(receipt, driver);
  });

  test.each(["release-upload-assets", "registry-copy-final", "release-publish"])("compensates a partial %s failure without touching prior fixtures", async (commandId) => {
    // Given: prior immutable release/package state and one injected provider failure.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { failAt: commandId });
    driver.releases.set("server-v0.9.0", { draft: false, assets: ["prior.bin"] });
    const priorRelease = JSON.stringify(driver.releases.get("server-v0.9.0"));
    const priorOci = driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0");

    // When: the transaction reaches the selected cut point.
    const receipt = await executeFailure(item, driver);

    // Then: only run-owned draft/temp/final state is compensated and the failure is authenticated.
    expect(receipt.status).toBe("failed");
    expect(driver.releases.has("server-v1.2.3")).toBe(false);
    expect(JSON.stringify(driver.releases.get("server-v0.9.0"))).toBe(priorRelease);
    expect(driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0")).toBe(priorOci);
    expect(driver.registry.has(registryReferences(item.prepared).temporary)).toBe(false);
    expect(driver.registry.has(registryReferences(item.prepared).final)).toBe(false);
    expect(receipt.cleanup).toEqual(expect.objectContaining({ priorReleaseTouched: false, priorOciTouched: false, failures: [] }));
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
    expectRehashBeforeEveryProviderCommand(receipt);
  });

  test.each(["control", "server"] as const)("severs every original %s stage asset after approval", async (component) => {
    // Given: approval has completed before the first provider read permanently mutates or replaces every original stage asset.
    using item = await fixture(component);
    const driver = new FakePublicationDriver(item.prepared, { mutateAfter: { commandId: "candidate-run", mutate: () => {
      for (const [index, artifact] of item.prepared.artifacts.entries()) {
        if (index % 2 === 1) unlinkSync(artifact.absolutePath);
        writeFileSync(artifact.absolutePath, `superseded-original-${index}`);
      }
    } } });

    // When: all later authorization reads and transfers use the approved closure.
    const receipt = await executePublication({ prepared: item.prepared, driver, receiptKey: item.key });

    // Then: approved bytes publish, including Server OCI, while no superseded stage identity remains in the transfer.
    expect(receipt.status).toBe("published");
    expect(driver.uploadedAssets).toEqual(item.prepared.artifacts.map(({ name, sha256, size }) => ({ name, sha256, size })));
    expect(item.prepared.artifacts.every((artifact) => fileSha256(artifact.absolutePath) !== artifact.sha256)).toBe(true);
    expect(driver.commands.find((command) => command.id === "release-upload-assets")?.argv.some((path) => item.prepared.artifacts.some((artifact) => path === artifact.absolutePath))).toBe(false);
    if (component === "server") expect(driver.registry.get(registryReferences(item.prepared).final)).toBe(item.prepared.verified.serverOci.indexDigest);
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });

  test("rejects original stage mutation before approval and provider access", async () => {
    // Given: selected stage bytes change before approved snapshots exist.
    using item = await fixture();
    for (const artifact of item.prepared.artifacts) writeFileSync(artifact.absolutePath, "changed before approval");
    const driver = new FakePublicationDriver(item.prepared);

    // When / Then: local approval fails before any provider command.
    let failure: unknown;
    try {
      await executePublication({ prepared: item.prepared, driver, receiptKey: item.key });
    } catch (error) {
      failure = error;
    }
    expect(failure).toEqual(expect.objectContaining({ code: "PUBLICATION_DIGEST_MISMATCH" }));
    expect(driver.commands).toHaveLength(0);
  });

  test.each(["mutate", "replace"] as const)("fails and cleans when an approved snapshot is %s after authorization", async (mode) => {
    // Given: the provider boundary tampers with an approved upload path after its command authorization.
    using item = await fixture("control");
    const driver = new FakePublicationDriver(item.prepared, { approvedAssetChangeDuringUpload: mode });

    // When: the next closure read observes changed approved bytes.
    const receipt = await executeFailure(item, driver);

    // Then: no release publishes and exact run-owned draft cleanup completes.
    expect(receipt.failure?.code).toBe("PUBLICATION_DIGEST_MISMATCH");
    expect(driver.releases.has("control-v1.2.3")).toBe(false);
    expect(receipt.cleanup.releaseOwnedByRun).toBe(false);
    expect(receipt.cleanup.failures).toEqual([]);
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });

  test.each([
    ["skipped dependency", { runConclusion: "skipped" }],
    ["artifact digest drift", { artifactDigest: "8".repeat(64) }],
  ])("denies %s before the first provider write", async (_name, options) => {
    // Given: provider metadata that disagrees with the signed run binding.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, options);

    // When: preflight observes the mismatch.
    const receipt = await executeFailure(item, driver);

    // Then: no publication mutation or speculative cleanup/provider probe is attempted.
    expect(driver.commands.some((command) => command.phase === "write" || command.phase === "cleanup" || command.mutates === true)).toBe(false);
    expect(receipt.status).toBe("failed");
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });

  test.each([
    "release-create-draft",
    "release-upload-assets",
    "registry-copy-temporary",
    "registry-copy-final",
    "release-publish",
  ])("reconciles and compensates %s when the provider errors after committing", async (commandId) => {
    // Given: a provider that applies the exact current-run side effect before returning failure.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { sideEffectThenErrorAt: commandId });
    driver.releases.set("server-v0.9.0", { draft: false, assets: ["prior.bin"] });
    const priorRelease = JSON.stringify(driver.releases.get("server-v0.9.0"));
    const priorOci = driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0");

    // When: the write result is ambiguous.
    const receipt = await executeFailure(item, driver);

    // Then: exact run-owned resources are adopted and removed, while prior fixtures remain byte-identical.
    const references = registryReferences(item.prepared);
    expect(driver.releases.has("server-v1.2.3")).toBe(false);
    expect(driver.registry.has(references.temporary)).toBe(false);
    expect(driver.registry.has(references.final)).toBe(false);
    expect(JSON.stringify(driver.releases.get("server-v0.9.0"))).toBe(priorRelease);
    expect(driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0")).toBe(priorOci);
    expect(receipt.cleanup).toEqual(expect.objectContaining({ releaseOwnedByRun: false, temporaryOciOwnedByRun: false, finalOciOwnedByRun: false, failures: [] }));
    expect(driver.commands.filter((command) => command.id === commandId)).toHaveLength(1);
    expectPossiblyCommittedBeforeWrites(receipt, driver);
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });

  test.each([
    ["release-cleanup", "release-publish"],
    ["registry-cleanup-final", "release-publish"],
    ["registry-cleanup-temporary", "registry-copy-final"],
  ])("reconciles %s when cleanup commits before returning an error", async (cleanupCommand, primaryFailure) => {
    // Given: a primary failure followed by ambiguous but completed compensation.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { failAt: primaryFailure, sideEffectThenErrorAt: cleanupCommand });

    // When: cleanup returns a transport error after deleting its exact resource.
    const receipt = await executeFailure(item, driver);

    // Then: absence is reconciled rather than falsely reported as owned or failed cleanup.
    const references = registryReferences(item.prepared);
    expect(driver.releases.has("server-v1.2.3")).toBe(false);
    expect(driver.registry.has(references.temporary)).toBe(false);
    expect(driver.registry.has(references.final)).toBe(false);
    expect(receipt.cleanup).toEqual(expect.objectContaining({ releaseOwnedByRun: false, temporaryOciOwnedByRun: false, finalOciOwnedByRun: false, failures: [] }));
  });

  test("fails closed with explicit indeterminate ownership when a final OCI probe has the wrong digest", async () => {
    // Given: an ambiguous final copy that leaves an unexpected digest at the new version tag.
    using item = await fixture();
    const unexpected = `sha256:${"7".repeat(64)}`;
    const driver = new FakePublicationDriver(item.prepared, { sideEffectThenErrorAt: "registry-copy-final", sideEffectDigest: unexpected });
    const prior = driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0");

    // When: exact ownership cannot be proven.
    const receipt = await executeFailure(item, driver);
    const cleanup = Object.fromEntries(Object.entries(receipt.cleanup));

    // Then: the unexpected resource is preserved and named indeterminate; prior state is untouched.
    expect(cleanup["indeterminateResources"]).toContain("final-oci");
    expect(receipt.cleanup.finalOciOwnedByRun).toBeNull();
    expect(driver.registry.get(registryReferences(item.prepared).final)).toBe(unexpected);
    expect(driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0")).toBe(prior);
    expect(receipt.cleanup.priorOciTouched).toBe(false);
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });

  test("uses approved transfer bytes when the mutable stage is swapped after authorization", async () => {
    // Given: stage bytes are swapped only while the provider opens upload paths, then restored.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { swapStageDuringUpload: true });

    // When: publication runs through the provider boundary.
    const receipt = await executePublication({ prepared: item.prepared, driver, receiptKey: item.key });

    // Then: the provider can observe only approved bytes and the receipt binds their exact digest.
    expect(driver.uploadedAssets).toEqual(item.prepared.artifacts.map((asset) => ({ name: asset.name, sha256: asset.sha256, size: readFileSync(asset.absolutePath).byteLength })));
    const transferredPaths = driver.commands.find((command) => command.id === "release-upload-assets")?.argv.filter((value) => value.startsWith("/")) ?? [];
    expect(transferredPaths.every((path) => !existsSync(path))).toBe(true);
    expect(receipt.status).toBe("published");
  });

  test("removes the draft when provider-observed release asset digests differ", async () => {
    // Given: an upload provider reports a substituted asset digest.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { providerAssetDigest: "6".repeat(64) });

    // When: uploaded draft assets are reconciled before publication.
    const receipt = await executeFailure(item, driver);

    // Then: the release never publishes and its current-run draft is removed.
    expect(receipt.failure?.code).toBe("RELEASE_ASSET_DIGEST_MISMATCH");
    expect(receipt.providerObservedAssets.every((asset) => asset.sha256 === "6".repeat(64))).toBe(true);
    expect(driver.releases.has("server-v1.2.3")).toBe(false);
    expect(receipt.cleanup.releaseOwnedByRun).toBe(false);
  });

  test("observes and compensates a partial asset upload that returns an ambiguous error", async () => {
    // Given: gh stores only the first approved asset before its upload command fails.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { sideEffectThenErrorAt: "release-upload-assets", uploadedAssetLimit: 1 });

    // When: the draft's provider-observed asset set is reconciled.
    const receipt = await executeFailure(item, driver);

    // Then: the partial set is authenticated, never published, and the run-owned draft is gone.
    expect(receipt.failure?.code).toBe("RELEASE_ASSET_DIGEST_MISMATCH");
    expect(receipt.providerObservedAssets).toHaveLength(1);
    expect(receipt.providerObservedAssets[0]).toEqual(expect.objectContaining({ name: item.prepared.artifacts[0]?.name, sha256: item.prepared.artifacts[0]?.sha256 }));
    expect(driver.releases.has("server-v1.2.3")).toBe(false);
  });

  test("fails closed when temporary removal commits before returning an error", async () => {
    // Given: run-scoped temporary OCI deletion succeeds but reports failure before release commit.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { sideEffectThenErrorAt: "registry-remove-temporary-before-commit" });

    // When: transaction reconciliation observes the temporary reference is absent.
    const receipt = await executeFailure(item, driver);

    // Then: no redundant temporary cleanup runs and every remaining owned resource is compensated.
    expect(driver.commands.some((command) => command.id === "registry-cleanup-temporary")).toBe(false);
    expect(driver.registry.has(registryReferences(item.prepared).temporary)).toBe(false);
    expect(driver.registry.has(registryReferences(item.prepared).final)).toBe(false);
    expect(receipt.cleanup.failures).toEqual([]);
  });

  test("preserves and reports a release whose ambiguous create has the wrong run marker", async () => {
    // Given: create reports failure after a foreign identity appears at the candidate tag.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { sideEffectThenErrorAt: "release-create-draft", sideEffectReleaseBody: "publication-run:foreign:1" });

    // When: ownership reconciliation cannot bind that release to this run.
    const receipt = await executeFailure(item, driver);

    // Then: no unsafe delete occurs and the authenticated receipt is explicitly indeterminate.
    expect(driver.releases.get("server-v1.2.3")?.body).toBe("publication-run:foreign:1");
    expect(receipt.cleanup.ownership.release).toBe("indeterminate");
    expect(receipt.cleanup.releaseOwnedByRun).toBeNull();
    expect(receipt.cleanup.indeterminateResources).toContain("release");
    expect(receipt.cleanup.failures).toContain("release-cleanup-ownership-indeterminate");
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });

  test("default-denies a same-run temporary OCI retry without deleting it", async () => {
    // Given: the run-scoped temporary reference already exists before this attempt's absence proof.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared);
    const temporary = registryReferences(item.prepared).temporary;
    driver.registry.set(temporary, item.prepared.verified.serverOci.indexDigest);

    // When: preflight observes retry residue.
    const receipt = await executeFailure(item, driver);

    // Then: publication does not start and unproven residue is preserved as indeterminate.
    expect(receipt.failure?.code).toBe("OCI_TEMPORARY_ALREADY_EXISTS");
    expect(driver.commands.some((command) => command.id === "release-create-draft")).toBe(false);
    expect(driver.registry.get(temporary)).toBe(item.prepared.verified.serverOci.indexDigest);
    expect(receipt.cleanup.indeterminateResources).toContain("temporary-oci");
  });

  test("surfaces cleanup failure without deleting any prior version", async () => {
    // Given: final publication fails and run-owned final-reference deletion also fails.
    using item = await fixture();
    const driver = new FakePublicationDriver(item.prepared, { failAt: "release-publish", failAlwaysAt: "registry-cleanup-final" });
    const prior = driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0");

    // When: compensation is attempted exactly once per owned resource.
    const receipt = await executeFailure(item, driver);

    // Then: the authenticated receipt names the residual run-owned reference, never prior state.
    expect(receipt.cleanup.failures).toContain("registry-cleanup-final");
    expect(receipt.cleanup.finalOciOwnedByRun).toBe(true);
    expect(driver.registry.get("ghcr.io/furyheimdall/jastreamer-server:0.9.0")).toBe(prior);
    expect(receipt.cleanup.priorOciTouched).toBe(false);
    expect(verifyReceiptAuthentication(receipt, item.key)).toBe(true);
  });
});
