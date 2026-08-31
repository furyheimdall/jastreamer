import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { execFileSync } from "node:child_process";
import { cp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { bindings, commands, sourceIdentity, transcriptSetDigest } from "./task23-evidence.mjs";
import { ledgerEntry } from "./task23-inventory.mjs";
import { currentDeliveryScope, productFiles } from "./task23-source-policy.mjs";

const repositoryRoot = resolve(import.meta.dirname, "..", "..");
const cleanupProofSource = resolve(repositoryRoot, ".omo/evidence/functional-jastreamer-products/task-23-cleanup-proof");
const sha = (value) => createHash("sha256").update(value).digest("hex");
const jsonBytes = (value) => Buffer.from(`${JSON.stringify(value, null, 2)}\n`);
const reference = (path, bytes) => ({ path, sha256: sha(bytes), size: bytes.length });
const emptyInventory = () => ({ schemaVersion: 1, observedAt: "2026-01-01T00:00:00.000Z", processes: [], containers: [], listeners: [], temporaryDirectories: [], builders: [], browsers: [] });
const writeJson = async (root, path, value) => { const bytes = jsonBytes(value); await writeFile(join(root, path), bytes); return reference(path, bytes); };

export const createTask23SourceFixture = async (sourceRoot) => {
  execFileSync("git", ["clone", "--quiet", "--shared", repositoryRoot, sourceRoot]);
  for (const record of currentDeliveryScope) {
    if (record.previousPath !== undefined) await rm(join(sourceRoot, record.previousPath), { recursive: true, force: true });
    if (!record.present) await rm(join(sourceRoot, record.path), { recursive: true, force: true });
  }
  for (const path of productFiles) {
    await mkdir(dirname(join(sourceRoot, path)), { recursive: true }); await cp(join(repositoryRoot, path), join(sourceRoot, path));
  }
};

export const createTask23EvidenceFixture = async (evidenceRoot, sourceRoot) => {
  await Promise.all([mkdir(join(evidenceRoot, "captures"), { recursive: true }), mkdir(join(evidenceRoot, "transcripts"), { recursive: true })]);
  await cp(cleanupProofSource, join(evidenceRoot, "cleanup-proof"), { recursive: true });
  const source = await sourceIdentity(sourceRoot); const currentBindings = await bindings(sourceRoot);
  const ledger = []; let previousSha256 = "0".repeat(64);
  for (const command of commands) {
    const entry = ledgerEntry({ sequence: ledger.length + 1, command, previousSha256, sourceText: "" });
    ledger.push(entry); previousSha256 = entry.entrySha256;
  }
  const ledgerPath = "captures/external-write-ledger.jsonl"; const ledgerBytes = Buffer.from(`${ledger.map((entry) => JSON.stringify(entry)).join("\n")}\n`);
  await writeFile(join(evidenceRoot, ledgerPath), ledgerBytes); const ledgerRef = reference(ledgerPath, ledgerBytes);
  const { privateKey, publicKey } = generateKeyPairSync("ed25519"); const spki = publicKey.export({ type: "spki", format: "der" }); const publicBytes = publicKey.export({ type: "spki", format: "pem" }); const keyId = sha(spki);
  await writeFile(join(evidenceRoot, "task23-public-key.pem"), publicBytes);
  const transcripts = [];
  for (const [index, command] of commands.entries()) {
    const allocation = { type: "process", id: `fixture-process-${index}`, pid: 10000 + index, identitySha256: sha(command.id), observedBy: "fixture" };
    const before = await writeJson(evidenceRoot, `captures/${command.id}.inventory-before.json`, emptyInventory());
    const during = await writeJson(evidenceRoot, `captures/${command.id}.inventory-during.json`, { ...emptyInventory(), processes: [allocation] });
    const after = await writeJson(evidenceRoot, `captures/${command.id}.inventory-after.json`, emptyInventory());
    const stdout = await writeJson(evidenceRoot, `captures/${command.id}.stdout.json`, { value: `${command.id}: fixture passed\n` });
    const stderr = await writeJson(evidenceRoot, `captures/${command.id}.stderr.json`, { value: "" });
    const transcript = {
      schemaVersion: 3, kind: "task23_command_transcript", id: command.id, source, bindings: currentBindings,
      command: { cwd: command.cwd, argv: command.argv }, executable: { name: command.argv[0], sha256: sha(command.argv[0]) },
      startedAt: "2026-01-01T00:00:00.000Z", endedAt: "2026-01-01T00:00:00.001Z", exitCode: 0, status: "passed",
      code: command.id === "k17-false-gate" ? "AWAITING_EXTERNAL_AUTHORIZATION" : "COMMAND_PASSED",
      captures: { stdout, stderr, bodies: [stdout] }, inventories: { before, during, after, allocated: [allocation] },
      ledgerSequence: index + 1, externalWrites: 0, externalWriteLedger: ledgerRef, redactionScan: "passed",
    };
    const bytes = jsonBytes(transcript); const path = `transcripts/${command.id}.json`; await writeFile(join(evidenceRoot, path), bytes);
    transcripts.push({ ...reference(path, bytes), keyId, signature: sign(null, bytes, privateKey).toString("base64") });
  }
  const finalInventory = await writeJson(evidenceRoot, "captures/final-inventory.json", emptyInventory());
  const redactionBytes = jsonBytes({ schemaVersion: 1, kind: "task23_redaction_receipt", transcriptIds: commands.map(({ id }) => id), filesScanned: commands.length * 6 + 2, result: "passed" });
  await writeFile(join(evidenceRoot, "redaction-receipt.json"), redactionBytes); const redactionReceipt = reference("redaction-receipt.json", redactionBytes);
  const transcriptSetSha256 = transcriptSetDigest(transcripts);
  const claimBytes = jsonBytes({ schemaVersion: 2, kind: "task23_final_machine_claim", sourceRevision: source.revision, sourceProductDigest: source.productDigest, candidateSetSha256: currentBindings.candidateSetSha256, contractSetSha256: currentBindings.contractSetSha256, schemaSetSha256: currentBindings.schemaSetSha256, transcriptCount: transcripts.length, transcriptSetSha256, ledgerSha256: ledgerRef.sha256, finalInventorySha256: finalInventory.sha256, externalWrites: 0, finalResources: 0, k17: "awaiting_external_authorization", wasapi: "awaiting_external_authorization" });
  await writeFile(join(evidenceRoot, "final-machine-claim.json"), claimBytes); const finalMachineClaim = { ...reference("final-machine-claim.json", claimBytes), keyId, signature: sign(null, claimBytes, privateKey).toString("base64") };
  const cleanupProofBytes = await readFile(join(evidenceRoot, "cleanup-proof/index.json"));
  const index = { schemaVersion: 4, kind: "task23_authenticated_evidence", recordedAt: "2026-01-01T00:00:00.000Z", source, bindings: currentBindings, keyId, publicKey: { path: "task23-public-key.pem", sha256: sha(publicBytes), size: publicBytes.length, spkiDer: spki.toString("base64") }, externalWriteLedger: ledgerRef, finalInventory, cleanupProof: reference("index.json", cleanupProofBytes), redactionReceipt, transcriptSetSha256, finalMachineClaim, transcripts, signingMaterialRetained: false };
  await writeFile(join(evidenceRoot, "evidence-index.json"), jsonBytes(index));
};
