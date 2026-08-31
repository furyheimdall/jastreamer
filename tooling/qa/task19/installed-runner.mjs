#!/usr/bin/env node
import { createHash, createPublicKey, verify } from "node:crypto";
import { spawn } from "node:child_process";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { validateInstalledProductReceipt } from "./product-e2e-receipt.mjs";
import { loadProductionTrust, snapshotCandidateClosure, validateCandidateManifest, validateExecutionResult } from "./installed-runner-policy.mjs";
import { executeProtectedLifecycle } from "./protected-lifecycle.mjs";
const HELP = `Usage: node tooling/qa/task19/installed-runner.mjs --candidates <json> [options]

Options:
  --dry-run                       Validate closure and print the repository-trusted plan; execute nothing
  --execute                       Execute only with signed physical authorization on the protected Windows runner
  --output <directory>            Protected execution output root
  --preflight <json>              Authorized-device preflight receipt
  --authorization <json>          Signed repository physical-authorization receipt
  --authorization-signature <file> Detached Ed25519 authorization signature
  --validate-execution <json>     Validate signer-completed executor evidence without running products
  --help                          Show this help
`;
const denied = (code, path, plan) => ({ status: "awaiting_external_authorization", code, path, productCommandsExecuted: 0, externalWrites: 0, cleanupComplete: true, ...(plan ? { plan } : {}) });
const emit = (value, exitCode) => { process.stdout.write(`${JSON.stringify(value, null, 2)}\n`); process.exitCode = exitCode; };
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

const parse = (arguments_) => {
  const value = {};
  const switches = new Set(["--help", "--dry-run", "--execute"]);
  const valued = new Set(["--candidates", "--output", "--preflight", "--authorization", "--authorization-signature", "--validate-execution"]);
  for (let index = 0; index < arguments_.length; index += 1) {
    const argument = arguments_[index];
    if (switches.has(argument)) value[argument.slice(2)] = true;
    else if (valued.has(argument) && arguments_[index + 1] !== undefined) value[argument.slice(2)] = arguments_[++index];
    else return { issue: `unknown or incomplete argument: ${argument}` };
  }
  return value;
};

const exactKeys = (value, keys) => value !== null && typeof value === "object" && !Array.isArray(value)
  && JSON.stringify(Object.keys(value).sort()) === JSON.stringify([...keys].sort());
const verifyAuthorization = async (args, candidate, preflight) => {
  if (!args.authorization || !args["authorization-signature"]) return { ok: false, code: "PHYSICAL_AUTHORIZATION_REQUIRED", path: "authorization" };
  let receipt; let bytes; let signature;
  try { bytes = await readFile(resolve(args.authorization)); receipt = JSON.parse(bytes); signature = Buffer.from((await readFile(resolve(args["authorization-signature"]), "utf8")).trim(), "base64"); } catch { return { ok: false, code: "PHYSICAL_AUTHORIZATION_REQUIRED", path: "authorization" }; }
  const keys = ["schemaVersion", "kind", "repository", "workflowPath", "eventName", "environment", "runId", "runAttempt", "headSha", "providerRunId", "providerRunAttempt", "physicalAuthorizationSha256", "deviceSerialSha256", "issuedAt", "expiresAt"];
  const trust = loadProductionTrust(); const auth = trust.authorization;
  const publicKey = createPublicKey(await readFile(resolve(auth.publicKeyPath)));
  const keyId = sha256(publicKey.export({ type: "spki", format: "der" }));
  if (!exactKeys(receipt, keys) || keyId !== auth.keyId || !verify(null, bytes, publicKey, signature)) return { ok: false, code: "TASK19_AUTHORIZATION_SIGNATURE_INVALID", path: "authorization" };
  const now = Date.now(); const issued = Date.parse(receipt.issuedAt); const expires = Date.parse(receipt.expiresAt);
  const valid = receipt.schemaVersion === 1 && receipt.kind === "task19_physical_authorization" && receipt.repository === trust.repository
    && receipt.workflowPath === ".github/workflows/task19-installed-qualification.yml" && receipt.eventName === "workflow_dispatch"
    && receipt.environment === auth.environment && receipt.runId === process.env.GITHUB_RUN_ID && receipt.runAttempt === Number(process.env.GITHUB_RUN_ATTEMPT)
    && receipt.headSha === process.env.GITHUB_SHA && receipt.headSha === candidate.manifest.source.revision
    && receipt.providerRunId === candidate.manifest.provider.runId && receipt.providerRunAttempt === candidate.manifest.provider.runAttempt
    && /^[0-9a-f]{64}$/.test(auth.physicalAuthorizationSha256) && receipt.physicalAuthorizationSha256 === auth.physicalAuthorizationSha256
    && preflight?.android?.androidDeviceSerialSha256 === receipt.deviceSerialSha256
    && Number.isFinite(issued) && Number.isFinite(expires) && issued <= now + 300_000 && now <= expires && expires - issued <= auth.maxAgeSeconds * 1000;
  return valid ? { ok: true, receipt } : { ok: false, code: "PHYSICAL_AUTHORIZATION_BINDING_MISMATCH", path: "authorization" };
};

