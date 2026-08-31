import { afterEach, describe, expect, test } from "bun:test";
import { get } from "node:https";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createEphemeralTlsIdentity } from "./task19-tls-identity.mjs";
import { assertTask19ConnectBinding, assertTask19ContentSecurityPolicy, createTask19ContentSecurityPolicy, createTask19WebOrigin, observeTask19OriginBinding } from "./task19-web-origin.mjs";

const roots = []; const origins = [];
afterEach(async () => { await Promise.all(origins.splice(0).map((origin) => origin.close())); await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))); });
const request = (url, _ca) => new Promise((resolve, reject) => get(url, { rejectUnauthorized: false }, (response) => { const chunks = []; response.on("data", (bytes) => chunks.push(bytes)); response.once("end", () => resolve({ status: response.statusCode, headers: response.headers, body: Buffer.concat(chunks).toString() })); }).once("error", reject));

describe("Task19 exact Control Web HTTPS origin", () => {
  test("serves only the staged artifact after explicit readiness with its pinned test certificate", async () => {
    // Given: an extracted exact Flutter candidate and the repository test TLS identity.
    const root = await mkdtemp(join(tmpdir(), "task19-web-origin-")); roots.push(root); await mkdir(join(root, "assets")); await writeFile(join(root, "index.html"), "<html>exact control</html>"); await writeFile(join(root, "main.dart.js"), "console.log('exact')");
    const identity = await createEphemeralTlsIdentity({ root: join(root, ".tls") }); origins.push({ close: identity.cleanup });
    // When: the task-owned origin reports its listening URL.
    const origin = await createTask19WebOrigin({ root, identity, host: "127.0.0.1", port: 0 }); origins.push(origin);
    const response = await request(`${origin.url}/main.dart.js`, identity.certificate);
    // Then: HTTPS serves exact candidate bytes and rejects traversal.
    expect(origin.url).toStartWith("https://127.0.0.1:"); expect(response.status).toBe(200); expect(response.body).toBe("console.log('exact')"); expect(response.headers["content-security-policy"]).toBe(createTask19ContentSecurityPolicy(Object.freeze([]))); expect(response.headers["cross-origin-opener-policy"]).toBe("same-origin"); expect(response.headers["referrer-policy"]).toBe("no-referrer"); expect((await request(`${origin.url}/missing.js`, identity.certificate)).status).toBe(404); await expect(observeTask19OriginBinding(origin.url, { host: origin.host, port: origin.port, certificateSha256: identity.certificateSha256, spkiSha256: identity.spkiSha256 })).resolves.toBeUndefined(); await expect(observeTask19OriginBinding(origin.url, { host: origin.host, port: origin.port + 1, certificateSha256: identity.certificateSha256, spkiSha256: identity.spkiSha256 })).rejects.toThrow("TASK19_WEB_ORIGIN_BINDING_MISMATCH");
  });

  test("permits only local scripts, WebAssembly compilation, and immutable bound connect origins", () => {
    const plan = Object.freeze(["https://127.0.0.1:8443", "wss://127.0.0.1:9443"]); const csp = createTask19ContentSecurityPolicy(plan); const directives = Object.fromEntries(csp.split("; ").map((directive) => { const [name, ...values] = directive.split(" "); return [name, values]; }));
    expect(directives["script-src"]).toEqual(["'self'", "'wasm-unsafe-eval'"]); expect(directives["script-src"]).not.toContain("'unsafe-eval'"); expect(directives["script-src"]).not.toContain("*"); expect(csp).not.toContain("gstatic"); expect(directives["connect-src"]).toEqual(["'self'", ...plan]); expect(directives["object-src"]).toEqual(["'none'"]); expect(directives["frame-src"]).toEqual(["'none'"]); expect(directives["base-uri"]).toEqual(["'self'"]); expect(directives["form-action"]).toEqual(["'none'"]);
    expect(assertTask19ConnectBinding("https://127.0.0.1:8443/api/v1/events", plan)).toBeUndefined(); expect(() => assertTask19ConnectBinding("wss://127.0.0.1:9443/events", plan)).not.toThrow(); expect(() => assertTask19ConnectBinding("https://127.0.0.1:9444/api", plan)).toThrow("TASK19_WEB_CONNECT_DESTINATION_UNBOUND");
  });

  test("rejects mutable, broad, malformed, or drifting connect plans", () => {
    expect(() => createTask19ContentSecurityPolicy(["https://127.0.0.1:8443"])).toThrow("TASK19_WEB_CONNECT_PLAN_MUTABLE"); expect(() => createTask19ContentSecurityPolicy(Object.freeze(["https:"]))).toThrow("TASK19_WEB_CONNECT_ORIGIN_INVALID"); expect(() => createTask19ContentSecurityPolicy(Object.freeze(["https://127.0.0.1:8443/path"]))).toThrow("TASK19_WEB_CONNECT_ORIGIN_INVALID"); expect(() => createTask19ContentSecurityPolicy(Object.freeze(["https://*.example.test"]))).toThrow("TASK19_WEB_CONNECT_ORIGIN_INVALID");
    const plan = Object.freeze(["https://127.0.0.1:8443"]); expect(() => assertTask19ConnectBinding("https://127.0.0.1:8444", plan)).toThrow("TASK19_WEB_CONNECT_DESTINATION_UNBOUND");
    const expected = createTask19ContentSecurityPolicy(plan); expect(() => assertTask19ContentSecurityPolicy(expected.replace("'wasm-unsafe-eval'", "'unsafe-eval'"), plan)).toThrow("TASK19_WEB_CSP_DRIFT"); expect(() => assertTask19ContentSecurityPolicy(expected.replace(" 'wasm-unsafe-eval'", ""), plan)).toThrow("TASK19_WEB_CSP_DRIFT"); expect(() => assertTask19ContentSecurityPolicy(`${expected}; script-src https://evil.invalid`, plan)).toThrow("TASK19_WEB_CSP_DRIFT"); expect(() => assertTask19ContentSecurityPolicy(expected.replace("base-uri 'self'", "base-uri *"), plan)).toThrow("TASK19_WEB_CSP_DRIFT"); expect(() => assertTask19ContentSecurityPolicy(expected.replace("frame-src 'none'", "frame-src https://evil.invalid"), plan)).toThrow("TASK19_WEB_CSP_DRIFT");
  });
});
