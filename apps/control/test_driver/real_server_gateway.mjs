import { randomBytes } from 'node:crypto';
import { spawn } from 'node:child_process';
import { mkdir, mkdtemp, rm, writeFile } from 'node:fs/promises';
import https from 'node:https';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import tls from 'node:tls';
import { requireGatewayDriver } from './gateway_driver_path.mjs';

const root = resolve(import.meta.dirname, '../../..');
const serverDirectory = join(root, 'apps/server');
const driverPath = await requireGatewayDriver({ repository: root });
const bounded = (promise, label, milliseconds = 30000) => Promise.race([
  promise,
  new Promise((_, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label} timed out`)), milliseconds);
    timer.unref();
  }),
]);

class RealWebSocket {
  constructor(socket, initial) {
    this.socket = socket;
    this.buffer = initial;
    this.messages = [];
    this.waiters = [];
    this.listeners = new Set();
    socket.on('data', (chunk) => {
      this.buffer = Buffer.concat([this.buffer, chunk]);
      this.#drain();
    });
    socket.on('error', (error) => this.#rejectAll(error));
    socket.on('close', () => this.#rejectAll(new Error('WebSocket closed')));
    this.#drain();
  }

  static async connect({host, port, path, headers = {}}) {
    const socket = tls.connect({host, port, rejectUnauthorized: false});
    await bounded(new Promise((resolve, reject) => {
      socket.once('secureConnect', resolve);
      socket.once('error', reject);
    }), 'TLS WebSocket connect');
    const key = randomBytes(16).toString('base64');
    socket.write([
      `GET ${path} HTTP/1.1`, `Host: ${host}:${port}`, 'Connection: Upgrade',
      'Upgrade: websocket', 'Sec-WebSocket-Version: 13', `Sec-WebSocket-Key: ${key}`,
      ...Object.entries(headers).map(([name, value]) => `${name}: ${value}`), '', '',
    ].join('\r\n'));
    let response = Buffer.alloc(0);
    while (!response.includes('\r\n\r\n')) {
      response = Buffer.concat([response, await bounded(new Promise((resolve, reject) => {
        socket.once('data', resolve);
        socket.once('error', reject);
      }), 'WebSocket upgrade')]);
    }
    const split = response.indexOf('\r\n\r\n');
    const head = response.subarray(0, split).toString('utf8');
    if (!head.startsWith('HTTP/1.1 101 ')) throw new Error(`WebSocket upgrade failed: ${head}`);
    return new RealWebSocket(socket, response.subarray(split + 4));
  }

  sendJson(value) { this.#sendFrame(0x1, Buffer.from(JSON.stringify(value))); }
  onJson(listener) { this.listeners.add(listener); return () => this.listeners.delete(listener); }
  async nextJson(label) {
    if (this.messages.length > 0) return this.messages.shift();
    return bounded(new Promise((resolve, reject) => this.waiters.push({resolve, reject})), label);
  }
  close() { this.#sendFrame(0x8, Buffer.from([0x03, 0xe8])); this.socket.end(); }

  #drain() {
    while (this.buffer.length >= 2) {
      const opcode = this.buffer[0] & 0x0f;
      let length = this.buffer[1] & 0x7f;
      let offset = 2;
      if (length === 126) {
        if (this.buffer.length < 4) return;
        length = this.buffer.readUInt16BE(2); offset = 4;
      } else if (length === 127) {
        if (this.buffer.length < 10) return;
        length = Number(this.buffer.readBigUInt64BE(2)); offset = 10;
      }
      if (this.buffer.length < offset + length) return;
      const payload = this.buffer.subarray(offset, offset + length);
      this.buffer = this.buffer.subarray(offset + length);
      if (opcode === 0x9) { this.#sendFrame(0xa, payload); continue; }
      if (opcode === 0x8) { this.#rejectAll(new Error('WebSocket peer closed')); return; }
      if (opcode !== 0x1) continue;
      const value = JSON.parse(payload.toString('utf8'));
      for (const listener of this.listeners) listener(value);
      const waiter = this.waiters.shift();
      if (waiter) waiter.resolve(value); else this.messages.push(value);
    }
  }

  #sendFrame(opcode, payload) {
    const mask = randomBytes(4);
    let header;
    if (payload.length <= 125) header = Buffer.from([0x80 | opcode, 0x80 | payload.length]);
    else if (payload.length <= 65535) {
      header = Buffer.alloc(4); header[0] = 0x80 | opcode; header[1] = 0xfe;
      header.writeUInt16BE(payload.length, 2);
    } else throw new Error('fixture frame too large');
    const masked = Buffer.alloc(payload.length);
    for (let index = 0; index < payload.length; index++) masked[index] = payload[index] ^ mask[index % 4];
    this.socket.write(Buffer.concat([header, mask, masked]));
  }

  #rejectAll(error) {
    for (const waiter of this.waiters.splice(0)) waiter.reject(error);
  }
}

function waveFixture() {
  const samples = 8000;
  const dataSize = samples * 2;
  const value = Buffer.alloc(44 + dataSize);
  value.write('RIFF', 0); value.writeUInt32LE(36 + dataSize, 4); value.write('WAVE', 8);
  value.write('fmt ', 12); value.writeUInt32LE(16, 16); value.writeUInt16LE(1, 20);
  value.writeUInt16LE(1, 22); value.writeUInt32LE(8000, 24); value.writeUInt32LE(16000, 28);
  value.writeUInt16LE(2, 32); value.writeUInt16LE(16, 34); value.write('data', 36);
  value.writeUInt32LE(dataSize, 40);
  return value;
}

async function request(runtime, method, path, token, body, headers = {}) {
  return bounded(new Promise((resolveRequest, reject) => {
    const encoded = body === undefined ? null : Buffer.from(JSON.stringify(body));
    const outgoing = https.request({
      hostname: '127.0.0.1', port: runtime.port, path, method, rejectUnauthorized: false,
      headers: {...headers, ...(token ? {authorization: `Bearer ${token}`} : {}),
        ...(encoded ? {'content-type': 'application/json', 'content-length': encoded.length} : {})},
    }, (response) => {
      let data = '';
      response.on('data', (chunk) => { data += chunk; });
      response.on('end', () => resolveRequest({
        status: response.statusCode, headers: response.headers,
        body: data.length === 0 ? {} : JSON.parse(data),
      }));
    });
    outgoing.on('error', reject);
    outgoing.end(encoded ?? undefined);
  }), `${method} ${path}`);
}

async function pair(runtime, adminToken, role, name) {
  const issued = await request(runtime, 'POST', '/api/v1/pairing-codes', adminToken, {role});
  if (issued.status !== 201) throw new Error(`pairing code failed: ${JSON.stringify(issued.body)}`);
  const paired = await request(runtime, 'POST', '/api/v1/pairings', null, {code: issued.body.code, name});
  if (paired.status !== 201) throw new Error(`pairing failed: ${JSON.stringify(paired.body)}`);
  return paired.body;
}

async function eventSocket(runtime, token) {
  const ticket = await request(runtime, 'POST', '/api/v1/event-tickets', token, {});
  const socket = await RealWebSocket.connect({
    host: '127.0.0.1', port: runtime.port, path: `/api/v1/events?ticket=${ticket.body.ticket}`,
  });
  const snapshot = await socket.nextJson('event snapshot');
  if (snapshot.type !== 'snapshot') throw new Error(`invalid event snapshot: ${JSON.stringify(snapshot)}`);
  return socket;
}

async function rendererSocket(runtime, credential) {
  const socket = await RealWebSocket.connect({
    host: '127.0.0.1', port: runtime.port,
    path: `/api/v1/renderers/${credential.device.id}/session`,
    headers: {Authorization: `Bearer ${credential.token}`, 'Sec-WebSocket-Protocol': 'jastreamer.renderer.v3'},
  });
  socket.sendJson({
    protocolMajor: 3, type: 'hello', rendererId: credential.device.id,
    supportedMajors: [3, 2], lastServerSequence: 0, pendingResults: [],
    capabilities: {commands: ['play','pause','resume','stop','seek'], mediaTypes: ['audio/wav'],
      supportsRange: true, maxChannels: 2, maxSampleRateHz: 192000},
  });
  const welcome = await socket.nextJson('renderer welcome');
  if (welcome.type !== 'welcome') throw new Error(`invalid renderer welcome: ${JSON.stringify(welcome)}`);
  socket.onJson((frame) => {
    if (frame.type !== 'command') return;
    socket.sendJson({protocolMajor:3,type:'command.ack',commandId:frame.commandId,
      sequence:frame.sequence,status:'received',error:null});
    const observed = ['play','start','resume'].includes(frame.kind) ? 'playing' : frame.kind === 'pause' ? 'paused' : 'idle';
    socket.sendJson({protocolMajor:3,type:'command.result',commandId:frame.commandId,
      resultId:`fixture-${frame.commandId}`,status:'succeeded',observedState:observed,positionMs:0,error:null});
  });
  return socket;
}

async function runDriver(runtime, token, fingerprint, mode, zone = 'main') {
  const child = spawn(driverPath, [mode], {cwd: root, env:{...process.env,
    JASTREAMER_SERVER_URL:`https://127.0.0.1:${runtime.port}`,
    JASTREAMER_CONTROL_TOKEN:token, JASTREAMER_CERTIFICATE_SHA256:fingerprint,
    JASTREAMER_ZONE_ID:zone}, stdio:['ignore','pipe','pipe']});
  let output = '', errors = '';
  child.stdout.on('data', (chunk) => { output += chunk; });
  child.stderr.on('data', (chunk) => { errors += chunk; });
  const code = await bounded(new Promise((resolveExit) => child.once('exit', resolveExit)), `driver ${mode}`);
  if (code !== 0) throw new Error(`driver ${mode} exited ${code}: ${errors}`);
  return JSON.parse(output);
}

