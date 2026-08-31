import { expect } from "@playwright/test";
import { join } from "node:path";
import { activateFlutter, registerPairedDevice, replaceFlutterText, resizeFlutter, revealFlutterAnchor, submitPairingToken } from './control-playwright-actions.mjs';
import { output } from './control-scenario-fixture.mjs';
import { markFixtureQueueHeadBlocked, markFixtureRendererConnected } from './control-servers.mjs';

export const runFailureFlow = async ({ page, context, api, server, failureProxy, token, rendererCredential, adminHeaders, recoveryAdmin }) => {
  const controllerHeaders = { authorization: `Bearer ${token}`, 'content-type': 'application/json' };
  const failureBanner = (code) => page.getByRole('group', { name: new RegExp(`^${code}\\b`) }).last();
  const captureFailure = async (slug, anchor) => {
    await resizeFlutter(page, 1280, 900);
    await revealFlutterAnchor(page, anchor, 20);
    await page.screenshot({ path: join(output, `${slug}-desktop.png`) });
    await resizeFlutter(page, 390, 6000);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: join(output, `${slug}-mobile-tall.png`) });
    await resizeFlutter(page, 390, 844);
    await revealFlutterAnchor(page, anchor, 20);
    await page.screenshot({ path: join(output, `${slug}-mobile.png`) });
    await resizeFlutter(page, 1280, 3000);
  };
  const queueRequest = async (revision, command, extra, key) => {
    const response = await context.request.post(`${api.origin}/api/v1/zones/main/queue`, {
      ignoreHTTPSErrors: true,
      headers: { ...controllerHeaders, 'if-match': String(revision), 'idempotency-key': key },
      data: { command, ...extra },
    });
    return { response, body: await response.json() };
  };

  const catalogPage = await (await context.request.get(`${api.origin}/api/v1/catalog/tracks?limit=10`, { ignoreHTTPSErrors: true, headers: controllerHeaders })).json();
  expect(catalogPage.tracks.length).toBeGreaterThanOrEqual(2);
  let serverState = await (await context.request.get(`${api.origin}/api/v1/zones/main/playback-state`, { ignoreHTTPSErrors: true, headers: controllerHeaders })).json();
  const firstBurst = await queueRequest(serverState.revision, 'append', { track_ids: [catalogPage.tracks[0].track_id] }, 'failure-gap-1');
  expect(firstBurst.response.status()).toBe(201);
  const dropped = await failureProxy.dropped;
  expect(dropped.resource).toBe('queue');
  const secondBurst = await queueRequest(firstBurst.body.revision, 'append', { track_ids: [catalogPage.tracks[1].track_id] }, 'failure-gap-2');
  expect(secondBurst.response.status()).toBe(201);
  await expect(page.getByText('Event gap recovered · full Server state resynchronized').last()).toBeVisible({ timeout: 20_000 });
  await captureFailure(
    'control-failure-resync',
    page.getByText('Event gap recovered · full Server state resynchronized').last(),
  );

  const offlineResponse = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/transport') && response.request().method() === 'POST',
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Play', exact: true }));
  expect((await offlineResponse).status()).toBe(409);
  await expect(page.getByText('RENDERER_OFFLINE').last()).toBeVisible();
  await expect(page.getByText(/No mutation was retried/)).toBeVisible();
  await captureFailure(
    'control-failure-renderer-offline',
    failureBanner('RENDERER_OFFLINE'),
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));

  serverState = await (await context.request.get(`${api.origin}/api/v1/zones/main/playback-state`, { ignoreHTTPSErrors: true, headers: controllerHeaders })).json();
  const clearUpdate = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
  );
  const cleared = await queueRequest(serverState.revision, 'clear', {}, 'failure-clear');
  expect(cleared.response.status()).toBe(200);
  expect((await clearUpdate).status()).toBe(200);
  const blocked = await queueRequest(cleared.body.revision, 'append', { track_ids: ['missing-track'] }, 'failure-blocked');
  expect(blocked.response.status()).toBe(201);
  markFixtureQueueHeadBlocked(server);
  const blockedRefresh = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server state' }));
  expect((await blockedRefresh).status()).toBe(200);
  const retryBlocked = page.getByRole('button', { name: 'Retry blocked track', exact: true }).last();
  const skipBlocked = page.getByRole('button', { name: 'Skip blocked track', exact: true }).last();
  await expect(retryBlocked).toBeVisible({ timeout: 20_000 });
  await expect(skipBlocked).toBeVisible();
  await captureFailure('control-failure-blocked-track', retryBlocked);
  const retryRequest = page.waitForRequest((request) =>
    request.url().endsWith('/api/v1/zones/main/queue') && request.method() === 'POST',
  );
  const retryResponse = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/queue') && response.request().method() === 'POST',
  );
  const retrySettled = retryBlocked.waitFor({ state: 'hidden' });
  await activateFlutter(page, retryBlocked);
  const [recoveryRequest, recoveryResponse] = await Promise.all([retryRequest, retryResponse]);
  expect(recoveryRequest.method()).toBe('POST');
  expect(recoveryResponse.status()).toBe(200);
  const retryRefresh = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server state' }));
  expect((await retryRefresh).status()).toBe(200);
  await retrySettled;
  await expect(page.getByRole('button', { name: 'Refresh Server state' })).toBeEnabled();

  markFixtureRendererConnected(server, rendererCredential.device.id);
  const blockedCommand = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/transport') && response.request().method() === 'POST',
  );
  const blockedPlay = page.getByRole('button', { name: 'Play', exact: true });
  await expect(blockedPlay).toBeVisible();
  await expect(blockedPlay).toBeEnabled();
  await activateFlutter(page, blockedPlay);
  expect((await blockedCommand).status()).toBe(409);
  await expect(page.getByText('BLOCKED_EXPLICIT_HEAD').last()).toBeVisible();
  await captureFailure(
    'control-failure-blocked-command',
    failureBanner('BLOCKED_EXPLICIT_HEAD'),
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));

  serverState = await (await context.request.get(`${api.origin}/api/v1/zones/main/playback-state`, { ignoreHTTPSErrors: true, headers: controllerHeaders })).json();
  const clearPending = await queueRequest(serverState.revision, 'clear', {}, 'failure-clear-pending');
  expect(clearPending.response.status()).toBe(200);
  const blockedAgain = await queueRequest(clearPending.body.revision, 'append', { track_ids: ['missing-track'] }, 'failure-blocked-again');
  expect(blockedAgain.response.status()).toBe(201);
  markFixtureQueueHeadBlocked(server);
  const blockedAgainRefresh = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server state' }));
  expect((await blockedAgainRefresh).status()).toBe(200);
  await expect(skipBlocked).toBeVisible();
  const skipResponse = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/queue') && response.request().method() === 'POST',
  );
  await activateFlutter(page, skipBlocked);
  expect((await skipResponse).status()).toBe(200);
  const skipRefresh = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server state' }));
  expect((await skipRefresh).status()).toBe(200);
  const pause = page.getByRole('button', { name: 'Pause', exact: true });
  await expect(pause).toBeEnabled({ timeout: 20_000 });
  const confirmationFailure = page.getByText('SERVER_OFFLINE').last();
  if (await confirmationFailure.isVisible()) {
    const confirmationRecovery = page.waitForResponse((response) =>
      response.url().endsWith('/api/v1/catalog/status') && response.request().method() === 'GET',
    );
    await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));
    expect((await confirmationRecovery).status()).toBe(200);
    await expect(pause).toBeEnabled();
  }

  const commandFailure = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/transport') && response.request().method() === 'POST',
  );
  await activateFlutter(page, pause);
  expect((await commandFailure).status()).toBe(409);
  await expect(page.getByText('INVALID_STATE').last()).toBeVisible();
  await expect(page.getByText(/No mutation was retried/)).toBeVisible();
  await captureFailure(
    'control-failure-command',
    failureBanner('INVALID_STATE'),
  );
  const commandRecovery = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));
  expect((await commandRecovery).status()).toBe(200);
  await expect(page.getByRole('button', { name: 'Refresh Server state' })).toBeEnabled();

  serverState = await (await context.request.get(`${api.origin}/api/v1/zones/main/playback-state`, { ignoreHTTPSErrors: true, headers: controllerHeaders })).json();
  const staleBase = await queueRequest(serverState.revision, 'append', { track_ids: [catalogPage.tracks[0].track_id] }, 'failure-stale-base');
  expect(staleBase.response.status()).toBe(201);
  await expect(page.getByRole('button', { name: 'Remove' }).first()).toBeVisible();
  const staleInvalidation = failureProxy.dropNextInvalidation();
  const advanced = await queueRequest(staleBase.body.revision, 'append', { track_ids: [catalogPage.tracks[1].track_id] }, 'failure-stale-advance');
  expect(advanced.response.status()).toBe(201);
  const staleSignal = await staleInvalidation;
  expect(staleSignal.resource).toBe('queue');
  let staleMutationCount = 0;
  const countStaleMutation = (request) => {
    if (request.url().endsWith('/api/v1/zones/main/queue') && request.method() === 'POST') staleMutationCount++;
  };
  page.on('request', countStaleMutation);
  const staleResponse = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/queue') && response.request().method() === 'POST' && response.status() === 409,
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Clear' }));
  expect((await staleResponse).status()).toBe(409);
  await expect(page.getByText('STALE_REVISION').last()).toBeVisible();
  await expect(page.getByText(/Preserved intent: clear.*No mutation was retried/)).toBeVisible();
  expect(staleMutationCount).toBe(1);
  page.off('request', countStaleMutation);
  await captureFailure(
    'control-failure-stale-revision',
    failureBanner('STALE_REVISION'),
  );
  const staleRecovery = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));
  expect((await staleRecovery).status()).toBe(200);
  await expect(page.getByRole('button', { name: 'Refresh Server state' })).toBeEnabled();

  const devices = await (await context.request.get(`${api.origin}/api/v1/devices`, { ignoreHTTPSErrors: true, headers: adminHeaders })).json();
  const controller = devices.devices.find((device) => device.name === 'Fixture Control');
  expect(controller).toBeTruthy();
  const revoked = await context.request.delete(`${api.origin}/api/v1/devices/${controller.id}`, { ignoreHTTPSErrors: true, headers: adminHeaders });
  expect(revoked.status()).toBe(204);
  const devicesAfter = await (await context.request.get(`${api.origin}/api/v1/devices`, { ignoreHTTPSErrors: true, headers: adminHeaders })).json();
  const revokedDevice = devicesAfter.devices.find((device) => device.id === controller.id);
  expect(revokedDevice?.revoked, JSON.stringify(devicesAfter.devices)).toBe(true);
  const revokedDirectProbe = await context.request.post(`${server.origin}/api/v1/event-tickets`, { ignoreHTTPSErrors: true, headers: controllerHeaders, data: {} });
  expect(revokedDirectProbe.status(), JSON.stringify(devicesAfter.devices)).toBe(401);
  const revokedProbe = await context.request.get(`${api.origin}/api/v1/catalog/tracks?limit=1`, { ignoreHTTPSErrors: true, headers: controllerHeaders });
  expect(revokedProbe.status(), JSON.stringify(devicesAfter.devices)).toBe(401);
  const revokedRequest = page.waitForResponse((response) =>
    response.url().includes('/api/v1/catalog/tracks') && response.status() === 401,
  );
  await replaceFlutterText(
    page.getByLabel('Search title, artist, or album'),
    'revoked',
  );
  await activateFlutter(page, page.getByRole('button', { name: 'Search catalog' }));
  await revokedRequest;
  const revokedBanner = failureBanner('TOKEN_REVOKED');
  await expect(revokedBanner).toBeVisible();
  await captureFailure('control-failure-token-revoked', revokedBanner);
  await activateFlutter(page, page.getByRole('button', { name: 'Clear & pair again' }));
  const rePair = page.getByRole('button', { name: 'Open pairing page' });
  await expect(rePair).toBeVisible();
  await captureFailure('control-failure-credential-cleared', rePair);

  const repairPopupPromise = context.waitForEvent('page', { timeout: 10_000 });
  await activateFlutter(page, page.getByRole('button', { name: 'Open pairing page' }));
  const repairPopup = await repairPopupPromise;
  await repairPopup.waitForLoadState('domcontentloaded');
  await repairPopup.getByLabel('Administrator token').fill(recoveryAdmin.token);
  await repairPopup.getByRole('button', { name: 'Use token' }).click();
  await expect(repairPopup.getByText('Session token loaded.', { exact: true })).toBeVisible();
  const repairedToken = await registerPairedDevice(
    repairPopup,
    'Fixture Control Re-paired',
    'control-failure-repair-portal-redacted',
  );
  await repairPopup.close();
  await page.bringToFront();
  await submitPairingToken(page, repairedToken);
  await expect(page.getByText('Paired device', { exact: true })).toBeVisible();
  await captureFailure(
    'control-failure-repaired',
    page.getByText('Paired device', { exact: true }).last(),
  );
};
