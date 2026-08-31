import assert from "node:assert/strict";
import { createHash, X509Certificate } from "node:crypto";
import { copyFile, mkdir, mkdtemp, readFile, rm, chmod } from "node:fs/promises";
import { createServer } from "node:http";
import { createServer as createHttpsServer, request } from "node:https";
import { createServer as createNetServer } from "node:net";
import { connect } from "node:tls";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, test } from "node:test";
import { createTask19CaptureProxy } from "./task19-native-capture.mjs";

const owned = [];
const nodeExecutable = typeof Bun === "undefined" ? process.execPath : Bun.which("node");
if (nodeExecutable === undefined || nodeExecutable === null) throw new Error("NODE_EXECUTABLE_REQUIRED");
afterEach(async () => { for (const release of owned.splice(0).reverse()) await release(); });
const listen = (server) => new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", () => { server.off("error", reject); resolve(); }); });
const closeServer = (server) => () => new Promise((resolve, reject) => server.close((error) => error === undefined ? resolve() : reject(error)));
const identityFixture = async () => { const root = await mkdtemp(join(tmpdir(), "task19-node-only-")); const identityRoot = join(root, "identity"); await mkdir(identityRoot, { mode: 0o700 }); const keyPath = join(identityRoot, "tls-key.pem"); const certificatePath = join(identityRoot, "tls-cert.pem"); await copyFile(join(import.meta.dirname, "fixtures/task19-web-origin-key.pem"), keyPath); await copyFile(join(import.meta.dirname, "fixtures/task19-web-origin-cert.pem"), certificatePath); await chmod(keyPath, 0o600); await chmod(certificatePath, 0o600); const [key, certificate] = await Promise.all([readFile(keyPath), readFile(certificatePath)]); owned.push(() => rm(root, { recursive: true, force: true })); return { kind: "task19-run-ephemeral-tls", root: identityRoot, keyPath, certificatePath, key, certificate, certificateSha256: createHash("sha256").update(new X509Certificate(certificate).raw).digest("hex") }; };
const getBytes = (url) => new Promise((resolve, reject) => { const outgoing = request(url, { rejectUnauthorized: false }, (response) => { const chunks = []; response.on("data", (chunk) => chunks.push(chunk)); response.once("end", () => resolve({ status: response.statusCode, headers: response.headers, body: Buffer.concat(chunks) })); }); outgoing.once("error", reject); outgoing.end(); });
const frame = (event) => { const payload = Buffer.from(JSON.stringify(event)); return Buffer.concat([Buffer.from([0x81, payload.length]), payload]); };
const nextFrame = (socket, initial = Buffer.alloc(0)) => new Promise((resolve, reject) => { let buffered = initial; const inspect = () => { if (buffered.length < 2 || buffered.length < 2 + (buffered[1] & 0x7f)) return; cleanup(); resolve(buffered.subarray(2, 2 + (buffered[1] & 0x7f))); }; const receive = (chunk) => { buffered = Buffer.concat([buffered, chunk]); inspect(); }; const cleanup = () => { clearTimeout(timer); socket.off("data", receive); }; const timer = setTimeout(() => { cleanup(); reject(new Error("TEST_FRAME_TIMEOUT")); }, 2_000); socket.on("data", receive); inspect(); });
const openWebSocket = (port) => new Promise((resolve, reject) => { const socket = connect({ host: "127.0.0.1", port, rejectUnauthorized: false }); let buffered = Buffer.alloc(0); const timer = setTimeout(() => { socket.destroy(); reject(new Error("TEST_UPGRADE_TIMEOUT")); }, 2_000); socket.once("secureConnect", () => socket.write("GET /events HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n")); socket.once("error", reject); socket.on("data", function receive(chunk) { buffered = Buffer.concat([buffered, chunk]); const marker = buffered.indexOf("\r\n\r\n"); if (marker < 0) return; clearTimeout(timer); socket.off("data", receive); resolve({ socket, response: buffered.subarray(0, marker + 4).toString("latin1"), head: buffered.subarray(marker + 4) }); }); });

test("Node parent and Node sidecar preserve TLS HTTP binary bytes", async () => {
  // Given: a binary HTTP origin and protected task identity files.
  const bytes = Buffer.from([0, 255, 1, 128, 13, 10]); const target = createServer((_request, response) => response.writeHead(206, { "content-type": "application/octet-stream", etag: '"binary"' }).end(bytes)); await listen(target); owned.push(closeServer(target)); const identity = await identityFixture(); const address = target.address();
  const proxy = await createTask19CaptureProxy({ target: `http://127.0.0.1:${address.port}`, identity, runId: "node-http-run", nodeExecutable }); owned.push(proxy.close);
  // When: bytes cross the real TLS listener and Node HTTP proxy.
  const observed = proxy.next({ method: "GET", route: "/binary" }); const response = await getBytes(`${proxy.url}/binary`);
  // Then: status, headers, and bytes are unchanged and the request was captured.
  assert.equal(response.status, 206); assert.equal(response.headers["content-type"], "application/octet-stream"); assert.deepEqual(response.body, bytes); assert.equal((await observed).response.headers.etag, '"binary"'); assert.equal(proxy.runtime.kind, "node-sidecar"); const abort = new AbortController(); const abandoned = proxy.next({ method: "GET", route: "/never", signal: abort.signal }); abort.abort(); await assert.rejects(abandoned, { message: "TASK19_CAPTURE_ABORTED" }); await proxy.diagnostics();
});

