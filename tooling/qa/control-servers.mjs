import { spawn, spawnSync } from 'node:child_process';
import { createServer } from 'node:http';
import { createServer as createSecureServer } from 'node:https';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';
import { tmpdir } from 'node:os';

export const startWeb = async (webRoot) => {
  const server = createServer(async (request, response) => {
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
  await new Promise((resolve) => server.listen(4173, '127.0.0.1', resolve));
  return { server, origin: 'http://127.0.0.1:4173' };
};

export const startTodo13 = async (serverRoot, controlOrigin) => {
  const directory = await mkdtemp(join(tmpdir(), 'jstreamer-control-qa-'));
  const binary = join(directory, 'jstreamer-server');
  const built = spawnSync('go', ['build', '-o', binary, './cmd/jstreamer-server'], { cwd: serverRoot, encoding: 'utf8' });
  if (built.status !== 0) throw new Error(`Server build failed: ${built.stderr}`);
  const config = join(directory, 'server.json');
  await writeFile(config, JSON.stringify({
    address: '127.0.0.1:0', data_directory: directory, catalog_root: join(directory, 'music'),
    catalog_migration: join(serverRoot, 'migrations/001_catalog.sql'),
    playback_migration: join(serverRoot, 'migrations/002_playback.sql'),
    playback_expansion: join(serverRoot, 'migrations/003_todo12.sql'),
    certificate_dns: ['localhost'], certificate_ips: ['127.0.0.1'],
    allowed_origins: [controlOrigin], pairing_ttl: '5m',
  }));
  const child = spawn(binary, ['--config', config], {
    cwd: serverRoot,
    env: { ...process.env, JSTREAMER_SETUP_SECRET: 'fixture-setup-secret' },
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
  return { child, directory, ...ready };
};

export const startEdgeApi = async (todo13, controlOrigin) => {
  const certificate = await readFile(join(todo13.directory, 'identity/tls-cert.pem'));
  const key = await readFile(join(todo13.directory, 'identity/tls-key.pem'));
  let revision = 0;
  let mode = 'stop';
  let catalogReads = 0;
  const decision = (reason = 'BLOCK_EXPLICIT') => ({
    decision_id: 'edge-decision', kind: reason === 'BLOCK_EXPLICIT' ? 'block' : 'stop',
    reason, source: reason === 'BLOCK_EXPLICIT' ? 'explicit' : '', track_id: 'unavailable-edge',
    algorithm_revision: 'policy-v1', catalog_revision: 7, policy_revision: revision,
    contract_revision: 'http-api-v1', signal_coverage: 61,
  });
  const server = createSecureServer({ cert: certificate, key }, async (request, response) => {
    const headers = { 'content-type': 'application/json', 'access-control-allow-origin': controlOrigin, vary: 'Origin' };
    if (request.method === 'OPTIONS') {
      response.writeHead(204, { ...headers, 'access-control-allow-methods': 'GET,PATCH,OPTIONS', 'access-control-allow-headers': 'authorization,content-type,if-match,x-jake-protocol-major' }).end();
      return;
    }
    const path = new URL(request.url ?? '/', 'https://edge.local').pathname;
    if (path.startsWith('/api/v1/') && path !== '/api/v1/identity' && request.headers.authorization === 'Bearer rejected-token') {
      response.writeHead(401, headers).end(JSON.stringify({ code: 'UNAUTHORIZED' })); return;
    }
    if (path === '/pair/') { response.writeHead(302, { location: `${todo13.origin}/pair/` }).end(); return; }
    let status = 200;
    let body;
    if (path === '/api/v1/identity') body = { common_name: 'Jake Streamer Edge Fixture', sha256_fingerprint: todo13.fingerprint, pairing_url: '/pair/' };
    else if (path === '/api/v1/discovery') body = { supported_protocol_majors: [1, 2], capabilities: ['catalog-status', 'queue', 'continuation-policy', 'automatic-preview', 'decision-explanation'], pairing_url: '/pair/', certificate_sha256: todo13.fingerprint, contract_revision: 'http-api-v1', algorithm_revision: 'policy-v1', analysis_revision: 1, catalog_revision: 7 };
    else if (path.endsWith('/continuation-policy') && request.method === 'GET') body = { mode, artist_gap: 4, album_gap: 10, session_override: '', revision };
    else if (path.endsWith('/continuation-policy') && request.method === 'PATCH') {
      if (Number(request.headers['if-match']) !== revision) { status = 412; body = { code: 'STALE_POLICY_REVISION' }; }
      else { let raw = ''; for await (const chunk of request) raw += chunk; mode = JSON.parse(raw).mode; revision++; body = { mode, artist_gap: 4, album_gap: 10, session_override: '', revision }; }
    } else if (path === '/api/v1/catalog/status') { catalogReads++; body = { scan_status: 'ready', catalog_revision: 7, track_count: 100, analysis_complete: 61, analysis_queued: 32, analysis_failed: 7, analysis_coverage: 61, analysis_revision: 1 }; }
    else if (path.endsWith('/queue')) body = { zone_id: 'main', revision, transport: 'idle', queue: catalogReads > 1 ? [] : [{ entry_id: 'edge-1', track_id: 'unavailable-edge', state: 'blocked', position: 1 }] };
    else if (path.endsWith('/automatic-preview')) body = catalogReads > 1 ? { active: true, replaceable: false, committed: true, decision: decision('STOP_SIMILAR_NO_SIGNAL') } : { active: false, replaceable: true, committed: false, decision: decision() };
    else if (path.endsWith('/decision-explanation')) body = catalogReads > 1 ? decision('STOP_SIMILAR_NO_SIGNAL') : decision();
    else { status = 404; body = { code: 'NOT_FOUND' }; }
    response.writeHead(status, headers).end(JSON.stringify(body));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new TypeError('edge API did not bind TCP');
  return { server, origin: `https://127.0.0.1:${address.port}`, fingerprint: todo13.fingerprint };
};

export const stopChild = async (child) => {
  if (child.exitCode !== null) return;
  const exited = new Promise((resolve) => child.once('exit', resolve));
  child.kill('SIGTERM');
  await Promise.race([exited, new Promise((_, reject) => setTimeout(() => reject(new Error('Server shutdown timeout')), 10_000))]);
};
