import { createRequire } from "node:module";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const { test, expect } = require("../../apps/control/node_modules/@playwright/test");
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
let work;
let server;
let origin;

const freePort = () => new Promise((resolvePort, reject) => {
  const listener = createServer();
  listener.once("error", reject);
  listener.listen(0, "127.0.0.1", () => {
    const address = listener.address();
    listener.close((error) => error ? reject(error) : resolvePort(address.port));
  });
});

const waitUntilReady = async (url, child) => {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error("jastreamer-server exited before becoming ready");
    try {
      const response = await fetch(`${url}/healthz`);
      if (response.ok) return;
    } catch {
      // Listener is not ready yet.
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 100));
  }
  throw new Error("jastreamer-server did not become ready within 15 seconds");
};

test.beforeAll(async () => {
  work = await mkdtemp(resolve(tmpdir(), "jastreamer-web-smoke-"));
  const dataDir = resolve(work, "data");
  await mkdir(dataDir);
  const port = await freePort();
  origin = `http://127.0.0.1:${port}`;
  const configPath = resolve(work, "server.json");
  await writeFile(configPath, JSON.stringify({
    version: 1,
    data_dir: dataDir,
    http: { enabled: true, address: `127.0.0.1:${port}` },
    https: { enabled: false, address: ":8443", certificate_file: "", private_key_file: "" },
    library_roots: [],
    network: { interfaces: [], discovery_interval_seconds: 30, poll_interval_seconds: 1, allowed_cidrs: [] },
    media: { base_url: "", ffmpeg_path: "", transcode: false },
  }));
  server = spawn(resolve(root, "apps/server/dist/jastreamer-server"), ["--config", configPath], {
    cwd: root,
    stdio: "ignore",
  });
  await waitUntilReady(origin, server);
});

test.afterAll(async () => {
  if (server?.exitCode === null) {
    server.kill("SIGTERM");
    await new Promise((resolveExit) => {
      server.once("exit", resolveExit);
      setTimeout(resolveExit, 3_000);
    });
  }
  if (work) await rm(work, { recursive: true, force: true });
});

test("first-account form, session restoration, logout, and login work in the real Web app", async ({ page }) => {
  await page.goto(origin, { waitUntil: "domcontentloaded" });
  await page.getByLabel("Username", { exact: true }).fill("browser-smoke");
  await page.getByLabel("Password", { exact: true }).fill("browser-smoke-password");
  await page.getByLabel("Confirm password", { exact: true }).fill("browser-smoke-password");
  await page.getByRole("button", { name: "Create account", exact: true }).click();
  await expect(page.getByLabel("Output device", { exact: true })).toBeVisible();

  await page.reload();
  await expect(page.getByLabel("Output device", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(page.getByLabel("Username", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Confirm password", { exact: true })).toHaveCount(0);
  const protectedStatus = await page.evaluate(async () => (await fetch("/api/v1/queue")).status);
  expect(protectedStatus).toBe(401);

  await page.getByLabel("Username", { exact: true }).fill("browser-smoke");
  await page.getByLabel("Password", { exact: true }).fill("browser-smoke-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByLabel("Output device", { exact: true })).toBeVisible();
});
