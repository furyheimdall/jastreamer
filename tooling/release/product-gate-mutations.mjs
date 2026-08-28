import { readFileSync } from "node:fs";
import { isAbsolute, resolve } from "node:path";

const lines = (path) => readFileSync(path, "utf8").split("\n").filter(Boolean).map(JSON.parse);
export const observeExternalMutations = (receiptPath, options) => {
  const root = resolve(options.root); let count = 0;
  try {
    const ledgerPath = isAbsolute(options.mutationLedgerPath) ? options.mutationLedgerPath : resolve(root, options.mutationLedgerPath);
    count += lines(ledgerPath).filter((entry) => entry.externallyObserved === true).length;
    const receipt = JSON.parse(readFileSync(isAbsolute(receiptPath) ? receiptPath : resolve(root, receiptPath), "utf8"));
    const before = JSON.parse(readFileSync(resolve(root, receipt.cleanup.inventoryBefore.path), "utf8"));
    const after = JSON.parse(readFileSync(resolve(root, receipt.cleanup.inventoryAfter.path), "utf8"));
    const identities = (inventory) => new Set(inventory.flatMap((group) => group.ids.map((id) => `${group.type}:${id}`)));
    const left = identities(before); const right = identities(after);
    count += [...left].filter((item) => !right.has(item)).length + [...right].filter((item) => !left.has(item)).length;
  } catch { return -1; }
  return count;
};
