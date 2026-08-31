import { access, open, readFile, rename as renamePath, rm, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";

const exists = async (path) => {
  try { await access(path); return true; }
  catch (error) {
    if (error instanceof Error && "code" in error && error.code === "ENOENT") return false;
    throw error;
  }
};

const syncPath = async (path) => {
  const handle = await open(path, "r");
  try { await handle.sync(); }
  finally { await handle.close(); }
};
const syncParent = (path) => syncPath(dirname(path));

export const task23EvidenceTransactionPaths = (destination, staging) => ({
  backup: `${destination}.backup`,
  marker: `${destination}.transaction.json`,
  markerTemporary: `${destination}.transaction.json.tmp`,
  staging,
});

const markerValue = (phase, destination, staging, backup) => ({
  schemaVersion: 1,
  kind: "task23_evidence_transaction",
  phase,
  destination,
  staging,
  backup,
});

const writeMarker = async (paths, phase, destination) => {
  await rm(paths.markerTemporary, { force: true });
  await writeFile(paths.markerTemporary, `${JSON.stringify(markerValue(phase, destination, paths.staging, paths.backup))}\n`, { mode: 0o600 });
  await syncPath(paths.markerTemporary);
  await renamePath(paths.markerTemporary, paths.marker);
  await syncParent(destination);
};

const clearTransaction = async (paths, destination) => {
  await Promise.all([
    rm(paths.backup, { recursive: true, force: true }),
    rm(paths.staging, { recursive: true, force: true }),
    rm(paths.marker, { force: true }),
    rm(paths.markerTemporary, { force: true }),
  ]);
  await syncParent(destination);
};

const readMarker = async (destination, marker) => {
  let value;
  try { value = JSON.parse(await readFile(marker, "utf8")); }
  catch (error) {
    if (error instanceof Error && "code" in error && error.code === "ENOENT") return undefined;
    throw new Error("TASK23_EVIDENCE_TRANSACTION_MARKER_INVALID", { cause: error });
  }
  const expectedPrefix = `${resolve(destination)}.staging-`;
  if (value?.schemaVersion !== 1 || value.kind !== "task23_evidence_transaction" || !["prepared", "backed_up", "installed"].includes(value.phase) || resolve(value.destination ?? "") !== resolve(destination) || resolve(value.backup ?? "") !== resolve(`${destination}.backup`) || typeof value.staging !== "string" || !resolve(value.staging).startsWith(expectedPrefix)) throw new Error("TASK23_EVIDENCE_TRANSACTION_MARKER_INVALID");
  return value;
};

export const recoverStagedEvidence = async ({ destination, rename = renamePath }) => {
  const marker = `${destination}.transaction.json`; const backup = `${destination}.backup`; const value = await readMarker(destination, marker);
  if (value === undefined) {
    if (!await exists(backup)) return false;
    if (await exists(destination)) await rm(backup, { recursive: true, force: true });
    else await rename(backup, destination);
    await syncParent(destination); return true;
  }
  const paths = task23EvidenceTransactionPaths(destination, value.staging);
  const destinationExists = await exists(destination); const backupExists = await exists(paths.backup); const stagingExists = await exists(paths.staging);
  if (value.phase === "prepared") {
    if (!destinationExists) {
      if (!backupExists) throw new Error("TASK23_EVIDENCE_TRANSACTION_RECOVERY_INVALID");
      await rename(paths.backup, destination); await syncParent(destination);
    }
    await clearTransaction(paths, destination); return true;
  }
  if (!destinationExists) {
    if (value.phase === "backed_up" && stagingExists) await rename(paths.staging, destination);
    else if (backupExists) await rename(paths.backup, destination);
    else throw new Error("TASK23_EVIDENCE_TRANSACTION_RECOVERY_INVALID");
    await syncParent(destination);
  }
  await clearTransaction(paths, destination); return true;
};

export const publishStagedEvidence = async ({ destination, staging, rename = renamePath }) => {
  await recoverStagedEvidence({ destination, rename });
  if (!await exists(staging)) return;
  const paths = task23EvidenceTransactionPaths(destination, staging);
  if (!await exists(destination)) {
    try { await rename(staging, destination); await syncParent(destination); }
    finally { await rm(staging, { recursive: true, force: true }); }
    return;
  }

  await writeMarker(paths, "prepared", destination);
  await rename(destination, paths.backup); await syncParent(destination);
  await writeMarker(paths, "backed_up", destination);
  try {
    await rename(staging, destination); await syncParent(destination);
    await writeMarker(paths, "installed", destination);
  } catch (error) {
    try {
      await rename(paths.backup, destination);
      await clearTransaction(paths, destination);
    } catch (restoreError) {
      await rm(staging, { recursive: true, force: true });
      throw new AggregateError([error, restoreError], "TASK23_EVIDENCE_RESTORE_FAILED");
    }
    throw error;
  }
  await rm(paths.backup, { recursive: true, force: true }); await syncParent(destination);
  await Promise.all([rm(paths.marker, { force: true }), rm(paths.markerTemporary, { force: true })]); await syncParent(destination);
};
