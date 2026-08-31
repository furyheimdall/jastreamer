import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createHash, X509Certificate } from "node:crypto";
import { EventEmitter } from "node:events";
import { chmod, copyFile, mkdir, mkdtemp, readFile, rm } from "node:fs/promises";
import { PassThrough } from "node:stream";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createTask19CaptureProxy } from "./task19-native-capture.mjs";
import { TASK19_SIDECAR_PROTOCOL_VERSION } from "./task19-sidecar-protocol.mjs";
import { sanitizeTask19DiagnosticRecord } from "./task19-sidecar-proxy.mjs";

const testRuntime = await import(typeof Bun === "undefined" ? "node:test" : "bun:test");
const { afterEach } = testRuntime;
const nodeExecutable = typeof Bun === "undefined" ? process.execPath : Bun.which("node");
if (nodeExecutable === undefined || nodeExecutable === null) throw new Error("NODE_EXECUTABLE_REQUIRED");
const test = (name, optionsOrTest, maybeTest) => {
  if (typeof optionsOrTest === "function") return testRuntime.test(name, optionsOrTest);
  return typeof Bun === "undefined"
    ? testRuntime.test(name, optionsOrTest, maybeTest)
    : testRuntime.test(name, maybeTest, optionsOrTest.timeout);
};

const sidecar = join(import.meta.dirname, "task19-capture-sidecar.mjs"); const owned = [];
afterEach(async () => { for (const release of owned.splice(0).reverse()) await release(); });

test("diagnostic records redact credentials without mutating waiter data", () => {
  const record = {
    route: "/api/v1/pairing-codes",
    headers: { authorization: "Bearer controller-secret" },
    body: { setup_secret: "bootstrap-secret", code: "123456" },
    response: { token: "controller-token", ticket: "event-ticket" },
  };
  const sanitized = sanitizeTask19DiagnosticRecord(record);
  assert.deepEqual(sanitized, {
    route: "/api/v1/pairing-codes",
    headers: { authorization: "[redacted]" },
    body: { setup_secret: "[redacted]", code: "[redacted]" },
    response: { token: "[redacted]", ticket: "[redacted]" },
  });
  assert.equal(record.response.token, "controller-token");
  assert.doesNotMatch(JSON.stringify(sanitized), /controller-secret|bootstrap-secret|123456|controller-token|event-ticket/);
});

test("diagnostic envelopes redact alternate token fields and embedded credentials", () => {
  const sanitized = sanitizeTask19DiagnosticRecord({
    kind: "event",
    event: {
      access_token: "live-access",
      adminToken: "live-admin",
      detail: "Authorization: Bearer live-bearer",
      url: "https://example.invalid/callback?setup_secret=live-setup",
    },
  });
  assert.doesNotMatch(JSON.stringify(sanitized), /live-access|live-admin|live-bearer|live-setup/);
});

test("HTTPS sidecar requires an exact upstream certificate fingerprint", async () => {
  const identity = await identityFixture();
  await assert.rejects(
    createTask19CaptureProxy({
      target: "https://127.0.0.1:1",
      identity,
      runId: "missing-upstream-fingerprint",
    }),
    { message: "TASK19_UPSTREAM_FINGERPRINT_REQUIRED" },
  );
});
const identityFixture = async () => { const root = await mkdtemp(join(tmpdir(), "task19-sidecar-security-")); const identityRoot = join(root, "identity"); await mkdir(identityRoot, { mode: 0o700 }); const keyPath = join(identityRoot, "tls-key.pem"); const certificatePath = join(identityRoot, "tls-cert.pem"); await copyFile(join(import.meta.dirname, "fixtures/task19-web-origin-key.pem"), keyPath); await copyFile(join(import.meta.dirname, "fixtures/task19-web-origin-cert.pem"), certificatePath); await chmod(keyPath, 0o600); await chmod(certificatePath, 0o600); const [key, certificate] = await Promise.all([readFile(keyPath), readFile(certificatePath)]); owned.push(() => rm(root, { recursive: true, force: true })); return { kind: "task19-run-ephemeral-tls", root: identityRoot, keyPath, certificatePath, key, certificate, certificateSha256: createHash("sha256").update(new X509Certificate(certificate).raw).digest("hex") }; };
const rawSidecar = () => { const child = spawn(nodeExecutable, [sidecar], { stdio: ["pipe", "pipe", "pipe"] }); owned.push(async () => { if (child.exitCode === null) child.kill("SIGKILL"); }); let buffered = ""; const lines = []; const waiters = []; child.stdout.setEncoding("utf8"); child.stdout.on("data", (text) => { buffered += text; for (;;) { const newline = buffered.indexOf("\n"); if (newline < 0) return; const value = JSON.parse(buffered.slice(0, newline)); buffered = buffered.slice(newline + 1); const waiter = waiters.shift(); if (waiter === undefined) lines.push(value); else waiter(value); } }); return { child, send: (value) => child.stdin.write(`${JSON.stringify(value)}\n`), next: () => lines.length > 0 ? Promise.resolve(lines.shift()) : new Promise((resolve) => waiters.push(resolve)) }; };
const request = (runId, id, command, payload = {}) => ({ version: TASK19_SIDECAR_PROTOCOL_VERSION, runId, id, command, payload });
const exitOf = (child) => child.exitCode !== null ? Promise.resolve(child.exitCode) : new Promise((resolve) => child.once("exit", resolve));
const fakeChild = (action) => { const child = new EventEmitter(); child.stdin = new PassThrough(); child.stdout = new PassThrough(); child.stderr = new PassThrough(); child.exitCode = null; child.signalCode = null; child.kill = () => { child.signalCode = "SIGKILL"; queueMicrotask(() => child.emit("exit", null, "SIGKILL")); return true; }; queueMicrotask(() => action(child)); return child; };

