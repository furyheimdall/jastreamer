#!/usr/bin/env bun
import { readFile, rm, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { validateWindowsAudioReceipt } from "./receipt.mjs";

export const finalizeQualification = async ({
  evidence, binding, now, workspace,
  removeWorkspace = rm,
  workspaceRemoved = () => {},
  validate = (receipt) => validateWindowsAudioReceipt(receipt, { now, expectedBinding: binding }),
  writeOutput = async () => {},
}) => {
  const cleanup = evidence?.cleanup;
  if (cleanup?.resources_released !== true || cleanup.processes_terminated !== true ||
      cleanup.raw_endpoint_retained !== false || cleanup.external_writes !== 0 ||
      Object.keys(cleanup).length !== 4) throw new Error("DRIVER_CLEANUP_INCOMPLETE");
  await removeWorkspace(workspace, { recursive: true, force: false });
  workspaceRemoved();
  const receipt = {
    ...evidence,
    cleanup: { ...cleanup, temporary_files_removed: true },
  };
  const result = validate(receipt);
  if (!result.ok) throw new Error(`${result.code}:${result.path}`);
  await writeOutput(receipt);
  return receipt;
};

const parseArgs = (values) => {
  const result = new Map();
  for (let index = 0; index < values.length; index += 2) {
    const key = values[index]; const value = values[index + 1];
    if (key === undefined || value === undefined || !key.startsWith("--")) throw new Error("ARGUMENT_INVALID");
    result.set(key.slice(2), value);
  }
  return result;
};
const required = (args, key) => {
  const value = args.get(key); if (value === undefined || value === "") throw new Error(`ARGUMENT_MISSING:${key}`);
  return value;
};

const invoked = process.argv[1] === undefined ? undefined : pathToFileURL(process.argv[1]).href;
if (invoked === import.meta.url) {
  const args = parseArgs(process.argv.slice(2));
  const evidence = JSON.parse(await readFile(required(args, "evidence"), "utf8"));
  const binding = JSON.parse(await readFile(required(args, "binding"), "utf8"));
  const workspace = required(args, "workspace");
  const output = required(args, "output");
  await finalizeQualification({
    evidence, binding, now: required(args, "recorded-at"), workspace,
    workspaceRemoved: () => process.stdout.write("WORKSPACE_REMOVED\n"),
    writeOutput: (receipt) => writeFile(output, `${JSON.stringify(receipt, null, 2)}\n`, { flag: "wx" }),
  });
}
