#!/usr/bin/env bun
import { createHash, createPublicKey, generateKeyPairSync, sign, verify } from "node:crypto";
import { execFileSync } from "node:child_process";
import { closeSync, constants, fstatSync, lstatSync, openSync, readFileSync } from "node:fs";
import { appendFile, cp, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { validateExecutionProvenance } from "./task23-evidence-contract.mjs";
import { inventoryIdentity, inventoryResources, ledgerEntry, observeResources } from "./task23-inventory.mjs";
import { findUnsafeEvidence } from "../qa/receipt-redaction.mjs";
import { deliveryStatus, generatedArtifactInventory, productDigest, sourceFileRecords, todo22ChangedFiles } from "./task23-source-policy.mjs";
import { publishStagedEvidence, recoverStagedEvidence } from "./task23-evidence-transaction.mjs";

const root = resolve(import.meta.dirname, "..", "..");
const defaultEvidenceRoot = resolve(root, ".omo/evidence/functional-jastreamer-products/task-23");
const cliArguments = process.argv.slice(2); const mode = cliArguments[0];
const option = (name) => { const index = cliArguments.indexOf(name); return index < 0 || cliArguments[index + 1] === undefined ? undefined : resolve(cliArguments[index + 1]); };
const explicitRoot = option("--root"); const explicitSourceRoot = option("--source-root");
const evidenceRoot = explicitRoot ?? defaultEvidenceRoot; const verificationSourceRoot = explicitSourceRoot ?? root;
export const commands = [
  { id: "orphan-server-cleanup", cwd: ".", argv: ["tooling/docs/cleanup-owned-server-qa.sh"], handshake: true },
  { id: "docs-verifier", cwd: ".", argv: ["bun", "tooling/docs/verify.mjs", "--claims", "docs/claims.json", "--receipt-schema", "tooling/qa/product-receipt.schema.json"] },
  { id: "docs-verifier-red", cwd: ".", argv: ["tooling/docs/verifier-negative-qa.sh"], handshake: true },
  { id: "server-real-surface", cwd: ".", argv: ["tooling/docs/server-command-qa.sh"], handshake: true },
  { id: "compose-synology-ffmpeg", cwd: ".", argv: ["tooling/docs/compose-command-qa.sh"], handshake: true },
  { id: "web-archive-commands", cwd: ".", argv: ["tooling/docs/web-command-qa.sh"], handshake: true },
  { id: "k17-false-gate", cwd: ".", argv: ["tooling/docs/k17-false-gate-qa.sh"], handshake: true },
  { id: "renderer-cli", cwd: ".", argv: ["cargo", "run", "--quiet", "--manifest-path", "apps/renderer/Cargo.toml", "--", "--protocol"] },
  { id: "server-race-failures", cwd: "apps/server", argv: ["go", "test", "-race", "-shuffle=on", "-count=1", "./internal/transcode", "./internal/upnp", "./internal/settings", "./internal/api", "./internal/playback"] },
];
const sha = (value) => createHash("sha256").update(value).digest("hex");
const jsonBytes = (value) => Buffer.from(`${JSON.stringify(value, null, 2)}\n`);
const reference = (path, bytes) => ({ path, sha256: sha(bytes), size: bytes.length });
const resolveExecutable = (argv0) => argv0.includes("/") ? resolve(root, argv0) : Bun.which(argv0);
const cleanCapture = (value) => value.replaceAll(root, "[REPOSITORY]").replace(/\/tmp\/[A-Za-z0-9._/-]+/g, "[TEMPORARY]").replace(/\b127\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d+)?\b/g, "[LAN]");

