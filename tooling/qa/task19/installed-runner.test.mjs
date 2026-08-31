import { afterEach, describe, expect, test } from "bun:test";
import { createHash, createPrivateKey, generateKeyPairSync, sign } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { snapshotCandidateClosure, validateCandidateManifest } from "./installed-runner-policy.mjs";
import { loadSyntheticTrustForTest } from "./fixtures/installed-runner-test-trust.mjs";

const repository = resolve(import.meta.dirname, "../../..");
const entrypoint = join(repository, "tooling/qa/task19/installed-runner.mjs");
const installedWorkflow = join(repository, ".github/workflows/task19-installed-qualification.yml");
const roots = [];
const revision = "4b0b24feff535edf4db6bb909de9b84ff25d6182";
const sha256 = (value) => createHash("sha256").update(value).digest("hex");
const output = (run) => JSON.parse(run.stdout.toString());
afterEach(async () => Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))));

const candidate = async (patch = {}) => {
  const root = await mkdtemp(join(tmpdir(), "task19-runner-test-")); roots.push(root);
  await mkdir(join(root, "files"));
  const items = {
    server: ["server-linux-deb", "server.deb"], controlWeb: ["control-web", "control.zip"], controlWindows: ["control-windows", "control.msix"],
    controlAndroid: ["control-android", "control.apk"], renderer: ["renderer-windows-msi", "renderer.msi"], k17: ["k17-receipt", "k17.json"],
    wasapi: ["wasapi-receipt", "wasapi.json"], stagedManifest: ["authoritative-staged-manifest", "staged.json"],
  };
  const references = {};
  for (const [key, [kind, name]] of Object.entries(items)) {
    const bytes = key === "k17" || key === "wasapi" ? Buffer.from('{"qualification_status":"qualified"}') : Buffer.from(`trusted-${key}`);
    const path = `files/${name}`; await writeFile(join(root, path), bytes); references[key] = { kind, path, sha256: sha256(bytes), size: bytes.length };
  }
  const manifest = {
    schemaVersion: 2, kind: "task19_exact_candidate_closure", source: { revision }, producer: { driverSha256: "f604c3cb4cfcf4f9be41bbbea5c64655c85db52cef7312ebf2da82c35e75bf29" },
    provider: { repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/product-qualification-dispatch.yml", eventName: "workflow_dispatch", runId: "1001", runAttempt: 2, headSha: revision, artifactId: "2001", artifactName: "task19-exact-candidates-1001-2", artifactDigest: "a".repeat(64), archiveSha256: "a".repeat(64), size: 4096, createdAt: "2026-08-26T11:00:00Z", expiresAt: "2026-08-27T12:00:00Z", observedAt: "2026-08-26T12:00:00Z" },
    files: { server: references.server, controlWeb: references.controlWeb, controlWindows: references.controlWindows, controlAndroid: references.controlAndroid, renderer: references.renderer },
    receipts: { k17: references.k17, wasapi: references.wasapi }, stagedManifest: references.stagedManifest, ...patch,
  };
  const path = join(root, "task19-exact-candidates.json"); await writeFile(path, `${JSON.stringify(manifest)}\n`);
  return { root, path, manifest };
};
const invoke = (path, mode = "--dry-run", extra = [], env = process.env) => Bun.spawnSync(["node", entrypoint, "--candidates", path, mode, ...extra], { cwd: repository, env });
const authorization = async (root, value, privateKeyPath = join(repository, "tooling/qa/task19/fixtures/synthetic-authorization-private.pem")) => {
  const bytes = Buffer.from(`${JSON.stringify(value)}\n`); const receipt = join(root, "authorization.json"); const signature = join(root, "authorization.sig");
  await writeFile(receipt, bytes); const key = createPrivateKey(await readFile(privateKeyPath)); await writeFile(signature, sign(null, bytes, key).toString("base64")); return { receipt, signature };
};

describe("Task19 installed-product protected boundary", () => {
  test("protected runner cleanup proves sensitive paths are absent", async () => {
    const workflow = await readFile(installedWorkflow, "utf8");
    for (const path of [
      "task19-authorization.json",
      "task19-authorization.sig",
      "task19-android-tools",
    ]) {
      expect(workflow).toContain(
        `Test-Path (Join-Path $env:RUNNER_TEMP '${path}')`,
      );
    }
    for (const path of [
      "task19-output",
      "task19-candidates",
      "provider-context.json",
      "task19-runner-preflight.json",
    ]) {
      expect(workflow).toContain(`Test-Path '${path}'`);
    }
  });
  test("authenticates provider identity and rejects candidate self-trust or driver options", async () => {
    const value = await candidate();
    for (const patch of [{ provider: { ...value.manifest.provider, repository: "evil/repo" } }, { trustedCandidates: {} }, { scenarioDriver: { path: "evil.ps1" } }, { productGateOptions: {} }]) {
      await writeFile(value.path, `${JSON.stringify({ ...value.manifest, ...patch })}\n`);
      expect(output(invoke(value.path))).toMatchObject({ status: "awaiting_external_authorization", productCommandsExecuted: 0 });
    }
  });

  test("rejects wrong provider current SHA before execution", async () => {
    const value = await candidate({ source: { revision: "b".repeat(40) } });
    expect(output(invoke(value.path, "--execute", [], { ...process.env, GITHUB_SHA: revision }))).toMatchObject({ code: "CANDIDATE_SOURCE_INVALID", productCommandsExecuted: 0 });
  });

  test("snapshots authenticated bytes without rereading a changed candidate path", async () => {
    const value = await candidate(); const validated = validateCandidateManifest(value.path, revision); expect(validated.ok).toBe(true);
    await writeFile(join(value.root, value.manifest.files.server.path), "malicious replacement");
    const outputRoot = join(value.root, "private"); await mkdir(outputRoot); const snapshot = snapshotCandidateClosure(validated, outputRoot);
    expect(await readFile(snapshot.files["server-linux-deb"].path, "utf8")).toBe("trusted-server");
  });

  test("rejects reparse output parents before snapshot creation", async () => {
    const value = await candidate(); const validated = validateCandidateManifest(value.path, revision); expect(validated.ok).toBe(true);
    const privateRoot = join(value.root, "private"); const link = join(value.root, "link"); await mkdir(privateRoot); await symlink(privateRoot, link, "dir");
    expect(() => snapshotCandidateClosure(validated, link)).toThrow("TASK19_SNAPSHOT_REPARSE_POINT_REJECTED");
  });

  test("requires a signed physical receipt; GitHub variables never authorize", async () => {
    const value = await candidate(); const target = join(value.root, "must-not-exist");
    const run = invoke(value.path, "--execute", ["--output", target], { ...process.env, GITHUB_RUN_ID: "42", GITHUB_RUN_ATTEMPT: "1", GITHUB_SHA: revision, RUNNER_OS: "Windows", RUNNER_ARCH: "X64", RUNNER_LABELS: "self-hosted,Windows,X64,task19-protected", TASK19_EXECUTION_AUTHORIZATION: "42" });
    expect(output(run)).toMatchObject({ code: "PHYSICAL_AUTHORIZATION_REQUIRED", productCommandsExecuted: 0 }); expect(await Bun.file(target).exists()).toBe(false);
  });

  test("production authorization root remains null and workflow exposes only protected dispatch", async () => {
    const workflow = Bun.YAML.parse(await readFile(join(repository, ".github/workflows/task19-installed-qualification.yml"), "utf8")); const trust = JSON.parse(await readFile(join(repository, "tooling/qa/task19/task19-production-trust-v1.json"), "utf8"));
    expect(workflow.on.workflow_dispatch).toBeDefined(); expect(workflow.on.workflow_call).toBeUndefined(); expect(workflow.jobs.authorize.environment).toBe("task19-qualification");
    expect(trust.authorization).toHaveProperty("physicalAuthorizationSha256", null); expect(trust.qualification.ready).toBe(false); expect(trust.qualification.runtime).toMatchObject({ harnessPath: "tooling/qa/task19/installed-product-harness.mjs", harnessSha256: expect.stringMatching(/^[0-9a-f]{64}$/), scenarioContractPath: "tooling/qa/task19/scenario-contract.mjs", scenarioContractSha256: expect.stringMatching(/^[0-9a-f]{64}$/), operationAdapterPath: "tooling/qa/task19/task19-operation-adapter.mjs", operationAdapterSha256: expect.stringMatching(/^[0-9a-f]{64}$/), inventoryAdapterSha256: expect.stringMatching(/^[0-9a-f]{64}$/), processAdapterSha256: expect.stringMatching(/^[0-9a-f]{64}$/) }); expect(trust.qualification.runtime).not.toHaveProperty("operationCommands"); expect(trust.qualification.runtime).not.toHaveProperty("receiptTemplate"); expect(trust.qualification.runtime).not.toHaveProperty("webOriginKeyPath"); expect(JSON.stringify(trust)).not.toContain("task19-web-origin-key.pem"); expect(await Bun.file(join(repository, "tooling/qa/task19/protected-runner.ps1")).text()).not.toContain("task19-web-origin-key.pem");
  });

  test("ambient synthetic trust selection is impossible through production entry code", async () => {
    const value = await candidate();
    const production = output(invoke(value.path, "--dry-run", [], { ...process.env, TASK19_TRUST_HARNESS: "repository-owned-synthetic-v1" }));
    expect(production).toMatchObject({ code: "PRODUCTION_TRUST_INCOMPLETE" });
    expect(await Bun.file(entrypoint).text()).not.toContain("TASK19_TRUST_HARNESS");
    expect(await Bun.file(join(repository, "tooling/qa/task19/installed-runner-policy.mjs")).text()).not.toContain("TASK19_TRUST_HARNESS");
  });

  test("test-only synthetic root is explicit and production entry rejects its signature", async () => {
    const value = await candidate(); const now = Date.now(); const device = "d".repeat(64); const receiptValue = { schemaVersion: 1, kind: "task19_physical_authorization", repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/task19-installed-qualification.yml", eventName: "workflow_dispatch", environment: "task19-qualification", runId: "42", runAttempt: 1, headSha: revision, providerRunId: "1001", providerRunAttempt: 2, physicalAuthorizationSha256: "e9ae1400988c2da39f60ea1fea7e6722a76623bceada376ae5558b71427360d1", deviceSerialSha256: device, issuedAt: new Date(now - 1000).toISOString(), expiresAt: new Date(now + 60_000).toISOString() }; const signed = await authorization(value.root, receiptValue); const preflight = join(value.root, "preflight.json"); await writeFile(preflight, JSON.stringify({ android: { androidDeviceSerialSha256: device } }));
    const env = { ...process.env, TASK19_TRUST_HARNESS: "repository-owned-synthetic-v1", GITHUB_RUN_ID: "42", GITHUB_RUN_ATTEMPT: "1", GITHUB_SHA: revision, RUNNER_OS: "Windows", RUNNER_ARCH: "X64", RUNNER_LABELS: "self-hosted,Windows,X64,task19-protected" }; const result = output(invoke(value.path, "--execute", ["--output", join(value.root, "output"), "--preflight", preflight, "--authorization", signed.receipt, "--authorization-signature", signed.signature], env));
    expect(loadSyntheticTrustForTest().authorization.physicalAuthorizationSha256).toBe(receiptValue.physicalAuthorizationSha256);
    expect(result).toMatchObject({ code: "TASK19_AUTHORIZATION_SIGNATURE_INVALID", productCommandsExecuted: 0 });
  });

  test.each(["wrong-root", "stale", "missing"])("synthetic trust harness rejects %s authorization with zero execution", async (mode) => {
    const value = await candidate(); const now = Date.now(); const device = "d".repeat(64); const base = { schemaVersion: 1, kind: "task19_physical_authorization", repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/task19-installed-qualification.yml", eventName: "workflow_dispatch", environment: "task19-qualification", runId: "42", runAttempt: 1, headSha: revision, providerRunId: "1001", providerRunAttempt: 2, physicalAuthorizationSha256: "e9ae1400988c2da39f60ea1fea7e6722a76623bceada376ae5558b71427360d1", deviceSerialSha256: device, issuedAt: new Date(now - (mode === "stale" ? 3_600_000 : 1000)).toISOString(), expiresAt: new Date(now + (mode === "stale" ? -1000 : 60_000)).toISOString() }; let signed;
    if (mode !== "missing") { signed = await authorization(value.root, base); if (mode === "wrong-root") { const keys = generateKeyPairSync("ed25519"); const bytes = await readFile(signed.receipt); await writeFile(signed.signature, sign(null, bytes, keys.privateKey).toString("base64")); } }
    const preflight = join(value.root, "preflight.json"); await writeFile(preflight, JSON.stringify({ android: { androidDeviceSerialSha256: device } })); const extra = ["--output", join(value.root, "output"), "--preflight", preflight, ...(signed ? ["--authorization", signed.receipt, "--authorization-signature", signed.signature] : [])]; const env = { ...process.env, TASK19_TRUST_HARNESS: "repository-owned-synthetic-v1", GITHUB_RUN_ID: "42", GITHUB_RUN_ATTEMPT: "1", GITHUB_SHA: revision, RUNNER_OS: "Windows", RUNNER_ARCH: "X64", RUNNER_LABELS: "self-hosted,Windows,X64,task19-protected" };
    expect(output(invoke(value.path, "--execute", extra, env))).toMatchObject({ code: mode === "missing" ? "PHYSICAL_AUTHORIZATION_REQUIRED" : "TASK19_AUTHORIZATION_SIGNATURE_INVALID", productCommandsExecuted: 0 });
  });

  test("authorized-shape dry-run exposes only the immutable repository plan", async () => {
    const value = await candidate(); const result = output(invoke(value.path));
    expect(result).toMatchObject({ code: "PRODUCTION_TRUST_INCOMPLETE", productCommandsExecuted: 0 });
    expect(result.plan.runs.map(({ platform, startupOrder }) => `${platform}:${startupOrder}`)).toEqual(["web:server_first", "web:control_first", "windows:server_first", "windows:control_first", "android:server_first", "android:control_first"]);
    expect(result.plan.driver.path).toBe("tooling/qa/task19/scenario-driver.ps1"); expect(result.plan).not.toHaveProperty("signingKey");
  });

  test("workflow isolates authorization and signing capabilities across jobs", async () => {
    const workflowText = await readFile(join(repository, ".github/workflows/task19-installed-qualification.yml"), "utf8"); const workflow = Bun.YAML.parse(workflowText);
    expect(workflow.jobs.qualify["runs-on"]).toEqual(["self-hosted", "Windows", "X64", "task19-protected"]);
    expect(workflow.jobs.qualify.permissions).toEqual({ actions: "read", contents: "read" }); expect(workflow.jobs.qualify.environment).toBeUndefined();
    expect(workflow.jobs["sign-evidence"]["runs-on"]).toBe("ubuntu-24.04"); expect(workflow.jobs["sign-evidence"].environment).toBe("task19-evidence-signing");
    expect(workflow.jobs.authorize.environment).toBe("task19-qualification"); const signer = workflow.jobs["sign-evidence"].steps.find((step) => step.name === "Sign evidence outside the candidate execution host"); const validator = workflow.jobs["sign-evidence"].steps.find((step) => step.name === "Validate signed evidence against repository production trust"); expect(signer.run).toContain("signed-boundary/task19-output/execution-result.json"); expect(validator.run).toContain("signed-boundary/task19-output/execution-result.signed.json");
    const text = await readFile(join(repository, ".github/workflows/task19-installed-qualification.yml"), "utf8");
    expect(text).toContain("--execution signed-boundary/task19-output/execution-result.json");
    expect(text).toContain("--candidates signed-boundary/task19-candidates/task19-exact-candidates.json");
    expect(text).not.toMatch(/path:\s*(?:\.|tooling\/qa\/task19\/fixtures)/);
  });

  test("classifies the current exact local candidate at its strongest product-specific denial without inventing native evidence", async () => {
    const current = join(repository, ".omo/evidence/functional-jastreamer-products/final/stage-exact-server-control-candidates.json");
    expect(output(invoke(current))).toMatchObject({ code: "SIGNED_MSIX_REQUIRED", productCommandsExecuted: 0 });
  });

  test("explicitly rejects unrelated unsupported schemas", async () => {
    const value = await candidate({ schemaVersion: 1 }); expect(output(invoke(value.path))).toMatchObject({ code: "CANDIDATE_SCHEMA_UNSUPPORTED" });
  });
});
