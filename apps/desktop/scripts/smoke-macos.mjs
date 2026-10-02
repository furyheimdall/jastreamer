import assert from "node:assert/strict";
import { createHash, randomBytes } from "node:crypto";
import { execFileSync, spawn } from "node:child_process";
import { once } from "node:events";
import { access, mkdir, mkdtemp, readFile, realpath, rm, stat, writeFile } from "node:fs/promises";
import { createServer as createHTTPServer, request } from "node:http";
import { createServer as createTCPServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { _electron } from "playwright-core";
import { digest, inventory } from "./macos-package-common.mjs";

const [serverArgument, appArgument, receiptArgument, ...extra] = process.argv.slice(2);
assert(serverArgument && appArgument && receiptArgument && extra.length === 0,
  "Usage: node scripts/smoke-macos.mjs <server-binary> <packaged-app-path> <output-receipt-path>");
assert.equal(process.platform, "darwin", "Packaged macOS smoke requires native macOS");
assert.equal(process.arch, "arm64", "Packaged macOS smoke requires native arm64 Node.js");
const command = (executable, args) => execFileSync(executable, args, {
  encoding: "utf8", timeout: 10_000, maxBuffer: 4 * 1024 * 1024,
}).trim();
assert.equal(command("/usr/bin/uname", ["-m"]), "arm64", "Rosetta is unsupported");
assert.notEqual(process.getuid(), 0, "Run the smoke as an ordinary logged-in user");
const sourceRevision = process.env.JASTREAMER_SOURCE_REVISION;
assert.match(sourceRevision ?? "", /^[0-9a-f]{40}$/, "JASTREAMER_SOURCE_REVISION must be a full lowercase source commit SHA");
const distribution = process.env.JASTREAMER_MACOS_DISTRIBUTION ?? "local-development";
assert(["public-adhoc", "local-development"].includes(distribution), "Unknown macOS distribution");
const serverBinary = await realpath(serverArgument);
const appPath = await realpath(appArgument);
assert(appPath.endsWith(".app"), "Pass the genuine packaged .app, not a development Electron executable");
const executableRelativePath = "Contents/MacOS/jastreamer-desktop";
const executablePath = path.join(appPath, executableRelativePath);
const helperPath = path.join(appPath, "Contents/Resources/native-audio/jastreamer-audio");
await access(helperPath);
assert.equal(command("/usr/bin/lipo", ["-archs", executablePath]), "arm64", "The packaged app executable must be thin arm64");
assert.equal(command("/usr/bin/lipo", ["-archs", helperPath]), "arm64", "The packaged native helper must be thin arm64");
const receiptPath = path.resolve(receiptArgument);
assert.equal(path.basename(receiptPath), "launch-verification.json", "Use the release launch receipt filename");
assert(!receiptPath.startsWith(`${appPath}${path.sep}`), "The receipt must not modify the signed application bundle");
// A failed run must not leave an older success receipt usable by the release job.
await rm(receiptPath, { force: true });
const outputDirectory = path.dirname(appPath);
const manifestPath = path.join(outputDirectory, "manifest.json");
const manifestBytes = await readFile(manifestPath);
const manifest = JSON.parse(manifestBytes);
assert.equal(manifest.product, "jastreamer-desktop");
assert.equal(manifest.platform, "darwin");
assert.equal(manifest.arch, "arm64");
assert.equal(manifest.directory, path.basename(appPath));
assert.equal(manifest.sourceRevision, sourceRevision, "Package source revision differs from smoke revision");
assert.equal(manifest.distribution, distribution);
assert.equal(typeof manifest.archive?.path, "string");
assert.equal(path.basename(manifest.archive.path), manifest.archive.path, "Archive must be adjacent to the app");
assert(manifest.archive.path.endsWith(".dmg"));
const archivePath = path.join(outputDirectory, manifest.archive.path);
const packageManifest = {
  path: "manifest.json", bytes: manifestBytes.length,
  sha256: createHash("sha256").update(manifestBytes).digest("hex"),
};
async function verifyBinding() {
  assert.equal((await stat(archivePath)).size, manifest.archive.bytes, "Archive size changed");
  assert.equal(await digest(archivePath), manifest.archive.sha256, "Archive digest changed");
  assert.equal(await digest(manifestPath), packageManifest.sha256, "Package manifest changed");
  assert.deepEqual(await inventory(appPath), manifest.files, "Actual packaged app differs from the release inventory");
}
await verifyBinding();
const executableSHA256 = await digest(executablePath);
const work = await mkdtemp(path.join(tmpdir(), "jastreamer-macos-smoke-"));
const home = path.join(work, "home");
const expectedUserData = path.join(home, "Library/Application Support/jastreamer-desktop");
const isolatedEnvironment = {
  ...process.env,
  HOME: home,
  CFFIXED_USER_HOME: home,
  TMPDIR: path.join(work, "tmp"),
};
// Do not inherit switches that turn Electron into Node or change native loader behavior.
for (const key of Object.keys(isolatedEnvironment)) {
  if (key === "ELECTRON_RUN_AS_NODE" || key === "NODE_OPTIONS" || key.startsWith("DYLD_")) delete isolatedEnvironment[key];
}
const password = randomBytes(24).toString("base64url");
const checks = [];
const processes = new Set();
const forbiddenRequests = [];
let application;
let server;
let proxy;
let helperPID;
let version;
let serverDiagnostics = "";
let serverError;
let interrupted;
const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function until(callback, message, timeout = 20_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (interrupted) throw interrupted;
    const result = await callback();
    if (result) return result;
    await sleep(50);
  }
  throw new Error(`${message}; server diagnostics: ${serverDiagnostics}`);
}

