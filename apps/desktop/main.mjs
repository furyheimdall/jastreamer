import { mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import {
  app,
  BrowserWindow,
  dialog,
  ipcMain,
  Menu,
  session,
  WebContentsView,
} from "electron";
import { LanDiscovery } from "./lib/discovery.mjs";
import {
  DEFAULT_LANGUAGE,
  getLanguage,
  LANGUAGE_COOKIE_NAME,
  normalizeLanguage,
  setLanguage,
  t,
} from "./lib/i18n.mjs";
import { probeEndpoint, probeErrorMessage } from "./lib/probe.mjs";
import {
  isTrustedShellSender,
  isLanguageCookieForOrigin,
  normalizeEndpoint,
  normalizeServerId,
  restrictLocalContents,
  restrictRemoteContents,
  restrictSession,
  sessionPartitionFor,
} from "./lib/security.mjs";
import {
  ensureWritableDirectory,
  LanguagePreferenceStore,
  RecentServerStore,
  resolveUserDataPath,
} from "./lib/storage.mjs";

const APP_DIRECTORY = path.dirname(fileURLToPath(import.meta.url));
const SHELL_PATH = path.join(APP_DIRECTORY, "index.html");
const SHELL_URL = pathToFileURL(SHELL_PATH).href;
const PRELOAD_PATH = path.join(APP_DIRECTORY, "preload.cjs");
const REMOTE_TOP = 76;
const RECENT_PROBE_INTERVAL_MS = 60_000;
const MAX_PARALLEL_RECENT_PROBES = 3;
const LANGUAGE_COOKIE_LIFETIME_SECONDS = 365 * 24 * 60 * 60;

const userDataPath = resolveUserDataPath({
  isPackaged: app.isPackaged,
  executablePath: app.getPath("exe"),
  appDirectory: APP_DIRECTORY,
});

let bootstrapFailed = false;
try {
  mkdirSync(userDataPath, { recursive: true, mode: 0o700 });
  app.setPath("userData", userDataPath);
} catch {
  bootstrapFailed = true;
  dialog.showErrorBox(
    t("main.start.title"),
    t("main.start.portable", { path: userDataPath }),
  );
  app.exit(1);
}

const ownsSingleInstance = !bootstrapFailed && app.requestSingleInstanceLock();
if (!bootstrapFailed && !ownsSingleInstance) app.quit();

let shellWindow = null;
let remoteView = null;
let remoteAttached = false;
let store = null;
let languageStore = null;
let discovery = null;
let operationGeneration = 0;
let connectController = null;
let recentProbePromise = null;
let recentProbeTimer = null;
let shuttingDown = false;
let lastAttempt = null;
let remoteLanguageObserver = null;
const pendingControllers = new Set();
const restrictedSessions = new WeakSet();
const recentAvailability = new Map();

const appState = {
  mode: "selection",
  current: null,
  error: null,
  language: DEFAULT_LANGUAGE,
  refreshing: false,
};

function recentKey(id, origin) {
  return `${id}\0${origin}`;
}

function publicDiscovery() {
  return (discovery?.snapshot() ?? []).map(({ origins: _origins, ...item }) => item);
}

function publicRecents() {
  return (store?.list() ?? []).map((recent) => {
    const status = recentAvailability.get(recentKey(recent.id, recent.origin));
    return {
      ...recent,
      name: status?.name ?? recent.name,
      version: status?.version ?? recent.version,
      availability: status?.availability ?? "checking",
      message: t(status?.messageKey ?? "status.checking", status?.messageParams),
      connectable: status?.availability === "available",
    };
  });
}

function publicState() {
  return {
    ...appState,
    discovered: publicDiscovery(),
    recents: publicRecents(),
  };
}

function broadcastState() {
  if (!shellWindow || shellWindow.isDestroyed() || shellWindow.webContents.isDestroyed()) return;
  shellWindow.webContents.send("desktop:state", publicState());
}

function updateState(changes) {
  Object.assign(appState, changes);
  broadcastState();
}

function configureSession(targetSession) {
  if (restrictedSessions.has(targetSession)) return;
  restrictSession(targetSession);
  restrictedSessions.add(targetSession);
}

function probeServer(origin, options) {
  const probeSession = session.fromPartition("jastreamer-probes", { cache: false });
  configureSession(probeSession);
  return probeEndpoint(origin, {
    ...options,
    fetchImpl: (url, init) => probeSession.fetch(String(url), init),
  });
}

function layoutRemoteView() {
  if (!shellWindow || !remoteView || !remoteAttached) return;
  const [width, height] = shellWindow.getContentSize();
  remoteView.setBounds({ x: 0, y: REMOTE_TOP, width, height: Math.max(0, height - REMOTE_TOP) });
}

function clearRemoteLanguageObserver(view = null) {
  if (!remoteLanguageObserver || (view && remoteLanguageObserver.view !== view)) return;
  const { cookies, listener } = remoteLanguageObserver;
  remoteLanguageObserver = null;
  cookies.off("changed", listener);
}

async function persistLanguagePreference(value) {
  const language = normalizeLanguage(value);
  if (!language) throw new TypeError(t("main.language.invalid"));
  if (languageStore.get() !== language) await languageStore.set(language);
  setLanguage(language);
  appState.language = language;
  broadcastState();
  return language;
}

function observeRemoteLanguage(view, server, token) {
  const cookies = view.webContents.session.cookies;
  const listener = (_event, cookie, _cause, removed) => {
    if (
      removed ||
      token !== operationGeneration ||
      remoteView !== view ||
      view.webContents.isDestroyed() ||
      !isLanguageCookieForOrigin(cookie, server.origin) ||
      cookie.value === languageStore.get()
    ) {
      return;
    }
    void persistLanguagePreference(cookie.value).catch(() => {});
  };
  cookies.on("changed", listener);
  remoteLanguageObserver = { view, cookies, listener };
}

async function setRemoteLanguageCookie(view, origin, language) {
  await view.webContents.session.cookies.set({
    url: `${origin}/`,
    name: LANGUAGE_COOKIE_NAME,
    value: language,
    path: "/",
    httpOnly: false,
    sameSite: "strict",
    expirationDate: Math.floor(Date.now() / 1_000) + LANGUAGE_COOKIE_LIFETIME_SECONDS,
  });
}

function closeRemoteView() {
  const view = remoteView;
  remoteView = null;
  clearRemoteLanguageObserver(view);
  if (!view) return;
  if (remoteAttached && shellWindow && !shellWindow.isDestroyed()) {
    shellWindow.contentView.removeChildView(view);
  }
  remoteAttached = false;
  if (!view.webContents.isDestroyed()) {
    view.webContents.close({ waitForBeforeUnload: false });
  }
}

function cancelConnection() {
  operationGeneration += 1;
  connectController?.abort();
  connectController = null;
}

function remoteFailureDetail(errorCode, description) {
  const text = `${errorCode ?? ""} ${description ?? ""}`.toUpperCase();
  if (text.includes("CERT") || text.includes("SSL") || text.includes("TLS")) {
    return t("main.remote.cert");
  }
  if (text.includes("NAME_NOT_RESOLVED")) {
    return t("main.remote.name");
  }
  return t("main.remote.load");
}

function failRemoteView(token, detail) {
  if (token !== operationGeneration) return;
  closeRemoteView();
  updateState({
    mode: "error",
    error: { title: t("main.remote.disconnected"), detail, retryable: true },
  });
}

async function loadRemoteServer(server, token) {
  if (!shellWindow || token !== operationGeneration) return;
  const partition = sessionPartitionFor(server.id, server.origin);
  const view = new WebContentsView({
    webPreferences: {
      partition,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      webviewTag: false,
      devTools: false,
      spellcheck: false,
      safeDialogs: true,
    },
  });
  remoteView = view;
  configureSession(view.webContents.session);
  restrictRemoteContents(view.webContents, server.origin);
  view.webContents.on("will-attach-webview", (event) => event.preventDefault());
  view.webContents.on("before-input-event", (event, input) => {
    if (input.key === "F12" || (input.control && input.shift && input.key.toLowerCase() === "i")) {
      event.preventDefault();
    }
  });

  let failed = false;
  let loadTimer;
  const showFailure = (detail) => {
    clearTimeout(loadTimer);
    if (failed || token !== operationGeneration || remoteView !== view) return;
    failed = true;
    failRemoteView(token, detail);
  };

  view.webContents.on("did-fail-load", (_event, errorCode, errorDescription, _validatedUrl, isMainFrame) => {
    if (!isMainFrame || errorCode === -3) return;
    showFailure(remoteFailureDetail(errorCode, errorDescription));
  });
  view.webContents.on("render-process-gone", () => {
    showFailure(t("main.remote.processGone"));
  });
  view.webContents.on("unresponsive", () => {
    showFailure(t("main.remote.unresponsive"));
  });
  view.webContents.once("destroyed", () => {
    clearTimeout(loadTimer);
    clearRemoteLanguageObserver(view);
  });
  view.webContents.once("did-finish-load", () => {
    if (failed || token !== operationGeneration || remoteView !== view || !shellWindow) return;
    shellWindow.contentView.addChildView(view);
    remoteAttached = true;
    layoutRemoteView();
    updateState({ mode: "connected", error: null });
    clearTimeout(loadTimer);
  });
  loadTimer = setTimeout(
    () => showFailure(t("main.remote.timeout")),
    20_000,
  );
  loadTimer.unref?.();

  try {
    await setRemoteLanguageCookie(view, server.origin, getLanguage());
  } catch {
    showFailure(t("main.language.cookieFailed"));
    return;
  }
  if (failed || token !== operationGeneration || remoteView !== view) return;
  observeRemoteLanguage(view, server, token);
  try {
    await view.webContents.loadURL(`${server.origin}/`);
  } catch (error) {
    showFailure(remoteFailureDetail(error?.errno, error?.message));
  }
}

function listedSelection(origin, expectedId) {
  if (!expectedId) return null;
  const id = normalizeServerId(expectedId);
  const discoveredMatch = publicDiscovery().find(
    (item) => item.id === id && item.origin === origin && item.connectable,
  );
  if (discoveredMatch) return discoveredMatch;
  return publicRecents().find((item) => item.id === id && item.origin === origin) ?? null;
}

async function connectToServer(input) {
  const origin = normalizeEndpoint(input?.origin ?? "");
  const expectedId = input?.expectedId ? normalizeServerId(input.expectedId) : null;
  const selected = listedSelection(origin, expectedId);
  if (expectedId && !selected) {
    throw new Error(t("main.selection.stale"));
  }

  cancelConnection();
  closeRemoteView();
  const token = operationGeneration;
  const controller = new AbortController();
  connectController = controller;
  pendingControllers.add(controller);
  lastAttempt = { origin, expectedId };
  updateState({
    mode: "connecting",
    current: selected
      ? { id: selected.id, name: selected.name, origin, version: selected.version }
      : { id: null, name: t("main.server.verifying"), origin, version: null },
    error: null,
  });

  try {
    const server = await probeServer(origin, {
      expectedId,
      timeoutMs: 5_000,
      signal: controller.signal,
    });
    if (token !== operationGeneration) return;

    try {
      await store.upsert(server);
    } catch (error) {
      throw new Error(t("main.recents.saveFailed"), { cause: error });
    }
    if (token !== operationGeneration) return;

    recentAvailability.set(recentKey(server.id, server.origin), {
      availability: "available",
      messageKey: "status.available",
    });
    appState.current = server;
    lastAttempt = { origin: server.origin, expectedId: server.id };
    broadcastState();
    await loadRemoteServer(server, token);
  } catch (error) {
    if (controller.signal.aborted || token !== operationGeneration) return;
    updateState({
      mode: "error",
      error: {
        title: t("shell.error.title"),
        detail: error?.message || probeErrorMessage(error),
        retryable: true,
      },
    });
  } finally {
    pendingControllers.delete(controller);
    if (connectController === controller) connectController = null;
  }
}

async function probeRecentServers() {
  if (recentProbePromise) return recentProbePromise;

  const recents = store?.list() ?? [];
  for (const recent of recents) {
    recentAvailability.set(recentKey(recent.id, recent.origin), {
      availability: "checking",
      messageKey: "status.checking",
    });
  }
  broadcastState();

  recentProbePromise = (async () => {
    let cursor = 0;
    const worker = async () => {
      while (!shuttingDown) {
        const recent = recents[cursor++];
        if (!recent) return;
        const controller = new AbortController();
        pendingControllers.add(controller);
        try {
          const verified = await probeServer(recent.origin, {
            expectedId: recent.id,
            timeoutMs: 3_500,
            signal: controller.signal,
          });
          recentAvailability.set(recentKey(recent.id, recent.origin), {
            name: verified.name,
            version: verified.version,
            availability: "available",
            messageKey: "status.available",
          });
        } catch (error) {
          if (!controller.signal.aborted) {
            recentAvailability.set(recentKey(recent.id, recent.origin), {
              availability: "unavailable",
              messageKey: error?.messageKey ?? "probe.unknown",
              messageParams: error?.params,
            });
          }
        } finally {
          pendingControllers.delete(controller);
          broadcastState();
        }
      }
    };
    await Promise.all(
      Array.from({ length: Math.min(MAX_PARALLEL_RECENT_PROBES, recents.length) }, () => worker()),
    );
  })().finally(() => {
    recentProbePromise = null;
  });
  return recentProbePromise;
}

async function refreshServers() {
  updateState({ refreshing: true });
  discovery?.refresh();
  try {
    await probeRecentServers();
  } finally {
    updateState({ refreshing: false });
  }
}

function changeServer() {
  cancelConnection();
  closeRemoteView();
  lastAttempt = null;
  updateState({ mode: "selection", current: null, error: null });
}

function assertTrustedSender(event) {
  if (!isTrustedShellSender(event, shellWindow?.webContents, SHELL_URL)) {
    throw new Error(t("main.ipc.denied"));
  }
}

function registerIpc() {
  ipcMain.handle("desktop:get-state", (event) => {
    assertTrustedSender(event);
    return publicState();
  });
  ipcMain.handle("desktop:refresh", async (event) => {
    assertTrustedSender(event);
    await refreshServers();
    return publicState();
  });
  ipcMain.handle("desktop:connect", async (event, input) => {
    assertTrustedSender(event);
    await connectToServer(input);
    return publicState();
  });
  ipcMain.handle("desktop:retry", async (event) => {
    assertTrustedSender(event);
    if (!lastAttempt) throw new Error(t("main.retry.none"));
    await connectToServer(lastAttempt);
    return publicState();
  });
  ipcMain.handle("desktop:change-server", (event) => {
    assertTrustedSender(event);
    changeServer();
    return publicState();
  });
  ipcMain.handle("desktop:set-language", async (event, value) => {
    assertTrustedSender(event);
    const language = normalizeLanguage(value);
    if (!language) throw new TypeError(t("main.language.invalid"));
    try {
      await persistLanguagePreference(language);
      if (remoteView && !remoteView.webContents.isDestroyed() && appState.current?.origin) {
        await setRemoteLanguageCookie(remoteView, appState.current.origin, language);
      }
    } catch (error) {
      throw new Error(t("main.language.saveFailed"), { cause: error });
    }
    return publicState();
  });
}

function createShellWindow() {
  const window = new BrowserWindow({
    width: 1180,
    height: 780,
    minWidth: 720,
    minHeight: 520,
    show: false,
    backgroundColor: "#111315",
    title: "JASTREAMER",
    icon: fileURLToPath(new URL("./assets/jastreamer.png", import.meta.url)),
    autoHideMenuBar: true,
    webPreferences: {
      preload: PRELOAD_PATH,
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
      webviewTag: false,
      spellcheck: false,
      devTools: !app.isPackaged,
    },
  });
  shellWindow = window;
  configureSession(window.webContents.session);
  restrictLocalContents(window.webContents, SHELL_URL);
  window.webContents.on("will-attach-webview", (event) => event.preventDefault());
  window.on("resize", layoutRemoteView);
  window.on("closed", () => {
    cancelConnection();
    closeRemoteView();
    shellWindow = null;
  });
  window.once("ready-to-show", () => window.show());
  window.loadFile(SHELL_PATH).catch((error) => {
    dialog.showErrorBox(t("main.shell.title"), t("main.shell.openFailed", { message: error.message }));
    app.quit();
  });
}

async function startApplication() {
  try {
    await ensureWritableDirectory(userDataPath);
    languageStore = new LanguagePreferenceStore(userDataPath);
    const language = await languageStore.load();
    setLanguage(language);
    appState.language = language;
    store = new RecentServerStore(userDataPath);
    await store.load();
  } catch {
    dialog.showErrorBox(
      t("main.data.title"),
      t("main.data.unavailable", { path: userDataPath }),
    );
    app.quit();
    return;
  }

  Menu.setApplicationMenu(null);
  configureSession(session.defaultSession);
  registerIpc();
  createShellWindow();
  discovery = new LanDiscovery({ onChange: broadcastState, probe: probeServer });
  discovery.start();
  void probeRecentServers();
  recentProbeTimer = setInterval(() => void probeRecentServers(), RECENT_PROBE_INTERVAL_MS);
  recentProbeTimer.unref?.();
}

app.on("second-instance", () => {
  if (!shellWindow) return;
  if (shellWindow.isMinimized()) shellWindow.restore();
  shellWindow.show();
  shellWindow.focus();
});

app.on("window-all-closed", () => app.quit());
app.on("before-quit", () => {
  shuttingDown = true;
  clearInterval(recentProbeTimer);
  discovery?.stop();
  cancelConnection();
  for (const controller of pendingControllers) controller.abort();
  pendingControllers.clear();
  closeRemoteView();
});

if (ownsSingleInstance) {
  app.whenReady().then(startApplication).catch((error) => {
    dialog.showErrorBox(t("main.start.title"), error?.message ?? String(error));
    app.quit();
  });
}
