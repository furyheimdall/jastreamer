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
  application = await _electron.launch({ executablePath: path.join(portable, path.basename(desktopBinary)), timeout: 20000 });
  const shell = await application.firstWindow();
  await shell.locator('#manual-origin').waitFor({ state: 'visible' });
  const dataPath = await application.evaluate(({ app }) => app.getPath('userData'));
  assert.equal(path.resolve(dataPath), path.join(portable, 'user-data'), 'Portable profile must follow the executable');
  return shell;
}
async function connect(shell, origin) {
  await shell.locator('#manual-origin').fill(origin);
  await shell.locator('#manual-form button').click();
  const page = await until(() => application.context().pages().find((candidate) => candidate.url() === origin + '/'), 'Remote Web view did not open').catch(async (error) => {
    const contents = await application.evaluate(({ webContents }) => webContents.getAllWebContents().map((content) => ({ id: content.id, type: content.getType(), url: content.getURL() })));
    const shellState = await shell.locator('body').innerText();
    throw new Error(`${error.message}; contents=${JSON.stringify(contents)}; shell=${shellState}`, { cause: error });
  });
  await page.waitForLoadState('domcontentloaded');
  await until(async () => (await shell.locator('#connection-state').innerText()) === '연결됨', 'Trusted connection bar did not become ready');
  return page;
}
async function createAccount(page, username) {
  await page.getByLabel('사용자 이름', { exact: true }).fill(username);
  await page.getByLabel('비밀번호', { exact: true }).fill(password);
  await page.getByLabel('비밀번호 확인', { exact: true }).fill(password);
  await page.getByRole('button', { name: '계정 만들기', exact: true }).click();
  await page.getByLabel('재생 기기', { exact: true }).waitFor({ state: 'visible' });
}
async function username(page) {
  return page.evaluate(async () => (await (await fetch('/api/v1/session')).json()).user?.username);
}
try {
  await cp(path.dirname(desktopBinary), portable, { recursive: true, filter: (source) => path.basename(source) !== 'user-data' });
  const [a, b] = await Promise.all([startServer('Desktop A'), startServer('Desktop B')]);
  let shell = await launch();
  let page = await connect(shell, a);
  await createAccount(page, 'desktop-a');
  assert.equal(await username(page), 'desktop-a');
  const privileges = await page.evaluate(() => ({ node: typeof require, process: typeof process, popupBlocked: window.open('https://example.invalid/') === null }));
  assert.deepEqual(privileges, { node: 'undefined', process: 'undefined', popupBlocked: true });
  await shell.locator('#change-server').click();
  page = await connect(shell, b);
  assert.equal(await username(page), undefined, 'A session must not enter B at another port of the same host');
  await createAccount(page, 'desktop-b');
  await shell.locator('#change-server').click();
  page = await connect(shell, a);
  await page.getByLabel('재생 기기', { exact: true }).waitFor({ state: 'visible' });
  assert.equal(await username(page), 'desktop-a', 'B login must not overwrite A session');
  await application.close();
  application = null;
  assert.deepEqual(commands, [], 'Switching and closing must not send queue or playback commands');
  const moved = path.join(work, 'moved portable with spaces');
  await rename(portable, moved);
  portable = moved;
  shell = await launch();
  assert.equal(application.context().pages().some((candidate) => candidate.url() === a + '/'), false, 'Restart must not auto-connect');
  page = await connect(shell, a);
  await page.getByLabel('재생 기기', { exact: true }).waitFor({ state: 'visible' });
  assert.equal(await username(page), 'desktop-a', 'Moving the complete portable directory must retain the session on the same user/machine');
  await application.close();
  application = null;
  assert.deepEqual(commands, []);
  assert.equal((await fetch(a + '/healthz')).status, 200, 'Server must remain alive after desktop exits');
  console.log(JSON.stringify({ packagedLaunch: true, platform: process.platform, twoServerSessionIsolation: true, portableMoveSessionRetained: true, noAutomaticConnection: true, remoteNodeBlocked: true, remotePopupBlocked: true, playbackCommandsOnSwitchOrExit: commands.length }));
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
  await rm(work, { recursive: true, force: true });
}
