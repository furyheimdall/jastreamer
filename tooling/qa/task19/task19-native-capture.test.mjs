import { afterEach, describe, expect, test } from "bun:test";
import { createServer } from "node:http";
import { request } from "node:https";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createTask19CaptureProxy, createTask19ServerFrameDecoder } from "./task19-native-capture.mjs";
import { createEphemeralTlsIdentity } from "./task19-tls-identity.mjs";
import { findUnsafeEvidence } from "../receipt-redaction.mjs";

const cleanup = []; afterEach(async () => { for (const close of cleanup.splice(0).reverse()) await close(); });
const listen = (server) => new Promise((resolve, reject) => { server.once("error", reject); server.listen(0, "127.0.0.1", () => { server.off("error", reject); resolve(); }); });
const close = (server) => () => new Promise((resolve, reject) => server.close((error) => error === undefined ? resolve() : reject(error)));
const frame = (value) => { const payload = Buffer.from(JSON.stringify(value)); return payload.length < 126 ? Buffer.concat([Buffer.from([0x81, payload.length]), payload]) : Buffer.concat([Buffer.from([0x81, 126, payload.length >> 8, payload.length & 255]), payload]); };
const get = (url) => new Promise((resolve, reject) => { const value = request(url, { rejectUnauthorized: false }, (response) => { const chunks = []; response.on("data", (bytes) => chunks.push(bytes)); response.once("end", () => resolve({ status: response.statusCode, contentType: response.headers["content-type"], body: Buffer.concat(chunks).toString() })); }); value.once("error", reject); value.end(); });
const send = (url) => new Promise((resolve, reject) => { const value = request(url, { method: "POST", rejectUnauthorized: false, headers: { "content-type": "application/json", "if-match": '"1"', "idempotency-key": "task19-command", "authorization": "Bearer secret", "x-request-id": "correlation-1" } }, (response) => { const chunks = []; response.on("data", (bytes) => chunks.push(bytes)); response.once("end", () => resolve({ status: response.statusCode, body: Buffer.concat(chunks).toString() })); }); value.once("error", reject); value.end('{"command":"start"}'); });

describe("Task19 native low-level capture channel", () => {
  test("correlates an actual forwarded request and response when subscribed before the UI action", async () => {
    // Given: a live Server wire endpoint and the task-owned TLS capture boundary.
    const target = createServer((incoming, response) => { response.writeHead(202, { "content-type": "application/json", etag: '"2"' }); response.end('{"revision":2,"command_id":"command-1","status":"pending"}'); }); await listen(target); cleanup.push(close(target)); const address = target.address();
    const root = await mkdtemp(join(tmpdir(), "task19-capture-identity-")); cleanup.push(() => rm(root, { recursive: true, force: true })); const identity = await createEphemeralTlsIdentity({ root: join(root, "primary") }); const rotated = await createEphemeralTlsIdentity({ root: join(root, "rotated") }); cleanup.push(rotated.cleanup, identity.cleanup); const proxy = await createTask19CaptureProxy({ target: `http://127.0.0.1:${address.port}`, identity }); cleanup.push(proxy.close);
    // When: capture is armed before the native product sends its action.
    const observed = proxy.next({ method: "POST", route: "/api/v1/zones/main/transport" }); const response = await send(`${proxy.url}/api/v1/zones/main/transport`);
    // Then: validator-compatible evidence comes from forwarded bytes, not expectedRequest.
    const capture = await observed;
    expect(response.status).toBe(202); expect(capture).toEqual({ request: { method: "POST", route: "/api/v1/zones/main/transport", headers: { ifMatch: '"1"', idempotencyKey: "task19-command", authenticationScheme: "bearer", correlationId: "correlation-1" }, body: { command: "start" } }, response: { status: 202, headers: { etag: '"2"' }, body: { revision: 2, command_id: "command-1", status: "pending" } } });
    expect(findUnsafeEvidence(capture)).toBeUndefined();
    expect(await proxy.rotate(rotated)).not.toBe(proxy.fingerprint);
  });

  test("preserves non-JSON pairing UI bytes instead of replacing them with capture metadata", async () => { const target = createServer((_incoming, response) => response.writeHead(200, { "content-type": "text/html" }).end("<!doctype html><label>Device name</label>")); await listen(target); cleanup.push(close(target)); const root = await mkdtemp(join(tmpdir(), "task19-capture-html-")); cleanup.push(() => rm(root, { recursive: true, force: true })); const identity = await createEphemeralTlsIdentity({ root: join(root, "identity") }); cleanup.push(identity.cleanup); const address = target.address(); const proxy = await createTask19CaptureProxy({ target: `http://127.0.0.1:${address.port}`, identity }); cleanup.push(proxy.close); expect(await get(`${proxy.url}/pair/`)).toEqual({ status: 200, contentType: "text/html", body: "<!doctype html><label>Device name</label>" }); });

  test("reassembles fragmented and coalesced Server event frames without dropping zone correlation", () => {
    const events = []; const decode = createTask19ServerFrameDecoder(({ payload }) => events.push(JSON.parse(payload.toString("utf8")))); const wire = Buffer.concat([frame({ type: "invalidation", resource: "queue", zone_id: "zone-a", revision: 1, sequence: 1 }), frame({ type: "invalidation", resource: "queue", zone_id: "zone-b", revision: 1, sequence: 2 })]); decode(wire.subarray(0, 3)); decode(wire.subarray(3)); expect(events).toEqual([expect.objectContaining({ zone_id: "zone-a", revision: 1 }), expect.objectContaining({ zone_id: "zone-b", revision: 1 })]);
  });
});
