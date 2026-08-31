const UINT64_MAX = 18_446_744_073_709_551_615n;
const forbiddenExactSpecSymbols = Object.freeze(["replayEvent", "dropNextEvent", "injectEvent"]);
const forbiddenExactSpecActions = Object.freeze([/\.fill\s*\(/, /\.click\s*\(/]);

const exactEpochLexeme = (value) => typeof value === "string" && /^(0|[1-9][0-9]*)$/.test(value) && BigInt(value) <= UINT64_MAX;

export const createTask19TrackPlan = ({ runId, runtime, event, relation = "exact" }) => {
  if (runtime !== "node-sidecar" || typeof runId !== "string" || runId === "") throw new Error("TASK19_TRACK_PROVENANCE_INVALID"); const frame = event?.observedFrame;
  if (!["exact", "next"].includes(relation) || event === null || typeof event !== "object" || frame === null || typeof frame !== "object" || !exactEpochLexeme(frame.epoch) || !exactEpochLexeme(frame.sequence) || frame.epoch !== event.server_epoch || Number(frame.sequence) !== event.sequence || typeof frame.frameId !== "string" || !/^frame:[0-9a-f]{64}$/.test(frame.frameId) || !/^[0-9a-f]{64}$/.test(frame.frameHash) || !/^[0-9a-f]{64}$/.test(frame.payloadHash) || frame.kind !== event.type || frame.resource !== (typeof event.resource === "string" ? event.resource : null) || frame.zoneId !== (typeof event.zone_id === "string" ? event.zone_id : null) || frame.revision !== (Number.isSafeInteger(event.revision) ? String(event.revision) : null)) throw new Error("TASK19_TRACK_EVENT_INVALID");
  const control = Object.freeze({ runId, frameId: frame.frameId, epoch: frame.epoch, sequence: frame.sequence, kind: frame.kind, resource: frame.resource, zoneId: frame.zoneId, revision: frame.revision, frameHash: frame.frameHash, payloadHash: frame.payloadHash, ...(relation === "next" ? { relation } : {}) }); return Object.freeze({ control, provenance: Object.freeze({ kind: "node-sidecar-observed-frame", ...control }) });
};

export const createTask19ZeroRequestWindow = ({ pageStart, sidecarStart, recoveryRoutes }) => {
  if (!Number.isSafeInteger(pageStart) || pageStart < 0 || !Number.isSafeInteger(sidecarStart) || sidecarStart < 0 || !Array.isArray(recoveryRoutes) || recoveryRoutes.some((route) => typeof route !== "string" || !route.startsWith("/"))) throw new Error("TASK19_ZERO_REQUEST_WINDOW_INVALID");
  const recovery = new Set(recoveryRoutes);
  return Object.freeze({ observe: ({ pageRequests, sidecarTransport }) => {
    const page = pageRequests.slice(pageStart).map((request) => Object.freeze({ source: "page", method: request.method, route: request.route, pageTargetId: request.pageTargetId, frameIdentity: request.frameIdentity, recoveryRoute: recovery.has(request.route) }));
    const sidecarTest = sidecarTransport.slice(sidecarStart).filter(({ kind }) => kind === "http").map(({ request }) => { const route = new URL(request.route, "https://task19.invalid").pathname; return Object.freeze({ source: "sidecar-test", method: request.method, route, pageTargetId: null, frameIdentity: null, recoveryRoute: recovery.has(route) }); });
    const classified = Object.freeze([...page, ...sidecarTest]); if (classified.length !== 0) { const error = new Error("TASK19_ZERO_REQUEST_WINDOW_CONTAMINATED"); error.classified = classified; throw error; } return classified;
  } });
};

export const createTask19PageRequestRecord = ({ request, page, targetId, observedAt }) => {
  if (typeof targetId !== "string" || targetId === "" || !Number.isSafeInteger(observedAt) || observedAt < 0) throw new Error("TASK19_PAGE_TARGET_INVALID");
  return { method: request.method(), route: new URL(request.url()).pathname, body: request.postData(), startedAt: observedAt, endedAt: null, status: null, headers: null, pageTargetId: targetId, frameIdentity: request.frame() === page.mainFrame() ? "main" : "other" };
};

export const createTask19DurableRestartTrack = ({ subscribeSnapshot, subscribeRecovery, restart }) => {
  if (![subscribeSnapshot, subscribeRecovery, restart].every((value) => typeof value === "function")) throw new Error("TASK19_RESTART_CALLBACK_INVALID");
  return Object.freeze({ subscribeSnapshot, subscribeRecovery, restart });
};

export const assertTask19ExactSpecSource = (source) => {
  if (typeof source !== "string") throw new Error("TASK19_EXACT_SPEC_SOURCE_INVALID");
  for (const symbol of forbiddenExactSpecSymbols) if (source.includes(symbol)) throw new Error(`TASK19_SYNTHETIC_ALIAS_REJECTED:${symbol}`);
  for (const action of forbiddenExactSpecActions) if (action.test(source)) throw new Error("TASK19_FORBIDDEN_UI_ACTION_REJECTED");
};
