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

function wave(seconds) {
  const rate = 44100;
  const samples = rate * seconds;
  const bytes = Buffer.alloc(44 + samples * 2);
  bytes.write("RIFF", 0);
  bytes.writeUInt32LE(bytes.length - 8, 4);
  bytes.write("WAVEfmt ", 8);
  bytes.writeUInt32LE(16, 16);
  bytes.writeUInt16LE(1, 20);
  bytes.writeUInt16LE(1, 22);
  bytes.writeUInt32LE(rate, 24);
  bytes.writeUInt32LE(rate * 2, 28);
  bytes.writeUInt16LE(2, 32);
  bytes.writeUInt16LE(16, 34);
  bytes.write("data", 36);
  bytes.writeUInt32LE(samples * 2, 40);
  for (let index = 0; index < samples; index += 1) {
    bytes.writeInt16LE(Math.round(Math.sin(index * 2 * Math.PI * 220 / rate) * 4096), 44 + index * 2);
  }
  return bytes;
}

test.beforeAll(async () => {
  work = await mkdtemp(resolve(tmpdir(), "jastreamer-web-smoke-"));
  const dataDir = resolve(work, "data");
  await mkdir(dataDir);
  const musicDir = resolve(work, "music");
  await mkdir(musicDir);
  await writeFile(resolve(musicDir, "short.wav"), wave(3));
  await writeFile(resolve(musicDir, "long.wav"), wave(45));
  const port = await freePort();
  origin = `http://127.0.0.1:${port}`;
  const configPath = resolve(work, "server.json");
  await writeFile(configPath, JSON.stringify({
    version: 1,
    data_dir: dataDir,
    http: { enabled: true, address: `127.0.0.1:${port}` },
    https: { enabled: false, address: ":8443", certificate_file: "", private_key_file: "" },
    library_roots: [{ id: "smoke", name: "Smoke music", path: musicDir }],
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

test("focused account fields remain visible across short viewport resizing", async ({ page }) => {
  await page.setViewportSize({ width: 393, height: 700 });
  await page.goto(origin, { waitUntil: "domcontentloaded" });
  const username = page.getByLabel("Username", { exact: true });
  const password = page.getByLabel("Password", { exact: true });
  await username.fill("viewport-smoke");
  await password.fill("viewport-fixture-password");
  const fieldIsVisible = () => password.evaluate((field) => {
    const bounds = field.getBoundingClientRect();
    const viewport = window.visualViewport;
    const top = viewport?.offsetTop ?? 0;
    return bounds.top >= top && bounds.bottom <= top + (viewport?.height ?? window.innerHeight);
  });

  // A landscape phone can leave only 70 CSS pixels above the software keyboard.
  await page.setViewportSize({ width: 734, height: 70 });
  await expect.poll(fieldIsVisible).toBe(true);
  await expect(password).toBeFocused();
  await page.setViewportSize({ width: 393, height: 303 });
  await expect.poll(fieldIsVisible).toBe(true);
  await expect(username).toHaveValue("viewport-smoke");
  await expect(password).toHaveValue("viewport-fixture-password");
  // Accessibility scrolling does not necessarily emit pointer or wheel events.
  const heading = page.getByRole("heading", { name: /^(Create an administrator account|Sign in)$/ });
  await heading.scrollIntoViewIfNeeded();
  await page.evaluate(() => new Promise((resolveFrame) => requestAnimationFrame(() => requestAnimationFrame(resolveFrame))));
  await expect(heading).toBeInViewport({ ratio: 1 });
  await expect(password).toBeFocused();
  await password.scrollIntoViewIfNeeded();
  await page.mouse.wheel(0, -10000);
  await expect(page.getByRole("heading", { name: /^(Create an administrator account|Sign in)$/ })).toBeInViewport();
  await expect(password).not.toBeInViewport();
  await expect(password).toBeFocused();
});

test("a later configuration change restores restart controls without remounting Settings", async ({ page, context }) => {
  await page.goto(origin, { waitUntil: "domcontentloaded" });
  await expect(page.getByLabel("Username", { exact: true })).toBeVisible();
  const needsSetup = await page.getByLabel("Confirm password", { exact: true }).count() > 0;
  await page.getByLabel("Username", { exact: true }).fill("browser-smoke");
  await page.getByLabel("Password", { exact: true }).fill("browser-smoke-password");
  if (needsSetup) await page.getByLabel("Confirm password", { exact: true }).fill("browser-smoke-password");
  await page.getByRole("button", { name: needsSetup ? "Create account" : "Sign in", exact: true }).click();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.locator("#server-name").fill("First restart");
  await page.getByRole("button", { name: "Save settings", exact: true }).click();
  await page.getByRole("button", { name: "Restart server", exact: true }).click();
  await page.getByRole("button", { name: "Restart now", exact: true }).click();
  await expect(page.locator(".restart-result.is-success")).toBeVisible();

  const other = await context.newPage();
  try {
    await other.goto(origin, { waitUntil: "domcontentloaded" });
    await other.getByRole("button", { name: "Settings", exact: true }).click();
    await other.locator("#server-name").fill("Second Control change");
    await other.getByRole("button", { name: "Save settings", exact: true }).click();

    await expect(page.locator("#server-name")).toHaveValue("Second Control change");
    await expect(page.getByRole("button", { name: "Restart server", exact: true })).toBeEnabled();
    await expect(page.locator(".restart-result.is-success")).toHaveCount(0);
    await page.getByRole("button", { name: "Restart server", exact: true }).click();
    await page.getByRole("button", { name: "Restart now", exact: true }).click();
    await expect(page.locator(".restart-result.is-success")).toBeVisible();
  } finally {
    await other.close();
  }
});

test("settings preserve cross-tab drafts and reveal invalid fields before saving", async ({ page }) => {
  const setup = await control(page, "/setup");
  await control(page, setup.required ? "/setup" : "/login", "POST", {
    username: "browser-smoke", password: "browser-smoke-password",
  });
  const original = await control(page, "/config");
  const originalPlayer = await control(page, "/player");
  let saves = 0;
  page.on("request", (request) => {
    if (request.method() === "PUT" && new URL(request.url()).pathname === "/api/v1/config") saves += 1;
  });
  try {
    await page.goto(origin, { waitUntil: "domcontentloaded" });
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await page.locator("#server-name").fill("Cross-tab draft");
    await page.getByRole("tab", { name: "Library", exact: true }).click();
    await page.locator(".root-row .input").first().fill("Cross-tab music");
    await page.getByRole("tab", { name: "Network", exact: true }).click();
    await page.locator("#http-address").fill("");
    await page.getByRole("tab", { name: "General", exact: true }).click();
    await expect(page.locator("#server-name")).toHaveValue("Cross-tab draft");
    await page.getByRole("button", { name: "Save settings", exact: true }).click();
    await expect(page.getByRole("tab", { name: "Network", exact: true })).toHaveAttribute("aria-selected", "true");
    await expect(page.locator("#http-address")).toBeFocused();
    expect(saves).toBe(0);

    await page.locator("#http-address").fill(original.config.http.address);
    await page.getByRole("tab", { name: "Library", exact: true }).click();
    await expect(page.locator(".root-row .input").first()).toHaveValue("Cross-tab music");
    await page.getByRole("tab", { name: "General", exact: true }).click();
    await page.getByRole("button", { name: "Save settings", exact: true }).click();
    await expect.poll(async () => (await control(page, "/config")).config.server_name).toBe("Cross-tab draft");
    const saved = await control(page, "/config");
    expect(saved.config.library_roots[0].name).toBe("Cross-tab music");
    expect(saves).toBe(1);
    const player = await control(page, "/player");
    expect(player.state).toBe(originalPlayer.state);
    expect(player.renderer_id).toBe(originalPlayer.renderer_id);
  } finally {
    const current = await control(page, "/config");
    await control(page, "/config", "PUT", { config: original.config, revision: current.revision });
  }
});

async function control(page, path, method = "GET", data) {
  const response = await page.request.fetch(`${origin}/api/v1${path}`, {
    method,
    headers: { "X-Jastreamer-Request": "web" },
    ...(data === undefined ? {} : { data }),
  });
  expect(response.ok(), `${method} ${path}: ${response.status()}`).toBe(true);
  return response.status() === 204 ? undefined : response.json();
}

async function prepareBrowserPlayback(page, paths) {
  const setup = await control(page, "/setup");
  await control(page, setup.required ? "/setup" : "/login", "POST", {
    username: "browser-smoke", password: "browser-smoke-password",
  });
  await control(page, "/library/scans", "POST", {});
  await expect.poll(async () => (await control(page, "/library/tracks")).total).toBe(2);
  const tracks = (await control(page, "/library/tracks")).items;
  const trackIDs = paths.map((path) => tracks.find((track) => track.path === path).id);
  const state = await control(page, "/player");
  if (state.state !== "stopped") {
    await control(page, "/player", "POST", { action: "stop" });
    await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
  }
  const queue = await control(page, "/queue");
  await control(page, "/queue", "POST", { action: "replace", track_ids: trackIDs, revision: queue.revision });
  await page.goto(origin, { waitUntil: "domcontentloaded" });
  const output = page.getByLabel("Output device", { exact: true });
  await expect(output).toBeVisible();
  const localValue = await output.locator("option").evaluateAll((options) =>
    options.find((option) => /this (device|browser)/i.test(option.textContent ?? ""))?.value);
  expect(localValue).toBeTruthy();
  await output.selectOption(localValue);
  await expect.poll(async () => {
    const player = await control(page, "/player");
    const devices = (await control(page, "/renderers")).items;
    return devices.find((device) => device.id === player.renderer_id)?.protocol;
  }).toBe("browser");
  expect((await control(page, "/player")).state).toBe("stopped");
  return { audio: page.locator("audio"), trackIDs };
}

async function startBrowserPlayback(page, audio) {
  const media = page.waitForResponse((response) =>
    response.request().resourceType() === "media" && [200, 206].includes(response.status()));
  await page.getByRole("button", { name: "Play", exact: true }).click();
  expect(await (await media).headerValue("content-type")).toMatch(/^audio\//);
  await expect.poll(async () => (await control(page, "/player")).state).toBe("playing");
  await expect.poll(() => audio.evaluate((element) => !element.paused && element.currentTime > 0)).toBe(true);
}

test("browser output performs real media actions and remains owned when another Control closes", async ({ page, context }) => {
  const { audio, trackIDs } = await prepareBrowserPlayback(page, ["long.wav"]);
  await startBrowserPlayback(page, audio);
  const other = await context.newPage();
  try {
    await other.goto(origin, { waitUntil: "domcontentloaded" });
    await control(other, "/player", "POST", { action: "pause" });
    await expect.poll(async () => (await control(page, "/player")).state).toBe("paused");
    await expect.poll(() => audio.evaluate((element) => element.paused)).toBe(true);
    await control(other, "/player", "POST", { action: "seek", position_ms: 20000 });
    await expect.poll(() => audio.evaluate((element) => Math.abs(element.currentTime - 20) < 0.5)).toBe(true);
    await control(other, "/player", "POST", { action: "play" });
    await expect.poll(() => audio.evaluate((element) => !element.paused && element.currentTime > 20.25)).toBe(true);
    await control(other, "/player", "POST", { action: "seek", position_ms: 30000 });
    await expect.poll(() => audio.evaluate((element) => !element.paused && element.currentTime >= 30)).toBe(true);
    await expect.poll(async () => {
      const state = await control(page, "/player");
      return state.pending_command ? "pending" : state.state;
    }).toBe("playing");
  } finally {
    await other.close();
  }
  const before = await audio.evaluate((element) => element.currentTime);
  await expect.poll(() => audio.evaluate((element) => element.currentTime)).toBeGreaterThan(before + 0.25);
  expect((await control(page, "/player")).state).toBe("playing");
  await page.getByRole("button", { name: "Stop", exact: true }).click();
  await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
  await expect.poll(() => audio.evaluate((element) => element.paused)).toBe(true);
  expect((await control(page, "/queue")).entries.map((entry) => entry.track_id)).toEqual(trackIDs);
});

test("Most Played displays counted browser playback without changing the queue", async ({ page }) => {
  const { audio, trackIDs } = await prepareBrowserPlayback(page, ["short.wav"]);
  const before = (await control(page, "/library/tracks")).items.find((track) => track.id === trackIDs[0]).play_count;
  await startBrowserPlayback(page, audio);
  await expect.poll(async () =>
    (await control(page, "/library/tracks")).items.find((track) => track.id === trackIDs[0]).play_count,
  ).toBe(before + 1);
  await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
  const queue = await control(page, "/queue");

  await page.getByRole("button", { name: "Most Played", exact: true }).click();
  const row = page.locator(".library-track-row").filter({
    has: page.locator(".library-track-main strong", { hasText: /^short$/ }),
  });
  await expect(row.getByText(new RegExp(`^${before + 1} plays?$`))).toBeVisible();
  expect((await control(page, "/queue")).entries).toEqual(queue.entries);
  expect((await control(page, "/player")).state).toBe("stopped");
});

test("browser natural completion advances once and owner reload preserves the queue without autoplay", async ({ page }) => {
  const { audio, trackIDs } = await prepareBrowserPlayback(page, ["short.wav", "long.wav"]);
  await startBrowserPlayback(page, audio);
  await expect.poll(async () => (await control(page, "/player")).track?.id).toBe(trackIDs[1]);
  await expect.poll(async () => (await control(page, "/queue")).entries.map((entry) => entry.status))
    .toEqual(["completed", "playing"]);
  await page.reload();
  await expect(page.getByLabel("Output device", { exact: true })).toBeVisible();
  await expect.poll(async () => (await control(page, "/player")).state).toBe("unavailable");
  await expect.poll(async () => (await control(page, "/queue")).entries.map((entry) => entry.status))
    .toEqual(["completed", "pending"]);
  expect((await control(page, "/queue")).entries.map((entry) => entry.track_id)).toEqual(trackIDs);
  await expect.poll(() => page.locator("audio").evaluate((element) => element.paused)).toBe(true);
  await control(page, "/player", "POST", { action: "stop" });
  await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
});

test("a terminal decode failure retains the failed entry and advances duplicate tracks exactly once", async ({ page }) => {
  const { audio, trackIDs } = await prepareBrowserPlayback(page, ["long.wav", "long.wav", "short.wav"]);
  const queued = await control(page, "/queue");
  await startBrowserPlayback(page, audio);
  await audio.evaluate((element) => {
    // Inject a terminal decoder fault, not a fabricated Server completion.
    // Keep it observable through an in-flight acknowledgment until source replacement.
    Object.defineProperty(element, "error", {
      configurable: true,
      get: () => ({ code: MediaError.MEDIA_ERR_DECODE }),
    });
    element.addEventListener("emptied", () => { delete element.error; }, { once: true });
    element.pause();
    element.dispatchEvent(new Event("error"));
    element.dispatchEvent(new Event("error"));
  });
  await expect.poll(async () => (await control(page, "/player")).current_entry_id).toBe(queued.entries[1].id);
  await expect.poll(async () => (await control(page, "/queue")).entries.map((entry) => entry.status))
    .toEqual(["error", "playing", "pending"]);
  await expect.poll(() => audio.evaluate((element) => !element.paused && element.currentTime > 0.25)).toBe(true);
  expect((await control(page, "/queue")).entries.map((entry) => entry.track_id)).toEqual(trackIDs);
  await page.getByRole("button", { name: "Queue", exact: true }).click();
  await expect(page.locator(".queue-row").first().getByText("Playback failed", { exact: true })).toBeVisible();
  await control(page, "/player", "POST", { action: "stop" });
  await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
  expect((await control(page, "/queue")).entries.map((entry) => entry.status))
    .toEqual(["error", "pending", "pending"]);
});

test("a rejected media request does not skip tracks as if it were a decode failure", async ({ page }) => {
  const { trackIDs } = await prepareBrowserPlayback(page, ["long.wav", "short.wav"]);
  const queued = await control(page, "/queue");
  await page.route("**/media/**", (route) => route.fulfill({ status: 403, body: "Forbidden" }));
  try {
    await page.getByRole("button", { name: "Play", exact: true }).click();
    await expect.poll(async () => (await control(page, "/player")).state).toBe("error");
    expect((await control(page, "/player")).current_entry_id).toBe(queued.entries[0].id);
    expect((await control(page, "/queue")).entries.map((entry) => entry.status)).toEqual(["error", "pending"]);
    expect((await control(page, "/queue")).entries.map((entry) => entry.track_id)).toEqual(trackIDs);
    await control(page, "/player", "POST", { action: "stop" });
    await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
  } finally {
    await page.unroute("**/media/**");
  }
});

test("browser completion survives a delayed play acknowledgment response", async ({ page }) => {
  const { audio, trackIDs } = await prepareBrowserPlayback(page, ["short.wav", "long.wav"]);
  let release;
  let held = false;
  const responseGate = new Promise((resolveResponse) => { release = resolveResponse; });
  const reports = "**/api/v1/browser-output/registrations/*/reports";
  await page.route(reports, async (route) => {
    const body = route.request().postDataJSON();
    const response = await route.fetch();
    if (!held && body.result === "succeeded" && body.observation?.event === "playing") {
      held = true;
      await responseGate;
    }
    await route.fulfill({ response });
  });
  try {
    await startBrowserPlayback(page, audio);
    await expect.poll(() => audio.evaluate((element) => element.ended)).toBe(true);
    release();
    await expect.poll(async () => (await control(page, "/player")).track?.id).toBe(trackIDs[1]);
    await expect.poll(async () => (await control(page, "/queue")).entries.map((entry) => entry.status))
      .toEqual(["completed", "playing"]);
    await control(page, "/player", "POST", { action: "stop" });
    await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
  } finally {
    release();
    await page.unroute(reports);
  }
});

test("Server restart receives browser Stop acknowledgment before draining the output transport", async ({ page }) => {
  const { audio, trackIDs } = await prepareBrowserPlayback(page, ["long.wav"]);
  await startBrowserPlayback(page, audio);
  const document = await control(page, "/config");
  document.config.server_name = "Browser playback restart";
  const saved = await control(page, "/config", "PUT", { config: document.config, revision: document.revision });
  await control(page, "/restart", "POST", { revision: saved.revision });
  await expect.poll(async () => {
    try { return (await control(page, "/config")).runtime_id; } catch { return document.runtime_id; }
  }).not.toBe(document.runtime_id);
  await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
  await expect.poll(() => audio.evaluate((element) => element.paused)).toBe(true);
  expect((await control(page, "/queue")).entries.map((entry) => entry.track_id)).toEqual(trackIDs);
});

test("blocked browser playback exposes a reachable permission button and starts only after its click", async ({ page, browser }) => {
  const { trackIDs } = await prepareBrowserPlayback(page, ["long.wav"]);
  const ownerContext = await browser.newContext({
    storageState: await page.context().storageState(),
    viewport: { width: 1440, height: 1000 },
  });
  const owner = await ownerContext.newPage();
  const session = await ownerContext.newCDPSession(owner);
  // Ordinary evaluation helpers may grant a user gesture and hide a genuine autoplay rejection.
  const withoutActivation = async (expression) => (await session.send("Runtime.evaluate", {
    expression, awaitPromise: true, returnByValue: true, userGesture: false,
  })).result.value;
  try {
    await owner.goto(origin, { waitUntil: "domcontentloaded" });
    await expect.poll(() => withoutActivation('!!document.querySelector("#player-output")')).toBe(true);
    await withoutActivation(`(() => {
      const output = document.querySelector("#player-output");
      output.value = "browser:local";
      output.dispatchEvent(new Event("change", { bubbles: true }));
    })()`);
    await expect.poll(() => withoutActivation(`(() => {
      const output = document.querySelector("#player-output");
      return output.value.startsWith("browser:") && output.value !== "browser:local";
    })()`)).toBe(true);
    expect(await withoutActivation("navigator.userActivation.hasBeenActive")).toBe(false);
    await control(page, "/player", "POST", { action: "play" });
    await expect.poll(() => withoutActivation('!!document.querySelector(".browser-autoplay button")')).toBe(true);
    expect(await withoutActivation("document.querySelector('audio').paused")).toBe(true);
    expect((await control(page, "/player")).state).not.toBe("playing");
    await expect.poll(() => withoutActivation(`(() => {
      const button = document.querySelector(".browser-autoplay button");
      if (!button) return false;
      const bounds = button.getBoundingClientRect();
      return bounds.top >= 0 && bounds.bottom <= innerHeight &&
        button.contains(document.elementFromPoint(bounds.x + bounds.width / 2, bounds.y + bounds.height / 2));
    })()`)).toBe(true);
    await owner.locator(".browser-autoplay button").click();
    await expect.poll(async () => (await control(page, "/player")).state).toBe("playing");
    await expect.poll(() => owner.locator("audio").evaluate((audio) => !audio.paused && audio.currentTime > 0.5)).toBe(true);
    await control(page, "/player", "POST", { action: "stop" });
    await expect.poll(async () => (await control(page, "/player")).state).toBe("stopped");
    expect((await control(page, "/queue")).entries.map((entry) => entry.track_id)).toEqual(trackIDs);
  } finally {
    await session.detach();
    await ownerContext.close();
  }
});

test("liked playlist creation reconciles an earlier event refresh and preserves its saved snapshot", async ({ page }) => {
  const setup = await control(page, "/setup");
  await control(page, setup.required ? "/setup" : "/login", "POST", {
    username: "browser-smoke", password: "browser-smoke-password",
  });
  await control(page, "/library/scans", "POST", {});
  await expect.poll(async () => (await control(page, "/library/tracks")).total).toBe(2);
  await page.goto(origin, { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "Tracks", exact: true }).click();
  await page.getByRole("button", { name: "Like short", exact: true }).click();
  await expect(page.getByRole("button", { name: "Unlike short", exact: true })).toHaveAttribute("aria-pressed", "true");
  let likedHeartFill = "";
  await expect.poll(async () => {
    likedHeartFill = await page.getByRole("button", { name: "Unlike short", exact: true })
      .locator("svg").evaluate((icon) => getComputedStyle(icon).fill);
    return likedHeartFill;
  }).not.toMatch(/^(none)?$/);
  await page.getByRole("button", { name: "Like long", exact: true }).click();
  await expect(page.getByRole("button", { name: "Unlike long", exact: true })).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Playlists", exact: true }).click();
  await page.getByLabel("Shuffled liked playlist name", { exact: true }).fill("Liked event snapshot");
  const queueBefore = await control(page, "/queue");
  let release;
  const responseGate = new Promise((resolveResponse) => { release = resolveResponse; });
  await page.route("**/api/v1/playlists/from-likes", async (route) => {
    const response = await route.fetch();
    await responseGate;
    await route.fulfill({ response });
  });
  try {
    await page.getByRole("button", { name: "Create shuffled liked playlist", exact: true }).click();
    const savedList = page.getByRole("button", { name: /Liked event snapshot/ });
    await expect(savedList).toHaveCount(1);
    release();
    await expect(page.getByRole("button", { name: "Create shuffled liked playlist", exact: true })).toBeEnabled();
    await expect(savedList).toHaveCount(1);
    const saved = (await control(page, "/playlists")).items.find((playlist) => playlist.name === "Liked event snapshot");
    const likedIDs = (await control(page, "/library/tracks?liked=true")).items.map((track) => track.id);
    expect([...saved.track_ids].sort()).toEqual([...likedIDs].sort());
    expect((await control(page, "/queue")).entries).toEqual(queueBefore.entries);
    await expect(page.getByRole("button", { name: "Unlike short", exact: true }).locator("svg"))
      .toHaveCSS("fill", likedHeartFill);
    await page.getByRole("button", { name: "Unlike short", exact: true }).click();
    await expect(page.getByRole("button", { name: "Like short", exact: true })).toHaveAttribute("aria-pressed", "false");
    await expect(page.getByRole("button", { name: "Like short", exact: true }).locator("svg"))
      .toHaveCSS("fill", "none");
    await page.getByRole("button", { name: "Like short", exact: true }).click();
    await expect(page.getByRole("button", { name: "Unlike short", exact: true }).locator("svg"))
      .toHaveCSS("fill", likedHeartFill);
    await expect.poll(async () => (await control(page, "/library/tracks")).items.find((track) => track.title === "short")?.liked).toBe(true);
    expect((await control(page, `/playlists/${saved.id}`)).track_ids).toEqual(saved.track_ids);
  } finally {
    release();
    await page.unroute("**/api/v1/playlists/from-likes");
  }
});

test.describe("native download presentation", () => {
  test.use({
    userAgent: "Mozilla/5.0 (Linux; Android 16; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Mobile Safari/537.36 JaStreamerAndroid/0.2",
    viewport: { width: 360, height: 740 },
    isMobile: true,
    hasTouch: true,
  });

  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: "wait" });
  });

  async function installNativeDownloads(page) {
    await page.addInitScript(() => {
      const listeners = new Set();
      const jobs = {};
      window.refinementDownloadJobs = jobs;
      window.JastreamerDownloads = {
        addEventListener(type, listener) {
          if (type === "message") listeners.add(listener);
        },
        removeEventListener(type, listener) {
          listeners.delete(listener);
        },
        postMessage(raw) {
          const request = JSON.parse(raw);
          let result = {};
          if (request.action === "capabilities") result = { version: 1 };
          if (request.action === "download") {
            jobs["fixture-download"] = {
              id: "fixture-download", status: "waiting",
              completed_tracks: 0, total_tracks: 2, failed_tracks: 0,
              received_bytes: 0, total_bytes: 2_000_000,
              error: { code: "network_unmetered_required", message: "Waiting for an unmetered network" },
            };
            result = { job_id: "fixture-download" };
          }
          if (request.action === "status") result = { jobs: Object.values(jobs) };
          if (request.action === "configure_network") {
            const job = jobs[request.job_id];
            if (job) {
              job.status = "downloading";
              job.received_bytes = 500_000;
              delete job.error;
            }
          }
          setTimeout(() => {
            const event = new MessageEvent("message", { data: JSON.stringify({ id: request.id, result }) });
            for (const listener of listeners) listener(event);
          }, 0);
        },
      };
    });
  }

  async function openPhoneLibrary(page) {
    const setup = await control(page, "/setup");
    await control(page, setup.required ? "/setup" : "/login", "POST", {
      username: "browser-smoke", password: "browser-smoke-password",
    });
    await control(page, "/library/scans", "POST", {});
    await expect.poll(async () => (await control(page, "/library/tracks")).total).toBe(2);
    await page.goto(origin, { waitUntil: "domcontentloaded" });
    await expect(page.locator(".library-album-tile").first()).toBeVisible();
  }

  test("long album metadata cannot expand the phone viewport or displace the player", async ({ page }) => {
    await installNativeDownloads(page);
    await page.route("**/api/v1/library/albums?*", async (route) => {
      const response = await route.fetch();
      const result = await response.json();
      result.items = result.items.map((album) => ({
        ...album,
        title: "Live Concert Recording — Complete Deluxe Anniversary Edition",
        artist: "A very long artist and orchestra collaboration",
      }));
      await route.fulfill({ response, json: result });
    });
    await openPhoneLibrary(page);
    await expect(page.locator(".library-album-tile .native-download-button").first()).toBeVisible();
    for (const viewport of [{ width: 360, height: 740 }, { width: 740, height: 360 }]) {
      await page.setViewportSize(viewport);
      await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width + 1);
      const cards = await page.locator(".library-album-card").evaluateAll((elements) =>
        elements.map((element) => ({ left: element.getBoundingClientRect().left, right: element.getBoundingClientRect().right })));
      for (const card of cards) {
        expect(card.left).toBeGreaterThanOrEqual(0);
        expect(card.right).toBeLessThanOrEqual(viewport.width + 1);
      }
      const player = await page.locator(".player-bar").boundingBox();
      expect(player).not.toBeNull();
      expect(player.x).toBeGreaterThanOrEqual(0);
      expect(player.x + player.width).toBeLessThanOrEqual(viewport.width + 1);
      expect(player.y).toBeGreaterThanOrEqual(0);
      expect(player.y + player.height).toBeLessThanOrEqual(viewport.height + 1);
      await page.locator(".library-album-card").first().click();
      await expect(page.locator(".library-detail-header")).toBeVisible();
      expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(viewport.width + 1);
      await page.getByRole("button", { name: /Back to/ }).click();
    }
  });

  test("accepted downloads expose waiting reasons and reopenable live progress without leaving Server", async ({ page }) => {
    await installNativeDownloads(page);
    await openPhoneLibrary(page);
    const queueBefore = await control(page, "/queue");
    const playerBefore = await control(page, "/player");
    const download = page.locator(".library-album-tile .native-download-button").first();
    await download.click();
    await page.locator("[data-download-confirm=true]").click();
    const panel = page.locator(".native-download-status-dialog");
    await expect(panel).toBeVisible();
    await expect(panel).toContainText(/unmetered|metered|network/i);
    await panel.getByRole("button", { name: "Close", exact: true }).click();
    await expect(panel).not.toBeVisible();
    await download.click();
    await expect(panel).toBeVisible();
    await page.evaluate(() => {
      const job = window.refinementDownloadJobs["fixture-download"];
      job.status = "downloading";
      job.completed_tracks = 1;
      job.received_bytes = 500_000;
      delete job.error;
    });
    await expect(panel).toContainText("25%");
    await expect(panel.getByRole("progressbar")).toBeVisible();
    await panel.getByRole("button", { name: "Close", exact: true }).click();
    await page.locator(".native-download-status-open").click();
    await expect(panel).toBeVisible();
    await page.evaluate(() => {
      const job = window.refinementDownloadJobs["fixture-download"];
      job.status = "completed";
      job.completed_tracks = 2;
      job.received_bytes = 2_000_000;
    });
    await expect(panel).toContainText("Saved to device");
    await panel.getByRole("button", { name: "Close", exact: true }).click();
    await download.click();
    await expect(page.locator("[data-download-confirm=true]")).toBeVisible();
    expect((await control(page, "/queue")).entries).toEqual(queueBefore.entries);
    expect((await control(page, "/player")).state).toBe(playerBefore.state);
  });

  test("native history removal before the final status arrives does not strand a download", async ({ page }) => {
    await installNativeDownloads(page);
    await openPhoneLibrary(page);
    const download = page.locator(".library-album-tile .native-download-button").first();
    await download.click();
    await page.locator("[data-download-confirm=true]").click();
    const panel = page.locator(".native-download-status-dialog");
    await expect(panel.locator(".native-download-job")).toHaveCount(1);
    await page.evaluate(() => { delete window.refinementDownloadJobs["fixture-download"]; });
    await expect(panel.locator(".native-download-job")).toHaveCount(0);
    await expect(panel.locator(".native-download-status-error")).toHaveCount(0);
    await panel.getByRole("button", { name: "Close", exact: true }).click();
    await download.click();
    await expect(page.locator("[data-download-confirm=true]")).toBeVisible();
  });

  test("an open download panel reflects removal of already completed native history", async ({ page }) => {
    await installNativeDownloads(page);
    await openPhoneLibrary(page);
    await page.locator(".library-album-tile .native-download-button").first().click();
    await page.locator("[data-download-confirm=true]").click();
    const panel = page.locator(".native-download-status-dialog");
    await page.evaluate(() => {
      Object.assign(window.refinementDownloadJobs["fixture-download"], {
        status: "completed", completed_tracks: 2, received_bytes: 2_000_000,
      });
    });
    await expect(panel).toContainText("Saved to device");
    await page.evaluate(() => { delete window.refinementDownloadJobs["fixture-download"]; });
    await expect(panel.locator(".native-download-job")).toHaveCount(0);
    await expect(panel.locator(".native-download-status-error")).toHaveCount(0);
  });
});

test("unliking the last item on the last liked page returns to remaining tracks", async ({ page }) => {
  const setup = await control(page, "/setup");
  await control(page, setup.required ? "/setup" : "/login", "POST", {
    username: "browser-smoke", password: "browser-smoke-password",
  });
  const sample = wave(0.01);
  await Promise.all(Array.from({ length: 99 }, (_, index) =>
    writeFile(resolve(work, "music", `liked-page-${String(index).padStart(3, "0")}.wav`), sample)));
  await control(page, "/library/scans", "POST", {});
  await expect.poll(async () => (await control(page, "/library/tracks?limit=200")).total).toBe(101);
  const tracks = (await control(page, "/library/tracks?limit=200")).items;
  for (const track of tracks) {
    await control(page, `/library/tracks/${track.id}/like`, "PUT", { liked: true });
  }
  await page.goto(origin, { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "Liked", exact: true }).click();
  const remainingTrack = page.getByRole("button", { name: `Unlike ${tracks[0].title}`, exact: true });
  await expect(remainingTrack).toBeVisible();
  await page.getByRole("button", { name: "Next", exact: true }).click();
  const lastLike = page.locator(".library-track-actions .library-like-button");
  await expect(lastLike).toHaveCount(1);
  await lastLike.click();
  await expect(remainingTrack).toBeVisible();
  expect((await control(page, "/library/tracks?liked=true")).total).toBe(100);
  await expect(page.getByRole("button", { name: "Liked", exact: true })).toHaveAttribute("aria-pressed", "true");
});

test("nested folders return to each parent without changing playback or queue", async ({ page }) => {
  const setup = await control(page, "/setup");
  await control(page, setup.required ? "/setup" : "/login", "POST", {
    username: "browser-smoke", password: "browser-smoke-password",
  });
  const nested = resolve(work, "music", "Parent", "Child", "Grandchild");
  await mkdir(nested, { recursive: true });
  await writeFile(resolve(nested, "nested.wav"), wave(0.01));
  const scan = await control(page, "/library/scans", "POST", {});
  await expect.poll(async () =>
    (await control(page, "/library/scans")).items.find((item) => item.id === scan.id)?.status,
  ).toBe("complete");
  const queueBefore = await control(page, "/queue");
  const playerBefore = await control(page, "/player");
  await page.setViewportSize({ width: 393, height: 852 });
  await page.goto(origin, { waitUntil: "domcontentloaded" });
  await page.getByRole("button", { name: "Folders", exact: true }).click();
  await page.locator(".library-folder-row").filter({ hasText: "Smoke music" }).click();
  for (const name of ["Parent", "Child", "Grandchild"]) {
    await page.locator(".library-child-folders button").filter({ hasText: name }).click();
    await expect(page.locator(".library-detail-header h2")).toHaveText(name);
  }
  for (const name of ["Child", "Parent", "Smoke music"]) {
    await page.getByRole("button", { name: "Parent folder", exact: true }).click();
    await expect(page.locator(".library-detail-header h2")).toHaveText(name);
  }
  await page.getByRole("button", { name: "Back to list", exact: true }).click();
  await expect(page.locator(".library-folder-row").filter({ hasText: "Smoke music" })).toBeVisible();
  expect((await control(page, "/queue")).entries).toEqual(queueBefore.entries);
  const playerAfter = await control(page, "/player");
  expect(playerAfter.state).toBe(playerBefore.state);
  expect(playerAfter.renderer_id).toBe(playerBefore.renderer_id);
});