test("Node sidecar rejects an upstream certificate mismatch before HTTP forwarding", async () => {
  const identity = await identityFixture();
  let requests = 0;
  const upstream = createHttpsServer({
    key: await readFile(identity.keyPath),
    cert: await readFile(identity.certificatePath),
  }, (_request, response) => {
    requests += 1;
    response.writeHead(200).end("unexpected");
  });
  await listen(upstream);
  const address = upstream.address();
  assert.notEqual(address, null);
  assert.equal(typeof address, "object");
  owned.push(closeServer(upstream));
  const proxy = await createTask19CaptureProxy({
    target: `https://127.0.0.1:${address.port}`,
    upstreamFingerprint: "0".repeat(64),
    identity,
    runId: "upstream-mismatch",
    nodeExecutable,
  });
  owned.push(proxy.close);

  await assert.rejects(
    getBytes(`${proxy.url}/credential-bearing-request`),
    { code: "ECONNRESET" },
  );
  assert.equal(requests, 0);
});

test("Node sidecar performs a real 101, exact drop, replay, and clean close", async () => {
  // Given: a raw upstream that returns a standards-valid WebSocket upgrade.
  let upstream; const target = createNetServer((socket) => { let requestBytes = Buffer.alloc(0); const receive = (chunk) => { requestBytes = Buffer.concat([requestBytes, chunk]); if (!requestBytes.includes("\r\n\r\n")) return; socket.off("data", receive); upstream = socket; socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"); }; socket.on("data", receive); }); await listen(target); owned.push(closeServer(target)); const identity = await identityFixture(); const runId = "node-ws-run"; const address = target.address(); const proxy = await createTask19CaptureProxy({ target: `http://127.0.0.1:${address.port}`, identity, runId, nodeExecutable }); owned.push(proxy.close);
  // When: normal, paused-overflow resync, exact drop, and replay traverse real sockets.
  const downstream = await openWebSocket(proxy.port); owned.push(async () => downstream.socket.destroy()); assert.match(downstream.response, /^HTTP\/1\.1 101 Switching Protocols\r\n/); const normal = nextFrame(downstream.socket, downstream.head); const normalObserved = proxy.nextEnvelope({ type: "snapshot", timeoutMs: 2_000 }); await proxy.diagnostics(); upstream.write(frame({ type: "snapshot", server_epoch: 7, sequence: 1 })); assert.equal(JSON.parse((await normal).toString()).sequence, 1); assert.equal((await normalObserved).server_epoch, "7");
  await proxy.pauseEvents(); const resyncObserved = proxy.nextEnvelope({ type: "resync_required", timeoutMs: 2_000 }); await proxy.diagnostics(); const resyncForwarded = nextFrame(downstream.socket); upstream.write(frame({ type: "invalidation", server_epoch: 7, sequence: 2, resource: "queue" })); upstream.write(frame({ type: "resync_required", server_epoch: 7, sequence: 3 })); await proxy.resumeAtResync(); assert.equal((await resyncObserved).sequence, 3); assert.equal(JSON.parse((await resyncForwarded).toString()).type, "resync_required");
  const fourth = { type: "invalidation", server_epoch: 7, sequence: 4, resource: "queue" }; const fourthForwarded = nextFrame(downstream.socket); const observed = proxy.nextEnvelope({ sequence: 4, timeoutMs: 2_000 }); await proxy.diagnostics(); upstream.write(frame(fourth)); await fourthForwarded; const exact = await observed; const exactPlan = Object.freeze({ runId, ...exact.observedFrame }); await proxy.armFrameDrop(exactPlan); const dropped = proxy.nextEnvelope({ sequence: 4, timeoutMs: 2_000 }); await proxy.diagnostics(); upstream.write(frame(fourth)); await dropped; const replay = nextFrame(downstream.socket); await proxy.replayFrame(exactPlan); assert.equal(JSON.parse((await replay).toString()).sequence, 4); const diagnostics = await proxy.diagnostics(); assert.equal(diagnostics.transport.find(({ kind }) => kind === "upstream-suppressed").count, 1);
  const timed = proxy.nextEnvelope({ stage: "node-timeout-diagnostic", type: "snapshot", sequence: 999, pageTargetId: "target-node", timeoutMs: 2_000 }); for (let sequence = 5; sequence < 17; sequence += 1) { const observedSequence = proxy.nextEnvelope({ sequence, timeoutMs: 1_000 }); await proxy.diagnostics(); upstream.write(frame({ type: "invalidation", server_epoch: 7, sequence, revision: sequence, resource: "queue" })); await observedSequence; } let timeoutError; try { await timed; } catch (error) { timeoutError = error; } assert.equal(timeoutError?.code, "TASK19_CAPTURE_ENVELOPE_TIMEOUT"); assert.equal(timeoutError.detail.requestId > 0, true); assert.equal(timeoutError.detail.stage, "node-timeout-diagnostic"); assert.equal(timeoutError.detail.observedEnvelopeCount, 12); assert.equal(timeoutError.detail.observedEnvelopeTail.length, 10); assert.equal(timeoutError.detail.selector.pageTargetId, "target-node"); assert.equal(Object.isFrozen(timeoutError.detail), true); assert.equal(JSON.stringify(timeoutError.detail).includes("forbidden"), false);
  // Then: closing owns both sides of the process and socket tree.
  await proxy.close(); owned.splice(owned.indexOf(proxy.close), 1); assert.equal(proxy.closed, true);
});
