import { spawn, spawnSync } from 'node:child_process';
import { createHash, X509Certificate } from 'node:crypto';
import { createServer as createSecureServer } from 'node:https';
import { copyFile, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';
import { tmpdir } from 'node:os';

export const trackServerConnections = (server) => {
  const sockets = new Set();
  const track = (socket) => {
    sockets.add(socket);
    socket.once('close', () => sockets.delete(socket));
  };
  return {
    track,
    close: async () => {
      if (server.listening) server.close();
      let destroyedSockets = 0;
      for (const socket of sockets) {
        if (!socket.destroyed) {
          socket.destroy();
          destroyedSockets++;
        }
      }
      return { destroyedSockets };
    },
  };
};

export const startWeb = async (webRoot, { certificatePath, keyPath }) => {
  const [certificate, key] = await Promise.all([
    readFile(certificatePath),
    readFile(keyPath),
  ]);
  const server = createSecureServer({ cert: certificate, key }, async (request, response) => {
    const raw = new URL(request.url ?? '/', 'http://control.local').pathname;
    const path = normalize(join(webRoot, raw === '/' ? 'index.html' : raw.slice(1)));
    if (!path.startsWith(webRoot)) { response.writeHead(404).end(); return; }
    try {
      const body = await readFile(path);
      const type = ({ '.html': 'text/html', '.js': 'text/javascript', '.wasm': 'application/wasm', '.json': 'application/json' })[extname(path)] ?? 'application/octet-stream';
      response.writeHead(200, { 'content-type': type, 'cache-control': 'no-store' }).end(body);
    } catch (error) {
      if (error instanceof Error && 'code' in error && error.code === 'ENOENT') { response.writeHead(404).end(); return; }
      throw error;
    }
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (address === null || typeof address === 'string') throw new TypeError('Control web host did not bind TCP');
  return { server, origin: `https://127.0.0.1:${address.port}` };
};

export const startTodo13 = async (serverRoot, controlOrigin, options = {}) => {
  const directory = await mkdtemp(join(tmpdir(), 'jastreamer-control-qa-'));
  const binary = join(directory, 'jastreamer-server');
  const built = spawnSync('go', ['build', '-o', binary, './cmd/jastreamer-server'], { cwd: serverRoot, encoding: 'utf8' });
  if (built.status !== 0) throw new Error(`Server build failed: ${built.stderr}`);
  const music = join(directory, 'music');
  await mkdir(music, { recursive: true });
  if (options.seedMedia !== false) {
    await Promise.all([
      copyFile(join(serverRoot, '../renderer/tests/fixtures/tone.mp3'), join(music, 'first-light.mp3')),
      copyFile(join(serverRoot, '../renderer/tests/fixtures/tone.flac'), join(music, 'second-light.flac')),
    ]);
  }
  const config = join(directory, 'server.json');
  await writeFile(config, JSON.stringify({
    address: '127.0.0.1:0', data_directory: directory, catalog_root: music,
    catalog_migration: join(serverRoot, 'migrations/001_catalog.sql'),
    playback_migration: join(serverRoot, 'migrations/002_playback.sql'),
    playback_expansion: join(serverRoot, 'migrations/003_todo12.sql'),
    certificate_dns: ['localhost'], certificate_ips: ['127.0.0.1'],
    allowed_origins: [controlOrigin], pairing_ttl: '5m',
  }));
  await options.beforeSpawn?.({ directory, music });
  const child = spawn(binary, ['--config', config], {
    cwd: serverRoot,
    env: {
      ...process.env,
      JASTREAMER_SETUP_SECRET: options.setupSecret ?? 'fixture-setup-secret',
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stderr = '';
  child.stderr.on('data', (chunk) => { stderr += chunk.toString(); });
  const ready = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error(`Server readiness timeout: ${stderr}`)), 30_000);
    child.once('exit', (code) => { clearTimeout(timeout); reject(new Error(`Server exited ${code}: ${stderr}`)); });
    child.stdout.on('data', (chunk) => {
      const match = chunk.toString().match(/ready (https:\/\/[^ ]+) fingerprint=([^\s]+)/);
      if (match?.[1] && match[2]) { clearTimeout(timeout); resolve({ origin: match[1], fingerprint: match[2] }); }
    });
  });
  const certificate = await readFile(join(directory, 'identity/tls-cert.pem'));
  const rendererFingerprint = createHash('sha256')
    .update(new X509Certificate(certificate).raw)
    .digest('hex');
  return { child, directory, ...ready, rendererFingerprint };
};

export const stopChild = async (child) => {
  if (child.exitCode !== null || child.signalCode !== null) return;
  let resolveExited;
  const exited = new Promise((resolve) => { resolveExited = resolve; });
  const finish = () => {
    child.off('exit', finish);
    child.off('close', finish);
    resolveExited(true);
  };
  child.once('exit', finish);
  child.once('close', finish);
  child.kill('SIGTERM');
  let timeout;
  const graceful = await Promise.race([
    exited,
    new Promise((resolve) => { timeout = setTimeout(() => resolve(false), 10_000); }),
  ]);
  clearTimeout(timeout);
  if (graceful || child.exitCode !== null || child.signalCode !== null) {
    finish();
    return;
  }
  child.kill('SIGKILL');
  await exited;
};
