import { afterEach, describe, expect, test } from "bun:test";
import { createHash, X509Certificate } from "node:crypto";
import { copyFile, mkdir, mkdtemp, readFile, rm, chmod } from "node:fs/promises";
import { createServer } from "node:net";
import { connect } from "node:tls";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createTask19CaptureProxy } from "./task19-native-capture.mjs";

const owned = [];
const nodeExecutable = Bun.which("node");
if (nodeExecutable === undefined || nodeExecutable === null) throw new Error("NODE_EXECUTABLE_REQUIRED");
afterEach(async () => { for (const release of owned.splice(0).reverse()) await release(); });
const listen = (server) => new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", () => { server.off("error", reject); resolve(); }); });
const closeServer = (server) => () => new Promise((resolve, reject) => server.close((error) => error === undefined ? resolve() : reject(error)));
const serverFrame = (event) => { const payload = Buffer.from(JSON.stringify(event)); return Buffer.concat([Buffer.from([0x81, payload.length]), payload]); };
const plan = (runId, event) => Object.freeze({ runId, ...event.observedFrame });

const identityFixture = async () => {
  const root = await mkdtemp(join(tmpdir(), "task19-node-sidecar-"));
  const identityRoot = join(root, "identity"); await mkdir(identityRoot, { mode: 0o700 });
  const keyPath = join(identityRoot, "tls-key.pem"); const certificatePath = join(identityRoot, "tls-cert.pem");
  await copyFile(join(import.meta.dirname, "fixtures/task19-web-origin-key.pem"), keyPath); await copyFile(join(import.meta.dirname, "fixtures/task19-web-origin-cert.pem"), certificatePath); await chmod(keyPath, 0o600); await chmod(certificatePath, 0o600);
  const [key, certificate] = await Promise.all([readFile(keyPath), readFile(certificatePath)]); const parsed = new X509Certificate(certificate);
  owned.push(() => rm(root, { recursive: true, force: true }));
  return { kind: "task19-run-ephemeral-tls", root: identityRoot, keyPath, certificatePath, key, certificate, certificateSha256: createHash("sha256").update(parsed.raw).digest("hex") };
};

const openWebSocket = (proxy) => new Promise((resolve, reject) => {
  const socket = connect({ host: "127.0.0.1", port: proxy.port, rejectUnauthorized: false }); let buffered = Buffer.alloc(0);
  socket.once("error", reject); socket.once("secureConnect", () => socket.write("GET /api/v1/events HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"));
  const timer = setTimeout(() => { socket.destroy(); reject(new Error("TEST_UPGRADE_TIMEOUT")); }, 2_000);
  const onData = (chunk) => { buffered = Buffer.concat([buffered, chunk]); const marker = buffered.indexOf("\r\n\r\n"); if (marker < 0) return; clearTimeout(timer); socket.off("data", onData); resolve({ socket, response: buffered.subarray(0, marker + 4).toString("latin1"), head: buffered.subarray(marker + 4) }); };
  socket.on("data", onData);
});
const nextFrame = (socket, initial = Buffer.alloc(0)) => new Promise((resolve, reject) => { let buffered = initial; const inspect = () => { if (buffered.length < 2) return; const length = buffered[1] & 0x7f; if (buffered.length < 2 + length) return; cleanup(); resolve(buffered.subarray(2, 2 + length)); }; const onData = (chunk) => { buffered = Buffer.concat([buffered, chunk]); inspect(); }; const cleanup = () => { clearTimeout(timer); socket.off("data", onData); socket.off("error", reject); }; const timer = setTimeout(() => { cleanup(); reject(new Error("TEST_FRAME_TIMEOUT")); }, 2_000); socket.on("data", onData); socket.once("error", reject); inspect(); });

describe("Task19 Node sidecar capture", () => {
  test("Bun parent receives canonical 101 and applies normal, exact drop, replay, and resync downstream only", async () => {
    // Given: a real upstream upgrade endpoint and a task-owned TLS identity.
    let upstream; const target = createServer((socket) => { let request = Buffer.alloc(0); const receive = (chunk) => { request = Buffer.concat([request, chunk]); if (!request.includes("\r\n\r\n")) return; socket.off("data", receive); upstream = socket; socket.write("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"); }; socket.on("data", receive); }); await listen(target); owned.push(closeServer(target)); const address = target.address();
    const identity = await identityFixture(); const runId = "task19-sidecar-run"; const proxy = await createTask19CaptureProxy({ target: `http://127.0.0.1:${address.port}`, identity, runId, nodeExecutable }); owned.push(proxy.close);
    // When: the downstream upgrades and four upstream events cross the sidecar plan.
    let downstream; try { downstream = await openWebSocket(proxy); } catch (error) { throw new Error(`${error.message}:${JSON.stringify(await proxy.diagnostics())}`); } owned.push(async () => downstream.socket.destroy()); expect(downstream.response).toStartWith("HTTP/1.1 101 Switching Protocols\r\n"); expect(proxy.runtime.kind).toBe("node-sidecar");
    const normalSignal = nextFrame(downstream.socket, downstream.head); upstream.write(serverFrame({ type: "invalidation", server_epoch: 7, sequence: 1, resource: "queue", zone_id: "main" })); expect(JSON.parse((await normalSignal).toString())).toMatchObject({ sequence: 1 });
    const secondEvent = { type: "invalidation", server_epoch: 7, sequence: 2, resource: "queue", zone_id: "main" }; const secondForwarded = nextFrame(downstream.socket); const secondObserved = proxy.nextEnvelope({ sequence: 2, timeoutMs: 2_000 }); await proxy.diagnostics(); upstream.write(serverFrame(secondEvent)); await secondForwarded; const exactSecond = await secondObserved.catch(async (error) => { throw new Error(`SECOND_OBSERVATION:${error.message}:${JSON.stringify(await proxy.diagnostics())}`); }); await proxy.armFrameDrop(plan(runId, exactSecond)); const droppedObserved = proxy.nextEnvelope({ sequence: 2, timeoutMs: 2_000 }); await proxy.diagnostics(); upstream.write(serverFrame(secondEvent)); await droppedObserved.catch(async (error) => { throw new Error(`DROP_OBSERVATION:${error.message}:${JSON.stringify(await proxy.diagnostics())}`); });
    const replaySignal = nextFrame(downstream.socket); await proxy.replayFrame(plan(runId, exactSecond)); expect(JSON.parse((await replaySignal).toString())).toMatchObject({ sequence: 2 });
    const resyncSignal = nextFrame(downstream.socket); upstream.write(serverFrame({ type: "resync_required", server_epoch: 7, sequence: 3 })); expect(JSON.parse((await resyncSignal).toString())).toMatchObject({ type: "resync_required", sequence: 3 });
    // Then: diagnostics prove the exact downstream plan and the child closes cleanly.
    expect((await proxy.diagnostics()).frames.map(({ kind }) => kind)).toEqual(["upstream", "forward", "upstream", "forward", "upstream", "drop", "replay", "upstream", "forward"]); await proxy.close(); owned.pop(); expect(proxy.closed).toBe(true);
  }, 10_000);
});
