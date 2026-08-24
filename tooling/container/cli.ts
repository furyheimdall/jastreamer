#!/usr/bin/env bun
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { GateError, UsageError, parseArgs, scanContext } from "./args";
import { execute } from "./qa";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
try {
  const options = parseArgs(process.argv.slice(2), root);
  scanContext(root, options.fixture);
  await execute(root, options);
  console.log("container build QA passed");
} catch (error) {
  const message = error instanceof Error ? error.message : String(error);
  console.error(message);
  process.exit(error instanceof UsageError || error instanceof GateError ? error.exitCode : 1);
}