function alive(pid) {
  try { process.kill(pid, 0); return true; }
  catch (error) { if (error.code === "ESRCH") return false; throw error; }
}

function processTable() {
  return command("/bin/ps", ["-axo", "pid=,ppid=,comm="]).split("\n").map((line) => {
    const match = line.trim().match(/^(\d+)\s+(\d+)\s+(.+)$/);
    return match ? { pid: Number(match[1]), parent: Number(match[2]), executable: match[3] } : null;
  }).filter(Boolean);
}

function rememberDescendants() {
  const table = processTable();
  // Track the real child even while Playwright is still waiting for launch, so a
  // failed launch cannot orphan the helper before application becomes available.
  for (const entry of table) {
    if (entry.parent === process.pid && entry.executable === executablePath) processes.add(entry.pid);
  }
  let added;
  do {
    added = false;
    for (const entry of table) {
      if (processes.has(entry.parent) && !processes.has(entry.pid)) {
        processes.add(entry.pid);
        added = true;
      }
    }
  } while (added);
  return table;
}

async function freePort() {
  const listener = createTCPServer();
  listener.listen(0, "127.0.0.1");
  await once(listener, "listening");
  const port = listener.address().port;
  await new Promise((resolve, reject) => listener.close((error) => error ? reject(error) : resolve()));
  return port;
}

async function startServer() {
  const port = await freePort();
  const configFile = path.join(work, "server.json");
  await writeFile(configFile, JSON.stringify({
    version: 1, server_name: "macOS packaged smoke", data_dir: path.join(work, "server-data"),
    http: { enabled: true, address: `127.0.0.1:${port}` },
    https: { enabled: false, address: ":8443", certificate_file: "", private_key_file: "" },
    library_roots: [],
    network: { interfaces: ["lo0"], discovery_interval_seconds: 30, poll_interval_seconds: 1, allowed_cidrs: ["127.0.0.0/8", "::1/128"] },
    media: { base_url: "", ffmpeg_path: "", transcode: false },
    cast: { enabled: false }, airplay: { enabled: false },
  }));
  server = spawn(serverBinary, ["--config", configFile], {
    env: isolatedEnvironment, cwd: work, stdio: ["ignore", "pipe", "pipe"],
  });
  if (server.pid) processes.add(server.pid);
  for (const stream of [server.stdout, server.stderr]) {
    stream.on("data", (chunk) => { serverDiagnostics = (serverDiagnostics + chunk).slice(-16_384); });
  }
  server.on("error", (error) => { serverError = error; });
  await until(async () => {
    if (serverError) throw serverError;
    if (server.exitCode !== null || server.signalCode !== null) throw new Error(`Server exited: ${serverDiagnostics}`);
    try { return (await fetch(`http://127.0.0.1:${port}/healthz`, { signal: AbortSignal.timeout(500) })).ok; }
    catch { return false; }
  }, "Isolated Server startup timed out");
  // Forward real Web/API traffic, with a fail-closed safety boundary against playback.
  // Output selection is harmless; no queue mutation, transport or media is needed.
  proxy = createHTTPServer((incoming, outgoing) => {
    const pathname = new URL(incoming.url, "http://127.0.0.1").pathname;
    const mutation = !["GET", "HEAD", "OPTIONS"].includes(incoming.method);
    const forbidden = (mutation && /^\/api\/v1\/(?:player|queue)(?:\/|$)/.test(pathname)
        && pathname !== "/api/v1/player/output")
      || /^\/media(?:\/|$)/.test(pathname);
    if (forbidden) {
      forbiddenRequests.push({ method: incoming.method, path: pathname });
      outgoing.writeHead(403, { "Content-Type": "text/plain" });
      outgoing.end("Packaged smoke forbids playback and media requests");
      return;
    }
    const upstream = request({ hostname: "127.0.0.1", port, path: incoming.url, method: incoming.method, headers: incoming.headers }, (response) => {
      outgoing.writeHead(response.statusCode, response.headers);
      response.pipe(outgoing);
    });
    upstream.on("error", () => { if (!outgoing.headersSent) outgoing.writeHead(502); outgoing.end(); });
    outgoing.on("close", () => upstream.destroy());
    incoming.pipe(upstream);
  });
  proxy.listen(0, "127.0.0.1");
  await once(proxy, "listening");
  return `http://127.0.0.1:${proxy.address().port}`;
}

