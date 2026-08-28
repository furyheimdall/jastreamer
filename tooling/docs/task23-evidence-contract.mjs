import { createHash } from "node:crypto";

const resourceKinds = ["processes", "containers", "listeners", "temporaryDirectories", "builders", "browsers"];
const sha = (value) => createHash("sha256").update(value).digest("hex");
const fail = (code) => ({ ok: false, code });
const identity = (value) => `${value.type}:${value.id}:${value.pid ?? ""}:${value.port ?? ""}:${value.identitySha256 ?? ""}`;

export const validateExecutionProvenance = ({ transcript, before, during, after, ledger }) => {
  if (!Array.isArray(ledger) || ledger.length === 0) return fail("LEDGER_PROVENANCE_MISSING");
  let previous = "0".repeat(64);
  for (const [index, entry] of ledger.entries()) {
    const { entrySha256, ...unsigned } = entry;
    if (entry.sequence !== index + 1 || entry.previousSha256 !== previous || entrySha256 !== sha(JSON.stringify(unsigned))) return fail("LEDGER_PROVENANCE_INVALID");
    previous = entrySha256;
  }
  const commandEntry = ledger.find((entry) => entry.commandId === transcript.id);
  if (commandEntry === undefined || commandEntry.argvSha256 !== sha(JSON.stringify(transcript.command.argv))) return fail("LEDGER_PROVENANCE_INVALID");
  const externalWrites = ledger.filter((entry) => entry.externalWriteAttempt).length;
  if (transcript.externalWrites !== externalWrites) return fail("LEDGER_TAMPERED");
  if (resourceKinds.some((kind) => !Array.isArray(before[kind]) || !Array.isArray(during[kind]) || !Array.isArray(after[kind]))) return fail("INVENTORY_INVALID");
  const observed = new Set(resourceKinds.flatMap((kind) => during[kind].map(identity)));
  if (!Array.isArray(transcript.inventories.allocated) || transcript.inventories.allocated.length === 0 || transcript.inventories.allocated.some((item) => !observed.has(identity(item)))) return fail("INVENTORY_FABRICATED");
  const baseline = new Set(resourceKinds.flatMap((kind) => before[kind].map(identity)));
  const residual = resourceKinds.flatMap((kind) => after[kind]).filter((item) => !baseline.has(identity(item)));
  if (residual.length !== 0 || transcript.inventories.allocated.some((item) => resourceKinds.some((kind) => after[kind].some((current) => identity(current) === identity(item))))) return fail("CLEANUP_INCOMPLETE");
  return { ok: true };
};
