import { expect, test } from "@playwright/test";
import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";

const repository = join(import.meta.dirname, "../..");
const serverRoot = join(repository, "apps/server");
const fixturePath = process.env.PAIRING_FIXTURE;
const output = process.env.PAIRING_OUTPUT;
if (!fixturePath || !output) throw new TypeError("PAIRING_FIXTURE and PAIRING_OUTPUT are required");
const fixtureText = await readFile(fixturePath, "utf8");
const scenario = fixtureText.match(/^scenario:\s*(.+)$/m)?.[1];
if (scenario !== "happy" && scenario !== "security-failures") throw new TypeError("unsupported pairing fixture scenario");
const expectedByScenario = {
  happy: ["portal-bootstrap", "controller-registration", "authenticated-discovery", "catalog-scan", "continuation-policy", "queue-enqueue", "wss-state", "mutation-p95-under-500ms"],
  "security-failures": ["pairing-expired-410", "pairing-reuse-409", "controller-admin-operation-403", "pairing-rate-limit-429", "certificate-mismatch", "stale-policy-412", "unauthorized-mutations-zero"],
};
const fixtureExpects = [...fixtureText.matchAll(/^\s+-\s+(.+)$/gm)].map((match) => match[1]);
if (JSON.stringify(fixtureExpects) !== JSON.stringify(expectedByScenario[scenario])) throw new TypeError("pairing fixture expects list drifted");

const startServer = async (pairingTTL = "5m") => {
  const directory = await mkdtemp(join(tmpdir(), "jastreamer-pairing-api-"));
  const binary = join(directory, "jastreamer-server");
  const built = spawnSync("go", ["build", "-o", binary, "./cmd/jastreamer-server"], { cwd: serverRoot, encoding: "utf8" });
  if (built.status !== 0) {
    await rm(directory, { recursive: true, force: true });
    throw new Error(`server build failed: ${built.stderr}`);
  }
  const child = spawn(binary, ["--config", "../../tooling/fixtures/e2e/local.yaml"], {
    cwd: serverRoot,
    env: { ...process.env, JASTREAMER_DATA_DIR: directory,
      JASTREAMER_CATALOG_ROOT: join(directory, "music"), JASTREAMER_SETUP_SECRET: "fixture-setup-secret",
      JASTREAMER_PAIRING_TTL: pairingTTL },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += chunk.toString(); });
  let origin;
  try {
    origin = await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error(`server readiness timeout: ${stderr}`)), 30_000);
      child.once("exit", (code) => { clearTimeout(timeout); reject(new Error(`server exited ${code}: ${stderr}`)); });
      child.stdout.on("data", (chunk) => {
        const match = chunk.toString().match(/ready (https:\/\/[^ ]+) fingerprint=/);
        if (match?.[1]) { clearTimeout(timeout); resolve(match[1]); }
      });
    });
  } catch (error) {
    child.kill("SIGTERM");
    await rm(directory, { recursive: true, force: true });
    throw error;
  }
  return { child, directory, origin };
};

const stopServer = async (server) => {
  if (server.child.exitCode === null) {
    const exited = new Promise((resolve) => server.child.once("exit", resolve));
    server.child.kill("SIGTERM");
    await Promise.race([exited, new Promise((_, reject) => setTimeout(() => reject(new Error("server shutdown timeout")), 10_000))]);
  }
  if (server.child.exitCode === null || server.child.signalCode === null && server.child.exitCode !== 0) {
    throw new Error(`server teardown failed: exit=${server.child.exitCode} signal=${server.child.signalCode}`);
  }
  await rm(server.directory, { recursive: true, force: true });
};

const json = async (request, method, url, token = "", data, headers = {}) => request.fetch(url, {
  method, data, headers: { ...headers, ...(token ? { Authorization: `Bearer ${token}` } : {}) },
});

const bootstrap = async (request, origin) => {
  const response = await json(request, "POST", `${origin}/api/v1/bootstrap`, "", { setup_secret: "fixture-setup-secret", name: "Fixture Admin" });
  expect(response.status()).toBe(201);
  return await response.json();
};