async function nativeStopped(page) {
  const state = await page.evaluate(async () => {
    if (!window.JastreamerDesktopAudio) throw new Error("Actual macOS preload bridge is missing");
    return window.JastreamerDesktopAudio.request("status");
  });
  assert.equal(state.audio.platform, "macos");
  assert.equal(state.audio.enabled, false, "Native audio must remain opt-in");
  assert.equal(state.audio.state, "stopped", "No native playback may start");
  assert.equal(state.audio.actual, null, "No CoreAudio device may be opened");
  assert.equal(state.device, null, "No native Server registration may be created");
  assert.equal(state.error, undefined, `Native helper probe failed: ${JSON.stringify(state.error)}`);
  assert.deepEqual(state.audio.requested, { device_id: "default", exclusive: false });
}

async function browserState(page) {
  return page.evaluate(async () => {
    const [playerResponse, renderersResponse] = await Promise.all([fetch("/api/v1/player"), fetch("/api/v1/renderers")]);
    if (!playerResponse.ok || !renderersResponse.ok) throw new Error("Real Server output query failed");
    const player = await playerResponse.json();
    const renderers = await renderersResponse.json();
    return { player, renderer: renderers.items.find((item) => item.id === player.renderer_id) ?? null };
  });
}

async function smoke() {
  await mkdir(expectedUserData, { recursive: true, mode: 0o700 });
  await mkdir(isolatedEnvironment.TMPDIR, { mode: 0o700 });
  const origin = await startServer();
  application = await _electron.launch({
    executablePath, env: isolatedEnvironment, cwd: work, chromiumSandbox: true, timeout: 30_000,
  });
  processes.add(application.process().pid);
  rememberDescendants();
  application.context().setDefaultTimeout(20_000);
  const shell = await application.firstWindow();
  await shell.locator("#open-manual").waitFor({ state: "visible" });
  const identity = await application.evaluate(({ app }) => ({
    platform: process.platform, arch: process.arch, packaged: app.isPackaged,
    executable: app.getPath("exe"), home: app.getPath("home"), userData: app.getPath("userData"),
    version: app.getVersion(), argv: process.argv,
  }));
  assert.equal(identity.platform, "darwin");
  assert.equal(identity.arch, "arm64");
  assert.equal(identity.packaged, true);
  assert.equal(await realpath(identity.executable), await realpath(executablePath));
  assert(!identity.argv.some((arg) => /^(?:--no-sandbox|--disable-web-security|--ignore-certificate-errors)(?:=|$)/.test(arg)), "Security bypass switches are forbidden");
  version = identity.version;
  assert.match(version, /^\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$/, "Packaged app version is invalid");
  assert.equal(version, manifest.version, "Running app version differs from release manifest");
  checks.push("native-arm64-packaged-app");
  assert.equal(await realpath(identity.home), await realpath(home), "macOS home must be isolated");
  assert.equal(await realpath(identity.userData), await realpath(expectedUserData), "macOS userData must be isolated");
  checks.push("isolated-profile");
  const helper = await until(() => {
    const matches = rememberDescendants().filter((entry) => entry.parent === application.process().pid && entry.executable === helperPath);
    assert(matches.length <= 1, "Expected only one actual packaged native helper");
    return matches.length === 1 ? matches[0] : null;
  }, "Actual packaged native helper did not start for its harmless inventory probe");
  helperPID = helper.pid;
  processes.add(helperPID);

  await shell.locator("#language").selectOption("en");
  await shell.locator("#open-manual").click();
  await shell.locator("#manual-origin").fill(origin);
  await shell.locator("#manual-form button[type=submit]").click();
  const page = await until(() => application.context().pages().find((candidate) => candidate.url() === `${origin}/`), "Real embedded Web did not open");
  await page.waitForLoadState("domcontentloaded");
  await until(async () => (await shell.locator("#connection-state").innerText()) === "Connected", "Trusted shell did not connect");
  await page.getByLabel("Username", { exact: true }).fill("macos-smoke");
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Confirm password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Create account", exact: true }).click();
  await page.getByLabel("Output device", { exact: true }).waitFor({ state: "visible" });
  const session = await page.evaluate(async () => {
    const response = await fetch("/api/v1/session");
    if (!response.ok) throw new Error(`Session failed: ${response.status}`);
    return response.json();
  });
  assert.equal(session.user?.username, "macos-smoke");
  checks.push("real-server-session");
  await nativeStopped(page);
  await page.getByRole("button", { name: "macOS audio settings", exact: true }).click();
  const panel = page.locator("#desktop-audio-panel");
  await panel.getByRole("heading", { name: "macOS local audio", exact: true }).waitFor({ state: "visible" });
  const backend = panel.getByLabel("Local audio backend", { exact: true });
  assert.equal(await backend.inputValue(), "browser");
  assert.equal(await backend.locator('option[value="browser"]').textContent(), "Browser (default)");
  assert.equal(await backend.locator('option[value="native"]').textContent(), "CoreAudio native (opt in)");
  assert.equal((await panel.innerText()).includes("Windows"), false, "macOS settings must not display Windows labels");
  await panel.getByRole("button", { name: "Close", exact: true }).click();
  await page.locator("#player-output").selectOption("browser:local");
  const output = await until(async () => {
    const state = await browserState(page);
    return state.player.renderer_id?.startsWith("browser:") && state.renderer?.online ? state : null;
  }, "Real browser output registration did not become selected and online");
  assert.equal(output.player.state, "stopped");
  checks.push("browser-output-default", "macos-native-bridge-settings");

  const before = await application.evaluate(({ BrowserWindow, webContents }, url) => {
    const window = BrowserWindow.getAllWindows()[0];
    const remote = webContents.getAllWebContents().find((content) => content.getURL() === url);
    if (!window || !remote) throw new Error("Actual shell/remote contents missing");
    const preferences = remote.getLastWebPreferences();
    if (!preferences.sandbox || !preferences.contextIsolation || preferences.nodeIntegration || !preferences.webSecurity) throw new Error("Remote renderer security preferences changed");
    return { windowID: window.id, remoteID: remote.id, storagePath: remote.session.getStoragePath() };
  }, `${origin}/`);
  assert(before.storagePath && path.resolve(before.storagePath).startsWith(`${path.resolve(identity.userData)}${path.sep}`), "Remote session must remain inside the temporary profile");
  await application.evaluate(({ BrowserWindow }, id) => BrowserWindow.fromId(id).close(), before.windowID);
  await until(() => application.evaluate(({ BrowserWindow }, id) => {
    const window = BrowserWindow.fromId(id);
    return Boolean(window && !window.isDestroyed() && !window.isVisible());
  }, before.windowID), "Closing the macOS window did not hide it");
  assert(alive(application.process().pid), "Window close must not quit macOS app");
  await application.evaluate(({ app }) => app.emit("activate"));
  await until(() => application.evaluate(({ BrowserWindow }, id) => {
    const window = BrowserWindow.fromId(id);
    return Boolean(window && window.isVisible() && !window.isMinimized() && window.isFocused());
  }, before.windowID), "macOS activate did not restore the original window");
  const restored = await application.evaluate(({ webContents }, id) => {
    const remote = webContents.fromId(id);
    return remote && !remote.isDestroyed() ? remote.getURL() : null;
  }, before.remoteID);
  assert.equal(restored, `${origin}/`, "Hide/activate must retain the live remote WebContents");
  assert.equal(await page.evaluate(async () => (await (await fetch("/api/v1/session")).json()).user?.username), "macos-smoke");
  assert.equal((await browserState(page)).player.renderer_id, output.player.renderer_id);
  await nativeStopped(page);
  checks.push("close-hides-activate-restores");
  assert.deepEqual(forbiddenRequests, [], "Smoke attempted a forbidden playback/native request");

  const appPID = application.process().pid;
  const table = rememberDescendants();
  const appProcesses = new Set([appPID]);
  for (let changed = true; changed;) {
    changed = false;
    for (const entry of table) if (appProcesses.has(entry.parent) && !appProcesses.has(entry.pid)) {
      appProcesses.add(entry.pid); changed = true;
    }
  }
  assert(appProcesses.has(helperPID) && alive(helperPID), "Native helper must still be owned by the app before explicit quit");
  const appChild = application.process();
  await application.evaluate(({ app }) => { setImmediate(() => app.quit()); });
  await until(() => [...appProcesses].every((pid) => !alive(pid)), "Explicit quit did not release the app, renderers and native helper");
  assert.equal(appChild.exitCode, 0, "Explicit app quit must exit successfully");
  application = null;
  checks.push("explicit-quit-releases-helper", "no-playback-commands");
  assert.deepEqual(forbiddenRequests, []);
}

