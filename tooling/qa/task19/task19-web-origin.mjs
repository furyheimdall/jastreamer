import { createHash, X509Certificate } from "node:crypto";
import { createServer } from "node:https";
import { isIP } from "node:net";
import { connect } from "node:tls";
import { readFile, stat } from "node:fs/promises";
import { extname, relative, resolve, sep } from "node:path";

const MIME = Object.freeze({ ".bin": "application/octet-stream", ".css": "text/css; charset=utf-8", ".html": "text/html; charset=utf-8", ".js": "text/javascript; charset=utf-8", ".json": "application/json; charset=utf-8", ".otf": "font/otf", ".ttf": "font/ttf", ".wasm": "application/wasm", ".woff": "font/woff", ".woff2": "font/woff2" });
const emptyConnectPlan = Object.freeze([]);
const normalizedConnectOrigins = (connectOrigins) => {
  if (!Array.isArray(connectOrigins) || !Object.isFrozen(connectOrigins)) throw new Error("TASK19_WEB_CONNECT_PLAN_MUTABLE");
  const normalized = connectOrigins.map((value) => { let parsed; try { parsed = new URL(value); } catch { throw new Error("TASK19_WEB_CONNECT_ORIGIN_INVALID"); } if ((parsed.protocol !== "https:" && parsed.protocol !== "wss:") || parsed.origin !== value || parsed.username !== "" || parsed.password !== "" || parsed.hostname.includes("*")) throw new Error("TASK19_WEB_CONNECT_ORIGIN_INVALID"); return parsed.origin; });
  if (new Set(normalized).size !== normalized.length) throw new Error("TASK19_WEB_CONNECT_ORIGIN_INVALID"); return normalized;
};
export const createTask19ContentSecurityPolicy = (connectOrigins = emptyConnectPlan) => {
  const bound = normalizedConnectOrigins(connectOrigins); return ["default-src 'none'", "script-src 'self' 'wasm-unsafe-eval'", `connect-src 'self'${bound.length === 0 ? "" : ` ${bound.join(" ")}`}`, "img-src 'self' data:", "style-src 'self' 'unsafe-inline'", "font-src 'self'", "worker-src 'self'", "manifest-src 'self'", "object-src 'none'", "frame-src 'none'", "base-uri 'self'", "form-action 'none'", "frame-ancestors 'none'"].join("; ");
};
export const assertTask19ContentSecurityPolicy = (observed, connectOrigins = emptyConnectPlan) => { if (observed !== createTask19ContentSecurityPolicy(connectOrigins)) throw new Error("TASK19_WEB_CSP_DRIFT"); };
export const assertTask19ConnectBinding = (destination, connectOrigins) => { const bound = normalizedConnectOrigins(connectOrigins); let parsed; try { parsed = new URL(destination); } catch { throw new Error("TASK19_WEB_CONNECT_DESTINATION_UNBOUND"); } if (!bound.includes(parsed.origin)) throw new Error("TASK19_WEB_CONNECT_DESTINATION_UNBOUND"); };
const inside = (root, requested) => {
  const path = resolve(root, requested === "/" ? "index.html" : `.${decodeURIComponent(requested)}`); const local = relative(root, path);
  return local !== "" && local !== ".." && !local.startsWith(`..${sep}`) ? path : undefined;
};

export const assertTask19OriginBinding = (observed, expected) => {
  if (observed?.host !== expected?.host || observed?.port !== expected?.port || observed?.certificateSha256 !== expected?.certificateSha256 || observed?.spkiSha256 !== expected?.spkiSha256) throw new Error("TASK19_WEB_ORIGIN_BINDING_MISMATCH");
};
export const observeTask19OriginBinding = (url, expected) => new Promise((resolveObservation, reject) => {
  const parsed = new URL(url); if (parsed.protocol !== "https:") { reject(new Error("TASK19_WEB_ORIGIN_BINDING_MISMATCH")); return; } const port = Number(parsed.port); const socket = connect({ host: parsed.hostname, port, ...(isIP(parsed.hostname) ? {} : { servername: parsed.hostname }), rejectUnauthorized: false });
  socket.once("secureConnect", () => { const peer = socket.getPeerCertificate(true); socket.end(); if (peer.raw === undefined || peer.raw.byteLength === 0) { reject(new Error("TASK19_WEB_ORIGIN_BINDING_MISMATCH")); return; } const certificate = new X509Certificate(Buffer.from(peer.raw)); const spki = certificate.publicKey.export({ type: "spki", format: "der" }); try { assertTask19OriginBinding({ host: parsed.hostname, port, certificateSha256: createHash("sha256").update(certificate.raw).digest("hex"), spkiSha256: createHash("sha256").update(spki).digest("hex") }, expected); resolveObservation(); } catch (error) { reject(error); } }); socket.once("error", reject);
});

export const createTask19WebOrigin = async ({ root, identity, host = "127.0.0.1", port = 0, connectOrigins = emptyConnectPlan }) => {
  if (identity?.kind !== "task19-run-ephemeral-tls") throw new Error("TASK19_EPHEMERAL_TLS_IDENTITY_REQUIRED");
  const artifactRoot = resolve(root); const rootState = await stat(artifactRoot); if (!rootState.isDirectory()) throw new Error("TASK19_WEB_ARTIFACT_ROOT_INVALID"); const contentSecurityPolicy = createTask19ContentSecurityPolicy(connectOrigins); assertTask19ContentSecurityPolicy(contentSecurityPolicy, connectOrigins);
  const tls = identity.pfx === undefined ? { cert: identity.certificate, key: identity.key } : { pfx: identity.pfx, passphrase: identity.passphrase };
  const server = createServer({ ...tls, minVersion: "TLSv1.3" }, async (request, response) => {
    const path = request.url === undefined ? undefined : inside(artifactRoot, new URL(request.url, "https://task19.invalid").pathname);
    if (request.method !== "GET" || path === undefined) { response.writeHead(404).end(); return; }
    try {
      const state = await stat(path); if (!state.isFile()) { response.writeHead(404).end(); return; }
      const bytes = await readFile(path); response.writeHead(200, { "content-type": MIME[extname(path)] ?? "application/octet-stream", "content-length": bytes.length, "cache-control": "no-store", "content-security-policy": contentSecurityPolicy, "cross-origin-opener-policy": "same-origin", "cross-origin-resource-policy": "same-origin", "permissions-policy": "camera=(), microphone=(), geolocation=(), payment=(), usb=()", "referrer-policy": "no-referrer", "x-content-type-options": "nosniff" }); response.end(bytes);
    } catch (error) { if (error instanceof Error && "code" in error && error.code === "ENOENT") { response.writeHead(404).end(); return; } response.destroy(error instanceof Error ? error : new Error("TASK19_WEB_ORIGIN_READ_FAILED")); }
  });
  await new Promise((resolveReady, reject) => { server.once("error", reject); server.listen(port, host, () => { server.off("error", reject); resolveReady(); }); });
  const address = server.address(); if (address === null || typeof address === "string") { server.close(); throw new Error("TASK19_WEB_ORIGIN_ADDRESS_INVALID"); }
  return { url: `https://${host}:${address.port}`, host, port: address.port, certificateSha256: identity.certificateSha256, spkiSha256: identity.spkiSha256, close: () => new Promise((resolveClose, reject) => server.close((error) => error === undefined ? resolveClose() : reject(error))) };
};
