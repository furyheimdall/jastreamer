#!/usr/bin/env bun
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

export const stablePeerEndpoint = (port, scheme = "https") => {
  if (!Number.isInteger(port) || port < 1 || port > 65_535 || !["http", "https"].includes(scheme)) throw new Error("STABLE_PEER_ENDPOINT_INVALID");
  return Object.freeze({ hostname: "127.0.0.1", port, origin: `${scheme}://127.0.0.1:${port}` });
};

const invoked = process.argv[1] === undefined ? undefined : pathToFileURL(process.argv[1]).href;
if (invoked === import.meta.url) {
const args = new Map();
for (let index = 2; index < process.argv.length; index += 2) args.set(process.argv[index]?.slice(2), process.argv[index + 1]);
const config = JSON.parse(await readFile(args.get("config"), "utf8"));
const observations = [];
const waiters = [];
let socket;
let activeToken = config.token;
let sequence = 1;
let epoch = "qa-epoch-1";

const emit = (event) => process.stdout.write(`${JSON.stringify(event)}\n`);
const observe = (frame) => {
  const index = waiters.findIndex((waiter) => waiter.predicate(frame));
  if (index < 0) observations.push(frame);
  else { const [waiter] = waiters.splice(index, 1); waiter.resolve(frame); }
};
const nextFrame = (predicate) => new Promise((resolve, reject) => {
  const existing = observations.findIndex(predicate);
  if (existing >= 0) { resolve(observations.splice(existing, 1)[0]); return; }
  const waiter = { predicate, resolve };
  waiters.push(waiter);
  const signal = AbortSignal.timeout(30_000);
  signal.addEventListener("abort", () => {
    const index = waiters.indexOf(waiter); if (index >= 0) waiters.splice(index, 1);
    reject(new Error("PEER_FRAME_TIMEOUT"));
  }, { once: true });
});
const send = (frame) => {
  if (socket === undefined) throw new Error("RENDERER_NOT_CONNECTED");
  socket.send(JSON.stringify(frame));
};
const command = (kind, values = {}) => ({
  protocolMajor: 3, type: "command", commandId: values.commandId ?? `qa-${sequence}`,
  sequence: values.sequence ?? sequence++, sessionEpoch: epoch, zoneId: "qa-zone",
  playId: values.playId ?? null, kind, deadline: new Date(Date.now() + 30_000).toISOString(),
  positionMs: values.positionMs, media: values.media,
});
const execute = async (frame) => {
  const ack = nextFrame((value) => value.type === "command.ack" && value.commandId === frame.commandId);
  const result = nextFrame((value) => value.type === "command.result" && value.commandId === frame.commandId);
  send(frame);
  const [acknowledgement, completion] = await Promise.all([ack, result]);
  if (!(["received", "duplicate"].includes(acknowledgement.status)) || completion.status !== "applied") throw new Error("COMMAND_FAILED");
  send({ protocolMajor: 3, type: "result.ack", resultId: completion.resultId });
  return { acknowledgement, completion };
};
const media = (name, mimeType) => ({
  url: `${origin}/media/${name}`, mimeType, headers: {}, seekable: true,
});
const disconnectScenario = async (cut) => {
  for (let index = observations.length - 1; index >= 0; index -= 1) {
    if (observations[index]?.type === "hello") observations.splice(index, 1);
  }
  const frame = command("stop", { commandId: `cut-${cut}` });
  const acknowledgement = nextFrame((value) => value.type === "command.ack" && value.commandId === frame.commandId);
  const completion = nextFrame((value) => value.type === "command.result" && value.commandId === frame.commandId);
  const reconnect = nextFrame((value) => value.type === "hello");
  send(frame);
  switch (cut) {
    case "before-ack": break;
    case "after-ack": case "before-result": await acknowledgement; break;
    case "after-result": await completion; break;
    default: throw new Error(`CUT_UNKNOWN:${cut}`);
  }
  socket.close();
  await reconnect;
  if (cut === "before-ack") send(frame);
  const ack = await acknowledgement; const result = await completion;
  if (!["received", "duplicate"].includes(ack.status)) throw new Error("CUT_ACK_FAILED");
  send({ protocolMajor: 3, type: "result.ack", resultId: result.resultId });
  emit({ event: `scenario:disconnect-${cut}`, scenario: `disconnect-${cut}`, result: "passed" });
};

const endpoint = stablePeerEndpoint(config.port);
const server = Bun.serve({
  port: endpoint.port,
  hostname: endpoint.hostname,
  tls: { cert: Bun.file(config.certificate), key: Bun.file(config.private_key) },
  async fetch(request, server_) {
    const url = new URL(request.url);
    if (url.pathname === "/api/v1/renderers/qa-renderer/session") {
      if (request.headers.get("authorization") !== `Bearer ${activeToken}`) return new Response("unauthorized", { status: 401 });
      if (!server_.upgrade(request, { headers: { "Sec-WebSocket-Protocol": "jastreamer.renderer.v3" } })) return new Response("upgrade failed", { status: 400 });
      return undefined;
    }
    if (url.pathname.startsWith("/media/")) {
      const name = url.pathname.slice(7);
      const path = `${config.media_directory}/${name}`;
      const bytes = await readFile(path);
      const range = request.headers.get("range")?.match(/^bytes=(\d+)-(\d+)$/);
      if (name === "corrupted.bin") return new Response(bytes, { status: 206, headers: { "Content-Range": `bytes 0-${bytes.length - 1}/${bytes.length}` } });
      if (name === "truncated.mp3") return new Response(bytes.subarray(0, Math.floor(bytes.length / 2)), { status: 206, headers: { "Content-Range": `bytes 0-${bytes.length - 1}/${bytes.length}` } });
      const start = range === undefined ? 0 : Number(range[1]);
      const end = Math.min(range === undefined ? bytes.length - 1 : Number(range[2]), bytes.length - 1);
      return new Response(bytes.subarray(start, end + 1), { status: range === undefined ? 200 : 206, headers: {
        "Content-Length": String(end - start + 1), "Content-Range": `bytes ${start}-${end}/${bytes.length}`,
      } });
    }
    return new Response("not found", { status: 404 });
  },
  websocket: {
    open(ws) { socket = ws; },
    message(_ws, data) {
      const frame = JSON.parse(typeof data === "string" ? data : new TextDecoder().decode(data));
      observe(frame);
      if (frame.type === "hello") {
        emit({ event: "renderer.hello" });
        send({ protocolMajor: 3, type: "welcome", selectedMajor: 3, sessionEpoch: epoch, nextSequence: sequence, capabilities: ["render"] });
      }
    },
    close() { socket = undefined; },
  },
});
const origin = endpoint.origin;
if (server.port !== endpoint.port) throw new Error("STABLE_PEER_PORT_MISMATCH");
emit({ event: "peer.ready", origin, fingerprint: config.fingerprint });

const inputLines = async function* () {
  const reader = Bun.stdin.stream().pipeThrough(new TextDecoderStream()).getReader();
  let pending = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    pending += value;
    let newline = pending.indexOf("\n");
    while (newline >= 0) {
      const line = pending.slice(0, newline); pending = pending.slice(newline + 1);
      if (line !== "") yield line;
      newline = pending.indexOf("\n");
    }
  }
  if (pending !== "") throw new Error("PEER_ACTION_TRUNCATED");
};

