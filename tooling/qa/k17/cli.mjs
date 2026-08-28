#!/usr/bin/env bun
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { parseSupportMatrix, runAuthorizationGate } from "./authorization.mjs";
import { runEmulatorMatrix } from "./emulator.mjs";
import { validatePhysicalQualification, validateQualificationReceipt } from "./receipt.mjs";
import { guardPublication } from "../../release/publication-guard.ts";

const ROOT = resolve(import.meta.dir, "../../..");
const sha256 = (value) => createHash("sha256").update(value).digest("hex");

class CliError extends Error {
  constructor(code) { super(code); this.name = "CliError"; this.code = code; }
}

const parseArguments = (values) => {
  const [command, ...rest] = values;
  if (command !== "emulator" && command !== "authorization" && command !== "gate") throw new CliError("USAGE");
  const options = new Map();
  for (let index = 0; index < rest.length; index += 2) {
    const key = rest[index];
    const value = rest[index + 1];
    if (key === undefined || value === undefined || !key.startsWith("--") || options.has(key)) throw new CliError("USAGE");
    options.set(key, value);
  }
  return { command, options };
};
const required = (options, key) => {
  const value = options.get(key);
  if (value === undefined || value === "") throw new CliError("USAGE");
  return value;
};

const runPhysicalCommand = async (command, candidateSha256, runnerLabel) => {
  const child = Bun.spawn([command, "--candidate-sha256", candidateSha256, "--runner-label", runnerLabel], { cwd: ROOT, stdout: "pipe", stderr: "pipe" });
  const [code, stdout, stderr] = await Promise.all([child.exited, new Response(child.stdout).text(), new Response(child.stderr).text()]);
  if (code !== 0) throw new CliError(stderr.trim() || "K17_PHYSICAL_RUNNER_FAILED");
  let receipt;
  try { receipt = JSON.parse(stdout); } catch (error) { throw error instanceof SyntaxError ? new CliError("K17_PHYSICAL_RECEIPT_INVALID") : error; }
  const validation = validatePhysicalQualification(receipt, { now: new Date().toISOString(), artifactSha256: candidateSha256, runnerLabel });
  if (!validation.ok) throw new CliError(validation.code);
  return receipt;
};

export const main = async (values) => {
  const { command, options } = parseArguments(values);
  const output = required(options, "--output");
  if (command === "emulator") {
    const candidateSha256 = options.get("--candidate-sha256") ?? "0".repeat(64);
    const result = await runEmulatorMatrix({ spawn: Bun.spawn, serverDirectory: join(ROOT, "apps/server"), candidateSha256 });
    if (!result.ok) throw new CliError(result.code);
    await writeFile(output, `${JSON.stringify(result.receipt, null, 2)}\n`);
    return;
  }
  const matrix = parseSupportMatrix(await readFile(required(options, "--matrix"), "utf8"));
  if (command === "authorization") {
    const status = { authorized: matrix.rendererControlAuthorized, runner_label: matrix.k17RunnerLabel };
    await writeFile(output, `${JSON.stringify(status, null, 2)}\n`);
    const githubOutput = options.get("--github-output");
    if (githubOutput !== undefined) await writeFile(githubOutput, `authorized=${status.authorized}\nrunner_label=${status.runner_label}\n`, { flag: "a" });
    return;
  }
  const manifestPath = required(options, "--candidate-manifest");
  const candidateSha256 = sha256(await readFile(manifestPath));
  const recordedAt = new Date().toISOString();
  const temporary = await mkdtemp(join(tmpdir(), "k17-publication-"));
  try {
    const result = await runAuthorizationGate({
      matrix,
      actualRunnerLabel: process.env.JASTREAMER_K17_RUNNER_LABEL,
      candidateSha256,
      recordedAt,
      verifyPublicationDenied: async () => {
        const guarded = guardPublication({ component: "server", event: "push", manifestPath, outputPath: join(temporary, "publication.json") });
        return { code: guarded.ok ? "PUBLICATION_ALLOWED" : guarded.code, externalWrites: guarded.receipt.external_writes.length };
      },
      runPhysical: async () => {
        const physical = await runPhysicalCommand(required(options, "--runner-command"), candidateSha256, matrix.k17RunnerLabel);
        return { schema_version: 1, kind: "k17_qualification", recorded_at: recordedAt, candidate_sha256: candidateSha256, qualification_status: "qualified", physical };
      },
    });
    const validation = validateQualificationReceipt(result, { now: recordedAt, candidateSha256, runnerLabel: matrix.k17RunnerLabel });
    if (!validation.ok) throw new CliError(validation.code);
    await writeFile(output, `${JSON.stringify(result, null, 2)}\n`);
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
};

if (import.meta.main) {
  try { await main(Bun.argv.slice(2)); } catch (error) {
    console.error(error instanceof Error ? error.message : "K17_QUALIFICATION_FAILED");
    process.exitCode = 65;
  }
}
