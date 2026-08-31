import { spawn } from 'node:child_process';
import { createHash, randomUUID, X509Certificate } from 'node:crypto';
import { createServer as createSecureServer } from 'node:https';
import { copyFile, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';
import { tmpdir } from 'node:os';
import { cleanupControlResources } from './control-cleanup.mjs';
import { stopChild } from './control-process-cleanup.mjs';

export { stopChild } from './control-process-cleanup.mjs';

const ownedServers = new Map();
let signalHandlersInstalled = false;
let signalCleanupStarted = false;
export const certificateSpkiPin = (certificate) => createHash('sha256')
  .update(new X509Certificate(certificate).publicKey.export({ type: 'spki', format: 'der' }))
  .digest('base64');

const cleanupOwnedServer = async (resource, awaitStartup = false, primaryFailure) => {
  ownedServers.delete(resource.directory);
  resource.active = false;
  const initialChild = resource.child;
  try {
    return await cleanupControlResources({
      primaryFailure,
      operations: [
        ...(initialChild ? [{ name: 'server-initial-process', state: 'present', close: () => stopChild(initialChild) }] : []),
        ...(awaitStartup ? [{ name: 'server-startup', state: 'present', close: () => resource.startupDone }] : []),
        { name: 'server-process', state: 'present', close: async () => {
          if (resource.child) await stopChild(resource.child);
        } },
        { name: 'server-temp-root', state: 'present', close: () => rm(resource.directory, { recursive: true, force: true }) },
      ],
    });
  } finally {
    if (ownedServers.size === 0 && !signalCleanupStarted) {
      process.off('SIGINT', handleSignal);
      process.off('SIGTERM', handleSignal);
      signalHandlersInstalled = false;
    }
  }
};

const handleSignal = (signal) => {
  if (signalCleanupStarted) return;
  signalCleanupStarted = true;
  process.off('SIGINT', handleSignal);
  process.off('SIGTERM', handleSignal);
  void Promise.allSettled([...ownedServers.values()].map((resource) =>
    cleanupOwnedServer(resource, true),
  )).then((results) => {
    const failures = results
      .filter(({ status }) => status === 'rejected')
      .map(({ reason }) => reason instanceof Error ? reason.message : String(reason));
    if (failures.length > 0) {
      process.stderr.write(`Control QA signal cleanup failed: ${failures.join('; ')}\n`);
    }
    process.kill(process.pid, signal);
  });
};

const subscribeSignalCleanup = () => {
  if (signalHandlersInstalled) return;
  process.on('SIGINT', handleSignal);
  process.on('SIGTERM', handleSignal);
  signalHandlersInstalled = true;
};

export const trackServerConnections = (server) => {
  const sockets = new Map();
  const track = (socket) => {
    if (sockets.has(socket)) return;
    const release = () => sockets.delete(socket);
    sockets.set(socket, release);
    socket.once('close', release);
  };
  return {
    track,
    close: async () => {
      const closed = server.listening
        ? new Promise((resolve, reject) => server.close((error) => error ? reject(error) : resolve()))
        : Promise.resolve();
      let destroyedSockets = 0;
      for (const [socket, release] of sockets) {
        socket.off('close', release);
        sockets.delete(socket);
        if (!socket.destroyed) {
          socket.destroy();
          destroyedSockets++;
        }
      }
      await closed;
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
  const connections = trackServerConnections(server);
  server.on('connection', connections.track);
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (address === null || typeof address === 'string') throw new TypeError('Control web host did not bind TCP');
  return {
    server,
    origin: `https://127.0.0.1:${address.port}`,
    spkiPinBase64: certificateSpkiPin(certificate),
    close: connections.close,
  };
};

export const createTodo13ReadinessParser = ({ maxBytes = 64 * 1024 } = {}) => {
  let pending = "";
  return Object.freeze({
    push: (chunk) => {
      pending += chunk.toString();
      const lines = pending.split(/\r?\n/);
      pending = lines.pop() ?? "";
      if (Buffer.byteLength(pending) > maxBytes) {
        throw new Error("SERVER_READINESS_OUTPUT_OVERSIZED");
      }
      for (const line of lines) {
        const match = line.match(
          /^ready (https:\/\/[^ ]+) fingerprint=([^\s]+)$/,
        );
        if (match?.[1] && match[2]) {
          return { origin: match[1], fingerprint: match[2] };
        }
      }
      return null;
    },
  });
};

const launchTodo13Process = async (resource) => {
  const child = spawn(resource.binary, ['--config', resource.config], {
    cwd: resource.serverRoot,
    detached: process.platform !== 'win32',
    env: { ...process.env, JASTREAMER_SETUP_SECRET: resource.setupSecret },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  child.controlQaProcessGroup = process.platform !== 'win32';
  resource.child = child;
  let stderr = ''; const counters = { stdoutBytes: 0, stderrBytes: 0, eventLogLines: 0, snapshotWriteLogLines: 0 }; let stdoutLines = ''; let stderrLines = ''; const observeLines = (text, stream) => { const combined = `${stream === 'stdout' ? stdoutLines : stderrLines}${text}`; const lines = combined.split(/\r?\n/); if (stream === 'stdout') stdoutLines = lines.pop() ?? ''; else stderrLines = lines.pop() ?? ''; for (const line of lines) { if (/(event|websocket)/i.test(line)) counters.eventLogLines += 1; if (/(snapshot).*(write|sent)|(?:write|sent).*(snapshot)/i.test(line)) counters.snapshotWriteLogLines += 1; } }; child.stdout.on('data', (chunk) => { counters.stdoutBytes += chunk.length; observeLines(chunk.toString(), 'stdout'); }); child.stderr.on('data', (chunk) => { stderr += chunk.toString(); counters.stderrBytes += chunk.length; observeLines(chunk.toString(), 'stderr'); }); resource.serverDiagnostics = () => Object.freeze({ processPid: child.pid, alive: child.exitCode === null && child.signalCode === null, ...counters });
  const ready = await new Promise((resolve, reject) => {
    const parser = createTodo13ReadinessParser();
    const onData = (chunk) => {
      try {
        const result = parser.push(chunk);
        if (result !== null) {
          clearTimeout(timeout);
          child.stdout.off('data', onData);
          resolve(result);
        }
      } catch (error) {
        clearTimeout(timeout);
        child.stdout.off('data', onData);
        reject(error);
      }
    };
    const timeout = setTimeout(() => {
      child.stdout.off('data', onData);
      reject(new Error(`Server readiness timeout: ${stderr}`));
    }, 30_000);
    child.once('exit', (code) => { clearTimeout(timeout); reject(new Error(`Server exited ${code}: ${stderr}`)); });
    child.stdout.on('data', onData);
  });
  return { child, ...ready, diagnostics: resource.serverDiagnostics };
};

export const startTodo13 = async (serverRoot, controlOrigin, options = {}) => {
  subscribeSignalCleanup();
  const directory = join(tmpdir(), `jastreamer-control-qa-${randomUUID()}`);
  let resolveStartup;
  const startupDone = new Promise((resolve) => { resolveStartup = resolve; });
  const resource = { directory, child: undefined, active: true, startupDone };
  const requireActive = () => {
    if (!resource.active) throw new Error('Control QA Server startup interrupted');
  };
  ownedServers.set(directory, resource);
  try {
    await mkdir(directory, { mode: 0o700 });
    requireActive();
    const binary = join(directory, 'jastreamer-server');
    const build = spawn('go', ['build', '-o', binary, './cmd/jastreamer-server'], {
      cwd: serverRoot,
      detached: process.platform !== 'win32',
      stdio: ['ignore', 'ignore', 'pipe'],
    });
    build.controlQaProcessGroup = process.platform !== 'win32';
    resource.child = build;
    let buildStderr = '';
    build.stderr.on('data', (chunk) => { buildStderr += chunk.toString(); });
    const buildCode = await new Promise((resolve, reject) => {
      build.once('error', reject);
      build.once('exit', resolve);
    });
    resource.child = undefined;
    requireActive();
    if (buildCode !== 0) throw new Error(`Server build failed: ${buildStderr}`);
    const music = join(directory, 'music');
    await mkdir(music, { recursive: true });
    requireActive();
    if (options.seedMedia !== false) {
      await Promise.all([
        copyFile(join(serverRoot, '../renderer/tests/fixtures/tone.mp3'), join(music, 'first-light.mp3')),
        copyFile(join(serverRoot, '../renderer/tests/fixtures/tone.flac'), join(music, 'second-light.flac')),
      ]);
      requireActive();
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
    requireActive();
    Object.assign(resource, { binary, config, serverRoot, setupSecret: options.setupSecret ?? 'fixture-setup-secret' });
    const ready = await launchTodo13Process(resource);
    requireActive();
    const certificate = await readFile(join(directory, 'identity/tls-cert.pem'));
    const identity = new X509Certificate(certificate);
    const rendererFingerprint = createHash('sha256').update(identity.raw).digest('hex');
    return { directory, ...ready, rendererFingerprint, spkiPinBase64: certificateSpkiPin(certificate) };
  } catch (error) {
    await cleanupOwnedServer(resource, false, error);
    throw error;
  } finally {
    resolveStartup();
  }
};

export const restartTodo13 = async (server) => {
  const resource = ownedServers.get(server.directory);
  if (!resource || resource.child !== server.child) throw new Error('Control QA Server restart ownership mismatch');
  await stopChild(resource.child);
  const ready = await launchTodo13Process(resource);
  return { ...server, ...ready };
};

export const releaseTodo13 = async (server) => {
  const resource = ownedServers.get(server.directory);
  if (resource) await cleanupOwnedServer(resource);
};
