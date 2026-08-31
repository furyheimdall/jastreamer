const freeze = (value) => {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const item of Object.values(value)) freeze(item);
    Object.freeze(value);
  }
  return value;
};

const PRECONDITIONS = freeze({
  paired: { kind: "session", state: "controller-paired" },
  admin: { kind: "credential", role: "admin", state: "active" },
  catalog: { kind: "api", method: "GET", route: "/api/v1/catalog/roots", body: null, expectedStatus: 200 },
  tracks: { kind: "cli", command: "task19-catalog-fixture", arguments: ["--tracks", "100000", "--root", "$catalogRoot"] },
  zone: { kind: "api", method: "GET", route: "/api/v1/zones", body: null, expectedStatus: 200 },
  renderer: { kind: "api", method: "PUT", route: "/api/v1/zones/:zoneId/renderer", body: { renderer_id: "$rendererId" }, expectedStatus: 200 },
  queue: { kind: "api", method: "POST", route: "/api/v1/zones/:zoneId/queue", body: { command: "append", track_ids: ["$trackId"] }, expectedStatus: 201 },
  largeQueue: { kind: "cli", command: "task19-queue-fixture", arguments: ["--zone", "$zoneId", "--entries", "10000"] },
  staleConfig: { kind: "api", method: "PATCH", route: "/api/v1/config", body: { display_name: "Task19 current" }, expectedStatus: 200 },
  revoked: { kind: "api", method: "DELETE", route: "/api/v1/devices/:controllerId", body: null, expectedStatus: 204 },
  certificateChanged: { kind: "process", state: "server-certificate-rotated-after-pairing" },
  eventGap: { kind: "process", state: "control-event-sequence-gap" },
  rendererOffline: { kind: "process", state: "task19-renderer-disconnected" },
  explicitHeadUnavailable: { kind: "api", method: "POST", route: "/api/v1/zones/:zoneId/queue", body: { command: "append", track_ids: ["$unavailableTrackId"] }, expectedStatus: 201 },
  interrupted: { kind: "process", state: "server-interrupted-after-durable-mutation" },
});

const UI_NAMES = freeze({
  pair: "Complete pairing", admin: "Open administration", "settings-restart": "Save settings", "multi-root-scan": "Scan Task19", "browse-search": "Search catalog", "secure-token-restart": "Restart Control",
  "queue-add": "Add track", "queue-reorder": "Earlier", "queue-remove": "Remove", "queue-clear": "Clear", "queue-retry": "Retry blocked track", "queue-skip": "Skip blocked track",
  "transport-start": "Play", "transport-pause": "Pause", "transport-resume": "Resume", "transport-stop": "Stop", "transport-skip": "Next", "transport-seek": "Seek", "transport-next": "Next", "transport-previous": "Previous",
  policy: "Save policy", "event-gap-resync": "Refresh Server state", "renderer-assignment-status": "Assigned Renderer", revocation: "Refresh Server state", "invalid-config": "Save settings", "stale-revision": "Save settings", "certificate-change": "Discover Server", "unavailable-explicit-head": "Play", "offline-renderer": "Play", "interrupted-restart": "Refresh Server state",
});
const SNAPSHOT_ROUTES = freeze({ pair: "/api/v1/discovery", admin: "/api/v1/config", "settings-restart": "/api/v1/config", "multi-root-scan": "/api/v1/catalog/status", "browse-search": "/api/v1/catalog/status", "secure-token-restart": "/api/v1/discovery", revocation: "/api/v1/discovery", "invalid-config": "/api/v1/config", "stale-revision": "/api/v1/config", "certificate-change": "/api/v1/identity", "renderer-assignment-status": "/api/v1/zones" });
const requiredHeaders = (method, route) => method === "POST" && (route.endsWith("/queue") || route.endsWith("/transport")) || method === "PATCH" || method === "PUT" ? ["ifMatch", "idempotencyKey"] : [];
const action = (name, method, route, body, status, semantic, resource, preconditions, failure = false, role = "button", gesture = "click", expected = {}) => freeze({
  name,
  preconditions,
  surface: { selector: { role, name: UI_NAMES[name] }, gesture, request: { method, route, body, requiredHeaders: requiredHeaders(method, route) } },
  expected: { status, semantic, code: semantic, failure, ...expected },
  state: { snapshotRoute: SNAPSHOT_ROUTES[name] ?? (route.endsWith("/queue") ? "/api/v1/zones/:zoneId/queue" : route.includes("/zones/:zoneId/") ? "/api/v1/zones/:zoneId/playback-state" : route), revision: failure ? "unchanged" : "advanced", namedDelta: failure ? "none" : resource },
  event: failure ? { kind: "none", resource, revision: "none" } : { kind: "invalidation", resource, revision: "equals-after" },
});

