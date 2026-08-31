import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { relative } from "node:path";
import { fileURLToPath } from "node:url";
import { exactFields, createJsonlDecoder, encodeLine, TASK19_SIDECAR_PROTOCOL_VERSION, Task19SidecarError } from "./task19-sidecar-protocol.mjs";

const SIDECAR = fileURLToPath(new URL("task19-capture-sidecar.mjs", import.meta.url));
const STARTUP_TIMEOUT_MS = 10_000; const COMMAND_TIMEOUT_MS = 120_000; const CLOSE_TIMEOUT_MS = 5_000; const MAX_STDERR_BYTES = 65_536;
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const environment = () => Object.fromEntries(["PATH", "Path", "SystemRoot", "WINDIR", "TEMP", "TMP", "ComSpec", "PATHEXT"].flatMap((key) => process.env[key] === undefined ? [] : [[key, process.env[key]]]));
const identityMessage = (identity) => {
  if (identity?.kind !== "task19-run-ephemeral-tls" || typeof identity.root !== "string" || typeof identity.keyPath !== "string" || typeof identity.certificatePath !== "string") throw new Task19SidecarError("TASK19_EPHEMERAL_TLS_IDENTITY_REQUIRED");
  const keyBytes = identity.pfx ?? identity.key; const certificateBytes = identity.certificate; if (!Buffer.isBuffer(keyBytes) || !Buffer.isBuffer(certificateBytes)) throw new Task19SidecarError("TASK19_SIDECAR_IDENTITY_BYTES_REQUIRED");
  const keyPath = relative(identity.root, identity.keyPath); const certificatePath = relative(identity.root, identity.certificatePath);
  return { kind: identity.pfx === undefined ? "pem" : "pfx", root: identity.root, keyPath, certificatePath, keySha256: sha256(keyBytes), certificateSha256: sha256(certificateBytes), ...(identity.pfx === undefined ? {} : { passphrase: identity.passphrase }) };
};
const immutable = (value) => { if (value !== null && typeof value === "object") { for (const child of Object.values(value)) immutable(child); Object.freeze(value); } return value; };
const errorFrom = (value) => { exactFields(value, ["code"], ["detail"]); return new Task19SidecarError(value.code, value.detail === undefined ? undefined : immutable(structuredClone(value.detail))); };

