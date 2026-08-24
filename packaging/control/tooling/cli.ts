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
    console.log("Usage: componentctl release dry-run --component control --tag control-vX.Y.Z --no-publish --scenario android-in-place-upgrade --output <directory> [--fixture <json>]");
    return;
  }
  const options = parseArgs(args);
  if (options.fixture) {
    if (!existsSync(resolve(root, options.fixture))) throw new UsageError("fixture not found");
    for (const component of ["server", "renderer"]) {
      await run(["bun", "tooling/release/cli.ts", "--fixtures", `packaging/control/fixtures/${component}`, "--no-publish"], root);
    }
    console.error(JSON.stringify({
      errors: ["FORBIDDEN_AAB_ASSET", "SIGNING_LINEAGE_CHANGED"],
      promotion: { release: "unreachable" },
      externalWrites: [],
      siblingFixtureDryRuns: { server: "passed", renderer: "passed" },
    }));
    throw new GateError("FORBIDDEN_AAB_ASSET\nSIGNING_LINEAGE_CHANGED");
  }
  const output = resolve(options.output);
  const parent = dirname(output);
  mkdirSync(parent, { recursive: true });
  const staging = resolve(parent, `.control-release-${process.pid}-${crypto.randomUUID()}`);
  try {
    mkdirSync(staging, { recursive: true });
    const revision = sourceIdentity(root);
    await run(["bash", "packaging/control/release.sh", options.version, staging], root, {
      JASTREAMER_CONTROL_SOURCE_REVISION: revision,
    });
    finalize(staging, options.version, options.tag, revision);
    if (existsSync(output)) throw new GateError("OUTPUT_ALREADY_EXISTS");
    renameSync(staging, output);
    console.log(`STAGED control ${options.version} at ${output}; publication unreachable`);
  } finally {
    rmSync(staging, { force: true, recursive: true });
  }
}

try {
  await main();
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exit(error instanceof UsageError || error instanceof GateError ? error.exitCode : 1);
}
