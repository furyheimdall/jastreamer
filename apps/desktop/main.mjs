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
import { probeEndpoint, probeErrorMessage } from "./lib/probe.mjs";
import {
  isTrustedShellSender,
  normalizeEndpoint,
  normalizeServerId,
  restrictLocalContents,
  restrictRemoteContents,
  restrictSession,
  sessionPartitionFor,
} from "./lib/security.mjs";
import {
  ensureWritableDirectory,
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
    "JASTREAMER 시작 실패",
    `휴대용 데이터 폴더를 만들 수 없습니다.\n\n${userDataPath}\n\n쓰기 가능한 폴더로 앱을 이동한 뒤 다시 실행해 주세요.`,
  );
  app.exit(1);
}

const ownsSingleInstance = !bootstrapFailed && app.requestSingleInstanceLock();
if (!bootstrapFailed && !ownsSingleInstance) app.quit();

let shellWindow = null;
let remoteView = null;
let remoteAttached = false;
let store = null;
let discovery = null;
let operationGeneration = 0;
let connectController = null;
let recentProbePromise = null;
let recentProbeTimer = null;
let shuttingDown = false;
let lastAttempt = null;
const pendingControllers = new Set();
const restrictedSessions = new WeakSet();
const recentAvailability = new Map();

const appState = {
  mode: "selection",
  current: null,
  error: null,
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
      message: status?.message ?? "서버를 확인하는 중입니다.",
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

function closeRemoteView() {
  const view = remoteView;
  remoteView = null;
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
    return "HTTPS 인증서를 확인할 수 없습니다. 인증서 유효 기간과 서버 이름을 확인해 주세요.";
  }
  if (text.includes("NAME_NOT_RESOLVED")) {
    return "서버 이름을 찾을 수 없습니다. 주소와 네트워크 연결을 확인해 주세요.";
  }
  return "서버 화면을 불러올 수 없습니다. 서버와 네트워크 상태를 확인한 뒤 다시 시도해 주세요.";
}

function failRemoteView(token, detail) {
  if (token !== operationGeneration) return;
  closeRemoteView();
  updateState({
    mode: "error",
    error: { title: "서버 연결이 끊어졌습니다", detail, retryable: true },
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
    showFailure("서버 화면 프로세스가 종료되었습니다. 다시 연결해 주세요.");
  });
  view.webContents.on("unresponsive", () => {
    showFailure("서버 화면이 응답하지 않습니다. 네트워크 상태를 확인한 뒤 다시 시도해 주세요.");
  });
  view.webContents.once("destroyed", () => clearTimeout(loadTimer));
  view.webContents.once("did-finish-load", () => {
    if (failed || token !== operationGeneration || remoteView !== view || !shellWindow) return;
    shellWindow.contentView.addChildView(view);
    remoteAttached = true;
    layoutRemoteView();
    updateState({ mode: "connected", error: null });
    clearTimeout(loadTimer);
  });
  loadTimer = setTimeout(
    () => showFailure("서버 화면을 불러오는 시간이 초과되었습니다. 네트워크 상태를 확인해 주세요."),
    20_000,
  );
  loadTimer.unref?.();

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
    throw new Error("현재 서버 목록에서 이 항목을 찾을 수 없습니다. 새로 고침한 뒤 다시 시도해 주세요.");
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
      : { id: null, name: "서버 확인 중", origin, version: null },
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
      throw new Error("최근 서버 정보를 휴대용 데이터 폴더에 저장할 수 없습니다.", { cause: error });
    }
    if (token !== operationGeneration) return;

    recentAvailability.set(recentKey(server.id, server.origin), {
      availability: "available",
      message: "연결할 수 있습니다.",
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
        title: "서버에 연결할 수 없습니다",
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
      message: "서버를 확인하는 중입니다.",
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
            message: "연결할 수 있습니다.",
          });
        } catch (error) {
          if (!controller.signal.aborted) {
            recentAvailability.set(recentKey(recent.id, recent.origin), {
              availability: "unavailable",
              message: probeErrorMessage(error),
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
    throw new Error("허용되지 않은 IPC 요청입니다.");
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
    if (!lastAttempt) throw new Error("다시 연결할 서버가 없습니다.");
    await connectToServer(lastAttempt);
    return publicState();
  });
  ipcMain.handle("desktop:change-server", (event) => {
    assertTrustedSender(event);
    changeServer();
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
    dialog.showErrorBox("JASTREAMER 화면 오류", `서버 선택 화면을 열 수 없습니다.\n\n${error.message}`);
    app.quit();
  });
}

async function startApplication() {
  try {
    await ensureWritableDirectory(userDataPath);
    store = new RecentServerStore(userDataPath);
    await store.load();
  } catch {
    dialog.showErrorBox(
      "JASTREAMER 데이터 폴더 오류",
      `휴대용 데이터 폴더를 읽거나 쓸 수 없습니다.\n\n${userDataPath}\n\n폴더 권한을 확인하거나 앱을 쓰기 가능한 위치로 이동해 주세요.`,
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
    dialog.showErrorBox("JASTREAMER 시작 실패", error?.message ?? String(error));
    app.quit();
  });
}