const P = PRECONDITIONS;
const CONTRACTS = [
  action("pair", "GET", "/api/v1/discovery", null, 200, "discovery", "devices", [] , false, "button"),
  action("admin", "GET", "/api/v1/config", null, 200, "config", "config", [P.paired, P.admin]),
  action("settings-restart", "PATCH", "/api/v1/config", { pairing_ttl_seconds: 600 }, 200, "config", "config", [P.paired, P.admin]),
  action("multi-root-scan", "POST", "/api/v1/catalog/scans", {}, 202, "scan-job", "catalog", [P.paired, P.admin, P.tracks, P.catalog]),
  action("browse-search", "GET", "/api/v1/catalog/tracks?query=task19&limit=100", null, 200, "catalog-page", "catalog", [P.paired, P.tracks, P.catalog]),
  action("secure-token-restart", "GET", "/api/v1/discovery", null, 200, "discovery", "devices", [P.paired], false, "application", "restart", { eventRequired: false }),
  action("queue-add", "POST", "/api/v1/zones/:zoneId/queue", { command: "append", track_ids: ["$trackId"] }, 201, "queue-mutation", "queue", [P.paired, P.tracks, P.catalog, P.zone, P.renderer]),
  action("queue-reorder", "POST", "/api/v1/zones/:zoneId/queue", { command: "move", entry_id: "$entryId", before_entry_id: null }, 200, "queue-mutation", "queue", [P.paired, P.tracks, P.catalog, P.zone, P.queue]),
  action("queue-remove", "POST", "/api/v1/zones/:zoneId/queue", { command: "remove", entry_id: "$entryId" }, 200, "queue-mutation", "queue", [P.paired, P.tracks, P.catalog, P.zone, P.queue]),
  action("queue-clear", "POST", "/api/v1/zones/:zoneId/queue", { command: "clear" }, 200, "queue-mutation", "queue", [P.paired, P.tracks, P.catalog, P.zone, P.queue]),
  action("queue-retry", "POST", "/api/v1/zones/:zoneId/queue", { command: "retry_blocked", entry_id: "$entryId" }, 200, "queue-mutation", "queue", [P.paired, P.tracks, P.catalog, P.zone, P.explicitHeadUnavailable]),
  action("queue-skip", "POST", "/api/v1/zones/:zoneId/queue", { command: "skip_blocked", entry_id: "$entryId" }, 200, "queue-mutation", "queue", [P.paired, P.tracks, P.catalog, P.zone, P.explicitHeadUnavailable]),
  action("transport-start", "POST", "/api/v1/zones/:zoneId/transport", { command: "start" }, 202, "transport-mutation", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.queue], false, "button", "click", { statusField: "pending" }),
  action("transport-pause", "POST", "/api/v1/zones/:zoneId/transport", { command: "pause" }, 202, "transport-mutation", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.queue], false, "button", "click", { statusField: "pending" }),
  action("transport-resume", "POST", "/api/v1/zones/:zoneId/transport", { command: "resume" }, 202, "transport-mutation", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.queue], false, "button", "click", { statusField: "pending" }),
  action("transport-stop", "POST", "/api/v1/zones/:zoneId/transport", { command: "stop" }, 202, "transport-mutation", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.queue], false, "button", "click", { statusField: "pending" }),
  action("transport-skip", "POST", "/api/v1/zones/:zoneId/transport", { command: "skip" }, 202, "transport-mutation", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.queue], false, "button", "click", { statusField: "pending" }),
  action("transport-seek", "POST", "/api/v1/zones/:zoneId/transport", { command: "seek", position_ms: 5000 }, 202, "transport-mutation", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.queue], false, "button", "click", { statusField: "pending" }),
  action("transport-next", "POST", "/api/v1/zones/:zoneId/transport", { command: "skip" }, 202, "transport-mutation", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.queue], false, "button", "click", { statusField: "pending" }),
  action("transport-previous", "POST", "/api/v1/zones/:zoneId/transport", { command: "previous" }, 202, "transport-mutation", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.queue], false, "button", "click", { statusField: "pending" }),
  action("policy", "PATCH", "/api/v1/zones/:zoneId/continuation-policy", { mode: "stop", artist_gap: 1, album_gap: 1, session_override: "" }, 200, "continuation-policy", "policy", [P.paired, P.zone]),
  action("event-gap-resync", "GET", "/api/v1/zones/:zoneId/playback-state", null, 200, "playback-state", "playback-state", [P.paired, P.zone, P.eventGap]),
  action("renderer-assignment-status", "PUT", "/api/v1/zones/:zoneId/renderer", { renderer_id: "$rendererId" }, 200, "renderer-assignment", "zones", [P.paired, P.zone, P.renderer], false, "combobox", "select"),
  action("revocation", "GET", "/api/v1/discovery", null, 401, "error", "devices", [P.paired, P.revoked], true, "button", "click", { code: "TOKEN_REVOKED" }),
  action("invalid-config", "PATCH", "/api/v1/config", { pairing_ttl_seconds: 59 }, 400, "error", "config", [P.paired, P.admin], true, "button", "click", { code: "CONFIG_VALIDATION_FAILED" }),
  action("stale-revision", "PATCH", "/api/v1/config", { display_name: "Task19 stale" }, 412, "error", "config", [P.paired, P.admin, P.staleConfig], true, "button", "click", { code: "STALE_CONFIG_REVISION" }),
  action("certificate-change", "GET", "/api/v1/identity", null, 0, "native-failure", "identity", [P.paired, P.certificateChanged], true, "button", "click", { surface: "control", code: "CERTIFICATE_MISMATCH" }),
  action("unavailable-explicit-head", "POST", "/api/v1/zones/:zoneId/transport", { command: "start" }, 409, "error", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.renderer, P.explicitHeadUnavailable], true, "button", "click", { code: "BLOCKED_EXPLICIT_HEAD" }),
  action("offline-renderer", "POST", "/api/v1/zones/:zoneId/transport", { command: "start" }, 409, "error", "transport", [P.paired, P.tracks, P.catalog, P.zone, P.queue, P.rendererOffline], true, "button", "click", { code: "RENDERER_OFFLINE" }),
  action("interrupted-restart", "GET", "/api/v1/zones/:zoneId/playback-state", null, 200, "playback-state", "playback-state", [P.paired, P.tracks, P.catalog, P.zone, P.queue, P.interrupted]),
];