async function cleanup() {
  const failures = [];
  try { rememberDescendants(); } catch (error) { failures.push(error); }
  if (application) {
    await Promise.race([
      application.close().catch(() => {}),
      sleep(3_000),
    ]);
  }
  for (const pid of processes) if (alive(pid)) {
    try { process.kill(pid, "SIGTERM"); } catch (error) { if (error.code !== "ESRCH") failures.push(error); }
  }
  const deadline = Date.now() + 3_000;
  while ([...processes].some(alive) && Date.now() < deadline) await sleep(50);
  for (const pid of processes) if (alive(pid)) {
    try { process.kill(pid, "SIGKILL"); } catch (error) { if (error.code !== "ESRCH") failures.push(error); }
  }
  const killDeadline = Date.now() + 3_000;
  while ([...processes].some(alive) && Date.now() < killDeadline) await sleep(50);
  const survivors = [...processes].filter(alive);
  if (survivors.length) failures.push(new Error(`Smoke-owned processes survived cleanup: ${survivors.join(", ")}`));
  if (proxy) {
    proxy.closeAllConnections();
    try {
      await new Promise((resolve, reject) => proxy.close((error) => error ? reject(error) : resolve()));
    } catch (error) { failures.push(error); }
  }
  if (!survivors.length) {
    try { await rm(work, { recursive: true, force: true }); }
    catch (error) { failures.push(error); }
  }
  if (failures.length) throw new AggregateError(failures, "macOS smoke cleanup failed");
}

