#!/usr/bin/env bun
import {
  existsSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmSync,
} from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  GateError,
  HostError,
  ProtocolError,
  UsageError,
  parseArgs,
} from "./args";
import { run } from "./process";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const supportedProtocolMajors = new Set([3, 2]);

function fixtureProtocolMajor(path: string): number {
  const fixture: unknown = JSON.parse(readFileSync(path, "utf8"));
  if (
    typeof fixture !== "object" ||
    fixture === null ||
    !("protocolMajor" in fixture) ||
    typeof fixture.protocolMajor !== "number"
  ) {
    throw new UsageError("fixture must contain a numeric protocolMajor");
  }
  return fixture.protocolMajor;
}

async function main(): Promise<void> {
  const args = process.argv.slice(2);
  if (args.includes("--help")) {
    console.log(
      "Usage: componentctl release dry-run --component renderer --tag renderer-vX.Y.Z --no-publish --scenario clean-windows-vm --output <directory> [--fixture <json>]",
    );
    return;
  }

  const options = parseArgs(args);
  if (options.fixture !== undefined) {
    const fixture = resolve(root, options.fixture);
    if (!existsSync(fixture)) {
      throw new UsageError("fixture not found");
    }
    if (!supportedProtocolMajors.has(fixtureProtocolMajor(fixture))) {
      throw new ProtocolError("UNSUPPORTED_PROTOCOL_MAJOR");
    }
  }
  if (process.platform !== "win32") {
    throw new HostError("WINDOWS_RUNNER_REQUIRED");
  }

  const output = resolve(options.output);
  if (existsSync(output)) {
    throw new GateError("OUTPUT_ALREADY_EXISTS");
  }
  const parent = dirname(output);
  mkdirSync(parent, { recursive: true });
  const staging = resolve(parent, `.renderer-release-${process.pid}-${crypto.randomUUID()}`);
  try {
    await run(
      [
        "pwsh",
        "-NoProfile",
        "-File",
        "packaging/renderer/release.ps1",
        "-Version",
        options.version,
        "-Out",
        staging,
      ],
      root,
    );
    renameSync(staging, output);
    console.log(`STAGED renderer ${options.version} at ${output}; publication unreachable`);
  } finally {
    rmSync(staging, { force: true, recursive: true });
  }
}

try {
  await main();
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(
    error instanceof UsageError ||
      error instanceof GateError ||
      error instanceof HostError ||
      error instanceof ProtocolError
      ? error.exitCode
      : 1,
  );
}
