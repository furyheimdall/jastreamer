import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { describe, expect, test } from "bun:test";
import { auditEvidenceIndex, CANONICAL_COMMAND, generateEvidenceIndex, stableRead } from "./task22-evidence-index.mjs";

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const withFixture = async (testBody) => {
  const root = await mkdtemp(join(tmpdir(), "task22-index-"));
  try {
    await writeFile(join(root, "retained.log"), "retained\n");
    return await testBody(root);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
};
const writeReferenced = async (root, path, value) => {
  const bytes = Buffer.from(typeof value === "string" ? value : JSON.stringify(value));
  await writeFile(join(root, path), bytes);
  return { path, sha256: sha256(bytes), size: bytes.byteLength };
};

describe("Task22 evidence index", () => {
  test("indexes the finalized DoneClaim and detects any post-index mutation", async () => withFixture(async (root) => {
    const claim = join(root, "DoneClaim.json");
    await writeFile(claim, '{"claim":"draft"}\n');
    await writeFile(claim, '{"claim":"final"}\n');
    const finalBytes = await readFile(claim);

    const index = await generateEvidenceIndex(root);
    const done = index.files.find(({ path }) => path === "DoneClaim.json");
    expect(done).toEqual({ path: "DoneClaim.json", sha256: sha256(finalBytes), size: finalBytes.byteLength });
    expect(index.files.some(({ path }) => path === "evidence-index.json")).toBe(false);
    for (const entry of index.files) { const bytes = await stableRead(join(root, entry.path)); expect({ sha256: sha256(bytes), size: bytes.byteLength }).toEqual({ sha256: entry.sha256, size: entry.size }); }
    expect(await auditEvidenceIndex(root)).toMatchObject({ files: 2, bad: 0, symlinks: 0, missing: 0, extra: 0 });

    await writeFile(claim, '{"claim":"post-index mutation"}\n');
    const mutated = await auditEvidenceIndex(root);
    expect(mutated.bad).toBeGreaterThan(0);
    expect(mutated.errors).toContain("MISMATCH DoneClaim.json");
  }));

  test("rejects a canonical verification claim whose count does not match its log", async () => withFixture(async (root) => {
    const sourceDigest = "a".repeat(64);
    const log = await writeReferenced(root, "canonical.log", " 12 pass\n 1 skip\n 0 fail\n");
    const exitCode = await writeReferenced(root, "canonical.exit-code", "0\n");
    const sourceManifest = await writeReferenced(root, "source.json", []);
    const runValue = { schemaVersion: 2, command: CANONICAL_COMMAND, sourceDigest, result: { passed: 13, skipped: 1, failed: 0 }, log, exitCode, sourceManifest };
    const canonicalRun = await writeReferenced(root, "canonical-run.json", runValue);
    const summaryValue = { schemaVersion: 2, sourceDigest, verification: { canonicalRun, canonical: { command: CANONICAL_COMMAND, ...runValue.result } } };
    const evidence = await writeReferenced(root, "security-summary.json", summaryValue);
    await writeFile(join(root, "DoneClaim.json"), JSON.stringify({ sourceDigest, verification: { canonicalRun }, evidence }));
    await generateEvidenceIndex(root); const result = await auditEvidenceIndex(root);
    expect(result.bad).toBeGreaterThan(0); expect(result.errors).toContain("CANONICAL_RESULT_MISMATCH");
  }));

  test("rejects the current cross-record source count and identity contradiction", async () => withFixture(async (root) => {
    const oldDigest = "e8fe973229afef3f0fab004e89e16ca34fa8151b84f31f05df979dfa212202f8";
    const currentDigest = "59355bc38fe7fb69d2f6b99e9e13bae37732749c8e13af56c4d187ffd7605c21";
    await writeFile(join(root, "canonical.log"), " 494 pass\n 1 skip\n 0 fail\n");
    await writeFile(join(root, "canonical-run.json"), JSON.stringify({ schemaVersion: 1, command: CANONICAL_COMMAND, sourceDigest: currentDigest, result: { passed: 494, skipped: 1, failed: 0 }, log: "canonical.log" }));
    await writeFile(join(root, "security-summary.json"), JSON.stringify({ schemaVersion: 1, sourceDigest: oldDigest, verification: { canonical: { passed: 487, skipped: 1, failed: 0 } } }));
    await writeFile(join(root, "DoneClaim.json"), JSON.stringify({ schemaVersion: 1, sourceDigest: oldDigest, verification: { canonicalRun: "canonical-run.json" }, evidence: "security-summary.json" }));

    await generateEvidenceIndex(root);
    const result = await auditEvidenceIndex(root);

    expect(result.bad).toBeGreaterThan(0);
    expect(result.errors).toContain("LINKED_SOURCE_DIGEST_MISMATCH");
    expect(result.errors).toContain("LINKED_CANONICAL_RESULT_MISMATCH");
    expect(result.errors).toContain("LINKED_EVIDENCE_IDENTITY_INVALID");
  }));

  test("rejects symlinks instead of indexing their targets", async () => withFixture(async (root) => {
    await writeFile(join(root, "DoneClaim.json"), '{"claim":"final"}\n'); await symlink("retained.log", join(root, "linked.log"));
    expect(generateEvidenceIndex(root)).rejects.toThrow("SYMLINK linked.log");
  }));
});
