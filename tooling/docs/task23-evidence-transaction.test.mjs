import { afterEach, describe, expect, test } from "bun:test";
import { mkdtemp, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { publishStagedEvidence, recoverStagedEvidence, task23EvidenceTransactionPaths } from "./task23-evidence-transaction.mjs";

const roots = [];
const temporaryRoot = async () => {
  const root = await mkdtemp(join(tmpdir(), "jastreamer-task23-transaction-test-"));
  roots.push(root);
  return root;
};
const siblingArtifacts = async (destination) => (await readdir(join(destination, "..")))
  .filter((name) => name.startsWith(`${basename(destination)}.`));

afterEach(async () => Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))));

describe("Task 23 evidence publication transaction", () => {
  test("publishes a complete staging tree when the destination is absent", async () => {
    // Given: a private sibling staging tree and no destination.
    const root = await temporaryRoot(); const destination = join(root, "evidence"); const staging = join(root, "evidence.staging-fixture");
    await Bun.write(join(staging, "evidence-index.json"), "complete\n");
    // When: the staged evidence is published.
    await publishStagedEvidence({ destination, staging });
    // Then: only the complete destination remains.
    expect(await readFile(join(destination, "evidence-index.json"), "utf8")).toBe("complete\n");
    expect(await siblingArtifacts(destination)).toEqual([]);
  });

  test("atomically replaces a previous destination", async () => {
    // Given: previous evidence and a complete private sibling staging tree.
    const root = await temporaryRoot(); const destination = join(root, "evidence"); const staging = join(root, "evidence.staging-fixture");
    await Bun.write(join(destination, "evidence-index.json"), "previous\n"); await Bun.write(join(staging, "evidence-index.json"), "replacement\n");
    // When: replacement is published.
    await publishStagedEvidence({ destination, staging });
    // Then: the replacement is visible with no backup residue.
    expect(await readFile(join(destination, "evidence-index.json"), "utf8")).toBe("replacement\n");
    expect(await siblingArtifacts(destination)).toEqual([]);
  });

  test("restores byte-identical previous evidence when the publish rename fails", async () => {
    // Given: previous evidence, staging evidence, and a rename operation that fails only while publishing staging.
    const root = await temporaryRoot(); const destination = join(root, "evidence"); const staging = join(root, "evidence.staging-fixture");
    await Bun.write(join(destination, "evidence-index.json"), "previous\n"); await Bun.write(join(staging, "evidence-index.json"), "replacement\n");
    const renameCalls = [];
    const rename = async (source, target) => {
      renameCalls.push([source, target]);
      if (source === staging) throw new Error("INJECTED_RENAME_FAILURE");
      await import("node:fs/promises").then((fs) => fs.rename(source, target));
    };
    // When: publication fails after the destination moved to backup.
    await expect(publishStagedEvidence({ destination, staging, rename })).rejects.toThrow("INJECTED_RENAME_FAILURE");
    // Then: previous bytes are restored and private artifacts are removed.
    expect(await readFile(join(destination, "evidence-index.json"), "utf8")).toBe("previous\n");
    expect(renameCalls).toHaveLength(3); expect(await siblingArtifacts(destination)).toEqual([]);
  });

  test("finishes a backed-up transaction after process restart", async () => {
    const root = await temporaryRoot(); const destination = join(root, "evidence"); const staging = join(root, "evidence.staging-fixture"); const paths = task23EvidenceTransactionPaths(destination, staging);
    await Bun.write(join(destination, "evidence-index.json"), "previous\n"); await Bun.write(join(staging, "evidence-index.json"), "replacement\n"); await writeFile(paths.marker, `${JSON.stringify({ schemaVersion: 1, kind: "task23_evidence_transaction", phase: "backed_up", destination, staging, backup: paths.backup })}\n`); await rename(destination, paths.backup);
    expect(await recoverStagedEvidence({ destination })).toBe(true);
    expect(await readFile(join(destination, "evidence-index.json"), "utf8")).toBe("replacement\n"); expect(await siblingArtifacts(destination)).toEqual([]);
  });

  test("restores previous evidence when interruption precedes durable backup state", async () => {
    const root = await temporaryRoot(); const destination = join(root, "evidence"); const staging = join(root, "evidence.staging-fixture"); const paths = task23EvidenceTransactionPaths(destination, staging);
    await Bun.write(join(destination, "evidence-index.json"), "previous\n"); await Bun.write(join(staging, "evidence-index.json"), "replacement\n"); await writeFile(paths.marker, `${JSON.stringify({ schemaVersion: 1, kind: "task23_evidence_transaction", phase: "prepared", destination, staging, backup: paths.backup })}\n`); await rename(destination, paths.backup);
    expect(await recoverStagedEvidence({ destination })).toBe(true);
    expect(await readFile(join(destination, "evidence-index.json"), "utf8")).toBe("previous\n"); expect(await siblingArtifacts(destination)).toEqual([]);
  });

  test("keeps installed evidence and removes backup residue after restart", async () => {
    const root = await temporaryRoot(); const destination = join(root, "evidence"); const staging = join(root, "evidence.staging-fixture"); const paths = task23EvidenceTransactionPaths(destination, staging);
    await Bun.write(join(destination, "evidence-index.json"), "replacement\n"); await Bun.write(join(paths.backup, "evidence-index.json"), "previous\n"); await writeFile(paths.marker, `${JSON.stringify({ schemaVersion: 1, kind: "task23_evidence_transaction", phase: "installed", destination, staging, backup: paths.backup })}\n`);
    expect(await recoverStagedEvidence({ destination })).toBe(true);
    expect(await readFile(join(destination, "evidence-index.json"), "utf8")).toBe("replacement\n"); expect(await siblingArtifacts(destination)).toEqual([]);
  });
});
