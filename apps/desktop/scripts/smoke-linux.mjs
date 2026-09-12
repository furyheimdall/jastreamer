import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import { spawn } from "node:child_process";
import { once } from "node:events";
import {
  lstat,
  mkdtemp,
  mkdir,
  readFile,
  readdir,
  readlink,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import { createServer as createHTTPServer, request } from "node:http";
import { createServer as createTCPServer } from "node:net";
import { tmpdir } from "node:os";
import path from "node:path";
import { _electron } from "playwright-core";

const [serverArgument, desktopArgument] = process.argv.slice(2);
if (!serverArgument || !desktopArgument) {
  throw new Error("Usage: node scripts/smoke-linux.mjs <server-binary> <installed-desktop-binary>");
}
const serverBinary = path.resolve(serverArgument);
const desktopBinary = path.resolve(desktopArgument);
assert.equal(process.platform, "linux", "The installed Linux smoke must run on native Linux");
assert.equal(process.arch, "x64", "The installed Linux smoke must run on native amd64");
assert.equal(desktopBinary, "/usr/lib/jastreamer-desktop/jastreamer-desktop", "Smoke the installed DEB executable, not a staging copy");
assert.equal(typeof process.getuid, "function");
assert.notEqual(process.getuid(), 0, "The desktop smoke must run as an ordinary user, never root");

const work = await mkdtemp(path.join(tmpdir(), "jastreamer-desktop-linux-smoke-"));
const home = path.join(work, "home");
const configHome = path.join(home, ".config");
const cacheHome = path.join(home, ".cache");
const runtimeDirectory = path.join(work, "runtime");
const expectedDataPath = path.join(configHome, "jastreamer-desktop");
const children = [];
const proxies = [];
const commands = [];
const password = randomBytes(24).toString("base64url");
const launchEnvironment = {
  ...process.env,
  HOME: home,
  XDG_CONFIG_HOME: configHome,
  XDG_CACHE_HOME: cacheHome,
  XDG_RUNTIME_DIR: runtimeDirectory,
};
let application;
let sandboxEvidence;
const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function until(callback, message, timeout = 15_000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    const result = await callback();
    if (result) return result;
    await sleep(50);
  }
  throw new Error(message);
}

async function optionalFile(file) {
  try {
    return (await readFile(file, "utf8")).trim();
  } catch (error) {
    return `<unavailable: ${error.code || error.message}>`;
  }
}

async function installationEvidence() {
  const executable = await stat(desktopBinary);
  const helperPath = path.join(path.dirname(desktopBinary), "chrome-sandbox");
  const helper = await stat(helperPath);
  return {
    executable: {
      path: desktopBinary,
      uid: executable.uid,
      gid: executable.gid,
      mode: (executable.mode & 0o7777).toString(8).padStart(4, "0"),
    },
    chromeSandbox: {
      path: helperPath,
      uid: helper.uid,
      gid: helper.gid,
      mode: (helper.mode & 0o7777).toString(8).padStart(4, "0"),
    },
    appArmorRestriction: await optionalFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"),
    appArmorEnabled: await optionalFile("/sys/module/apparmor/parameters/enabled"),
    appArmorProfile: await optionalFile("/etc/apparmor.d/jastreamer-desktop"),
  };
}

async function freePort() {
  const listener = createTCPServer();
  listener.listen(0, "127.0.0.1");
  await once(listener, "listening");
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  return port;
}

