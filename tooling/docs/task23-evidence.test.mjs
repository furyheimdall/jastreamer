import { afterAll, beforeAll, afterEach, beforeEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { cp, lstat, mkdir, mkdtemp, readFile, rm, symlink, unlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import { createTask23EvidenceFixture, createTask23SourceFixture } from "./task23-evidence-fixture.mjs";
import { currentDeliveryScope, documentedNonSourceCategories, generatedArtifactCategories, generatedArtifactInventory, productDigest, productFiles, sourceFileRecords, todo22ChangedFiles } from "./task23-source-policy.mjs";

const root = resolve(import.meta.dirname, "..", "..");
const verifier = resolve(root, "tooling/docs/task23-evidence.mjs");
const sha = (value) => createHash("sha256").update(value).digest("hex");
let suiteRoot; let sourceRoot; let testRoot; let evidenceRoot;

const run = (isolatedEvidenceRoot) => Bun.spawnSync(["bun", verifier, "--verify", "--root", isolatedEvidenceRoot, "--source-root", sourceRoot], { cwd: root, stdout: "pipe", stderr: "pipe" });
const withEvidence = async (action) => {
  const temporary = await mkdtemp(join(tmpdir(), "jastreamer-task23-evidence-test-"));
  const isolated = join(temporary, "evidence");
  try { await createTask23EvidenceFixture(isolated, sourceRoot); return await action(isolated); }
  finally { await rm(temporary, { recursive: true, force: true }); }
};
const statusFingerprint = () => sha(execFileSync("git", ["-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all"]));
const reject = (result, code) => {
  expect(result.exitCode).not.toBe(0);
  expect(result.stderr.toString()).toContain(code);
};
const nested = async (copied) => {
  const index = JSON.parse(await readFile(join(copied, "evidence-index.json"), "utf8"));
  const proofRoot = join(copied, "cleanup-proof");
  const proof = JSON.parse(await readFile(join(proofRoot, index.cleanupProof.path), "utf8"));
  const transcript = JSON.parse(await readFile(join(proofRoot, proof.transcript.path), "utf8"));
  return { proofRoot, transcript };
};

beforeAll(async () => {
  suiteRoot = await mkdtemp(join(tmpdir(), "jastreamer-task23-suite-")); sourceRoot = join(suiteRoot, "source");
  await createTask23SourceFixture(sourceRoot);
});
afterAll(async () => rm(suiteRoot, { recursive: true, force: true }));
beforeEach(async () => {
  testRoot = await mkdtemp(join(tmpdir(), "jastreamer-task23-test-")); evidenceRoot = join(testRoot, "evidence");
  await createTask23EvidenceFixture(evidenceRoot, sourceRoot);
});
afterEach(async () => rm(testRoot, { recursive: true, force: true }));

describe("Task 23 evidence isolation", () => {
  test("excludes post-Task23 synthesis receipts without hiding ordinary evidence", async () => {
    const fixture = await mkdtemp(join(tmpdir(), "jastreamer-task23-source-test-"));
    try {
      await createTask23SourceFixture(fixture);
      const finalDirectory = join(
        fixture,
        ".omo/evidence/functional-jastreamer-products/final",
      );
      await mkdir(finalDirectory, { recursive: true });
      const baseline = await generatedArtifactInventory(fixture);

      await writeFile(
        join(finalDirectory, "final-source-freeze-restage.json"),
        '{"status":"passed"}\n',
      );
      expect(await generatedArtifactInventory(fixture)).toEqual(baseline);

      await writeFile(
        join(finalDirectory, "unexpected-evidence.json"),
        '{"status":"passed"}\n',
      );
      expect(await generatedArtifactInventory(fixture)).not.toEqual(baseline);
    } finally {
      await rm(fixture, { recursive: true, force: true });
    }
  });

  test("accepts complete source-bound Ed25519 evidence from an explicit root", async () => {
    const index = JSON.parse(await readFile(join(evidenceRoot, "evidence-index.json"), "utf8"));
    const covered = new Set([...productFiles, ...Object.values(documentedNonSourceCategories).flat()]);
    expect(todo22ChangedFiles).toHaveLength(41); expect(todo22ChangedFiles.filter((path) => !covered.has(path))).toEqual([]);
    const dirtyPaths = execFileSync("git", ["-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all"]).toString().split("\0").filter(Boolean).map((entry) => entry.slice(3)).sort();
    expect(currentDeliveryScope.map(({ path }) => path)).toEqual(dirtyPaths);
    expect(currentDeliveryScope.find(({ path }) => path === ".github/workflows/task19-runner-preflight.yml")).toEqual(expect.objectContaining({ state: "deleted", present: false, sha256: null }));
    expect(currentDeliveryScope.filter(({ classification }) => classification !== "delivery_source")).toEqual([]);
    expect(generatedArtifactCategories.map(({ classification }) => classification)).toEqual(["candidate_binary", "screenshot", "generated_evidence"]);
    const generatedBefore = await generatedArtifactInventory(sourceRoot); const privateParent = join(sourceRoot, ".omo/evidence/functional-jastreamer-products");
    await Promise.all([Bun.write(join(privateParent, "task-23.staging-fixture/partial.json"), "partial"), Bun.write(join(privateParent, "task-23.backup-fixture/previous.json"), "previous")]);
    expect(await generatedArtifactInventory(sourceRoot)).toEqual(generatedBefore); await Promise.all([rm(join(privateParent, "task-23.staging-fixture"), { recursive: true }), rm(join(privateParent, "task-23.backup-fixture"), { recursive: true })]);
    expect(index.source.deliveryScope).toEqual(currentDeliveryScope);
    expect(index.source.deliveryCoverage).toEqual({ covered: dirtyPaths.length, total: dirtyPaths.length, omitted: [] });
    const orderedSet = index.transcripts.map(({ path, sha256, size }) => ({ path, sha256, size }));
    expect(index.transcriptSetSha256).toBe(sha(JSON.stringify(orderedSet)));
    expect(index.redactionReceipt).toEqual(expect.objectContaining({ path: "redaction-receipt.json", sha256: expect.any(String) }));
    expect(index.finalMachineClaim).toEqual(expect.objectContaining({ path: "final-machine-claim.json", sha256: expect.any(String), signature: expect.any(String) }));
    const claim = JSON.parse(await readFile(join(evidenceRoot, index.finalMachineClaim.path), "utf8")); expect(claim.transcriptSetSha256).toBe(index.transcriptSetSha256);

    const fixture = await mkdtemp(join(tmpdir(), "jastreamer-task23-source-test-"));
    try {
      for (const path of productFiles) { await mkdir(dirname(join(fixture, path)), { recursive: true }); await cp(join(root, path), join(fixture, path)); }
      const baseline = productDigest(await sourceFileRecords(fixture));
      for (const path of ["packaging/server/tooling/identity.ts", "packaging/server/tests/release.test.ts", "tooling/qa/task19/installed-product-fixture.mjs"]) {
        const target = join(fixture, path); const original = await readFile(target); await writeFile(target, Buffer.concat([original, Buffer.from("\n// task23 source mutation\n")]));
        expect(productDigest(await sourceFileRecords(fixture))).not.toBe(baseline); await writeFile(target, original);
      }
    } finally { await rm(fixture, { recursive: true, force: true }); }

    const result = run(evidenceRoot); expect(result.exitCode).toBe(0);
    expect(JSON.parse(result.stdout.toString())).toEqual(expect.objectContaining({ transcripts: 9, ledgerEntries: 9, finalResources: 0, redaction: "passed", machineClaim: "verified" }));
  });

  test("rejects a stale source revision before accepting authenticated records", async () => withEvidence(async (copied) => {
    const path = join(copied, "evidence-index.json");
    const index = JSON.parse(await readFile(path, "utf8"));
    index.source.files = await sourceFileRecords(root);
    index.source.productDigest = productDigest(index.source.files);
    index.source.revision = "0".repeat(40);
    await writeFile(path, `${JSON.stringify(index, null, 2)}\n`);
    reject(run(copied), "SOURCE_REVISION_INVALID");
  }));

  test("rejects a stale source inventory before signature verification", async () => withEvidence(async (copied) => {
    const path = join(copied, "evidence-index.json");
    const index = JSON.parse(await readFile(path, "utf8"));
    index.source.files = (await sourceFileRecords(root)).slice(1);
    index.source.productDigest = productDigest(index.source.files);
    await writeFile(path, `${JSON.stringify(index, null, 2)}\n`);
    reject(run(copied), "SOURCE_POLICY_INVALID");
  }));

  test("rejects transcript-set mutations and forged signatures only in run-unique copies", async () => {
    for (const mutation of [
      (index) => index.transcripts.pop(),
      (index) => index.transcripts.reverse(),
      (index) => index.transcripts.push(index.transcripts[0]),
      (index) => { index.transcripts[0].sha256 = "0".repeat(64); },
      (index) => { index.transcriptSetSha256 = "0".repeat(64); },
    ]) await withEvidence(async (copied) => {
      const path = join(copied, "evidence-index.json"); const index = JSON.parse(await readFile(path, "utf8")); mutation(index); await writeFile(path, `${JSON.stringify(index, null, 2)}\n`);
      reject(run(copied), "TRANSCRIPT_SET_INVALID");
    });
    await withEvidence(async (copied) => {
      const path = join(copied, "evidence-index.json"); const index = JSON.parse(await readFile(path, "utf8"));
      index.transcripts[0].signature = Buffer.alloc(64).toString("base64"); await writeFile(path, `${JSON.stringify(index, null, 2)}\n`);
      reject(run(copied), "TRANSCRIPT_AUTHENTICATION_FAILED");
    });
  });

  test("rejects copied output and ledger tampering", async () => {
    await withEvidence(async (copied) => {
      const index = JSON.parse(await readFile(join(copied, "evidence-index.json"), "utf8"));
      const transcript = JSON.parse(await readFile(join(copied, index.transcripts[0].path), "utf8"));
      await writeFile(join(copied, transcript.captures.stdout.path), "changed");
      reject(run(copied), "CAPTURE_INVALID");
    });
    await withEvidence(async (copied) => {
      const index = JSON.parse(await readFile(join(copied, "evidence-index.json"), "utf8"));
      await writeFile(join(copied, index.externalWriteLedger.path), "tampered\n");
      reject(run(copied), "LEDGER_PROVENANCE_INVALID");
    });
  });

  for (const [kind, select] of [
    ["stdout", (value) => value.captures.stdout], ["stderr", (value) => value.captures.stderr],
    ["body", (value) => value.captures.bodies[0]], ["before", (value) => value.inventories.before],
    ["during", (value) => value.inventories.during], ["after", (value) => value.inventories.after],
  ]) test(`rejects nested ${kind} capture tampering`, () => withEvidence(async (copied) => {
    const { proofRoot, transcript } = await nested(copied); const ref = select(transcript);
    await writeFile(join(proofRoot, ref.path), `${kind}-tampered`);
    reject(run(copied), "CLEANUP_PROOF_CAPTURE_INVALID");
  }));

  test("rejects missing and symlinked nested captures", async () => {
    await withEvidence(async (copied) => {
      const { proofRoot, transcript } = await nested(copied); await unlink(join(proofRoot, transcript.inventories.before.path));
      reject(run(copied), "CLEANUP_PROOF_CAPTURE_MISSING");
    });
    await withEvidence(async (copied) => {
      const { proofRoot, transcript } = await nested(copied); const path = join(proofRoot, transcript.inventories.after.path);
      const target = `${path}.target`; await writeFile(target, await readFile(path)); await unlink(path); await symlink(relative(dirname(path), target), path);
      expect((await lstat(path)).isSymbolicLink()).toBe(true);
      reject(run(copied), "CLEANUP_PROOF_CAPTURE_SYMLINK");
    });
  });

  test("runs isolated generation and tampering concurrently without changing repository status", async () => {
    const before = statusFingerprint(); const tamperedRoot = join(testRoot, "concurrent-tampered"); const validRoot = join(testRoot, "concurrent-valid");
    await Promise.all([createTask23EvidenceFixture(tamperedRoot, sourceRoot), createTask23EvidenceFixture(validRoot, sourceRoot)]);
    const path = join(tamperedRoot, "evidence-index.json"); const index = JSON.parse(await readFile(path, "utf8"));
    index.transcripts[0].signature = Buffer.alloc(64).toString("base64"); await writeFile(path, `${JSON.stringify(index, null, 2)}\n`);

    const [tampered, isolatedResult] = await Promise.all([
      Bun.spawn(["bun", verifier, "--verify", "--root", tamperedRoot, "--source-root", sourceRoot], { cwd: root, stdout: "pipe", stderr: "pipe" }).exited,
      Bun.spawn(["bun", verifier, "--verify", "--root", validRoot, "--source-root", sourceRoot], { cwd: root, stdout: "pipe", stderr: "pipe" }).exited,
    ]);

    expect(tampered).not.toBe(0); expect(isolatedResult).toBe(0); expect(statusFingerprint()).toBe(before);
  });
});
