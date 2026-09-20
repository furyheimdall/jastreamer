#!/usr/bin/env node
// Test-only transport faults around the real Server; never packaged or deployed.
import http from "node:http";

const upstreamPort = Number(process.argv[2]);
const listenPort = Number(process.argv[3] ?? 18080);
if (![upstreamPort, listenPort].every((port) => Number.isInteger(port) && port > 0 && port <= 65535)) {
  throw new Error("Expected upstream and listening loopback ports");
}
let fault = null;
const timers = new Set();
const sockets = new Set();

function target(request) {
  return { hostname: "127.0.0.1", port: upstreamPort, method: request.method, path: request.url, headers: request.headers };
}

function forward(request, response) {
  if (response.destroyed) return;
  const upstream = http.request(target(request), (result) => {
    response.writeHead(result.statusCode, result.headers);
    result.on("error", () => response.destroy());
    result.pipe(response);
  });
  upstream.on("error", () => {
    if (!response.headersSent) response.writeHead(502);
    response.end();
  });
  request.on("aborted", () => upstream.destroy());
  response.on("close", () => upstream.destroy());
  request.pipe(upstream);
}

async function configure(request, response, pathname) {
  const expectedHost = `127.0.0.1:${listenPort}`;
  if (request.method !== "POST" || request.headers.host !== expectedHost ||
      (request.headers.origin && request.headers.origin !== `http://${expectedHost}`)) {
    response.writeHead(403).end();
    return;
  }
  if (pathname === "/__android_smoke/fault/reset") {
    fault = null;
    response.writeHead(204).end();
    return;
  }
  if (request.headers["content-type"]?.split(";", 1)[0] !== "application/json") {
    response.writeHead(415).end();
    return;
  }
  try {
    const chunks = [];
    let size = 0;
    for await (const chunk of request) {
      size += chunk.length;
      if (size > 4096) throw new Error("Fault request exceeds limit");
      chunks.push(chunk);
    }
    const candidate = JSON.parse(Buffer.concat(chunks).toString("utf8"));
    if (typeof candidate.path !== "string" || !candidate.path.startsWith("/") ||
        candidate.path.startsWith("/__android_smoke/") || /[?#\r\n\0]/.test(candidate.path) ||
        !Number.isInteger(candidate.count) || candidate.count < 1 || candidate.count > 8 ||
        !["disconnect", "delay"].includes(candidate.mode) ||
        (candidate.prefix !== undefined && typeof candidate.prefix !== "boolean") ||
        (candidate.mode === "delay" && (!Number.isInteger(candidate.delay_ms) || candidate.delay_ms < 1 || candidate.delay_ms > 15000))) {
      throw new Error("Invalid fault request");
    }
    fault = candidate;
    response.writeHead(204).end();
  } catch {
    if (!response.destroyed) response.writeHead(400).end();
  }
}

const server = http.createServer((request, response) => {
  const pathname = new URL(request.url, `http://127.0.0.1:${listenPort}`).pathname;
  if (pathname === "/__android_smoke/fault" || pathname === "/__android_smoke/fault/reset") {
    void configure(request, response, pathname);
    return;
  }
  if (fault && (fault.prefix ? pathname.startsWith(fault.path) : pathname === fault.path)) {
    const active = fault;
    if (--active.count === 0) fault = null;
    if (active.mode === "disconnect") {
      request.socket.destroy();
      return;
    }
    const timer = setTimeout(() => {
      timers.delete(timer);
      forward(request, response);
    }, active.delay_ms);
    timers.add(timer);
    response.on("close", () => { clearTimeout(timer); timers.delete(timer); });
    return;
  }
  forward(request, response);
});

server.on("upgrade", (request, socket, head) => {
  const upstream = http.request(target(request));
  upstream.on("upgrade", (response, peer, upstreamHead) => {
    socket.write(`HTTP/1.1 ${response.statusCode} ${response.statusMessage}\r\n`);
    for (let index = 0; index < response.rawHeaders.length; index += 2) {
      socket.write(`${response.rawHeaders[index]}: ${response.rawHeaders[index + 1]}\r\n`);
    }
    socket.write("\r\n");
    if (upstreamHead.length) socket.write(upstreamHead);
    if (head.length) peer.write(head);
    socket.on("error", () => peer.destroy());
    peer.on("error", () => socket.destroy());
    socket.on("close", () => peer.destroy());
    peer.on("close", () => socket.destroy());
    socket.pipe(peer).pipe(socket);
  });
  upstream.on("response", () => { upstream.destroy(); socket.destroy(); });
  upstream.on("error", () => socket.destroy());
  socket.on("close", () => upstream.destroy());
  upstream.end();
});
server.on("connection", (socket) => {
  sockets.add(socket);
  socket.on("close", () => sockets.delete(socket));
});
function stop() {
  for (const timer of timers) clearTimeout(timer);
  for (const socket of sockets) socket.destroy();
  server.close();
}
process.once("SIGTERM", stop);
process.once("SIGINT", stop);
server.listen(listenPort, "127.0.0.1", () => console.log(`Android smoke proxy ready on 127.0.0.1:${listenPort}`));
