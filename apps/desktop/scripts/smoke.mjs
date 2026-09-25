import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { cp, mkdtemp, mkdir, rename, rm, writeFile } from 'node:fs/promises';
import { createServer as createHTTPServer, request } from 'node:http';
import { createServer as createTCPServer } from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { _electron } from 'playwright-core';

const [serverBinary, desktopBinary] = process.argv.slice(2).map((value) => path.resolve(value));
if (!serverBinary || !desktopBinary) throw new Error('Usage: node scripts/smoke.mjs <server-binary> <packaged-desktop-binary>');
const work = await mkdtemp(path.join(tmpdir(), 'jastreamer-desktop-smoke-'));
const children = [];
const proxies = [];
const commands = [];
const browserOutputRequests = [];
const password = randomBytes(24).toString('base64url');
let application;
let portable = path.join(work, 'portable with spaces');
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function until(callback, message, timeout = 15000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const result = await callback();
    if (result) return result;
    await sleep(50);
  }
  throw new Error(message);
}
async function withWindowsFileUnlock(operation) {
  let lastLock;
  try {
    await until(async () => {
      try {
        await operation();
        return true;
      } catch (error) {
        if (process.platform !== 'win32' || !['EPERM', 'EBUSY'].includes(error.code)) throw error;
        // Hosted-runner processes can retain file handles after every app process exits.
        if (!lastLock) console.log(JSON.stringify({ waitingForWindowsFileUnlock: error.code, path: error.path }));
        lastLock = error;
        return false;
      }
    }, 'Windows filesystem lock did not clear after application exit');
  } catch (error) {
    if (lastLock) error.cause = lastLock;
    throw error;
  }
  if (lastLock) console.log(JSON.stringify({ windowsFileLockCleared: true }));
}
async function freePort() {
  const listener = createTCPServer();
  listener.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  return port;
}
async function startServer(label) {
  const port = await freePort();
  const folder = path.join(work, label);
  await mkdir(folder);
  const configFile = path.join(folder, 'server.json');
  await writeFile(configFile, JSON.stringify({
    version: 1, server_name: label, data_dir: path.join(folder, 'data'),
    http: { enabled: true, address: `127.0.0.1:${port}` },
    https: { enabled: false, address: ':8443', certificate_file: '', private_key_file: '' },
    library_roots: [],
    network: { interfaces: [], discovery_interval_seconds: 30, poll_interval_seconds: 1, allowed_cidrs: [] },
    media: { base_url: '', ffmpeg_path: '', transcode: false },
  }));
  const child = spawn(serverBinary, ['--config', configFile], { stdio: ['ignore', 'ignore', 'pipe'], windowsHide: true });
  children.push(child);
  let diagnostics = '';
  child.stderr.on('data', (chunk) => { diagnostics = (diagnostics + chunk).slice(-8192); });
  child.on('error', (error) => { diagnostics = String(error); });
  await until(async () => {
    if (child.exitCode !== null) throw new Error(`Server exited: ${diagnostics}`);
    try { return (await fetch(`http://127.0.0.1:${port}/healthz`, { signal: AbortSignal.timeout(500) })).ok; }
    catch { return false; }
  }, `Server startup timed out: ${label}`);
  const proxy = createHTTPServer((incoming, outgoing) => {
    if (incoming.method !== 'GET' && /^\/api\/v1\/(?:player|queue)(?:\/|$)/.test(incoming.url)) {
      commands.push({ method: incoming.method, path: incoming.url });
    }
    if (/^\/api\/v1\/browser-output\/registrations(?:\/|$)/.test(incoming.url)) {
      browserOutputRequests.push({ method: incoming.method, path: incoming.url });
    }
    const upstream = request({ hostname: '127.0.0.1', port, path: incoming.url, method: incoming.method, headers: incoming.headers }, (response) => {
      outgoing.writeHead(response.statusCode, response.headers);
      response.pipe(outgoing);
    });
    upstream.on('error', () => { if (!outgoing.headersSent) outgoing.writeHead(502); outgoing.end(); });
    outgoing.on('close', () => upstream.destroy());
    incoming.pipe(upstream);
  });
  proxy.listen(0, '127.0.0.1');
  await once(proxy, 'listening');
  proxies.push(proxy);
  return `http://127.0.0.1:${proxy.address().port}`;
}
async function launch() {
  application = await _electron.launch({
    executablePath: path.join(portable, path.basename(desktopBinary)),
    timeout: 20000,
  });
  const shell = await application.firstWindow();
  await shell.locator('#open-manual').waitFor({ state: 'visible' });
  assert.equal(await shell.locator('#manual-origin').isVisible(), false, 'Address entry must require an explicit action');
  const dataPath = await application.evaluate(({ app }) => app.getPath('userData'));
  assert.equal(path.resolve(dataPath), path.join(portable, 'user-data'), 'Portable profile must follow the executable');
  await application.evaluate(({ Tray }) => {
    const setContextMenu = Tray.prototype.setContextMenu;
    Tray.prototype.setContextMenu = function (menu) {
      globalThis.__traySmoke = { tray: this, menu };
      return setContextMenu.call(this, menu);
    };
  });
  // Rebuild the actual menu through the trusted shell's existing language action.
  await shell.evaluate((language) => window.jastreamerDesktop.setLanguage(language), await shell.locator("#language").inputValue());
  return shell;
}
async function connect(shell, origin) {
  await shell.locator('#open-manual').click();
  await shell.locator('#manual-origin').fill(origin);
  await shell.locator('#manual-form button[type=submit]').click();
  const page = await until(() => application.context().pages().find((candidate) => candidate.url() === origin + '/'), 'Remote Web view did not open').catch(async (error) => {
    const contents = await application.evaluate(({ webContents }) => webContents.getAllWebContents().map((content) => ({ id: content.id, type: content.getType(), url: content.getURL() })));
    const shellState = await shell.locator('body').innerText();
    throw new Error(`${error.message}; contents=${JSON.stringify(contents)}; shell=${shellState}`, { cause: error });
  });
  await page.waitForLoadState('domcontentloaded');
  const expectedState = await shell.locator("#language").inputValue() === "ko" ? "연결됨" : "Connected";
  await until(async () => (await shell.locator("#connection-state").innerText()) === expectedState, "Trusted connection bar did not become ready");
  return page;
}
async function createAccount(page, username) {
  await page.getByLabel("Username", { exact: true }).fill(username);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Confirm password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Create account", exact: true }).click();
  await page.getByLabel("Output device", { exact: true }).waitFor({ state: "visible" });
}
async function username(page) {
  return page.evaluate(async () => (await (await fetch('/api/v1/session')).json()).user?.username);
}
function remainingProcesses(processes) {
  return processes.filter(({ pid }) => {
    try {
      process.kill(pid, 0);
      return true;
    } catch (error) {
      if (error.code === "ESRCH") return false;
      throw error;
    }
  });
}
async function waitForApplicationExit(processes) {
  const exiting = remainingProcesses(processes);
  if (exiting.length) console.log(JSON.stringify({ desktopProcessesStillExiting: exiting }));
  await until(
    () => remainingProcesses(processes).length === 0,
    `Desktop processes did not exit: ${JSON.stringify(processes)}`,
  );
  application = null;
}
async function closeApplication() {
  const processes = await application.evaluate(({ app }) => app.getAppMetrics().map(({ pid, type }) => ({ pid, type })));
  await application.close();
  await waitForApplicationExit(processes);
}
async function trayControl(action) {
  return application.evaluate((_electron, nextAction) => {
    const { tray, menu } = globalThis.__traySmoke ?? {};
    if (!tray || !menu) throw new Error("The actual tray menu was not observed");
    if (nextAction === "labels") return {
      open: menu.getMenuItemById("tray-open")?.label,
      exit: menu.getMenuItemById("tray-exit")?.label,
    };
    if (nextAction === "click" || nextAction === "double-click") return tray.emit(nextAction);
    menu.getMenuItemById(`tray-${nextAction}`).click();
  }, action);
}
async function exitFromTrayMenu() {
  const processes = await application.evaluate(({ app }) => app.getAppMetrics().map(({ pid, type }) => ({ pid, type })));
  await application.evaluate(() => {
    const item = globalThis.__traySmoke?.menu.getMenuItemById("tray-exit");
    if (!item) throw new Error("The actual tray Exit item was not observed");
    setImmediate(() => item.click());
  });
  await waitForApplicationExit(processes);
}
async function browserOutputState(page) {
  return page.evaluate(async () => {
    const [playerResponse, renderersResponse] = await Promise.all([
      fetch("/api/v1/player"),
      fetch("/api/v1/renderers"),
    ]);
    if (!playerResponse.ok || !renderersResponse.ok) {
      throw new Error(`Local output state failed: player=${playerResponse.status}, renderers=${renderersResponse.status}`);
    }
    const player = await playerResponse.json();
    const renderers = await renderersResponse.json();
    return {
      rendererID: player.renderer_id,
      playerState: player.state,
      renderer: renderers.items.find((item) => item.id === player.renderer_id) ?? null,
    };
  });
}
async function selectLocalBrowserOutput(page) {
  await page.locator("#player-output").selectOption("browser:local");
  return until(async () => {
    const state = await browserOutputState(page);
    return state.rendererID?.startsWith("browser:") && state.renderer?.online ? state.rendererID : null;
  }, "This device did not become the selected online browser output");
}
async function closeShellWindow(windowId) {
  await application.evaluate(({ BrowserWindow }, id) => {
    BrowserWindow.fromId(id)?.close();
  }, windowId);
  await until(async () => application.evaluate(({ BrowserWindow }, id) => {
    const window = BrowserWindow.fromId(id);
    return Boolean(window && !window.isDestroyed() && !window.isVisible());
  }, windowId), "Windows close did not hide the shell");
}
async function assertShellRestored(windowId, message) {
  await until(async () => application.evaluate(({ BrowserWindow }, id) => {
    const window = BrowserWindow.fromId(id);
    return Boolean(window && window.isVisible() && !window.isMinimized() && window.isFocused());
  }, windowId), message);
}
async function hideAndRestore(page, origin, rendererID) {
  await page.evaluate(() => {
    window.__jastreamerTrayHeartbeat = 0;
    window.__jastreamerTrayHeartbeatTimer = setInterval(() => {
      window.__jastreamerTrayHeartbeat += 1;
    }, 25);
  });
  const before = await application.evaluate(({ BrowserWindow, webContents }, remoteUrl) => {
    const window = BrowserWindow.getAllWindows()[0];
    const remote = webContents.getAllWebContents().find((contents) => contents.getURL() === remoteUrl);
    return {
      windowId: window?.id ?? null,
      remoteId: remote?.id ?? null,
    };
  }, `${origin}/`);
  assert(before.windowId, "The foreground shell window must exist before its close action");
  assert(before.remoteId, "The connected remote WebContentsView must exist before its close action");
  const heartbeatBefore = await page.evaluate(() => window.__jastreamerTrayHeartbeat);
  const renewalsBefore = browserOutputRequests.filter(({ method, path }) => (
    method === "PUT" && path.endsWith("/lease")
  )).length;

  // Windows may query logoff and then cancel it without a session-end event.
  await application.evaluate(({ BrowserWindow }, windowId) => {
    BrowserWindow.fromId(windowId)?.emit("query-session-end", { preventDefault() {} });
  }, before.windowId);
  await closeShellWindow(before.windowId);
  await sleep(2_500);
  assert.equal(page.isClosed(), false, "Hiding the shell must preserve the remote WebContentsView");
  assert(
    await page.evaluate(() => window.__jastreamerTrayHeartbeat) > heartbeatBefore,
    "Remote browser timers must continue while the shell is hidden",
  );
  const hiddenOutput = await browserOutputState(page);
  assert.equal(hiddenOutput.rendererID, rendererID, "Hiding the shell must preserve the selected local output");
  assert.equal(hiddenOutput.renderer?.online, true, "The local audio registration must remain online while hidden");
  assert(
    browserOutputRequests.filter(({ method, path }) => method === "PUT" && path.endsWith("/lease")).length > renewalsBefore,
    "The local audio lease heartbeat must continue while hidden",
  );

  await trayControl("open");
  await assertShellRestored(before.windowId, "Tray Open did not restore and focus the hidden shell");
  await closeShellWindow(before.windowId);
  await trayControl("click");
  await assertShellRestored(before.windowId, "Tray click did not restore and focus the hidden shell");
  await closeShellWindow(before.windowId);
  await trayControl("double-click");
  await assertShellRestored(before.windowId, "Tray double-click did not restore and focus the hidden shell");
  await closeShellWindow(before.windowId);

  const relaunch = spawn(path.join(portable, path.basename(desktopBinary)), [], {
    stdio: ["ignore", "ignore", "pipe"],
    windowsHide: true,
  });
  children.push(relaunch);
  let diagnostics = "";
  relaunch.stderr.on("data", (chunk) => {
    diagnostics = (diagnostics + chunk).slice(-8192);
  });
  await until(
    () => relaunch.exitCode !== null,
    `Second desktop instance did not exit after handing off to the resident instance: ${diagnostics}`,
  );
  assert.equal(relaunch.exitCode, 0, `Second desktop instance failed: ${diagnostics}`);
  await assertShellRestored(before.windowId, "Single-instance relaunch did not restore and focus the hidden shell");
  const restoredRemoteId = await application.evaluate(({ webContents }, remoteUrl) => (
    webContents.getAllWebContents().find((contents) => contents.getURL() === remoteUrl)?.id ?? null
  ), `${origin}/`);
  assert.equal(restoredRemoteId, before.remoteId, "Restoring the shell must retain its existing remote WebContentsView");
  return {
    windowsCloseHides: true,
    hiddenRemoteTimersContinue: true,
    hiddenAudioRegistrationContinues: true,
    trayMenuRestores: true,
    trayClickRestores: true,
    trayDoubleClickRestores: true,
    secondInstanceRestores: true,
  };
}
try {
  await cp(path.dirname(desktopBinary), portable, { recursive: true, filter: (source) => path.basename(source) !== 'user-data' });
  const [a, b] = await Promise.all([startServer('Desktop A'), startServer('Desktop B')]);
  let trayLifecycle;
  let shell = await launch();
  await shell.locator("#open-manual").click();
  await shell.locator("#manual-origin").fill("http://127.0.0.1:1");
  await shell.keyboard.press("Escape");
  assert.equal(await shell.locator("#manual-origin").isVisible(), false);
  assert.equal(await shell.locator("#open-manual").evaluate((button) => button === document.activeElement), true);
  await shell.locator("#open-manual").click();
  assert.equal(await shell.locator("#manual-origin").inputValue(), "http://127.0.0.1:1", "Dismissal must retain the address draft");
  await shell.locator("#cancel-manual").click();
  assert.equal(await shell.locator("#selection-screen").isVisible(), true);
  assert.equal(application.context().pages().some((candidate) => /^https?:/.test(candidate.url())), false, "Cancel must not connect");
  await shell.locator("#language").selectOption("en");
  let page = await connect(shell, a);
  await createAccount(page, 'desktop-a');
  assert.equal(await username(page), 'desktop-a');
  if (process.platform === 'win32') {
    assert.equal(await page.getByRole('button', { name: 'Windows audio settings', exact: true }).count(), 0,
      'Windows audio settings must stay hidden until this device is the selected output');
  }
  assert.equal(await page.locator('.sidebar > .brand').isVisible(), false, 'The native shell must not duplicate Web branding');
  assert.equal(await page.locator('.sidebar-account').getByText('desktop-a', { exact: true }).isVisible(), true, 'The signed-in account must remain visible');
  assert.equal(await page.locator('.sidebar-account').getByRole('button', { name: 'Sign out', exact: true }).isVisible(), true, 'Sign out must remain available');
  const privileges = await page.evaluate(() => ({
    node: typeof require,
    process: typeof process,
    desktopBridge: typeof window.jastreamerDesktop,
    popupBlocked: window.open("https://example.invalid/") === null,
  }));
  assert.deepEqual(privileges, { node: "undefined", process: "undefined", desktopBridge: "undefined", popupBlocked: true });
  assert.match(await page.evaluate(() => document.cookie), /(?:^|; )jastreamer_language=en(?:;|$)/, "The active server partition must receive English before load");
  await page.evaluate(() => {
    document.cookie = "jastreamer_language=ko; Path=/; SameSite=Strict; Max-Age=31536000";
  });
  await until(async () => (await shell.locator("#language").inputValue()) === "ko", "Remote language cookie did not update the trusted shell");
  assert.equal(await shell.locator("#change-server").innerText(), "서버 변경");
  await page.evaluate(() => {
    document.cookie = "jastreamer_language=en; Path=/; SameSite=Strict; Max-Age=31536000";
  });
  await until(async () => (await shell.locator("#language").inputValue()) === "en", "English language cookie did not update the trusted shell");
  await shell.locator('#change-server').click();
  page = await connect(shell, b);
  assert.equal(await username(page), undefined, 'A session must not enter B at another port of the same host');
  await createAccount(page, 'desktop-b');
  await shell.locator('#change-server').click();
  page = await connect(shell, a);
  await page.getByLabel("Output device", { exact: true }).waitFor({ state: "visible" });
  assert.equal(await username(page), 'desktop-a', 'B login must not overwrite A session');
  await shell.locator("#change-server").click();
  await shell.locator("#language").selectOption("ko");
  await until(async () => (await shell.locator("html").getAttribute("lang")) === "ko", "Local language selection did not apply to the accessible document language");
  await closeApplication();
  assert.deepEqual(commands, [], 'Switching and closing must not send queue or playback commands');
  const moved = path.join(work, 'moved portable with spaces');
  await withWindowsFileUnlock(() => rename(portable, moved));
  portable = moved;
  shell = await launch();
  assert.equal(await shell.locator("#language").inputValue(), "ko", "Portable restart must retain the language preference");
  assert.deepEqual(await trayControl("labels"), { open: "JASTREAMER 열기", exit: "종료" }, "The tray menu must use the restored Korean preference");
  await shell.locator("#language").selectOption("en");
  await until(async () => (await shell.locator("html").getAttribute("lang")) === "en", "English selection did not apply to the accessible document language");
  await until(async () => {
    const labels = await trayControl("labels");
    return labels.open === "Open JASTREAMER" && labels.exit === "Exit";
  }, "The tray menu did not update after the language changed");
  assert.equal(application.context().pages().some((candidate) => candidate.url() === a + '/'), false, 'Restart must not auto-connect');
  page = await connect(shell, a);
  await page.getByLabel("Output device", { exact: true }).waitFor({ state: "visible" });
  assert.equal(await username(page), 'desktop-a', 'Moving the complete portable directory must retain the session on the same user/machine');
  const localRendererID = await selectLocalBrowserOutput(page);
  assert.equal((await browserOutputState(page)).playerState, "stopped", "Selecting the local output must not start playback");
  if (process.platform === 'win32') {
    // Exercise the real main/preload/UI contract even on runners without speakers.
    await page.getByRole('button', { name: 'Windows audio settings', exact: true }).click();
    const audioPanel = page.getByRole('region', { name: 'Windows local audio', exact: true });
    await audioPanel.getByRole('combobox', { name: 'Local audio backend', exact: true }).waitFor();
    assert.equal(await audioPanel.getByRole('combobox', { name: 'Local audio backend', exact: true }).inputValue(), 'browser', 'Native output must remain opt-in');
    assert.equal(await audioPanel.getByRole('combobox', { name: 'Exclusive', exact: true }).inputValue(), 'off', 'Exclusive output must require an explicit request');
    for (const name of ['Windows audio endpoint', 'Exclusive']) {
      assert.equal(await audioPanel.getByRole('combobox', { name, exact: true }).isDisabled(), true, `${name} must be disabled for Browser audio`);
    }
    await mkdir('test-results', { recursive: true });
    await page.screenshot({ path: 'test-results/windows-audio-settings.png', fullPage: true });
    await audioPanel.getByRole('button', { name: 'Close', exact: true }).click();
  }
  assert(browserOutputRequests.some(({ method }) => method === "POST"), "The browser audio output did not register");
  trayLifecycle = await hideAndRestore(page, a, localRendererID);
  const observerCookies = await application.evaluate(async ({ webContents }, remoteUrl) => {
    const remote = webContents.getAllWebContents().find((contents) => contents.getURL() === remoteUrl);
    return remote.session.cookies.get({ url: remoteUrl });
  }, `${a}/`);
  const observerHeaders = { Cookie: observerCookies.map(({ name, value }) => `${name}=${value}`).join("; ") };
  await exitFromTrayMenu();
  assert.deepEqual(commands, [{ method: "PUT", path: "/api/v1/player/output" }], "Hide, restore, and Exit must not send playback or queue commands");
  // Process shutdown may outlive unload's best-effort DELETE. The 15-second lease
  // must still retire the output; observe Server state without reopening a client.
  await until(async () => {
    const response = await fetch(`${a}/api/v1/renderers`, { headers: observerHeaders });
    assert.equal(response.status, 200, "The independent output observer must remain authenticated");
    const renderers = await response.json();
    return !renderers.items.find(({ id }) => id === localRendererID)?.online;
  }, "Tray Exit left the browser output online beyond its lease", 20_000);
  assert.equal((await fetch(a + '/healthz')).status, 200, 'Server must remain alive after desktop exits');
  console.log(JSON.stringify({ packagedLaunch: true, platform: process.platform, twoServerSessionIsolation: true, portableMoveSessionRetained: true, portableLanguageRetained: true, remoteLanguageCookieObserved: true, noAutomaticConnection: true, remoteNodeBlocked: true, remoteDesktopBridgeBlocked: true, remotePopupBlocked: true, ...trayLifecycle, trayExitTerminates: true, trayLanguageUpdates: true, playbackCommandsOnHideRestoreOrExit: commands.length - 1 }));
} finally {
  if (application) await application.close().catch(() => {});
  for (const proxy of proxies) { proxy.closeAllConnections(); proxy.close(); }
  for (const child of children) {
    if (child.exitCode === null) {
      child.kill();
      await Promise.race([once(child, 'exit'), sleep(3000)]);
      if (child.exitCode === null) child.kill('SIGKILL');
    }
  }
  await withWindowsFileUnlock(() => rm(work, { recursive: true, force: true }));
}