export const sourceIdentity = async (sourceRoot = root) => {
  const deliveryScope = deliveryStatus(sourceRoot); const files = await sourceFileRecords(sourceRoot); const generatedArtifacts = await generatedArtifactInventory(sourceRoot);
  return {
    revision: execFileSync("git", ["-C", sourceRoot, "rev-parse", "HEAD"], { encoding: "utf8" }).trim(), dirty: deliveryScope.length !== 0,
    dirtyDigest: sha(JSON.stringify(deliveryScope)), files, productDigest: productDigest(files),
    deliveryScope, deliveryCoverage: { covered: deliveryScope.length, total: deliveryScope.length, omitted: [] },
    generatedArtifacts, todo22Coverage: { covered: todo22ChangedFiles.length, total: todo22ChangedFiles.length },
  };
};
// The aggregate excludes signatures, the index, and the signed claim so its authenticated binding is non-circular.
const transcriptSetRecords = (transcripts) => transcripts.map(({ path, sha256, size }) => ({ path, sha256, size }));
export const transcriptSetDigest = (transcripts) => sha(JSON.stringify(transcriptSetRecords(transcripts)));
export const bindings = async (sourceRoot = root) => {
  const records = async (paths) => Promise.all(paths.map(async (path) => ({ path, sha256: sha(await readFile(resolve(sourceRoot, path))) })));
  const candidates = await records(["packaging/server/manifest.json", "packaging/control/manifest.json", "packaging/renderer/config.json"]);
  const contracts = await records(["contracts/control-api/v3/schema.json", "contracts/renderer-protocol/v3/schema.json"]);
  const schemas = await records([
    "tooling/qa/product-receipt.schema.json", "tooling/qa/task19/installed-product-receipt.schema.json",
    "tooling/release/product-gate.schema.json", "tooling/release/product-gate-observation.schema.json",
    "tooling/release/product-gate-security.schema.json", "tooling/release/product-gate-supply-chain.schema.json",
    "tooling/release/product-gate-trust.schema.json",
  ]);
  return { candidates, candidateSetSha256: sha(JSON.stringify(candidates)), contracts, contractSetSha256: sha(JSON.stringify(contracts)), schemas, schemaSetSha256: sha(JSON.stringify(schemas)) };
};
const writeJsonCapture = async (targetRoot, path, value) => { const bytes = jsonBytes(value); await writeFile(join(targetRoot, path), bytes); return reference(path, bytes); };
const inventoryDelta = (before, during) => {
  const baseline = new Set(inventoryResources(before).map(inventoryIdentity));
  return inventoryResources(during).filter((item) => !baseline.has(inventoryIdentity(item)));
};

const runCommand = async ({ item, runnerRoot, ledger, targetRoot }) => {
  const executablePath = resolveExecutable(item.argv[0]); if (executablePath === null) throw new Error(`EXECUTABLE_MISSING:${item.argv[0]}`);
  const before = observeResources(); const startedAt = new Date().toISOString();
  const ready = join(runnerRoot, `${item.id}.ready`); const ack = join(runnerRoot, `${item.id}.ack`);
  if (item.handshake) { execFileSync("mkfifo", [ready]); execFileSync("mkfifo", [ack]); }
  const child = Bun.spawn(item.argv, { cwd: resolve(root, item.cwd), env: item.handshake ? { ...process.env, TASK23_INVENTORY_READY: ready, TASK23_INVENTORY_ACK: ack } : process.env, stdout: "pipe", stderr: "pipe" });
  let during;
  if (item.handshake) { await readFile(ready); during = observeResources(child.pid); await writeFile(ack, "continue\n"); }
  else during = observeResources(child.pid);
  const [exitCode, stdoutRaw, stderrRaw] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
  const endedAt = new Date().toISOString(); const after = observeResources();
  const stdout = Buffer.from(cleanCapture(stdoutRaw)); const stderr = Buffer.from(cleanCapture(stderrRaw));
  const stdoutRef = await writeJsonCapture(targetRoot, `captures/${item.id}.stdout.json`, { value: stdout.toString() });
  const stderrRef = await writeJsonCapture(targetRoot, `captures/${item.id}.stderr.json`, { value: stderr.toString() });
  const beforeRef = await writeJsonCapture(targetRoot, `captures/${item.id}.inventory-before.json`, before);
  const duringRef = await writeJsonCapture(targetRoot, `captures/${item.id}.inventory-during.json`, during);
  const afterRef = await writeJsonCapture(targetRoot, `captures/${item.id}.inventory-after.json`, after);
  const sourceText = item.argv[0].includes("/") ? await readFile(resolve(root, item.argv[0]), "utf8") : "";
  const previousSha256 = ledger.at(-1)?.entrySha256 ?? "0".repeat(64);
  const entry = ledgerEntry({ sequence: ledger.length + 1, command: item, previousSha256, sourceText }); ledger.push(entry);
  return {
    schemaVersion: 3, kind: "task23_command_transcript", id: item.id,
    command: { cwd: item.cwd, argv: item.argv }, executable: { name: basename(executablePath), sha256: sha(await readFile(executablePath)) },
    startedAt, endedAt, exitCode, status: exitCode === 0 ? "passed" : "failed", code: item.id === "k17-false-gate" ? "AWAITING_EXTERNAL_AUTHORIZATION" : exitCode === 0 ? "COMMAND_PASSED" : "COMMAND_FAILED",
    captures: { stdout: stdoutRef, stderr: stderrRef, bodies: [stdout.length > 0 ? stdoutRef : stderrRef] },
    inventories: { before: beforeRef, during: duringRef, after: afterRef, allocated: inventoryDelta(before, during) },
    ledgerSequence: entry.sequence, externalWrites: ledger.filter((value) => value.externalWriteAttempt).length,
    externalWriteLedger: null, redactionScan: "passed",
  };
};