export const createTask19CaptureProxy = async ({ target, upstreamFingerprint = null, identity, runId = identity?.certificateSha256, host = "127.0.0.1", originHost = "localhost", port = 0, nodeExecutable = "node", nodeExecutableSha256, sidecarSha256, spawnProcess = spawn }) => {
  if (typeof runId !== "string" || runId === "" || typeof nodeExecutable !== "string" || nodeExecutable === "") throw new Task19SidecarError("TASK19_SIDECAR_CONFIGURATION_INVALID");
  const targetUrl = new URL(target); if (targetUrl.protocol === "https:" && (typeof upstreamFingerprint !== "string" || !/^[0-9a-f]{64}$/.test(upstreamFingerprint))) throw new Task19SidecarError("TASK19_UPSTREAM_FINGERPRINT_REQUIRED");
  const child = spawnProcess(nodeExecutable, [SIDECAR], { env: environment(), stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
  const pending = new Map(); let nextId = 1; let exited = false; let closed = false; let stderrBytes = 0; let exitState;
  const failAll = (error) => { for (const request of pending.values()) { clearTimeout(request.timer); request.signal?.removeEventListener("abort", request.abort); if (!request.abandoned) request.reject(error); } pending.clear(); };
  const terminate = () => { if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL"); };
  child.stderr.on("data", (bytes) => { stderrBytes += bytes.length; if (stderrBytes > MAX_STDERR_BYTES) { const error = new Task19SidecarError("TASK19_SIDECAR_STDERR_OVERSIZED"); failAll(error); terminate(); } });
  child.once("error", (error) => { exited = true; exitState = { kind: "spawn-error", code: error.code ?? "UNKNOWN" }; failAll(new Task19SidecarError("TASK19_SIDECAR_SPAWN_FAILED")); });
  child.once("exit", (code, signal) => { exited = true; exitState = { kind: "exit", code, signal }; if (!closed) failAll(new Task19SidecarError("TASK19_SIDECAR_PREMATURE_EXIT")); });
  const protocolFailure = (error) => { failAll(error instanceof Task19SidecarError ? error : new Task19SidecarError("TASK19_SIDECAR_PROTOCOL_CORRUPT")); terminate(); };
  child.stdout.on("data", createJsonlDecoder((message) => {
    try {
      exactFields(message, ["version", "runId", "id", "ok"], ["result", "error"]); if (message.version !== TASK19_SIDECAR_PROTOCOL_VERSION || message.runId !== runId || !Number.isSafeInteger(message.id)) throw new Task19SidecarError("TASK19_SIDECAR_RESPONSE_INVALID");
      const request = pending.get(message.id); if (request === undefined) throw new Task19SidecarError("TASK19_SIDECAR_RESPONSE_UNCORRELATED"); pending.delete(message.id); clearTimeout(request.timer); request.signal?.removeEventListener("abort", request.abort); if (request.abandoned) return;
      if (message.ok === true && Object.hasOwn(message, "result") && !Object.hasOwn(message, "error")) request.resolve(message.result); else if (message.ok === false && Object.hasOwn(message, "error") && !Object.hasOwn(message, "result")) request.reject(errorFrom(message.error)); else throw new Task19SidecarError("TASK19_SIDECAR_RESPONSE_INVALID");
    } catch (error) { protocolFailure(error); }
  }, protocolFailure));
  const command = (name, payload, timeoutMs = COMMAND_TIMEOUT_MS, signal) => new Promise((resolveResult, reject) => {
    if (closed || exited || signal?.aborted) return reject(new Task19SidecarError(closed ? "TASK19_CAPTURE_CLOSED" : exited ? "TASK19_SIDECAR_PREMATURE_EXIT" : "TASK19_CAPTURE_ABORTED")); const id = nextId++;
    const request = { resolve: resolveResult, reject, timer: undefined, signal, abort: undefined, abandoned: false };
    request.timer = setTimeout(() => { pending.delete(id); reject(new Task19SidecarError("TASK19_SIDECAR_COMMAND_TIMEOUT")); terminate(); }, timeoutMs);
    request.abort = () => { if (request.abandoned) return; request.abandoned = true; reject(new Task19SidecarError("TASK19_CAPTURE_ABORTED")); void command("cancel", { id }, CLOSE_TIMEOUT_MS).catch(() => terminate()); };
    pending.set(id, request); try { child.stdin.write(encodeLine({ version: TASK19_SIDECAR_PROTOCOL_VERSION, runId, id, command: name, payload })); if (signal !== undefined) { signal.addEventListener("abort", request.abort, { once: true }); if (signal.aborted) request.abort(); } } catch (error) { pending.delete(id); clearTimeout(request.timer); reject(error); }
  });
  let ready;
  try { ready = await command("initialize", { target, upstreamFingerprint, fingerprint: identity.certificateSha256, host, originHost, port, identity: identityMessage(identity) }, STARTUP_TIMEOUT_MS); }
  catch (error) { terminate(); throw error; }
  exactFields(ready, ["url", "host", "port", "fingerprint", "runtime"]); exactFields(ready.runtime, ["kind", "executablePath", "version", "executableSha256", "modules"]);
  if (ready.runtime.kind !== "node-sidecar" || typeof ready.runtime.version !== "string" || !ready.runtime.version.startsWith("v") || (nodeExecutableSha256 !== undefined && ready.runtime.executableSha256 !== nodeExecutableSha256) || (sidecarSha256 !== undefined && !Object.values(ready.runtime.modules).includes(sidecarSha256))) { terminate(); throw new Task19SidecarError("TASK19_SIDECAR_RUNTIME_TRUST_MISMATCH"); }
  const close = async () => { if (closed) return; let closeError; try { await command("close", {}, CLOSE_TIMEOUT_MS); } catch (error) { closeError = error; } closed = true; child.stdin.end(); if (!exited) await new Promise((resolveExit) => { const timer = setTimeout(() => { terminate(); resolveExit(); }, CLOSE_TIMEOUT_MS); child.once("exit", () => { clearTimeout(timer); resolveExit(); }); }); failAll(new Task19SidecarError("TASK19_CAPTURE_CLOSED")); if (closeError !== undefined) throw closeError; };
  return {
    ...ready, frameRunId: runId, runtime: Object.freeze(structuredClone(ready.runtime)), get closed() { return closed; }, get exitState() { return exitState === undefined ? undefined : structuredClone(exitState); },
    armFrameDrop: (plan) => command("armDrop", { plan }), replayFrame: (plan) => command("replay", { plan }), restoreEvents: () => command("restore", {}), pauseEvents: () => command("pauseEvents", {}), resumeEvents: () => command("resumeEvents", {}), resumeAtResync: () => command("resumeAtResync", {}),
    next: ({ method, route, stage, timeoutMs = 60_000, signal }) => command("nextRequest", { method, route, ...(stage === undefined ? {} : { stage }), timeoutMs }, timeoutMs + 1_000, signal),
    nextEvent: (resource, timeoutMs = 120_000, options = {}) => command("nextEvent", { resource, ...(options.stage === undefined ? {} : { stage: options.stage }), ...(options.zoneId === undefined ? {} : { zoneId: options.zoneId }), timeoutMs }, timeoutMs + 1_000, options.signal),
    nextEnvelope: ({ stage, type, sequence, pageTargetId, timeoutMs = 120_000, signal } = {}) => command("nextEnvelope", { ...(stage === undefined ? {} : { stage }), ...(type === undefined ? {} : { type }), ...(sequence === undefined ? {} : { sequence }), ...(pageTargetId === undefined ? {} : { pageTargetId }), timeoutMs }, timeoutMs + 1_000, signal),
    diagnostics: () => command("diagnostics", {}), rotate: async (nextIdentity) => (await command("rotate", { fingerprint: nextIdentity.certificateSha256, identity: identityMessage(nextIdentity) })).fingerprint, reset: () => command("reset", {}), close,
  };
};

const serverFrame = (buffer) => { if (buffer.length < 2) return undefined; const encoded = buffer[1] & 0x7f; const offset = encoded === 126 ? 4 : encoded === 127 ? 10 : 2; if (buffer.length < offset) return undefined; const size = encoded === 126 ? buffer.readUInt16BE(2) : encoded === 127 ? Number(buffer.readBigUInt64BE(2)) : encoded; if (!Number.isSafeInteger(size) || buffer.length < offset + size) return undefined; return { bytes: buffer.subarray(0, offset + size), payload: buffer.subarray(offset, offset + size), consumed: offset + size, opcode: buffer[0] & 0x0f }; };
export const createTask19ServerFrameDecoder = (consume) => { let buffered = Buffer.alloc(0); return (chunk) => { buffered = Buffer.concat([buffered, chunk]); for (;;) { const frame = serverFrame(buffered); if (frame === undefined) return; buffered = buffered.subarray(frame.consumed); consume(frame); } }; };
