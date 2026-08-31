import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { createScenarioProvisioner, Task19ProvisioningError } from "./task19-scenario-provisioner.mjs";
import { TASK19_SCENARIOS } from "./scenario-contract.mjs";

const clone = (value) => structuredClone(value);
const semanticState = (value) => JSON.parse(JSON.stringify(value, (key, item) => key === "revision" ? undefined : item));
const credentials = { origin: "https://server.test", token: "controller", adminToken: "admin", rendererId: "renderer-live", controllerId: "controller-live", catalogRoot: "/catalog-live" };
const deterministicBackend = (patch = {}) => {
  const initial = { revision: 1, settings: { display_name: "Server", pairing_ttl_seconds: 300, catalog_roots: [] }, policy: { mode: "stop", artist_gap: 4, album_gap: 10, session_override: "", revision: 1 }, zones: [{ zone_id: "zone-live", revision: 1, renderer_id: null }], queues: { "zone-live": [] }, devices: [{ device_id: "controller-live" }] }; let state = clone(initial); let nextEntry = 0; let nextTrack = 0; const operations = []; const cleanupCounts = new Map();
  const request = async (input) => {
    operations.push(["api", clone(input)]); const path = input.path; const zoneId = /\/zones\/([^/]+)/.exec(path)?.[1];
    if (path === "/api/v1/discovery") return { status: 200, body: { pairing_url: "https://server.test/pair/" }, headers: {} };
    if (path === "/api/v1/config" && input.method === "GET") return { status: 200, body: { revision: state.revision, settings: clone(state.settings) }, headers: { etag: `"${state.revision}"` } };
    if (path === "/api/v1/config" && input.method === "PATCH") { if (input.headers?.["if-match"] !== `"${state.revision}"`) return { status: 412, body: { code: "STALE_CONFIG_REVISION" } }; Object.assign(state.settings, input.body); state.revision += 1; return { status: 200, body: { revision: state.revision, settings: clone(state.settings) }, headers: { etag: `"${state.revision}"` } }; }
    if (path === "/api/v1/zones" && input.method === "GET") return { status: 200, body: { zones: clone(state.zones) }, headers: {} };
    if (path?.endsWith("/playback-state") || path?.endsWith("/queue") && input.method === "GET") { const zone = state.zones.find((item) => item.zone_id === zoneId); return { status: 200, body: { zone_id: zoneId, revision: zone.revision, renderer_id: zone.renderer_id, queue: clone(state.queues[zoneId]) }, headers: { etag: `"${zone.revision}"` } }; }
    if (path?.endsWith("/renderer") && input.method === "PUT") { const zone = state.zones.find((item) => item.zone_id === zoneId); zone.revision += 1; zone.renderer_id = input.body.renderer_id; return { status: 200, body: { zone_id: zoneId, revision: zone.revision, renderer_id: zone.renderer_id } }; }
    if (path?.endsWith("/queue") && input.method === "POST") { const zone = state.zones.find((item) => item.zone_id === zoneId); zone.revision += 1; if (input.body.command === "clear") state.queues[zoneId] = []; else if (input.body.command === "append") { const ids = input.body.track_ids.map(() => `entry-${++nextEntry}`); state.queues[zoneId].push(...ids.map((entry_id, index) => ({ entry_id, track_id: input.body.track_ids[index], state: "pending" }))); return { status: 201, body: { revision: zone.revision, entry_ids: ids } }; } return { status: 200, body: { revision: zone.revision, entry_ids: [] } }; }
    if (path === "/api/v1/catalog/roots") return { status: 200, body: { items: clone(state.settings.catalog_roots) }, headers: {} };
    if (path === "/api/v1/catalog/status") return { status: 200, body: { catalog_revision: state.revision, track_count: 0 }, headers: {} };
    if (path.startsWith("/api/v1/catalog/tracks")) return { status: 200, body: { catalog_revision: state.revision, tracks: [] }, headers: {} };
    if (path === "/api/v1/identity") return { status: 200, body: { sha256_fingerprint: "f".repeat(64) }, headers: {} };
    if (path?.endsWith("/continuation-policy") && input.method === "GET") return { status: 200, body: clone(state.policy), headers: { etag: `"${state.policy.revision}"` } };
    if (path?.endsWith("/continuation-policy") && input.method === "PATCH") { state.policy = { ...input.body, revision: state.policy.revision + 1 }; return { status: 200, body: clone(state.policy), headers: { etag: `"${state.policy.revision}"` } }; }
    if (path === "/api/v1/devices" && input.method === "GET") return { status: 200, body: { devices: clone(state.devices) }, headers: {} };
    if (path?.startsWith("/api/v1/devices/") && input.method === "DELETE") { state.devices = state.devices.filter((item) => item.device_id !== decodeURIComponent(path.split("/").at(-1))); return { status: 204, body: null, headers: {} }; }
    throw new Error(`UNHANDLED_API:${input.method}:${path}`);
  };
  const snapshot = async ({ runId, scenarioId }) => { const baseline = clone(state); const id = `${runId}:${scenarioId}`; let restored = false; return { material: { stateSnapshotId: createHash("sha256").update(id).digest("hex").slice(0, 24), baselineStateSha256: createHash("sha256").update(JSON.stringify(baseline)).digest("hex"), baselineConfigRevision: baseline.revision }, mutation: true, cleanup: async () => { if (!restored) { state = clone(baseline); cleanupCounts.set(`snapshot:${id}`, (cleanupCounts.get(`snapshot:${id}`) ?? 0) + 1); restored = true; } return { reversed: true, verified: true }; }, verify: async () => ({ verified: restored && JSON.stringify(state) === JSON.stringify(baseline) }) }; };
  const fixture = async () => { const trackId = `track-${++nextTrack}`; let cleaned = false; return { material: { trackId }, mutation: true, cleanup: async () => { if (!cleaned) cleanupCounts.set(`fixture:${trackId}`, (cleanupCounts.get(`fixture:${trackId}`) ?? 0) + 1); cleaned = true; return { reversed: true, verified: true }; }, verify: async () => ({ verified: cleaned }) }; };
  const process = async (item) => item.state === "server-interrupted-after-durable-mutation" ? { material: { serverRestartObserved: true }, mutation: false, cleanup: async () => ({ verified: true }), verify: async () => ({ verified: true }) } : { material: { [`process_${item.state.replaceAll("-", "_")}`]: true }, mutation: true, cleanup: async () => ({ reversed: true, verified: true }), verify: async () => ({ verified: true }) };
  return { backend: { request, snapshot, fixture, process, ...patch }, operations, cleanupCounts, state: () => clone(state), initial };
};