const formatMime = { flac: "audio/flac", mp3: "audio/mpeg", vorbis: "audio/vorbis", opus: "audio/opus", wav: "audio/wav" };
for await (const line of inputLines()) {
  const action = JSON.parse(line);
  switch (action.action) {
    case "format": await execute(command("play", { playId: `format-${action.format}`, media: media(`tone_1khz.${action.format}`, formatMime[action.format]) })); break;
    case "play": await execute(command("play", { playId: "transport", media: media("seek_tones.wav", "audio/wav") })); emit({ event: "control:play", action: action.action }); break;
    case "pause": await execute(command("pause", { playId: "transport" })); emit({ event: "control:pause", action: action.action }); break;
    case "resume": await execute(command("resume", { playId: "transport" })); emit({ event: "control:resume", action: action.action }); break;
    case "seek": await execute(command("seek", { playId: "transport", positionMs: action.position_ms })); break;
    case "stop": await execute(command("stop", { playId: "transport" })); emit({ event: "control:stop", action: action.action }); break;
    case "transport-complete": emit({ event: "scenario:play-pause-resume-seek-stop", scenario: "play-pause-resume-seek-stop", result: "passed" }); break;
    case "duplicate-conflict": {
      const original = command("stop", { commandId: "duplicate" }); await execute(original);
      const duplicate = await execute(original); if (duplicate.acknowledgement.status !== "duplicate") throw new Error("DUPLICATE_FAILED");
      const conflictAck = nextFrame((value) => value.type === "command.ack" && value.commandId === "duplicate");
      send({ ...original, kind: "pause" }); if ((await conflictAck).error?.code !== "COMMAND_ID_CONFLICT") throw new Error("CONFLICT_FAILED");
      emit({ event: "scenario:duplicate-conflict", scenario: "duplicate-conflict", result: "passed" }); break;
    }
    case "corrupted-media": {
      const frame = command("play", { playId: action.action, media: media("corrupted.bin", "audio/mpeg") });
      const result = nextFrame((value) => value.type === "command.result" && value.commandId === frame.commandId); send(frame);
      if ((await result).status === "applied") throw new Error("CORRUPTED_MEDIA_ACCEPTED");
      emit({ event: `scenario:${action.action}`, scenario: action.action, result: "passed" }); break;
    }
    case "truncated-media": {
      await execute(command("play", { playId: action.action, media: media("truncated.mp3", "audio/mpeg") }));
      const failure = await nextFrame((value) => value.type === "playback.event" && value.playId === action.action && value.kind === "failed");
      if (failure.kind !== "failed") throw new Error("TRUNCATION_NOT_OBSERVED");
      emit({ event: `scenario:${action.action}`, scenario: action.action, result: "passed" }); break;
    }
    case "expect-output-unavailable": {
      const frame = command("play", { playId: "unavailable", media: media("tone_1khz.wav", "audio/wav") });
      const result = nextFrame((value) => value.type === "command.result" && value.commandId === frame.commandId); send(frame);
      if ((await result).status === "applied") throw new Error("OUTPUT_UNAVAILABLE_NOT_OBSERVED");
      emit({ event: "control:expect-output-unavailable", action: action.action }); break;
    }
    case "endpoint-invalidation-armed":
      await execute(command("play", { playId: "invalidation", media: media("seek_tones.wav", "audio/wav") }));
      emit({ event: "control:endpoint-invalidation-armed", action: action.action }); break;
    case "endpoint-restored": {
      const invalidated = await nextFrame((value) => value.type === "playback.event" && value.playId === "invalidation" && value.kind === "failed");
      if (invalidated.kind !== "failed") throw new Error("INVALIDATION_NOT_OBSERVED");
      await execute(command("play", { playId: "restored", media: media("tone_1khz.wav", "audio/wav") }));
      emit({ event: "control:endpoint-restored", action: action.action }); break;
    }
    case "revocation": activeToken = "revoked"; send({ protocolMajor: 3, type: "error", code: "TOKEN_REVOKED", message: "revoked", retryable: false }); emit({ event: "control:revocation", action: action.action }); break;
    case "rotate-token": activeToken = action.token; emit({ event: "control:rotate-token", action: action.action }); break;
    case "disconnect-before-ack": await disconnectScenario("before-ack"); break;
    case "disconnect-after-ack": await disconnectScenario("after-ack"); break;
    case "disconnect-before-result": await disconnectScenario("before-result"); break;
    case "disconnect-after-result": await disconnectScenario("after-result"); break;
    default: emit({ event: `control:${action.action}`, action: action.action });
  }
}
}