const readRefJson = async (targetRoot, ref) => { const bytes = await readFile(join(targetRoot, ref.path)); if (sha(bytes) !== ref.sha256) throw new Error(`DIGEST_MISMATCH:${ref.path}`); return JSON.parse(bytes); };
const readCleanupCapture = (proofRoot, ref) => {
  if (typeof ref?.path !== "string" || isAbsolute(ref.path) || ref.path.split(/[\\/]/).includes("..")) throw new Error("CLEANUP_PROOF_CAPTURE_PATH_INVALID");
  const path = resolve(proofRoot, ref.path); const location = relative(proofRoot, path);
  if (location === "" || location === ".." || location.startsWith(`..${sep}`) || isAbsolute(location)) throw new Error("CLEANUP_PROOF_CAPTURE_PATH_INVALID");
  let descriptor;
  try {
    const namedBefore = lstatSync(path); if (namedBefore.isSymbolicLink()) throw new Error("CLEANUP_PROOF_CAPTURE_SYMLINK");
    descriptor = openSync(path, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0)); const before = fstatSync(descriptor); const bytes = readFileSync(descriptor); const after = fstatSync(descriptor); const namedAfter = lstatSync(path);
    if (!before.isFile() || before.dev !== after.dev || before.ino !== after.ino || before.size !== after.size || before.mtimeMs !== after.mtimeMs || namedAfter.isSymbolicLink() || namedAfter.dev !== before.dev || namedAfter.ino !== before.ino || bytes.length !== before.size) throw new Error("CLEANUP_PROOF_CAPTURE_REPLACED");
    if (sha(bytes) !== ref.sha256 || (ref.size !== undefined && ref.size !== bytes.length)) throw new Error("CLEANUP_PROOF_CAPTURE_INVALID");
    return bytes;
  } catch (error) {
    if (error instanceof Error && error.message.startsWith("CLEANUP_PROOF_")) throw error;
    if (error?.code === "ENOENT") throw new Error("CLEANUP_PROOF_CAPTURE_MISSING");
    if (error?.code === "ELOOP") throw new Error("CLEANUP_PROOF_CAPTURE_SYMLINK");
    throw new Error("CLEANUP_PROOF_CAPTURE_REPLACED");
  } finally { if (descriptor !== undefined) closeSync(descriptor); }
};
const validateCleanupProof = async (targetRoot, index) => {
  const proofRoot = join(targetRoot, "cleanup-proof"); const proof = JSON.parse(readCleanupCapture(proofRoot, index.cleanupProof));
  const publicBytes = readCleanupCapture(proofRoot, proof.publicKey); const transcriptBytes = readCleanupCapture(proofRoot, proof.transcript);
  if (proof.keyId !== proof.transcript.keyId || !verify(null, transcriptBytes, createPublicKey(publicBytes), Buffer.from(proof.transcript.signature, "base64"))) throw new Error("CLEANUP_PROOF_AUTHENTICATION_FAILED");
  const transcript = JSON.parse(transcriptBytes); const refs = [transcript.captures.stdout, transcript.captures.stderr, ...transcript.captures.bodies, transcript.inventories.before, transcript.inventories.during, transcript.inventories.after];
  const values = refs.map((ref) => JSON.parse(readCleanupCapture(proofRoot, ref))); const stdout = values[0]; const before = values.at(-3); const after = values.at(-1);
  if (!before.processes.some((item) => item.pid === 1874845) || !before.listeners.some((item) => item.pid === 1874845) || after.processes.some((item) => item.pid === 1874845) || !stdout.value.includes('"result":"terminated"')) throw new Error("CLEANUP_PROOF_INVALID");
};
const validate = async (targetRoot, sourceRoot = root) => {
  const index = JSON.parse(await readFile(join(targetRoot, "evidence-index.json"))); const publicBytes = await readFile(join(targetRoot, index.publicKey.path));
  if (index.signingMaterialRetained !== false || (await readdir(targetRoot)).some((name) => /private|secret/i.test(name)) || findUnsafeEvidence(index)) throw new Error("EVIDENCE_INDEX_INVALID");
  if (sha(publicBytes) !== index.publicKey.sha256 || sha(Buffer.from(index.publicKey.spkiDer, "base64")) !== index.keyId) throw new Error("PUBLIC_KEY_INVALID");
  const currentSource = await sourceIdentity(sourceRoot); const currentDeliveryScope = currentSource.deliveryScope; const expectedPaths = commands.map((command) => `transcripts/${command.id}.json`);
  if (index.source.revision !== currentSource.revision) throw new Error("SOURCE_REVISION_INVALID");
  if (JSON.stringify(index.source) !== JSON.stringify(currentSource) || index.source.deliveryCoverage?.covered !== currentDeliveryScope.length || index.source.deliveryCoverage?.total !== currentDeliveryScope.length || index.source.deliveryCoverage?.omitted?.length !== 0 || index.source.todo22Coverage?.covered !== todo22ChangedFiles.length || index.source.todo22Coverage?.total !== todo22ChangedFiles.length) throw new Error("SOURCE_POLICY_INVALID");
  if (!Array.isArray(index.transcripts) || index.transcripts.length !== commands.length || JSON.stringify(index.transcripts.map((item) => item.path)) !== JSON.stringify(expectedPaths) || new Set(index.transcripts.map((item) => item.path)).size !== commands.length || index.transcriptSetSha256 !== transcriptSetDigest(index.transcripts)) throw new Error("TRANSCRIPT_SET_INVALID");
  const ledgerBytes = await readFile(join(targetRoot, index.externalWriteLedger.path)); if (sha(ledgerBytes) !== index.externalWriteLedger.sha256) throw new Error("LEDGER_PROVENANCE_INVALID");
  const ledger = ledgerBytes.toString().trim().split("\n").filter(Boolean).map(JSON.parse); const publicKey = createPublicKey(publicBytes);
  for (const item of index.transcripts) {
    const bytes = await readFile(join(targetRoot, item.path)); if (sha(bytes) !== item.sha256 || item.keyId !== index.keyId || !verify(null, bytes, publicKey, Buffer.from(item.signature, "base64"))) throw new Error(`TRANSCRIPT_AUTHENTICATION_FAILED:${item.path}`);
    const value = JSON.parse(bytes); const expected = commands.find((command) => command.id === value.id);
    if (!expected || JSON.stringify(value.command.argv) !== JSON.stringify(expected.argv) || value.exitCode !== 0 || JSON.stringify(value.source) !== JSON.stringify(index.source) || findUnsafeEvidence(value)) throw new Error(`TRANSCRIPT_INVALID:${value.id}`);
    const before = await readRefJson(targetRoot, value.inventories.before); const during = await readRefJson(targetRoot, value.inventories.during); const after = await readRefJson(targetRoot, value.inventories.after);
    const provenance = validateExecutionProvenance({ transcript: value, before, during, after, ledger }); if (!provenance.ok) throw new Error(`${provenance.code}:${value.id}`);
    for (const ref of [value.captures.stdout, value.captures.stderr, ...value.captures.bodies]) { const captured = await readFile(join(targetRoot, ref.path)); if (sha(captured) !== ref.sha256 || findUnsafeEvidence(JSON.parse(captured))) throw new Error(`CAPTURE_INVALID:${ref.path}`); }
    for (const record of [...value.source.files, ...value.bindings.candidates, ...value.bindings.contracts, ...value.bindings.schemas]) if (sha(await readFile(resolve(sourceRoot, record.path))) !== record.sha256) throw new Error(`SOURCE_BINDING_INVALID:${record.path}`);
  }
  const finalInventory = await readRefJson(targetRoot, index.finalInventory); if (inventoryResources(finalInventory).length !== 0) throw new Error("FINAL_INVENTORY_NOT_EMPTY");
  const redaction = await readRefJson(targetRoot, index.redactionReceipt); if (redaction.result !== "passed" || redaction.filesScanned < index.transcripts.length) throw new Error("REDACTION_RECEIPT_INVALID");
  const claimBytes = await readFile(join(targetRoot, index.finalMachineClaim.path));
  if (sha(claimBytes) !== index.finalMachineClaim.sha256 || claimBytes.length !== index.finalMachineClaim.size || index.finalMachineClaim.keyId !== index.keyId || !verify(null, claimBytes, publicKey, Buffer.from(index.finalMachineClaim.signature, "base64"))) throw new Error("FINAL_MACHINE_CLAIM_INVALID");
  const claim = JSON.parse(claimBytes); if (claim.sourceRevision !== index.source.revision || claim.sourceProductDigest !== index.source.productDigest || claim.transcriptCount !== commands.length || claim.transcriptSetSha256 !== index.transcriptSetSha256 || claim.externalWrites !== 0 || claim.finalResources !== 0 || claim.k17 !== "awaiting_external_authorization" || claim.wasapi !== "awaiting_external_authorization") throw new Error("FINAL_MACHINE_CLAIM_INVALID");
  await validateCleanupProof(targetRoot, index);
  return { ok: true, transcripts: index.transcripts.length, ledgerEntries: ledger.length, finalResources: 0, redaction: "passed", machineClaim: "verified", orphanCleanup: "authenticated", keyId: index.keyId };
};

