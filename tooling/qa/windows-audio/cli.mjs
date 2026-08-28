#!/usr/bin/env bun
import { readFile, writeFile } from "node:fs/promises";
import { pendingReceipt } from "./authorization.mjs";
import { validateWindowsAudioReceipt } from "./receipt.mjs";

class CliError extends Error {
  constructor(code) { super(code); this.name = "CliError"; }
}

const argumentsMap = (values) => {
  const result = new Map();
  for (let index = 0; index < values.length; index += 2) {
    const key = values[index]; const value = values[index + 1];
    if (typeof key !== "string" || !key.startsWith("--") || value === undefined) throw new CliError("ARGUMENT_INVALID");
    result.set(key.slice(2), value);
  }
  return result;
};
const required = (arguments_, key) => {
  const value = arguments_.get(key);
  if (value === undefined || value === "") throw new CliError("ARGUMENT_MISSING");
  return value;
};

const main = async () => {
  const [command, ...rest] = process.argv.slice(2);
  const args = argumentsMap(rest);
  const binding = JSON.parse(await readFile(required(args, "binding"), "utf8"));
  const now = required(args, "recorded-at");
  let receipt;
  switch (command) {
    case "pending": {
      const publication = JSON.parse(await readFile(required(args, "publication"), "utf8"));
      receipt = pendingReceipt({ recordedAt: now, binding, publication: { code: publication.code, externalWrites: publication.external_writes.length } });
      break;
    }
    case "validate":
      receipt = JSON.parse(await readFile(required(args, "receipt"), "utf8"));
      break;
    default:
      throw new CliError("COMMAND_INVALID");
  }
  const result = validateWindowsAudioReceipt(receipt, { now, expectedBinding: binding });
  if (!result.ok) throw new CliError(`${result.code}:${result.path}`);
  const output = args.get("output");
  if (output !== undefined) await writeFile(output, `${JSON.stringify(receipt, null, 2)}\n`, { flag: "wx" });
  process.stdout.write(`${JSON.stringify(result)}\n`);
};

try {
  await main();
} catch (error) {
  if (error instanceof Error) console.error(error.message);
  else console.error("UNKNOWN_ERROR");
  process.exit(65);
}