async function startServer(label) {
  const port = await freePort();
  const folder = path.join(work, label);
  await mkdir(folder);
  const configFile = path.join(folder, "server.json");
  await writeFile(configFile, JSON.stringify({
    version: 1,
    server_name: label,
    data_dir: path.join(folder, "data"),
    http: { enabled: true, address: `127.0.0.1:${port}` },
    https: { enabled: false, address: ":8443", certificate_file: "", private_key_file: "" },
    library_roots: [],
    network: { interfaces: [], discovery_interval_seconds: 30, poll_interval_seconds: 1, allowed_cidrs: [] },
    media: { base_url: "", ffmpeg_path: "", transcode: false },
  }));
  const child = spawn(serverBinary, ["--config", configFile], {
    stdio: ["ignore", "ignore", "pipe"],
  });
  children.push(child);
  let diagnostics = "";
  child.stderr.on("data", (chunk) => {
    diagnostics = (diagnostics + chunk).slice(-8192);
  });
  child.on("error", (error) => {
    diagnostics = String(error);
  });
  await until(async () => {
    if (child.exitCode !== null) throw new Error(`Server exited: ${diagnostics}`);
    try {
      return (await fetch(`http://127.0.0.1:${port}/healthz`, { signal: AbortSignal.timeout(500) })).ok;
    } catch {
      return false;
    }
  }, `Server startup timed out: ${label}`);

  const proxy = createHTTPServer((incoming, outgoing) => {
    if (incoming.method !== "GET" && /^\/api\/v1\/(?:player|queue)(?:\/|$)/.test(incoming.url)) {
      commands.push({ method: incoming.method, path: incoming.url });
    }
    const upstream = request({
      hostname: "127.0.0.1",
      port,
      path: incoming.url,
      method: incoming.method,
      headers: incoming.headers,
    }, (response) => {
      outgoing.writeHead(response.statusCode, response.headers);
      response.pipe(outgoing);
    });
    upstream.on("error", () => {
      if (!outgoing.headersSent) outgoing.writeHead(502);
      outgoing.end();
    });
    outgoing.on("close", () => upstream.destroy());
    incoming.pipe(upstream);
  });
  proxy.listen(0, "127.0.0.1");
  await once(proxy, "listening");
  proxies.push(proxy);
  return `http://127.0.0.1:${proxy.address().port}`;
}

async function launch() {
  try {
    application = await _electron.launch({
      executablePath: desktopBinary,
      chromiumSandbox: true,
      env: launchEnvironment,
      timeout: 20_000,
    });
    const shell = await application.firstWindow();
    await shell.locator("#manual-origin").waitFor({ state: "visible" });
    const dataPath = await application.evaluate(({ app }) => app.getPath("userData"));
    assert.equal(path.resolve(dataPath), expectedDataPath, "Installed Linux profile must use the ordinary user's XDG configuration root");
    return shell;
  } catch (error) {
    const diagnostics = await installationEvidence().catch((failure) => ({ unavailable: failure.message }));
    throw new Error(`Installed desktop launch failed with Chromium sandboxing required. diagnostics=${JSON.stringify(diagnostics)}; cause=${error.message}`, { cause: error });
  }
}

async function connect(shell, origin) {
  await shell.locator("#manual-origin").fill(origin);
  await shell.locator("#manual-form button").click();
  const page = await until(
    () => application.context().pages().find((candidate) => candidate.url() === `${origin}/`),
    "Remote WebContentsView did not open",
  ).catch(async (error) => {
    const contents = await application.evaluate(({ webContents }) => webContents.getAllWebContents().map((content) => ({
      id: content.id,
      pid: content.getOSProcessId(),
      type: content.getType(),
      url: content.getURL(),
    })));
    const shellState = await shell.locator("body").innerText();
    throw new Error(`${error.message}; contents=${JSON.stringify(contents)}; shell=${shellState}`, { cause: error });
  });
  await page.waitForLoadState("domcontentloaded");
  const expectedState = await shell.locator("#language").inputValue() === "ko" ? "연결됨" : "Connected";
  await until(
    async () => (await shell.locator("#connection-state").innerText()) === expectedState,
    "Trusted connection bar did not become ready",
  );
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
  return page.evaluate(async () => (await (await fetch("/api/v1/session")).json()).user?.username);
}

function statusField(status, name) {
  const match = status.match(new RegExp(`^${name}:\\s*(.+)$`, "m"));
  assert(match, `Missing ${name} in renderer /proc status`);
  return match[1].trim();
}