const dataDirectory = await mkdtemp(join(tmpdir(), 'jastreamer-control-real-'));
const catalogRoot = join(dataDirectory, 'catalog');
await mkdir(catalogRoot, {recursive:true});
await writeFile(join(catalogRoot, 'fixture.wav'), waveFixture());
const server = spawn('go', ['run','./cmd/jastreamer-server'], {
  cwd: serverDirectory, detached: true,
  env:{...process.env,JASTREAMER_ADDR:'127.0.0.1:0',JASTREAMER_DATA_DIR:dataDirectory,
    JASTREAMER_CATALOG_ROOT:catalogRoot,JASTREAMER_SETUP_SECRET:'ephemeral-harness-bootstrap'},
  stdio:['ignore','pipe','pipe'],
});
let serverErrors = '';
server.stderr.on('data', (chunk) => { serverErrors += chunk; });
const runtime = await bounded(new Promise((resolveReady, reject) => {
  let output = '';
  server.stdout.on('data', (chunk) => {
    output += chunk;
    const match = output.match(/ready https:\/\/([^ ]+) fingerprint=([^\r\n]+)/);
    if (match) resolveReady({port:Number(match[1].split(':').at(-1)),fingerprint:match[2]});
  });
  server.once('exit', (code) => reject(new Error(`Server exited ${code}: ${serverErrors}`)));
}), 'production Server ready', 120000);

