import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { validateExecutionProvenance } from "./task23-evidence-contract.mjs";

const sha = (value) => createHash("sha256").update(value).digest("hex");
const process = { type: "process", id: "process-41", pid: 41, identitySha256: "a".repeat(64), observedBy: "procfs" };
const empty = { processes: [], containers: [], listeners: [], temporaryDirectories: [], builders: [], browsers: [] };
const fixture = () => {
  const argv = ["bun", "tooling/docs/verify.mjs"];
  const unsigned = { sequence: 1, commandId: "docs-verifier", argvSha256: sha(JSON.stringify(argv)), allowlistId: "task23-local-v1", externalWriteAttempt: false, previousSha256: "0".repeat(64) };
  const ledger = [{ ...unsigned, entrySha256: sha(JSON.stringify(unsigned)) }];
  return {
    transcript: { id: "docs-verifier", command: { argv }, externalWrites: 0, inventories: { allocated: [process] } },
    before: structuredClone(empty), during: { ...structuredClone(empty), processes: [process] }, after: structuredClone(empty), ledger,
  };
};

const reject = (value, code) => expect(validateExecutionProvenance(value)).toEqual({ ok: false, code });

describe("observed Task 23 resource and write provenance", () => {
  test("accepts an allocated PID observed in procfs and absent after cleanup", () => {
    expect(validateExecutionProvenance(fixture())).toEqual({ ok: true });
  });

  test("rejects fabricated or empty allocation inventory", () => {
    const value = fixture(); value.during.processes = [];
    reject(value, "INVENTORY_FABRICATED");
  });

  test("rejects an orphan resource after command completion", () => {
    const value = fixture(); value.after.processes = [process];
    reject(value, "CLEANUP_INCOMPLETE");
  });

  test("rejects missing append-only ledger provenance", () => {
    const value = fixture(); value.ledger = [];
    reject(value, "LEDGER_PROVENANCE_MISSING");
  });

  test("rejects ledger chain tampering", () => {
    const value = fixture(); value.ledger[0].previousSha256 = "b".repeat(64);
    reject(value, "LEDGER_PROVENANCE_INVALID");
  });
});