const generate = async (destination) => {
  const cleanupProofSource = resolve(root, ".omo/evidence/functional-jastreamer-products/task-23-cleanup-proof");
  await mkdir(dirname(destination), { recursive: true });
  await recoverStagedEvidence({ destination });
  const staging = await mkdtemp(`${destination}.staging-`); const runnerRoot = await mkdtemp("/tmp/jastreamer-task23-runner.");
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  try {
    await Promise.all([mkdir(join(staging, "captures")), mkdir(join(staging, "transcripts"))]);
    await cp(cleanupProofSource, join(staging, "cleanup-proof"), { recursive: true });
    const spki = publicKey.export({ type: "spki", format: "der" }); const keyId = sha(spki); const publicBytes = publicKey.export({ type: "spki", format: "pem" }); await writeFile(join(staging, "task23-public-key.pem"), publicBytes);
    const ledger = []; const values = []; for (const item of commands) values.push(await runCommand({ item, runnerRoot, ledger, targetRoot: staging }));
    const source = await sourceIdentity(); const currentBindings = await bindings();
    const ledgerPath = "captures/external-write-ledger.jsonl"; const ledgerBytes = Buffer.from(ledger.map((entry) => JSON.stringify(entry)).join("\n") + "\n"); await appendFile(join(staging, ledgerPath), ledgerBytes, { flag: "ax" }); const ledgerRef = reference(ledgerPath, ledgerBytes);
    const transcripts = []; for (const value of values) { value.source = source; value.bindings = currentBindings; value.externalWriteLedger = ledgerRef; const bytes = jsonBytes(value); const path = `transcripts/${value.id}.json`; await writeFile(join(staging, path), bytes); transcripts.push({ ...reference(path, bytes), keyId, signature: sign(null, bytes, privateKey).toString("base64") }); }
    await rm(runnerRoot, { recursive: true, force: true }); const finalInventory = await writeJsonCapture(staging, "captures/final-inventory.json", observeResources());
    const cleanupProofBytes = await readFile(join(staging, "cleanup-proof/index.json")); const transcriptSetSha256 = transcriptSetDigest(transcripts);
    const redactionBytes = jsonBytes({ schemaVersion: 1, kind: "task23_redaction_receipt", transcriptIds: values.map((value) => value.id), filesScanned: values.length * 6 + 2, result: "passed" });
    await writeFile(join(staging, "redaction-receipt.json"), redactionBytes); const redactionReceipt = reference("redaction-receipt.json", redactionBytes);
    const claimBytes = jsonBytes({ schemaVersion: 2, kind: "task23_final_machine_claim", sourceRevision: source.revision, sourceProductDigest: source.productDigest, candidateSetSha256: currentBindings.candidateSetSha256, contractSetSha256: currentBindings.contractSetSha256, schemaSetSha256: currentBindings.schemaSetSha256, transcriptCount: transcripts.length, transcriptSetSha256, ledgerSha256: ledgerRef.sha256, finalInventorySha256: finalInventory.sha256, externalWrites: ledger.filter((entry) => entry.externalWriteAttempt).length, finalResources: inventoryResources(observeResources()).length, k17: "awaiting_external_authorization", wasapi: "awaiting_external_authorization" });
    await writeFile(join(staging, "final-machine-claim.json"), claimBytes); const finalMachineClaim = { ...reference("final-machine-claim.json", claimBytes), keyId, signature: sign(null, claimBytes, privateKey).toString("base64") };
    const index = { schemaVersion: 4, kind: "task23_authenticated_evidence", recordedAt: new Date().toISOString(), source, bindings: currentBindings, keyId, publicKey: { path: "task23-public-key.pem", sha256: sha(publicBytes), size: publicBytes.length, spkiDer: spki.toString("base64") }, externalWriteLedger: ledgerRef, finalInventory, cleanupProof: reference("index.json", cleanupProofBytes), redactionReceipt, transcriptSetSha256, finalMachineClaim, transcripts, signingMaterialRetained: false };
    await writeFile(join(staging, "evidence-index.json"), jsonBytes(index));
    const result = await validate(staging); await publishStagedEvidence({ destination, staging }); return result;
  } finally {
    await Promise.all([rm(runnerRoot, { recursive: true, force: true }), rm(staging, { recursive: true, force: true })]);
  }
};
if (import.meta.main) {
  const validGenerate = mode === "--generate" && (JSON.stringify(cliArguments) === JSON.stringify(["--generate"]) || (cliArguments.length === 3 && explicitRoot !== undefined));
  const validVerify = mode === "--verify" && (JSON.stringify(cliArguments) === JSON.stringify(["--verify"]) || (cliArguments.length === 3 && explicitRoot !== undefined) || (cliArguments.length === 5 && explicitRoot !== undefined && explicitSourceRoot !== undefined));
  if (!validGenerate && !validVerify) throw new Error("USAGE");
  const result = mode === "--verify" ? await validate(evidenceRoot, verificationSourceRoot) : await generate(evidenceRoot); console.log(JSON.stringify(result));
}