let scanEvents, activeRenderer;
try {
  const bootstrap = await request(runtime, 'POST', '/api/v1/bootstrap', null,
    {setup_secret:'ephemeral-harness-bootstrap',name:'Harness Admin'});
  if (bootstrap.status !== 201) throw new Error(`bootstrap failed: ${JSON.stringify(bootstrap.body)}`);
  const admin = bootstrap.body;
  const controller = await pair(runtime, admin.token, 'controller', 'Gateway Controller');
  const revokedController = await pair(runtime, admin.token, 'controller', 'Revoked Controller');
  const renderer = await pair(runtime, admin.token, 'renderer', 'Fixture Renderer');
  const offlineRenderer = await pair(runtime, admin.token, 'renderer', 'Offline Renderer');

  for (const [zoneId, rendererId] of [['main',renderer.device.id],['offline',offlineRenderer.device.id]]) {
    const created = await request(runtime, 'POST', '/api/v1/zones', admin.token, {zone_id:zoneId,name:zoneId});
    if (created.status !== 201) throw new Error(`zone create failed: ${JSON.stringify(created.body)}`);
    const assigned = await request(runtime, 'PUT', `/api/v1/zones/${zoneId}/renderer`, admin.token,
      {renderer_id:rendererId},{'if-match':'0','idempotency-key':`assign-${zoneId}`});
    if (assigned.status !== 200) throw new Error(`zone assign failed: ${JSON.stringify(assigned.body)}`);
  }

  activeRenderer = await rendererSocket(runtime, renderer);
  scanEvents = await eventSocket(runtime, controller.token);
  const scan = await request(runtime, 'POST', '/api/v1/catalog/scans', admin.token, {});
  if (scan.status !== 202) throw new Error(`scan start failed: ${JSON.stringify(scan.body)}`);
  let catalogEvent;
  do { catalogEvent = await scanEvents.nextJson('catalog invalidation'); }
  while (!(catalogEvent.type === 'invalidation' && catalogEvent.resource === 'catalog'));
  const page = await request(runtime, 'GET', '/api/v1/catalog/tracks?limit=2', controller.token);
  if (page.status !== 200 || page.body.tracks.length === 0) throw new Error('scan produced no browsable tracks');

  const happy = await runDriver(runtime, controller.token, runtime.fingerprint, '--happy');
  const offline = await runDriver(runtime, controller.token, runtime.fingerprint, '--offline', 'offline');
  const revoked = await request(runtime, 'DELETE', `/api/v1/devices/${revokedController.device.id}`, admin.token);
  if (revoked.status !== 204) throw new Error(`revocation failed: ${JSON.stringify(revoked.body)}`);
  const revokedResult = await runDriver(runtime, revokedController.token, runtime.fingerprint, '--revoked');
  const mismatch = await runDriver(runtime, controller.token, '0'.repeat(64), '--certificate-mismatch');

  process.stdout.write(`${JSON.stringify({
    server:'production-go',catalog_seeded:true,renderer_session:'real-wss-fixture',
    happy,offline,revoked:revokedResult,certificate_mismatch:mismatch,
  })}\n`);
} finally {
  try { scanEvents?.close(); } catch {}
  try { activeRenderer?.close(); } catch {}
  const exited = new Promise((resolveExit) => server.once('exit', resolveExit));
  try { process.kill(-server.pid, 'SIGTERM'); } catch {}
  await bounded(exited, 'production Server shutdown');
  await rm(dataDirectory, {recursive:true,force:true});
}
