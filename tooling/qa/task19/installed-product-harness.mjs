#!/usr/bin/env node
import { resolve } from "node:path";
import { createOperationAdapter } from "./task19-operation-adapter.mjs";

const OPERATIONS = new Set(["inventory", "start", "scenario", "performance", "probe", "terminate"]);
const readInput = async () => { const chunks = []; for await (const chunk of process.stdin) chunks.push(chunk); try { return JSON.parse(Buffer.concat(chunks).toString("utf8")); } catch { throw new Error("TASK19_HARNESS_INPUT_INVALID"); } };

export const createProductionHarness = (backend, options) => createOperationAdapter(backend, options);
const productionHarness = createProductionHarness();
export const executeHarnessOperation = async (operation, input, options = {}) => (options.harness ?? productionHarness).execute(operation, input);

if (process.argv[1] && resolve(process.argv[1]) === new URL(import.meta.url).pathname) {
  const operation = process.argv[2]; if (!OPERATIONS.has(operation) || process.argv.length !== 3) throw new Error("TASK19_HARNESS_USAGE");
  process.stdout.write(`${JSON.stringify(await executeHarnessOperation(operation, await readInput()))}\n`);
}
