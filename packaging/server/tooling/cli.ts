#!/usr/bin/env bun
import { existsSync, mkdirSync, renameSync, rmSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { GateError, UsageError, parseArgs } from "./args";
import { finalize } from "./finalize";
import { sourceIdentity } from "./identity";
import { run } from "./process";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
async function main(): Promise<void> {
  const args = process.argv.slice(2);
  if (args.includes("--help")) {
    console.log("Usage: componentctl release dry-run --component server --tag server-vX.Y.Z --no-publish --output <directory> [--inject-failure native-linux-arm64-smoke]");
    return;
  }
  const options = parseArgs(args);
  if (options.failure) {
    for (const component of ["control", "renderer"]) {
      await run(["bun", "tooling/release/cli.ts", "--fixtures", `packaging/server/fixtures/${component}`, "--no-publish"], root);
    }
    console.error(JSON.stringify({ code: "NATIVE_LINUX_ARM64_SMOKE_FAILED", promotion: { release: "unreachable", ghcr: "unreachable" }, siblingFixtureDryRuns: { control: "passed", renderer: "passed" } }));
    throw new GateError("native-linux-arm64-smoke", 70);
  }
  const output = resolve(options.output); const parent = dirname(output);
  mkdirSync(parent, { recursive: true });
  const staging = resolve(parent, `.server-release-${process.pid}-${crypto.randomUUID()}`);
  const backup = `${output}.previous-${process.pid}`;
  try {
    mkdirSync(staging, { recursive: true });
    const revision = sourceIdentity(root);
    await run(["bash", "packaging/server/release.sh", options.version, staging], root, { JSTREAMER_RELEASE_TAG: options.tag, JSTREAMER_SOURCE_REVISION: revision, SOURCE_DATE_EPOCH: "0" });
    finalize(staging, options.version, options.tag, revision);
    if (existsSync(output)) renameSync(output, backup);
    renameSync(staging, output);
    rmSync(backup, { recursive: true, force: true });
    console.log(`STAGED server ${options.version} at ${output}; publication unreachable`);
  } catch (error) {
    if (existsSync(backup) && !existsSync(output)) renameSync(backup, output);
    throw error;
  } finally { rmSync(staging, { recursive: true, force: true }); }
}
try { await main(); } catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(error instanceof UsageError || error instanceof GateError ? error.exitCode : 1);
}