const protectedEnvironment = () => Object.fromEntries(["PATH", "Path", "SystemRoot", "WINDIR", "TEMP", "TMP", "ComSpec", "PATHEXT", "PSModulePath"].flatMap((key) => process.env[key] === undefined ? [] : [[key, process.env[key]]]));
const runProtected = async (planPath, output) => {
  const script = resolve(dirname(fileURLToPath(import.meta.url)), "protected-runner.ps1");
  const executionPath = resolve(output, "execution-result.json");
  const child = spawn("powershell.exe", ["-NoProfile", "-NonInteractive", "-File", script, "-Plan", planPath, "-Output", executionPath], { stdio: ["ignore", "pipe", "inherit"], env: protectedEnvironment(), windowsHide: true });
  const exitCode = await executeProtectedLifecycle({ kind: "process", child, setTimer: setTimeout, clearTimer: clearTimeout, forward: (line) => process.stdout.write(line), terminateTree: (pid, _phase, complete) => {
    const killer = spawn("taskkill.exe", ["/PID", String(pid), "/T", "/F"], { stdio: "ignore", windowsHide: true, env: protectedEnvironment() });
    killer.once("error", complete); killer.once("exit", () => complete());
  } });
  if (exitCode !== 0) throw new Error(`TASK19_PROTECTED_EXECUTOR_FAILED:${exitCode}`);
  return { executionPath, value: JSON.parse(await readFile(executionPath)) };
};

const validateTrustedInterfaces = async (execution, candidate, evidenceRoot) => {
  const trust = candidate.trust;
  if (!candidate.qualificationReady) return { ok: false, code: "PRODUCTION_TRUST_INCOMPLETE", path: "trust.qualification" };
  const harness = trust.qualification.harness; const publicKey = createPublicKey(await readFile(resolve(harness.publicKeyPath)));
  if (sha256(publicKey.export({ type: "spki", format: "der" })) !== harness.keyId) return { ok: false, code: "PRODUCTION_HARNESS_TRUST_INVALID", path: "trust.qualification.harness" };
  const installed = validateInstalledProductReceipt(execution.installedProductReceipt, { now: new Date().toISOString(), root: evidenceRoot, trustedCandidates: trust.qualification.trustedCandidates, harnessTrust: { keyId: harness.keyId, publicKey }, expectedBindings: trust.qualification.expectedBindings });
  return installed.ok ? { ok: true, installed } : { ok: false, code: installed.code, path: `installedProductReceipt.${installed.path}` };
};

const args = parse(process.argv.slice(2));
if (args.help) process.stdout.write(HELP);
else if (args.issue || !args.candidates || (!args["dry-run"] && !args.execute && !args["validate-execution"])) { process.stderr.write(`${args.issue ?? "--candidates and one mode are required"}\n${HELP}`); process.exitCode = 64; }
else {
  const candidates = validateCandidateManifest(resolve(args.candidates), process.env.GITHUB_SHA);
  if (!candidates.ok) emit(denied(candidates.code, candidates.path), 77);
  else if (args["validate-execution"]) { const executionPath = resolve(args["validate-execution"]); const execution = JSON.parse(await readFile(executionPath)); const result = validateExecutionResult(execution); const evidenceRoot = execution.evidenceRoot === "." ? dirname(executionPath) : undefined; const interfaces = result.ok && evidenceRoot ? await validateTrustedInterfaces(execution, candidates, evidenceRoot) : result; emit(interfaces.ok ? { status: "qualified", code: "TASK19_QUALIFIED", productCommandsExecuted: execution.productCommandsExecuted, cleanupComplete: true, externalWrites: 0 } : denied(interfaces.code, interfaces.path), interfaces.ok ? 0 : 77); }
  else if (args["dry-run"]) emit(denied(candidates.qualificationReady ? "DRY_RUN_ONLY" : "PRODUCTION_TRUST_INCOMPLETE", candidates.qualificationReady ? "authorization" : "trust.qualification", candidates.plan), candidates.qualificationReady ? 0 : 77);
  else {
    let preflight; try { preflight = args.preflight ? JSON.parse(await readFile(resolve(args.preflight))) : undefined; } catch { preflight = undefined; }
    const authorization = await verifyAuthorization(args, candidates, preflight);
    const platform = process.env.RUNNER_OS === "Windows" && process.env.RUNNER_ARCH === "X64" && (process.env.RUNNER_LABELS ?? "").split(",").includes("task19-protected");
    if (!authorization.ok || !platform || !args.output || !candidates.qualificationReady) emit(denied(!authorization.ok ? authorization.code : !candidates.qualificationReady ? "PRODUCTION_TRUST_INCOMPLETE" : "PROTECTED_RUNNER_AUTHORIZATION_REQUIRED", !authorization.ok ? authorization.path : !candidates.qualificationReady ? "trust.qualification" : "authorization", candidates.plan), 77);
    else {
      const output = resolve(args.output); await mkdir(output, { recursive: false }); let snapshot;
      try {
        snapshot = snapshotCandidateClosure(candidates, output); const planPath = resolve(output, "execution-plan.json"); await writeFile(planPath, `${JSON.stringify(snapshot.plan, null, 2)}\n`, { mode: 0o400 });
        const execution = await runProtected(planPath, output); const shape = validateExecutionResult(execution.value);
        emit(shape.ok ? { status: "awaiting_trusted_signature", code: "TASK19_EXECUTION_UNSIGNED", productCommandsExecuted: execution.value.productCommandsExecuted, cleanupComplete: true, externalWrites: 0 } : { ...shape, status: "failed", cleanupComplete: false }, shape.ok ? 0 : 1);
      } finally { if (snapshot) await rm(snapshot.root, { recursive: true, force: true }); }
    }
  }
}