const materialize = (value, material) => {
  if (typeof value === "string") { if (value.startsWith("$") && !value.slice(1).includes("/")) { const resolved = material[value.slice(1)]; if (typeof resolved !== "string" || resolved === "") throw new Error(`TASK19_SCENARIO_MATERIAL_MISSING:${value.slice(1)}`); return resolved; } return value.replaceAll(/:([A-Za-z][A-Za-z0-9]*)/g, (_match, key) => { const resolved = material[key]; if (typeof resolved !== "string" || resolved === "") throw new Error(`TASK19_SCENARIO_MATERIAL_MISSING:${key}`); return encodeURIComponent(resolved); }); }
  if (Array.isArray(value)) return value.map((item) => materialize(item, material)); if (value !== null && typeof value === "object") return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, materialize(item, material)])); return value;
};
export const materializeScenarioContract = (contract, material) => { const { preconditions, ...wireContract } = contract; return freeze({ ...materialize(wireContract, material), preconditions }); };

export const TASK19_SCENARIOS = freeze(Object.fromEntries(CONTRACTS.map((contract) => [contract.name, contract])));
export const TASK19_SCENARIO_IDS = freeze(CONTRACTS.map((contract) => contract.name));
export const task19Scenario = (id) => TASK19_SCENARIOS[id];