describe("Task19 production scenario provisioner", () => {
  test("returns immutable observed material and never reuses literal identifiers", async () => {
    const fixture = deterministicBackend(); const provision = createScenarioProvisioner(fixture.backend); const first = await provision({ runId: "run-a", contract: TASK19_SCENARIOS["queue-remove"], credentials }); expect(Object.isFrozen(first.material)).toBe(true); expect(first.material).toMatchObject({ zoneId: "zone-live", rendererId: "renderer-live", controllerId: "controller-live", trackId: "track-1", entryId: "entry-1", queueRevision: expect.any(Number), actionBaselineRevision: expect.any(Number) }); expect(JSON.stringify(first.material)).not.toMatch(/task19-(?:entry|track)/); const firstEntry = first.material.entryId; await first.cleanup(); const second = await provision({ runId: "run-a", contract: TASK19_SCENARIOS["queue-remove"], credentials }); expect(second.material.entryId).not.toBe(firstEntry); expect(second.material.trackId).toBe("track-2"); await second.cleanup(); expect(semanticState(fixture.state())).toEqual(semanticState(fixture.initial));
  });

  test("maps all typed prerequisites and restores isolated state between scenarios", async () => {
    const fixture = deterministicBackend(); const provision = createScenarioProvisioner(fixture.backend); for (const contract of Object.values(TASK19_SCENARIOS)) { const baseline = fixture.state(); const handle = await provision({ runId: "run-all", contract, credentials }); await handle.cleanup(); expect(semanticState(fixture.state()), contract.name).toEqual(semanticState(baseline)); } expect(new Set(fixture.operations.map(([kind]) => kind)).has("api")).toBe(true);
  });

  test("threads observed material through a deterministic live HTTP API boundary", async () => {
    const fixture = deterministicBackend(); const server = createServer(async (incoming, outgoing) => { const chunks = []; for await (const chunk of incoming) chunks.push(chunk); const text = Buffer.concat(chunks).toString("utf8"); const response = await fixture.backend.request({ method: incoming.method, path: incoming.url, headers: { "if-match": incoming.headers["if-match"], "idempotency-key": incoming.headers["idempotency-key"] }, body: text === "" ? null : JSON.parse(text) }); outgoing.writeHead(response.status, { "content-type": "application/json", ...(response.headers?.etag ? { etag: response.headers.etag } : {}) }); outgoing.end(response.body === null ? "" : JSON.stringify(response.body)); }); await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve)); const address = server.address(); const liveRequest = async ({ origin, token, method, path, body, headers = {} }) => { const response = await fetch(new URL(path, origin), { method, headers: { ...headers, ...(token ? { authorization: `Bearer ${token}` } : {}), ...(body == null ? {} : { "content-type": "application/json" }) }, body: body == null ? undefined : JSON.stringify(body) }); const text = await response.text(); return { status: response.status, headers: Object.fromEntries(response.headers), body: text === "" ? null : JSON.parse(text) }; }; try { const provision = createScenarioProvisioner({ ...fixture.backend, request: liveRequest }); const handle = await provision({ runId: "run-live", contract: TASK19_SCENARIOS["queue-add"], credentials: { ...credentials, origin: `http://127.0.0.1:${address.port}` } }); expect(handle.material).toMatchObject({ zoneId: "zone-live", trackId: "track-1", rendererRevision: expect.any(Number), actionBaselineRevision: expect.any(Number) }); const baseline = await liveRequest({ origin: `http://127.0.0.1:${address.port}`, token: credentials.token, method: "GET", path: "/api/v1/zones/zone-live/queue" }); const action = await liveRequest({ origin: `http://127.0.0.1:${address.port}`, token: credentials.token, method: "POST", path: "/api/v1/zones/zone-live/queue", headers: { "if-match": `"${baseline.body.revision}"`, "idempotency-key": "live-action" }, body: { command: "append", track_ids: [handle.material.trackId] } }); expect(action).toMatchObject({ status: 201, body: { entry_ids: [expect.any(String)] } }); await handle.cleanup(); expect(semanticState(fixture.state())).toEqual(semanticState(fixture.initial)); } finally { await new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve())); }
  });

  test("rejects stale revisions before product action", async () => {
    const fixture = deterministicBackend(); const original = fixture.backend.request; fixture.backend.request = async (input) => { const response = await original(input); if (input.method === "POST" && input.path.endsWith("/queue") && input.body.command === "append") response.body.revision -= 1; return response; }; const provision = createScenarioProvisioner(fixture.backend); await expect(provision({ runId: "run-stale", contract: TASK19_SCENARIOS["queue-remove"], credentials })).rejects.toThrow("TASK19_PREREQUISITE_REVISION_STALE"); expect(semanticState(fixture.state())).toEqual(semanticState(fixture.initial));
  });

  test("rejects stale/conflicting material and missing inverse ownership", async () => {
    const conflicting = deterministicBackend({ fixture: async () => ({ material: { rendererId: "stale-renderer" }, mutation: true, cleanup: async () => ({ reversed: true, verified: true }), verify: async () => ({ verified: true }) }) }); const provisionConflict = createScenarioProvisioner(conflicting.backend); await expect(provisionConflict({ runId: "run-conflict", contract: TASK19_SCENARIOS["queue-add"], credentials })).rejects.toThrow("TASK19_PREREQUISITE_MATERIAL_CONFLICT:rendererId");
    const missing = deterministicBackend({ process: async () => ({ material: {}, mutation: true, verify: async () => ({ verified: false }) }) }); const provisionMissing = createScenarioProvisioner(missing.backend); await expect(provisionMissing({ runId: "run-missing", contract: TASK19_SCENARIOS["certificate-change"], credentials })).rejects.toThrow("TASK19_PREREQUISITE_OWNERSHIP_REQUIRED");
  });

  test("owns production certificate rotation and reset", async () => {
    const fixture = deterministicBackend();
    const { process: _stubbedProcess, ...backend } = fixture.backend;
    let rotations = 0;
    let resets = 0;
    const provision = createScenarioProvisioner(backend);
    const runtimeCredentials = { ...credentials };
    Object.defineProperty(runtimeCredentials, "capture", {
      enumerable: false,
      value: Object.freeze({
        rotate: async () => {
          rotations += 1;
          return "b".repeat(64);
        },
        reset: async () => {
          resets += 1;
        },
      }),
    });
    const handle = await provision({
      runId: "run-certificate",
      contract: TASK19_SCENARIOS["certificate-change"],
      credentials: runtimeCredentials,
    });
    expect(handle.material).toMatchObject({
      rotatedCertificateSha256: "b".repeat(64),
    });
    expect(rotations).toBe(1);
    await handle.cleanup();
    expect(resets).toBe(1);
    expect(semanticState(fixture.state())).toEqual(semanticState(fixture.initial));
  });

  test("subscribes before renderer disconnect and removes the owned rule", async () => {
    const fixture = deterministicBackend();
    const { process: _stubbedProcess, ...backend } = fixture.backend;
    const trace = [];
    backend.execute = async (command) => {
      trace.push(["command", clone(command)]);
      return { exitCode: 0, stdout: "", stderr: "" };
    };
    const runtimeCredentials = { ...credentials };
    Object.defineProperty(runtimeCredentials, "capture", {
      enumerable: false,
      value: Object.freeze({
        nextEvent: (resource) => {
          trace.push(["subscribe", resource]);
          return Promise.resolve({
            type: "invalidation",
            resource: "renderers",
            sequence: 1,
          });
        },
      }),
    });
    const provision = createScenarioProvisioner(backend);
    const handle = await provision({
      runId: "run-renderer-disconnect",
      contract: TASK19_SCENARIOS["offline-renderer"],
      credentials: runtimeCredentials,
    });
    expect(trace[0]).toEqual(["subscribe", "renderers"]);
    expect(trace[1][0]).toBe("command");
    expect(trace[1][1].join(" ")).toContain("New-NetFirewallRule");
    expect(handle.material.rendererDisconnectRule).toBe("Task19-renderer-live");
    await handle.cleanup();
    expect(trace.at(-1)[1].join(" ")).toContain("Remove-NetFirewallRule");
    expect(semanticState(fixture.state())).toEqual(semanticState(fixture.initial));
  });

  test("runs the production durable-restart command and restores scenario state", async () => {
    const fixture = deterministicBackend();
    const { process: _stubbedProcess, ...backend } = fixture.backend;
    const commands = [];
    backend.execute = async (command) => {
      commands.push(clone(command));
      return { exitCode: 0, stdout: "", stderr: "" };
    };
    const provision = createScenarioProvisioner(backend);
    const handle = await provision({
      runId: "run-durable-restart",
      contract: TASK19_SCENARIOS["interrupted-restart"],
      credentials,
    });
    expect(commands).toContainEqual([
      "wsl.exe",
      "--exec",
      "sudo",
      "systemctl",
      "restart",
      "jastreamer-server.service",
    ]);
    expect(handle.material.serverRestartObserved).toBe(true);
    await handle.cleanup();
    expect(semanticState(fixture.state())).toEqual(semanticState(fixture.initial));
  });

  test("materializes an unavailable queue head and restores the queue baseline", async () => {
    const fixture = deterministicBackend();
    const provision = createScenarioProvisioner(fixture.backend);
    const handle = await provision({
      runId: "run-unavailable-head",
      contract: TASK19_SCENARIOS["unavailable-explicit-head"],
      credentials,
    });
    expect(handle.material).toMatchObject({
      unavailableTrackId: expect.any(String),
      entryId: expect.any(String),
      queueRevision: expect.any(Number),
    });
    const queued = fixture.state().queues[handle.material.zoneId];
    expect(queued).toEqual([
      expect.objectContaining({
        entry_id: handle.material.entryId,
        track_id: handle.material.unavailableTrackId,
      }),
    ]);
    await handle.cleanup();
    expect(fixture.state().queues[handle.material.zoneId]).toEqual([]);
    expect(semanticState(fixture.state())).toEqual(semanticState(fixture.initial));
  });

  test("detects cleanup no-op and records cleanup failure structurally", async () => {
    const fixture = deterministicBackend({ fixture: async () => ({ material: { trackId: "track-live" }, mutation: true, cleanup: async () => undefined, verify: async () => ({ verified: false }) }) }); const provision = createScenarioProvisioner(fixture.backend); const handle = await provision({ runId: "run-noop", contract: TASK19_SCENARIOS["browse-search"], credentials }); try { await handle.cleanup(); throw new Error("TEST_EXPECTED_FAILURE"); } catch (error) { expect(error).toBeInstanceOf(AggregateError); expect(error.failures).toEqual(expect.arrayContaining([expect.objectContaining({ phase: "inverse", code: "TASK19_PREREQUISITE_CLEANUP_NOOP" })])); }
  });

  test("preserves setup failure while recording partial cleanup failure", async () => {
    let snapshotCleanup = 0; const fixture = deterministicBackend(); fixture.backend.snapshot = async () => ({ material: { stateSnapshotId: "snapshot", baselineStateSha256: "a".repeat(64), baselineConfigRevision: 1 }, mutation: true, cleanup: async () => { snapshotCleanup += 1; throw new Error("SNAPSHOT_RESTORE_FAILED"); }, verify: async () => ({ verified: false }) }); const baseRequest = fixture.backend.request; fixture.backend.request = async (input) => { if (input.method === "DELETE") throw new Error("DEVICE_SETUP_FAILED"); return baseRequest(input); }; const provision = createScenarioProvisioner(fixture.backend); try { await provision({ runId: "run-partial", contract: TASK19_SCENARIOS.revocation, credentials }); throw new Error("TEST_EXPECTED_FAILURE"); } catch (error) { expect(error).toBeInstanceOf(Task19ProvisioningError); expect(error.cause.message).toBe("DEVICE_SETUP_FAILED"); expect(error.cleanupFailures).toEqual(expect.arrayContaining([expect.objectContaining({ phase: "snapshot-restore", code: "SNAPSHOT_RESTORE_FAILED" })])); } expect(snapshotCleanup).toBe(1);
  });

  test("cleanup is idempotent and unregisters scenario ownership", async () => {
    const fixture = deterministicBackend(); const provision = createScenarioProvisioner(fixture.backend); const first = await provision({ runId: "run-double", contract: TASK19_SCENARIOS.revocation, credentials }); await Promise.all([first.cleanup(), first.cleanup()]); expect(fixture.cleanupCounts.get("snapshot:run-double:revocation")).toBe(1); const second = await provision({ runId: "run-double", contract: TASK19_SCENARIOS.revocation, credentials }); await second.cleanup(); expect(fixture.cleanupCounts.get("snapshot:run-double:revocation")).toBe(2);
  });

  test("run cleanup is only an idempotent backstop", async () => {
    const fixture = deterministicBackend(); const provision = createScenarioProvisioner(fixture.backend); await provision({ runId: "run-backstop", contract: TASK19_SCENARIOS.revocation, credentials }); await provision.closeRun("run-backstop"); await provision.closeRun("run-backstop"); expect(fixture.cleanupCounts.get("snapshot:run-backstop:revocation")).toBe(1); expect(semanticState(fixture.state())).toEqual(semanticState(fixture.initial));
  });

  test("runs the bounded batch path and deletes all eight owned zones through the API", async () => {
    const zones = new Map(); const deletes = []; const captureRequests = new Map(); const captureEvents = new Map(); let sequence = 0; const capture = { next: ({ route }) => new Promise((resolve) => captureRequests.set(route, resolve)), nextEvent: (_resource, _timeout, { zoneId }) => new Promise((resolve) => captureEvents.set(zoneId, resolve)) }; const request = async (input) => { if (input.method === "POST" && input.path === "/api/v1/zones") { zones.set(input.body.zone_id, { revision: 0, queue: [] }); return { status: 201, body: { zone_id: input.body.zone_id, revision: 0 } }; } const zoneId = decodeURIComponent(/^\/api\/v1\/zones\/([^/]+)/.exec(input.path)?.[1] ?? ""); const zone = zones.get(zoneId); if (input.method === "POST" && input.path.endsWith("/queue")) { zone.revision += 1; const entryId = `${zoneId}:entry`; zone.queue.push({ entry_id: entryId, track_id: input.body.track_ids[0], position: 1 }); const response = { status: 201, headers: { etag: `"${zone.revision}"` }, body: { revision: zone.revision, entry_ids: [entryId] } }; captureRequests.get(input.path)({ request: { method: "POST", route: input.path, headers: { ifMatch: input.headers["if-match"], idempotencyKey: input.headers["idempotency-key"], correlationId: input.headers["x-request-id"], authorization: "bearer" }, body: clone(input.body) }, response: clone(response) }); captureEvents.get(zoneId)({ type: "invalidation", resource: "queue", zone_id: zoneId, revision: zone.revision, sequence: ++sequence }); return response; } if (input.method === "GET" && input.path.endsWith("/queue")) return { status: 200, body: { zone_id: zoneId, revision: zone.revision, queue: clone(zone.queue) } }; if (input.method === "DELETE") { deletes.push({ zoneId, headers: input.headers }); zones.delete(zoneId); captureEvents.get(zoneId)({ type: "invalidation", resource: "zones", zone_id: zoneId, revision: zone.revision + 1, sequence: ++sequence }); return { status: 200, headers: { etag: `"${zone.revision + 1}"` }, body: { zone_id: zoneId, revision: zone.revision + 1 } }; } if (input.method === "GET" && input.path === "/api/v1/zones") return { status: 200, body: { zones: [...zones.keys()].map((zone_id) => ({ zone_id })) } }; throw new Error(`UNHANDLED_SCALE_API:${input.method}:${input.path}`); }; const provision = createScenarioProvisioner({ request, fixture: async () => { let cleaned = false; return { material: { catalogTrackIds: ["track-live"] }, mutation: true, cleanup: async () => { cleaned = true; return { reversed: true, verified: true }; }, verify: async () => ({ verified: cleaned }) }; } }); const scale = await provision.scale({ runId: "run-scale", credentials: { token: "control", adminToken: "admin", catalogRoot: "/catalog-live", capture }, entriesPerZone: 1, batchSize: 1, concurrency: 4 }); expect(scale.material).toMatchObject({ entryCount: 8, batchCount: 8, concurrency: 4 }); await scale.cleanup(); expect(deletes).toHaveLength(8); expect(deletes.every(({ headers }) => headers["if-match"] === `"1"` && headers["idempotency-key"].startsWith("task19-zone-delete-"))).toBe(true); expect(zones.size).toBe(0);
  });
});
