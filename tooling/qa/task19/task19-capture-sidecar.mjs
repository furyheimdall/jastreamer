import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { exactFields, createJsonlDecoder, encodeLine, TASK19_SIDECAR_PROTOCOL_VERSION, Task19SidecarError } from "./task19-sidecar-protocol.mjs";
import { createTask19SidecarProxy } from "./task19-sidecar-proxy.mjs";

const hashFile = async (path) => createHash("sha256").update(await readFile(path)).digest("hex");
if (process.release.name !== "node" || process.versions.bun !== undefined) throw new Task19SidecarError("TASK19_SIDECAR_NODE_REQUIRED");
const modules = [import.meta.filename, "task19-sidecar-proxy.mjs", "task19-sidecar-protocol.mjs", "task19-frame-controller.mjs"].map((value) => value === import.meta.filename ? value : fileURLToPath(new URL(value, import.meta.url)));
let proxy; let expectedId = 1; let runId; let closing = false;
const write = (value) => process.stdout.write(encodeLine(value));
const errorValue = (error) => error instanceof Task19SidecarError ? { code: error.code, ...(error.detail === undefined ? {} : { detail: error.detail }) } : { code: "TASK19_SIDECAR_INTERNAL" };
const identity = (value) => {
  exactFields(value, ["kind", "root", "keyPath", "certificatePath", "keySha256", "certificateSha256"], ["passphrase"]);
  if (!new Set(["pem", "pfx"]).has(value.kind) || typeof value.root !== "string" || typeof value.keyPath !== "string" || typeof value.certificatePath !== "string" || (value.kind === "pfx" && typeof value.passphrase !== "string")) throw new Task19SidecarError("TASK19_SIDECAR_IDENTITY_INVALID"); return value;
};
const boundedTimeout = (value, fallback) => { const timeout = value ?? fallback; if (!Number.isSafeInteger(timeout) || timeout < 1 || timeout > 120_000) throw new Task19SidecarError("TASK19_SIDECAR_TIMEOUT_INVALID"); return timeout; };
const dispatch = async (command, payload, requestId) => {
  if (command === "initialize") { if (proxy !== undefined) throw new Task19SidecarError("TASK19_SIDECAR_ALREADY_INITIALIZED"); exactFields(payload, ["target", "upstreamFingerprint", "fingerprint", "host", "originHost", "port", "identity"]); proxy = await createTask19SidecarProxy({ ...payload, runId, identity: identity(payload.identity) }); return { ...proxy.ready, runtime: { kind: "node-sidecar", executablePath: process.execPath, version: process.version, executableSha256: await hashFile(process.execPath), modules: Object.fromEntries(await Promise.all(modules.map(async (path) => [path, await hashFile(path)]))) } }; }
  if (proxy === undefined) throw new Task19SidecarError("TASK19_SIDECAR_NOT_INITIALIZED");
  if (command === "armDrop") { exactFields(payload, ["plan"]); proxy.armDrop(payload.plan); return { armed: true }; }
  if (command === "replay") { exactFields(payload, ["plan"]); proxy.replay(payload.plan); return { replayed: true }; }
  if (command === "restore") { exactFields(payload, []); proxy.restore(); return { restored: true }; }
  if (command === "pauseEvents") { exactFields(payload, []); proxy.pauseEvents(); return { paused: true }; }
  if (command === "resumeEvents") { exactFields(payload, []); proxy.resumeEvents(false); return { resumed: true }; }
  if (command === "resumeAtResync") { exactFields(payload, []); proxy.resumeEvents(true); return { resumed: true, suppressing: true }; }
  if (command === "nextRequest") { exactFields(payload, ["method", "route"], ["stage", "timeoutMs"]); return proxy.nextRequest({ ...payload, ownerId: requestId, timeoutMs: boundedTimeout(payload.timeoutMs, 60_000) }); }
  if (command === "nextEvent") { exactFields(payload, ["resource"], ["stage", "zoneId", "timeoutMs"]); return proxy.nextEvent({ ...payload, ownerId: requestId, timeoutMs: boundedTimeout(payload.timeoutMs, 120_000) }); }
  if (command === "nextEnvelope") { exactFields(payload, [], ["stage", "type", "sequence", "pageTargetId", "timeoutMs"]); return proxy.nextEnvelope({ ...payload, ownerId: requestId, timeoutMs: boundedTimeout(payload.timeoutMs, 120_000) }); }
  if (command === "cancel") { exactFields(payload, ["id"]); proxy.cancelWaiter(payload.id); return { cancelled: true }; }
  if (command === "diagnostics") { exactFields(payload, []); return proxy.diagnostics(); }
  if (command === "rotate") { exactFields(payload, ["fingerprint", "identity"]); return { fingerprint: await proxy.rotate({ fingerprint: payload.fingerprint, identity: identity(payload.identity) }) }; }
  if (command === "reset") { exactFields(payload, []); proxy.reset(); return { reset: true }; }
  if (command === "close") { exactFields(payload, []); await proxy.close(); closing = true; return { closed: true }; }
  throw new Task19SidecarError("TASK19_SIDECAR_COMMAND_UNKNOWN", command);
};
const handle = async (message) => {
  let id = 0;
  try {
    exactFields(message, ["version", "runId", "id", "command", "payload"]); id = message.id;
    if (message.version !== TASK19_SIDECAR_PROTOCOL_VERSION) throw new Task19SidecarError("TASK19_SIDECAR_VERSION_MISMATCH");
    if (typeof message.runId !== "string" || message.runId === "" || (runId !== undefined && message.runId !== runId)) throw new Task19SidecarError("TASK19_SIDECAR_RUN_MISMATCH");
    if (!Number.isSafeInteger(id) || id !== expectedId) throw new Task19SidecarError(id < expectedId ? "TASK19_SIDECAR_REQUEST_REPLAY" : "TASK19_SIDECAR_REQUEST_OUT_OF_ORDER"); expectedId += 1; runId ??= message.runId;
    const result = await dispatch(message.command, message.payload, id); write({ version: TASK19_SIDECAR_PROTOCOL_VERSION, runId, id, ok: true, result }); if (closing) process.exitCode = 0;
  } catch (error) { write({ version: TASK19_SIDECAR_PROTOCOL_VERSION, runId: runId ?? "uninitialized", id, ok: false, error: errorValue(error) }); }
};
const fatal = async (error) => { if (closing) return; closing = true; try { await proxy?.close(); } catch (closeError) { if (!(closeError instanceof Error)) process.stderr.write("sidecar close failed\n"); } process.stderr.write(`${error instanceof Task19SidecarError ? error.code : "TASK19_SIDECAR_PROTOCOL_FAILURE"}\n`); process.exitCode = 1; process.stdin.destroy(); };
process.stdin.on("data", createJsonlDecoder((message) => { void handle(message); }, (error) => { void fatal(error); }));
process.stdin.once("end", () => { if (!closing) void fatal(new Task19SidecarError(proxy === undefined ? "TASK19_SIDECAR_PARTIAL_INITIALIZATION" : "TASK19_SIDECAR_PARENT_CLOSED")); });
process.once("SIGTERM", () => { void fatal(new Task19SidecarError("TASK19_SIDECAR_PARENT_ABORTED")); });
process.once("SIGINT", () => { void fatal(new Task19SidecarError("TASK19_SIDECAR_PARENT_ABORTED")); });