const interrupt = (reason) => {
  interrupted = new Error(reason);
};
const onSIGINT = () => interrupt("macOS smoke interrupted by SIGINT");
const onSIGTERM = () => interrupt("macOS smoke interrupted by SIGTERM");
process.once("SIGINT", onSIGINT);
process.once("SIGTERM", onSIGTERM);
const watchdog = setTimeout(() => interrupt("macOS packaged smoke exceeded 180 seconds"), 180_000);
const processTracker = setInterval(() => {
  try { rememberDescendants(); }
  catch (error) { interrupt(`Cannot track smoke-owned processes: ${error.message}`); }
}, 250);
try {
  // Do not race cleanup against still-running launch/actions: an interrupted launch
  // must settle before cleanup can own every process it created.
  await smoke();
  if (interrupted) throw interrupted;
} finally {
  clearTimeout(watchdog);
  clearInterval(processTracker);
  try { await cleanup(); }
  finally {
    process.off("SIGINT", onSIGINT);
    process.off("SIGTERM", onSIGTERM);
  }
}
checks.push("cleanup");
await verifyBinding();
const receipt = {
  schema: 1, product: "jastreamer-desktop", version, sourceRevision,
  platform: "darwin", arch: "arm64", distribution, checks,
  archive: manifest.archive, packageManifest,
  appExecutable: { path: executableRelativePath, sha256: executableSHA256 },
  helper: { pid: helperPID, exited: true },
};
await mkdir(path.dirname(receiptPath), { recursive: true });
await writeFile(receiptPath, `${JSON.stringify(receipt, null, 2)}\n`, { flag: "wx" });
console.log(JSON.stringify(receipt));