const pairController = async (request, origin, adminToken, name = "Fixture Controller") => {
  const generated = await json(request, "POST", `${origin}/api/v1/pairing-codes`, adminToken, {});
  expect(generated.status()).toBe(201);
  const code = await generated.json();
  const paired = await json(request, "POST", `${origin}/api/v1/pairings`, "", { code: code.code, name });
  expect(paired.status()).toBe(201);
  return { code: code.code, credential: await paired.json() };
};

await mkdir(output, { recursive: true });

test("pairing API fixture", async ({ browser, playwright }) => {
  const server = await startServer();
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
    const page = await context.newPage();
    if (scenario === "happy") {
      await page.goto(`${server.origin}/pair/`);
      await expect(page.getByRole("heading", { name: "Pair trusted devices" })).toBeVisible();
      await page.getByLabel("Device name").first().fill("Fixture Admin");
      await page.getByLabel("One-time setup secret").fill("fixture-setup-secret");
      await page.getByRole("button", { name: "Create administrator" }).click();
      await expect(page.getByText("Administrator created.", { exact: false })).toBeVisible();
      await page.getByRole("button", { name: "Generate code" }).click();
      await expect(page.locator("#pairing-code")).toHaveText(/^\d{6}$/);
      const code = await page.locator("#pairing-code").textContent();
      await page.getByLabel("Device name").nth(1).fill("Fixture Control");
      await page.getByLabel("Six-digit code").fill(code ?? "");
      await page.getByRole("button", { name: "Register device" }).click();
      await expect(page.getByText("Device registered.", { exact: false })).toBeVisible();
      const adminToken = await page.evaluate(() => sessionStorage.getItem("jastreamer-admin-token"));
      const controllerText = await page.locator("#register-message").textContent();
      const controllerToken = controllerText?.split(": ").at(-1) ?? "";
      expect(adminToken).toBeTruthy();
      expect(controllerToken).toBeTruthy();
      const request = context.request;
      expect((await json(request, "GET", `${server.origin}/api/v1/discovery`, controllerToken, undefined,
        { "X-Jake-Protocol-Major": "2" })).status()).toBe(200);
      expect((await json(request, "POST", `${server.origin}/api/v1/catalog/scans`, adminToken ?? "", {})).status()).toBe(202);
      const policy = await json(request, "PATCH", `${server.origin}/api/v1/zones/main/continuation-policy`, controllerToken,
        { mode: "similar", artist_gap: 4, album_gap: 10 }, { "If-Match": "0" });
      expect(policy.status()).toBe(200);
      const timings = [];
      for (let zone = 0; zone < 8; zone++) {
        const started = performance.now();
        const queued = await json(request, "POST", `${server.origin}/api/v1/zones/zone-${zone}/queue`, controllerToken,
          { tracks: [{ track_id: `fixture-${zone}`, available: true }] }, { "If-Match": "0", "Idempotency-Key": `fixture-${zone}` });
        expect(queued.status()).toBe(201);
        timings.push(performance.now() - started);
      }
      timings.sort((left, right) => left - right);
      const p95 = timings[Math.ceil(timings.length * 0.95) - 1] ?? Number.POSITIVE_INFINITY;
      expect(p95).toBeLessThan(500);
      const event = await page.evaluate((token) => new Promise((resolve, reject) => {
        const socket = new WebSocket(`${location.origin.replace("https", "wss")}/api/v1/events`, `jastreamer.bearer.${token}`);
        let messages = 0;
        socket.onmessage = async (message) => {
          messages++;
          if (messages === 1) {
            const response = await fetch("/api/v1/zones/stream/queue", { method: "POST",
              headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", "If-Match": "0", "Idempotency-Key": "stream-event" },
              body: JSON.stringify({ tracks: [{ track_id: "stream-track", available: true }] }) });
            if (!response.ok) reject(new Error(`WSS trigger failed: ${response.status}`));
            return;
          }
          resolve(message.data); socket.close();
        };
        socket.onerror = () => reject(new Error("WSS state stream failed"));
      }), controllerToken);
      expect(String(event)).toContain('"resource":"queue"');
      await page.locator("#register-message").evaluate((element) => { element.textContent = "Device registered. Token: [REDACTED TOKEN]"; });
      await page.locator("#pairing-code").evaluate((element) => { element.textContent = "[REDACTED CODE]"; });
      await page.locator("#code-message").evaluate((element) => { element.textContent = "Expiry redacted for QA evidence."; });
      await page.screenshot({ path: join(output, "pairing-happy.png"), fullPage: true });
      await page.setViewportSize({ width: 390, height: 844 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await page.screenshot({ path: join(output, "pairing-happy-mobile.png"), fullPage: true });
      await writeFile(join(output, "result.json"), `${JSON.stringify({ scenario, status: "passed", mutationP95Ms: p95, wss: "state" }, null, 2)}\n`);
    } else {
      const request = context.request;
      const admin = await bootstrap(request, server.origin);
      const paired = await pairController(request, server.origin, admin.token);
      const role = await json(request, "POST", `${server.origin}/api/v1/pairing-codes`, paired.credential.token, {});
      expect(role.status()).toBe(403); expect((await role.json()).code).toBe("ADMIN_REQUIRED");
      const reuse = await json(request, "POST", `${server.origin}/api/v1/pairings`, "", { code: paired.code, name: "Replay" });
      expect(reuse.status()).toBe(409); expect((await reuse.json()).code).toBe("PAIRING_CODE_USED");
      let limited;
      for (let attempt = 0; attempt < 6; attempt++) limited = await json(request, "POST", `${server.origin}/api/v1/pairings`, "", { code: "999999", name: "Attacker" });
      expect(limited?.status()).toBe(429); expect((await limited.json()).code).toBe("PAIRING_RATE_LIMITED");
      const updated = await json(request, "PATCH", `${server.origin}/api/v1/zones/main/continuation-policy`, paired.credential.token,
        { mode: "album" }, { "If-Match": "0" });
      expect(updated.status()).toBe(200);
      const stale = await json(request, "PATCH", `${server.origin}/api/v1/zones/main/continuation-policy`, paired.credential.token,
        { mode: "stop" }, { "If-Match": "0" });
      expect(stale.status()).toBe(412); expect((await stale.json()).code).toBe("STALE_POLICY_REVISION");
      const unauthorized = await json(request, "POST", `${server.origin}/api/v1/zones/secure/queue`, "",
        { tracks: [{ track_id: "forbidden", available: true }] }, { "If-Match": "0", "Idempotency-Key": "forbidden" });
      expect(unauthorized.status()).toBe(401);
      const state = await json(request, "GET", `${server.origin}/api/v1/zones/secure/queue`, paired.credential.token);
      expect((await state.json()).queue).toHaveLength(0);
      const untrusted = await playwright.request.newContext({ ignoreHTTPSErrors: false });
      let certificateMismatch = false;
      try { await untrusted.get(`${server.origin}/api/v1/identity`); } catch { certificateMismatch = true; }
      await untrusted.dispose();
      expect(certificateMismatch).toBe(true);
      await page.goto(`${server.origin}/pair/`);
      await page.screenshot({ path: join(output, "pairing-security-failures.png"), fullPage: true });
      const expiryServer = await startServer("1ns");
      try {
        const expiryContext = await playwright.request.newContext({ ignoreHTTPSErrors: true });
        const expiryAdmin = await bootstrap(expiryContext, expiryServer.origin);
        const generated = await json(expiryContext, "POST", `${expiryServer.origin}/api/v1/pairing-codes`, expiryAdmin.token, {});
        const expiryCode = await generated.json();
        const expired = await json(expiryContext, "POST", `${expiryServer.origin}/api/v1/pairings`, "", { code: expiryCode.code, name: "Late" });
        expect(expired.status()).toBe(410); expect((await expired.json()).code).toBe("PAIRING_CODE_EXPIRED");
        await expiryContext.dispose();
      } finally { await stopServer(expiryServer); }
      await writeFile(join(output, "result.json"), `${JSON.stringify({ scenario, status: "passed", unauthorizedMutations: 0,
        codes: ["PAIRING_CODE_EXPIRED", "PAIRING_CODE_USED", "ADMIN_REQUIRED", "PAIRING_RATE_LIMITED", "CERTIFICATE_MISMATCH", "STALE_POLICY_REVISION"] }, null, 2)}\n`);
    }
    await context.close();
  } finally { await stopServer(server); }
});
