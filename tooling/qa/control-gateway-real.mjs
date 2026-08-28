import { randomBytes } from 'node:crypto';
import { watch } from 'node:fs';
import { copyFile, mkdir, readFile, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { startEventGapProxy, startFixtureRenderer, startTodo13, stopChild } from './control-servers.mjs';
import { deadline, pairRole, requestJSON, requireStatus } from './control-gateway-http.mjs';
import { startDriver } from './control-gateway-driver-process.mjs';
import { startUnknownEnumProxy } from './control-gateway-proxy.mjs';
import { waitForPlaybackState, waitForRendererStatus } from './control-gateway-waits.mjs';
import { writeControlGatewayResult } from './control-gateway-output.mjs';

const repository = resolve(import.meta.dirname, '../..');
const serverRoot = join(repository, 'apps/server');
const eventGapOnly = process.env.TASK15_EVENT_GAP_ONLY === '1';

let initialScanResolve;
let initialScanReject;
const initialScan = new Promise((resolveScan, rejectScan) => {
  initialScanResolve = resolveScan;
  initialScanReject = rejectScan;
});
let scanWatcher;
let server;
let renderer;
let gapProxy;
let unknownProxy;
const driverChildren = new Set();
const results = [];
let stage = 'server-start';

try {
  const setupSecret = randomBytes(24).toString('base64url');
  server = await startTodo13(serverRoot, 'https://control.task15.invalid', {
    seedMedia: false,
    setupSecret,
    beforeSpawn: async ({ directory }) => {
      const catalogDirectory = join(directory, 'catalog');
      await mkdir(catalogDirectory, { recursive: true });
      scanWatcher = watch(catalogDirectory, async () => {
        try {
          const state = JSON.parse(await readFile(join(catalogDirectory, 'coordinator.json'), 'utf8'));
          const completed = state.jobs?.find((job) => job.status === 'complete');
          if (completed) initialScanResolve(completed);
        } catch (error) {
          if (!(error && error.code === 'ENOENT')) initialScanReject(error);
        }
      });
    },
  });
  await deadline(initialScan, 'initial empty catalog scan');
  scanWatcher.close();
  scanWatcher = undefined;

  stage = 'bootstrap-and-pair';
  const admin = requireStatus(
    await requestJSON(server.origin, 'POST', '/api/v1/bootstrap', '', {
      setup_secret: setupSecret,
      name: 'Task 15 Admin',
    }),
    201,
    'bootstrap admin',
  );
  const control = await pairRole(server, admin.token, 'controller', 'Task 15 Control');

  stage = 'catalog-scan';
  const catalogDriver = startDriver(server, control.token, '--wait-catalog');
  driverChildren.add(catalogDriver.child);
  await catalogDriver.ready;
  await Promise.all([
    copyFile(join(serverRoot, '../renderer/tests/fixtures/tone.mp3'), join(server.directory, 'music/first-light.mp3')),
    copyFile(join(serverRoot, '../renderer/tests/fixtures/tone.flac'), join(server.directory, 'music/second-light.flac')),
  ]);
  const scan = requireStatus(
    await requestJSON(server.origin, 'POST', '/api/v1/catalog/scans', admin.token, { root_id: 'default' }),
    202,
    'start fixture catalog scan',
  );
  results.push(await catalogDriver.done);
  driverChildren.delete(catalogDriver.child);
  const scanStatus = requireStatus(
    await requestJSON(server.origin, 'GET', `/api/v1/catalog/scans/${encodeURIComponent(scan.job_id)}`, admin.token),
    200,
    'read exact fixture scan job',
  );
  if (scanStatus.status !== 'complete' || scanStatus.catalog_revision <= 0) {
    throw new Error(`fixture scan was not complete: ${JSON.stringify(scanStatus)}`);
  }

  stage = 'renderer-setup';
  const zone = requireStatus(
    await requestJSON(server.origin, 'POST', '/api/v1/zones', admin.token, { zone_id: 'main', name: 'Task 15 Zone' }),
    201,
    'create fixture zone',
  );
  let rendererCredential;
  if (!eventGapOnly) {
    rendererCredential = await pairRole(server, admin.token, 'renderer', 'Task 15 Renderer');
    requireStatus(
    await requestJSON(server.origin, 'PUT', '/api/v1/zones/main/renderer', admin.token, {
      renderer_id: rendererCredential.device.id,
    }, {
      'if-match': String(zone.revision),
      'idempotency-key': 'task15-assign-renderer',
    }),
    200,
    'assign fixture renderer',
  );

  const rendererWatcher = startDriver(server, control.token, '--wait-renderer', {
    JASTREAMER_RENDERER_ID: rendererCredential.device.id,
    JASTREAMER_RENDERER_SIGNAL: 'stdin',
  });
  driverChildren.add(rendererWatcher.child);
  await rendererWatcher.ready;
  const rendererConnected = waitForRendererStatus(
    server,
    control.token,
    rendererCredential.device.id,
    'connected',
  );
  renderer = await startFixtureRenderer(serverRoot, server, rendererCredential);
  await rendererConnected;
  rendererWatcher.child.stdin.end('connected\n');
  results.push(await rendererWatcher.done);
  driverChildren.delete(rendererWatcher.child);

  stage = 'happy-driver';
  const playTerminal = waitForPlaybackState(
    server,
    control.token,
    (state) => state.transport === 'playing' &&
      state.observed_transport === 'playing' &&
      !state.pending_command_id,
    'play terminal state',
  );
  const happy = startDriver(server, control.token, '--happy', {
    JASTREAMER_TRANSPORT_SIGNAL: 'stdin',
  });
  driverChildren.add(happy.child);
  await happy.waitForReady('play-terminal');
  const pauseTerminal = waitForPlaybackState(
    server,
    control.token,
    (state) => state.transport === 'paused' &&
      state.observed_transport === 'paused' &&
      !state.pending_command_id,
    'pause terminal state',
  );
  await playTerminal;
  happy.child.stdin.write('play-terminal\n');
  await happy.waitForReady('pause-terminal');
  await pauseTerminal;
  happy.child.stdin.end('pause-terminal\n');
    results.push(await happy.done);
    driverChildren.delete(happy.child);
  }

  stage = 'event-gap';
  gapProxy = await startEventGapProxy(server);
  const gap = startDriver(gapProxy, control.token, '--event-gap');
  driverChildren.add(gap.child);
  try {
    results.push(await gap.done);
  } catch (error) {
    error.message += `; proxy=${JSON.stringify(gapProxy.diagnostics())}`;
    throw error;
  }
  driverChildren.delete(gap.child);
  await gapProxy.close();
  gapProxy = undefined;

  if (!eventGapOnly) {
    stage = 'unknown-enum';
    unknownProxy = await startUnknownEnumProxy(server);
  const unknown = startDriver(unknownProxy, control.token, '--unknown-enum');
  driverChildren.add(unknown.child);
  const unknownResult = await unknown.done;
  driverChildren.delete(unknown.child);
  if (unknownProxy.mutationCount() !== 0) {
    throw new Error(`unknown enum path issued ${unknownProxy.mutationCount()} mutations`);
  }
  results.push({ ...unknownResult, mutations: unknownProxy.mutationCount() });
  await unknownProxy.close();
  unknownProxy = undefined;

  stage = 'renderer-offline';
  const offlineWatcher = startDriver(server, control.token, '--wait-renderer', {
    JASTREAMER_RENDERER_ID: rendererCredential.device.id,
    JASTREAMER_RENDERER_STATUS: 'available',
    JASTREAMER_RENDERER_SIGNAL: 'stdin',
  });
  driverChildren.add(offlineWatcher.child);
  await offlineWatcher.ready;
  const rendererUnavailable = waitForRendererStatus(
    server,
    control.token,
    rendererCredential.device.id,
    'available',
  );
  await stopChild(renderer);
  renderer = undefined;
  await rendererUnavailable;
  offlineWatcher.child.stdin.end('available\n');
  results.push(await offlineWatcher.done);
  driverChildren.delete(offlineWatcher.child);

  const offline = startDriver(server, control.token, '--offline');
  driverChildren.add(offline.child);
  results.push(await offline.done);
  driverChildren.delete(offline.child);

  stage = 'revoked-token';
  const revokedControl = await pairRole(server, admin.token, 'controller', 'Revoked Task 15 Control');
  requireStatus(
    await requestJSON(
      server.origin,
      'DELETE',
      `/api/v1/devices/${encodeURIComponent(revokedControl.device.id)}`,
      admin.token,
    ),
    204,
    'revoke controller',
  );
  const revoked = startDriver(server, revokedControl.token, '--revoked');
  driverChildren.add(revoked.child);
  results.push(await revoked.done);
  driverChildren.delete(revoked.child);

  stage = 'certificate-mismatch';
  const mismatch = startDriver(
    { ...server, fingerprint: '0'.repeat(64) },
    control.token,
    '--certificate-mismatch',
  );
  driverChildren.add(mismatch.child);
    results.push(await mismatch.done);
    driverChildren.delete(mismatch.child);
  }
} catch (error) {
  error.message = `[${stage}] ${error.message}`;
  throw error;
} finally {
  scanWatcher?.close();
  for (const child of driverChildren) child.kill('SIGTERM');
  if (renderer) await stopChild(renderer).catch(() => {});
  if (gapProxy) await gapProxy.close();
  if (unknownProxy) await unknownProxy.close();
  if (server) {
    await stopChild(server.child).catch(() => {});
    await rm(server.directory, { recursive: true, force: true });
  }
}

writeControlGatewayResult(results);