async function inspectRendererSandbox(origin) {
  const renderer = await application.evaluate(({ webContents }, expectedUrl) => {
    const content = webContents.getAllWebContents().find((candidate) => candidate.getURL() === expectedUrl);
    if (!content) return null;
    return {
      pid: content.getOSProcessId(),
      type: content.getType(),
      electronSandboxPreference: content.getLastWebPreferences().sandbox,
    };
  }, `${origin}/`);
  assert(renderer?.pid > 1, "Could not identify the remote renderer OS process");
  assert.equal(renderer.type, "window");
  assert.equal(renderer.electronSandboxPreference, true, "Electron renderer sandbox preference must remain enabled");

  const processRoot = `/proc/${renderer.pid}`;
  const status = await readFile(path.join(processRoot, "status"), "utf8");
  const commandLine = (await readFile(path.join(processRoot, "cmdline"))).toString("utf8").split("\0").filter(Boolean);
  const uidValues = statusField(status, "Uid").split(/\s+/).map(Number);
  const capEff = statusField(status, "CapEff");
  const noNewPrivileges = Number(statusField(status, "NoNewPrivs"));
  const seccomp = Number(statusField(status, "Seccomp"));
  const seccompFilters = Number(statusField(status, "Seccomp_filters"));
  assert.equal(uidValues[0], process.getuid(), "Renderer real UID must be the ordinary smoke user");
  assert.equal(uidValues[1], process.getuid(), "Renderer effective UID must not be elevated");
  assert(/^0+$/.test(capEff), `Renderer has effective capabilities: ${capEff}`);
  assert.equal(noNewPrivileges, 1, "Renderer must have Linux no_new_privs enabled");
  assert.equal(seccomp, 2, "Renderer must run under a seccomp filter");
  assert(seccompFilters >= 1, "Renderer must have at least one seccomp filter");
  assert(commandLine.includes("--type=renderer"), "Inspected process is not a Chromium renderer");
  assert(!commandLine.includes("--no-sandbox"), "Renderer was launched with --no-sandbox");
  assert(!commandLine.includes("--disable-setuid-sandbox"), "Renderer disabled the setuid sandbox");
  assert(!commandLine.includes("--disable-seccomp-filter-sandbox"), "Renderer disabled the seccomp sandbox");

  const browserPid = application.process().pid;
  const browserCommandLine = (await readFile(`/proc/${browserPid}/cmdline`)).toString("utf8").split("\0").filter(Boolean);
  const browserStatus = await readFile(`/proc/${browserPid}/status`, "utf8");
  const browserSeccompFilters = Number(statusField(browserStatus, "Seccomp_filters"));
  assert(seccompFilters > browserSeccompFilters, `Renderer did not add a Chromium seccomp filter (browser=${browserSeccompFilters}, renderer=${seccompFilters})`);
  assert(!browserCommandLine.includes("--no-sandbox"), "Electron browser process was launched with --no-sandbox");
  assert(!browserCommandLine.includes("--disable-setuid-sandbox"), "Electron browser process disabled the setuid sandbox");
  const appArmorLabel = await optionalFile(path.join(processRoot, "attr/current"));
  assert.match(appArmorLabel, /jastreamer-desktop/, "Renderer did not inherit the installed executable-specific AppArmor profile");
  return {
    rendererPid: renderer.pid,
    rendererType: renderer.type,
    noNewPrivileges,
    seccomp,
    seccompFilters,
    browserSeccompFilters,
    effectiveCapabilities: capEff,
    uid: uidValues[1],
    uidMap: await optionalFile(path.join(processRoot, "uid_map")),
    appArmorLabel,
    userNamespace: await readlink(path.join(processRoot, "ns/user")),
    forbiddenSandboxSwitches: false,
  };
}

async function assertPrivateUserFile(file) {
  const details = await stat(file);
  assert.equal(details.uid, process.getuid(), `${file} must belong to the ordinary user`);
  assert.equal(details.mode & 0o077, 0, `${file} must not grant group or other permissions`);
}

async function persistentPartitions() {
  const directory = path.join(expectedDataPath, "Partitions");
  const entries = await readdir(directory, { withFileTypes: true });
  return entries.filter((entry) => entry.isDirectory() && entry.name.startsWith("jastreamer-")).map((entry) => entry.name).sort();
}