test("sidecar rejects unknown fields, out-of-order IDs, replay, and cross-run requests", { timeout: 30_000 }, async () => {
  // Given: a private raw JSONL channel to an uninitialized sidecar.
  const channel = rawSidecar();
  // When/Then: each hostile request is rejected with its exact structured code.
  channel.send(request("run-a", 2, "diagnostics")); assert.equal((await channel.next()).error.code, "TASK19_SIDECAR_REQUEST_OUT_OF_ORDER");
  channel.send(request("run-a", 1, "unknown")); assert.equal((await channel.next()).error.code, "TASK19_SIDECAR_NOT_INITIALIZED");
  channel.send(request("run-a", 1, "unknown")); assert.equal((await channel.next()).error.code, "TASK19_SIDECAR_REQUEST_REPLAY");
  channel.send(request("run-b", 2, "diagnostics")); assert.equal((await channel.next()).error.code, "TASK19_SIDECAR_RUN_MISMATCH");
  channel.send({ ...request("run-a", 2, "diagnostics"), hostile: true }); assert.equal((await channel.next()).error.code, "TASK19_SIDECAR_UNKNOWN_FIELD"); channel.child.stdin.end(); assert.equal(await exitOf(channel.child), 1);
});

test("sidecar fails closed on oversized and partial initialization streams", async () => {
  // Given: two fresh protocol streams with no accepted initialization.
  const oversized = rawSidecar(); const partial = rawSidecar(); const duplicate = rawSidecar();
  // When: one exceeds the line bound, one duplicates a field, and one closes before initialization.
  oversized.child.stdin.write(Buffer.alloc(1_048_577, 97)); duplicate.child.stdin.end('{"version":1,"version":1,"runId":"run","id":1,"command":"initialize","payload":{}}\n'); partial.child.stdin.end();
  // Then: all terminate nonzero without binding a listener.
  assert.equal(await exitOf(oversized.child), 1); assert.equal(await exitOf(duplicate.child), 1); assert.equal(await exitOf(partial.child), 1);
});

test("parent rejects wrong executable trust, path escape, hash drift, and permissive mode", async () => {
  // Given: protected identity material and explicit hostile variants.
  const identity = await identityFixture(); const outside = join(identity.root, "../outside-key.pem"); await copyFile(identity.keyPath, outside); owned.push(() => rm(outside, { force: true }));
  // When/Then: every trust-boundary violation fails during initialization.
  await assert.rejects(createTask19CaptureProxy({ target: "http://127.0.0.1:1", identity, nodeExecutableSha256: "0".repeat(64) }), { message: "TASK19_SIDECAR_RUNTIME_TRUST_MISMATCH" });
  await assert.rejects(createTask19CaptureProxy({ target: "http://127.0.0.1:1", identity: { ...identity, keyPath: outside } }), { message: "TASK19_SIDECAR_IDENTITY_OUTSIDE_RUN_ROOT" });
  await assert.rejects(createTask19CaptureProxy({ target: "http://127.0.0.1:1", identity: { ...identity, key: Buffer.from("wrong") } }), { message: "TASK19_SIDECAR_IDENTITY_HASH_MISMATCH" });
  await chmod(identity.keyPath, 0o644); await assert.rejects(createTask19CaptureProxy({ target: "http://127.0.0.1:1", identity }), { message: "TASK19_SIDECAR_IDENTITY_PERMISSIONS_INVALID" });
  await assert.rejects(createTask19CaptureProxy({ target: "http://127.0.0.1:1", identity, nodeExecutable: join(identity.root, "missing-node") }), { message: /TASK19_SIDECAR_(SPAWN_FAILED|PREMATURE_EXIT)/ });
});

test("parent treats child exit and bounded-stderr overflow as hard startup failures", async () => {
  // Given: platform-neutral child-process doubles that preserve stream semantics.
  const identity = await identityFixture(); const exiting = () => fakeChild((child) => { child.exitCode = 9; child.emit("exit", 9, null); }); const noisy = () => fakeChild((child) => child.stderr.write(Buffer.alloc(65_537, 120)));
  // When/Then: premature exit and stderr overflow reject, rather than falling back in-process.
  await assert.rejects(createTask19CaptureProxy({ target: "http://127.0.0.1:1", identity, spawnProcess: exiting }), { message: "TASK19_SIDECAR_PREMATURE_EXIT" });
  await assert.rejects(createTask19CaptureProxy({ target: "http://127.0.0.1:1", identity, spawnProcess: noisy }), { message: "TASK19_SIDECAR_STDERR_OVERSIZED" });
});
