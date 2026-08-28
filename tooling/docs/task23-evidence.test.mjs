import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { cp, lstat, mkdir, mkdtemp, readFile, readdir, rm, symlink, unlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import { documentedNonSourceCategories, productDigest, productFiles, sourceFileRecords, todo22ChangedFiles } from "./task23-source-policy.mjs";

const root = resolve(import.meta.dirname, "..", "..");
const canonical = resolve(root, ".omo/evidence/functional-jastreamer-products/task-23");
const verifier = resolve(root, "tooling/docs/task23-evidence.mjs");
const sha = (value) => createHash("sha256").update(value).digest("hex");

const run = (evidenceRoot) => Bun.spawnSync(["bun", verifier, "--verify", "--root", evidenceRoot], { cwd: root, stdout: "pipe", stderr: "pipe" });
const treeDigest = async (directory) => {
  const files = [];
  const visit = async (path) => {
    for (const entry of await readdir(path, { withFileTypes: true })) {
      const child = join(path, entry.name);
      if (entry.isDirectory()) await visit(child);
      else files.push({ path: relative(directory, child), sha256: sha(await readFile(child)) });
    }
  };
  await visit(directory); files.sort((left, right) => left.path.localeCompare(right.path));
  return sha(JSON.stringify(files));
};
const withCopy = async (action) => {
  const temporary = await mkdtemp(join(tmpdir(), "jastreamer-task23-evidence-test-"));
  const copied = join(temporary, "evidence");
  try { await cp(canonical, copied, { recursive: true }); return await action(copied); }
  finally { await rm(temporary, { recursive: true, force: true }); }
};
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

describe("Task 23 evidence isolation", () => {
  test("accepts complete source-bound Ed25519 evidence from an explicit root", async () => {
    const index = JSON.parse(await readFile(join(canonical, "evidence-index.json"), "utf8"));
    const covered = new Set([...productFiles, ...Object.values(documentedNonSourceCategories).flat()]);
    expect(todo22ChangedFiles).toHaveLength(41); expect(todo22ChangedFiles.filter((path) => !covered.has(path))).toEqual([]);
    expect(index.source.todo22Coverage).toEqual({ covered: 41, total: 41 });
    const orderedSet = index.transcripts.map(({ path, sha256, size }) => ({ path, sha256, size }));
    expect(index.transcriptSetSha256).toBe(sha(JSON.stringify(orderedSet)));
    expect(index.redactionReceipt).toEqual(expect.objectContaining({ path: "redaction-receipt.json", sha256: expect.any(String) }));
    expect(index.finalMachineClaim).toEqual(expect.objectContaining({ path: "final-machine-claim.json", sha256: expect.any(String), signature: expect.any(String) }));
    const claim = JSON.parse(await readFile(join(canonical, index.finalMachineClaim.path), "utf8")); expect(claim.transcriptSetSha256).toBe(index.transcriptSetSha256);

    const fixture = await mkdtemp(join(tmpdir(), "jastreamer-task23-source-test-"));
    try {
      for (const path of productFiles) { await mkdir(dirname(join(fixture, path)), { recursive: true }); await cp(join(root, path), join(fixture, path)); }
      const baseline = productDigest(await sourceFileRecords(fixture));
      for (const path of ["packaging/server/tooling/identity.ts", "packaging/server/tests/release.test.ts", "tooling/qa/task19/installed-product-fixture.mjs"]) {
        const target = join(fixture, path); const original = await readFile(target); await writeFile(target, Buffer.concat([original, Buffer.from("\n// task23 source mutation\n")]));
        expect(productDigest(await sourceFileRecords(fixture))).not.toBe(baseline); await writeFile(target, original);
      }
    } finally { await rm(fixture, { recursive: true, force: true }); }

    const result = run(canonical); expect(result.exitCode).toBe(0);
    expect(JSON.parse(result.stdout.toString())).toEqual(expect.objectContaining({ transcripts: 9, ledgerEntries: 9, finalResources: 0, redaction: "passed", machineClaim: "verified" }));
  });

  test("rejects transcript-set mutations and forged signatures only in run-unique copies", async () => {
    for (const mutation of [
      (index) => index.transcripts.pop(),
      (index) => index.transcripts.reverse(),
      (index) => index.transcripts.push(index.transcripts[0]),
      (index) => { index.transcripts[0].sha256 = "0".repeat(64); },
      (index) => { index.transcriptSetSha256 = "0".repeat(64); },
    ]) await withCopy(async (copied) => {
      const path = join(copied, "evidence-index.json"); const index = JSON.parse(await readFile(path, "utf8")); mutation(index); await writeFile(path, `${JSON.stringify(index, null, 2)}\n`);
      reject(run(copied), "TRANSCRIPT_SET_INVALID");
    });
    await withCopy(async (copied) => {
      const path = join(copied, "evidence-index.json"); const index = JSON.parse(await readFile(path, "utf8"));
      index.transcripts[0].signature = Buffer.alloc(64).toString("base64"); await writeFile(path, `${JSON.stringify(index, null, 2)}\n`);
      reject(run(copied), "TRANSCRIPT_AUTHENTICATION_FAILED");
    });
  });

  test("rejects copied output and ledger tampering", async () => {
    await withCopy(async (copied) => {
      const index = JSON.parse(await readFile(join(copied, "evidence-index.json"), "utf8"));
      const transcript = JSON.parse(await readFile(join(copied, index.transcripts[0].path), "utf8"));
      await writeFile(join(copied, transcript.captures.stdout.path), "changed");
      reject(run(copied), "CAPTURE_INVALID");
    });
    await withCopy(async (copied) => {
      const index = JSON.parse(await readFile(join(copied, "evidence-index.json"), "utf8"));
      await writeFile(join(copied, index.externalWriteLedger.path), "tampered\n");
      reject(run(copied), "LEDGER_PROVENANCE_INVALID");
    });
  });

  for (const [kind, select] of [
    ["stdout", (value) => value.captures.stdout], ["stderr", (value) => value.captures.stderr],
    ["body", (value) => value.captures.bodies[0]], ["before", (value) => value.inventories.before],
    ["during", (value) => value.inventories.during], ["after", (value) => value.inventories.after],
  ]) test(`rejects nested ${kind} capture tampering`, () => withCopy(async (copied) => {
    const { proofRoot, transcript } = await nested(copied); const ref = select(transcript);
    await writeFile(join(proofRoot, ref.path), `${kind}-tampered`);
    reject(run(copied), "CLEANUP_PROOF_CAPTURE_INVALID");
  }));

  test("rejects missing and symlinked nested captures", async () => {
    await withCopy(async (copied) => {
      const { proofRoot, transcript } = await nested(copied); await unlink(join(proofRoot, transcript.inventories.before.path));
      reject(run(copied), "CLEANUP_PROOF_CAPTURE_MISSING");
    });
    await withCopy(async (copied) => {
      const { proofRoot, transcript } = await nested(copied); const path = join(proofRoot, transcript.inventories.after.path);
      const target = `${path}.target`; await writeFile(target, await readFile(path)); await unlink(path); await symlink(relative(dirname(path), target), path);
      expect((await lstat(path)).isSymbolicLink()).toBe(true);
      reject(run(copied), "CLEANUP_PROOF_CAPTURE_SYMLINK");
    });
  });

  test("runs copied tampering concurrently without changing canonical evidence", async () => withCopy(async (copied) => {
    const before = await treeDigest(canonical); const path = join(copied, "evidence-index.json");
    const index = JSON.parse(await readFile(path, "utf8")); index.transcripts[0].signature = Buffer.alloc(64).toString("base64"); await writeFile(path, `${JSON.stringify(index, null, 2)}\n`);

    const [tampered, canonicalResult] = await Promise.all([
      Bun.spawn(["bun", verifier, "--verify", "--root", copied], { cwd: root, stdout: "pipe", stderr: "pipe" }).exited,
      Bun.spawn(["bun", verifier, "--verify", "--root", canonical], { cwd: root, stdout: "pipe", stderr: "pipe" }).exited,
    ]);

    expect(tampered).not.toBe(0); expect(canonicalResult).toBe(0); expect(await treeDigest(canonical)).toBe(before);
  }));
});