try {
  await mkdir(home, { mode: 0o700 });
  await mkdir(configHome, { mode: 0o700 });
  await mkdir(cacheHome, { mode: 0o700 });
  await mkdir(runtimeDirectory, { mode: 0o700 });
  const installation = await installationEvidence();
  assert.deepEqual(installation.executable, {
    path: desktopBinary,
    uid: 0,
    gid: 0,
    mode: "0755",
  }, "Installed executable must be root-owned and not user-writable");
  assert.deepEqual(installation.chromeSandbox, {
    path: "/usr/lib/jastreamer-desktop/chrome-sandbox",
    uid: 0,
    gid: 0,
    mode: "4755",
  }, "chrome-sandbox must be root:root mode 4755");
  assert.match(installation.appArmorProfile, /^\/usr\/lib\/jastreamer-desktop\/jastreamer-desktop flags=\(unconfined\) \{$/m, "Missing executable-specific AppArmor attachment");
  assert.match(installation.appArmorProfile, /^\s+userns,$/m, "AppArmor profile must grant only this executable user namespaces");
  assert.match(installation.appArmorEnabled, /^[Yy]$/, "AppArmor must remain enabled for the Ubuntu 24.04 smoke");
  assert.equal(installation.appArmorRestriction, "1", "Ubuntu's unprivileged user-namespace restriction must remain enabled");

  const [a, b] = await Promise.all([startServer("Desktop A"), startServer("Desktop B")]);
  let shell = await launch();
  assert.equal(await shell.locator("#language").inputValue(), "en", "A new per-user profile must default to English");
  assert.equal(await shell.locator("#manual-heading").innerText(), "Connect by address");
  let page = await connect(shell, a);
  await createAccount(page, "desktop-a");
  assert.equal(await username(page), "desktop-a");
  sandboxEvidence = await inspectRendererSandbox(a);
  const privileges = await page.evaluate(() => ({
    node: typeof require,
    process: typeof process,
    desktopBridge: typeof window.jastreamerDesktop,
    popupBlocked: window.open("https://example.invalid/") === null,
  }));
  assert.deepEqual(privileges, {
    node: "undefined",
    process: "undefined",
    desktopBridge: "undefined",
    popupBlocked: true,
  });
  assert.match(await page.evaluate(() => document.cookie), /(?:^|; )jastreamer_language=en(?:;|$)/, "The active server partition must receive English before load");

  await shell.locator("#change-server").click();
  page = await connect(shell, b);
  assert.equal(await username(page), undefined, "A session must not enter B at another port of the same host");
  await createAccount(page, "desktop-b");
  await shell.locator("#change-server").click();
  page = await connect(shell, a);
  await page.getByLabel("Output device", { exact: true }).waitFor({ state: "visible" });
  assert.equal(await username(page), "desktop-a", "B login must not overwrite A session");
  await shell.locator("#change-server").click();
  await shell.locator("#language").selectOption("ko");
  await until(async () => (await shell.locator("#manual-heading").innerText()) === "주소로 연결", "Local language selection did not apply");
  await application.close();
  application = null;
  assert.deepEqual(commands, [], "Switching and closing must not send queue or playback commands");

  await assertPrivateUserFile(path.join(expectedDataPath, "preferences.json"));
  await assertPrivateUserFile(path.join(expectedDataPath, "recents.json"));
  const partitions = await persistentPartitions();
  assert.equal(partitions.length, 2, `Expected two isolated persistent session partitions, found ${partitions.join(", ")}`);

  shell = await launch();
  assert.equal(await shell.locator("#language").inputValue(), "ko", "Installed restart must retain the per-user language preference");
  assert.equal(application.context().pages().some((candidate) => candidate.url() === `${a}/` || candidate.url() === `${b}/`), false, "Restart must not auto-connect");
  await shell.locator("#language").selectOption("en");
  await until(async () => (await shell.locator("#manual-heading").innerText()) === "Connect by address", "English selection did not apply");
  page = await connect(shell, a);
  await page.getByLabel("Output device", { exact: true }).waitFor({ state: "visible" });
  assert.equal(await username(page), "desktop-a", "Installed restart must retain the ordinary user's server session");
  await application.close();
  application = null;
  assert.deepEqual(commands, []);
  assert.equal((await fetch(`${a}/healthz`)).status, 200, "Server must remain alive after desktop exits");

  console.log(JSON.stringify({
    packagedLaunch: true,
    platform: process.platform,
    arch: process.arch,
    installedExecutable: desktopBinary,
    ordinaryUserUid: process.getuid(),
    userDataPath: expectedDataPath,
    perUserSettingsWritable: true,
    twoServerSessionIsolation: true,
    installedRestartSessionRetained: true,
    installedRestartLanguageRetained: true,
    noAutomaticConnection: true,
    remoteNodeBlocked: true,
    remoteDesktopBridgeBlocked: true,
    remotePopupBlocked: true,
    playbackCommandsOnSwitchOrExit: commands.length,
    chromeSandbox: installation.chromeSandbox,
    appArmorRestriction: installation.appArmorRestriction,
    appArmorEnabled: installation.appArmorEnabled,
    sandbox: sandboxEvidence,
  }));
} finally {
  if (application) await application.close().catch(() => {});
  for (const proxy of proxies) {
    proxy.closeAllConnections();
    proxy.close();
  }
  for (const child of children) {
    if (child.exitCode === null) {
      child.kill();
      await Promise.race([once(child, "exit"), sleep(3000)]);
      if (child.exitCode === null) child.kill("SIGKILL");
    }
  }
  await rm(work, { recursive: true, force: true });
}
