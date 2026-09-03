import { expect, test } from "@playwright/test";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import {
  markFixtureQueueHeadBlocked,
  markFixtureRendererConnected,
  startEventGapProxy,
  startTodo13,
  startWeb,
  stopChild,
} from './control-servers.mjs';
import { startControlRendererFixture } from './control-renderer-fixture.mjs';

const root = join(import.meta.dirname, "../..");
const serverRoot = join(root, "apps/server");
const controlFixtureRoot = join(root, "apps/control/test/fixtures");
const configuredWebRoot = process.env.CONTROL_WEB_ROOT;
const webRoot = configuredWebRoot === undefined
  ? join(root, "apps/control/build/web")
  : resolve(configuredWebRoot);
const fixturePath = process.env.CONTROL_FIXTURE;
const output = process.env.CONTROL_OUTPUT;
const qaEnabled = fixturePath !== undefined && output !== undefined;
const fixture = qaEnabled ? await readFile(fixturePath, "utf8") : "";
const scenario = fixture.match(/^scenario:\s*(.+)$/m)?.[1];
if (qaEnabled && scenario !== "control-policy-happy" && scenario !== "control-policy-failure") {
  throw new TypeError("unsupported Control QA scenario");
}
const expectedByScenario = {
  "control-policy-happy": [
    "real-todo13-authenticated-discovery-and-pairing",
    "catalog-browse-and-search",
    "zone-renderer-assignment",
    "two-queue-adds-and-reorder",
    "play-pause-seek-resume-next-stop",
    "desktop-mobile-keyboard-overflow-evidence",
    "no-secret-evidence",
  ],
  "control-policy-failure": [
    "certificate-mismatch-and-rejected-token-recovery",
    "renderer-offline-no-blind-retry",
    "blocked-track-retry-and-skip",
    "stale-revision-preserves-intent-no-blind-retry",
    "command-failure",
    "event-sequence-gap-full-resync",
    "token-revocation-clear-and-repair",
    "desktop-mobile-keyboard-overflow-evidence",
    "no-secret-evidence",
  ],
};
const fixtureExpects = [...fixture.matchAll(/^\s+-\s+(.+)$/gm)].map((match) => match[1]);
if (qaEnabled && JSON.stringify(fixtureExpects) !== JSON.stringify(expectedByScenario[scenario])) {
  throw new TypeError("Control fixture expects list drifted");
}

const enableSemantics = async (page) => {
  const placeholder = page.locator('flt-semantics-placeholder');
  const discover = page.getByRole('button', { name: 'Discover Server' });
  await placeholder.or(discover).waitFor({ state: 'visible', timeout: 60_000 });
  if (await discover.isVisible()) return;
  await placeholder.focus();
  await expect(placeholder).toBeFocused();
  await placeholder.dispatchEvent('click');
  await discover.waitFor({ state: 'visible' });
};

const activateFlutter = async (page, locator) => {
  await expect(locator).toBeVisible();
  await expect(locator).toBeEnabled({ timeout: 20_000 });
  if (!await locator.boundingBox()) throw new TypeError('Flutter control has no action target');
  await locator.focus();
  await expect(locator).toBeFocused();
  await page.keyboard.press('Enter');
};

const replaceFlutterText = async (page, locator, value) => {
  await expect(locator).toHaveCount(1);
  await expect(locator).toBeVisible();
  await locator.click();
  await expect(locator).toBeFocused();
  const current = await locator.inputValue();
  if (current !== value) {
    await page.keyboard.press('Control+A');
    if (value.length === 0) {
      await page.keyboard.press('Backspace');
    } else {
      await page.keyboard.insertText(value);
    }
  }
  if ((await locator.inputValue()) !== value) {
    await locator.fill(value);
  }
  await expect(locator).toHaveValue(value);
};

const resizeFlutter = async (page, width, height) => {
  const resized = page.evaluate(({ width, height }) => new Promise((resolve) => {
    const complete = () => requestAnimationFrame(() => requestAnimationFrame(
      () => resolve([window.innerWidth, window.innerHeight]),
    ));
    window.addEventListener('resize', complete, { once: true });
    if (window.innerWidth === width && window.innerHeight === height) complete();
  }), { width, height });
  await page.setViewportSize({ width, height });
  expect(await resized).toEqual([width, height]);
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
};

const revealFlutterAnchor = async (page, anchor, pages = 12) => {
  for (let pageDown = 0; pageDown < pages; pageDown++) {
    if (await anchor.isVisible()) return;
    const viewport = await page.evaluate(() => ({ width: window.innerWidth, height: window.innerHeight }));
    await page.mouse.move(viewport.width / 2, viewport.height / 2);
    await page.mouse.wheel(0, 700);
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(resolve)));
  }
  await expect(anchor).toBeVisible();
};

const expectTouchTarget = async (page, locator) => {
  for (let attempt = 0; attempt < 12; attempt++) {
    const size = await locator.evaluate((element) => ({
      width: element.offsetWidth,
      height: element.offsetHeight,
    }));
    if (size.width >= 48 && size.height >= 48) return;
    const bounds = await locator.boundingBox();
    const viewportHeight = await page.evaluate(() => window.innerHeight);
    await page.mouse.move(195, viewportHeight / 2);
    await page.mouse.wheel(0, bounds && bounds.y < viewportHeight / 2 ? -200 : 200);
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(resolve)));
  }
  const size = await locator.evaluate((element) => ({
    width: element.offsetWidth,
    height: element.offsetHeight,
  }));
  expect(size.width).toBeGreaterThanOrEqual(48);
  expect(size.height).toBeGreaterThanOrEqual(48);
};

const submitPairingToken = async (page, token) => {
  const input = page.getByLabel('Controller token');
  await replaceFlutterText(page, input, token);
  const responsePromise = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/discovery') &&
    response.request().method() === 'GET',
  );
  const submit = page.getByRole('button', { name: 'Complete pairing', exact: true });
  await activateFlutter(page, submit);
  const response = await responsePromise;
  expect(response.status()).toBe(token === 'rejected-token' ? 401 : 200);
};

const redactPairingReceipt = async (popup, slug) => {
  await popup.locator("#register-message").evaluate((element) => {
    element.textContent = "Device registered. Token: [REDACTED]";
  });
  await popup.locator("#pairing-code").evaluate((element) => {
    element.textContent = "[REDACTED]";
  });
  await popup.screenshot({ path: join(output, `${slug}.png`), fullPage: true });
};

const registerPairedDevice = async (popup, name, receiptSlug) => {
  await popup.getByRole("button", { name: "Generate code" }).click();
  await expect(popup.locator("#pairing-code")).toHaveText(/^\d{6}$/);
  const code = await popup.locator("#pairing-code").textContent();
  await popup.getByLabel("Device name").nth(1).fill(name);
  await popup.getByLabel("Six-digit code").fill(code ?? "");
  await popup.getByRole("button", { name: "Register device" }).click();
  await expect(popup.getByText("Device registered.", { exact: false })).toBeVisible();
  const message = await popup.locator("#register-message").textContent();
  const token = message?.split(": ").at(-1) ?? "";
  expect(token).not.toBe("");
  await redactPairingReceipt(popup, receiptSlug);
  return token;
};

const registerController = async (popup) => {
  await popup.getByLabel("Device name").first().fill("Fixture Admin");
  await popup.getByLabel("One-time setup secret").fill("fixture-setup-secret");
  await popup.getByRole("button", { name: "Create administrator" }).click();
  await expect(popup.getByText("Administrator created.", { exact: false })).toBeVisible();
  const adminToken = await popup.evaluate(() => sessionStorage.getItem('jastreamer-admin-token'));
  expect(adminToken).not.toBeNull();
  const token = await registerPairedDevice(
    popup,
    "Fixture Control",
    `${scenario}-pairing-portal-redacted`,
  );
  return { token, adminToken };
};

if (qaEnabled) await mkdir(output, { recursive: true });

if (qaEnabled) test("real Todo13 Control policy flow", async ({ browser }) => {
  test.setTimeout(300_000);
  const web = await startWeb(webRoot, {
    certificatePath: join(controlFixtureRoot, "control_gateway_tls_cert.pem"),
    keyPath: join(controlFixtureRoot, "control_gateway_tls_key.pem"),
  });
  const server = await startTodo13(serverRoot, web.origin);
  const failureProxy = scenario === 'control-policy-failure'
    ? await startEventGapProxy(server, web.origin)
    : undefined;
  const api = failureProxy ?? server;
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  let renderer;
  try {
    let page = await context.newPage();
    const apiTrace = [];
    page.on('response', (response) => {
      const url = new URL(response.url());
      if (url.pathname.startsWith('/api/v1/')) {
        apiTrace.push({ path: url.pathname, status: response.status() });
      }
    });
    page.setDefaultTimeout(20_000);
    const controlUrl = `${web.origin}/?server=${encodeURIComponent(api.origin)}`;
    await page.goto(scenario === "control-policy-failure"
      ? `${controlUrl}&fingerprint=${"00".repeat(32)}`
      : controlUrl);
    await enableSemantics(page);
    if (scenario === "control-policy-failure") {
      await activateFlutter(page, page.getByRole("button", { name: "Discover Server" }));
      await expect(page.getByText("Advertised fingerprint does not match Server identity.")).toBeVisible();
      await page.goto(`${controlUrl}&fingerprint=${api.fingerprint}`);
      await enableSemantics(page);
    }
    const discover = page.getByRole("button", { name: "Discover Server" });
    await expect(discover).toBeVisible();
    await resizeFlutter(page, 1280, 900);
    await page.getByLabel('Advertised SHA-256 fingerprint').focus();
    await page.keyboard.press('Tab');
    await expect(discover).toBeFocused();
    await page.screenshot({ path: join(output, `${scenario}-discover-focused.png`), fullPage: true });
    await expect(discover).toBeEnabled();
    expect(await discover.boundingBox()).not.toBeNull();
    const identityResponsePromise = page.waitForResponse(
      (response) => response.url().endsWith('/api/v1/identity'),
      { timeout: 10_000 },
    );
    await activateFlutter(page, discover);
    const identityResponse = await identityResponsePromise;
    expect(identityResponse.status()).toBe(200);
    expect(identityResponse.headers()['access-control-allow-origin']).toBe(web.origin);
    const openPairing = page.getByRole("button", { name: "Open pairing page" });
    await expect(openPairing).toBeVisible();
    await page.screenshot({ path: join(output, `${scenario}-server-card.png`), fullPage: true });
    const popupPromise = context.waitForEvent("page", { timeout: 10_000 });
    await activateFlutter(page, openPairing);
    const popup = await popupPromise;
    popup.setDefaultTimeout(10_000);
    await popup.waitForLoadState("domcontentloaded");
    const registration = await registerController(popup);
    const token = registration.token;
    const bootstrapHeaders = { authorization: `Bearer ${registration.adminToken}` };
    const recoveryCodeResponse = await context.request.post(`${api.origin}/api/v1/pairing-codes`, {
      ignoreHTTPSErrors: true,
      headers: bootstrapHeaders,
      data: { role: 'admin' },
    });
    expect(recoveryCodeResponse.status()).toBe(201);
    const recoveryCode = await recoveryCodeResponse.json();
    const recoveryPairResponse = await context.request.post(`${api.origin}/api/v1/pairings`, {
      ignoreHTTPSErrors: true,
      data: { code: recoveryCode.code, name: 'Recovery Admin' },
    });
    expect(recoveryPairResponse.status()).toBe(201);
    const recoveryAdmin = await recoveryPairResponse.json();
    const adminHeaders = { authorization: `Bearer ${recoveryAdmin.token}` };
    await popup.evaluate((adminToken) => {
      sessionStorage.setItem('jastreamer-admin-token', adminToken);
    }, recoveryAdmin.token);
      const zoneCreated = await context.request.post(`${api.origin}/api/v1/zones`, {
        ignoreHTTPSErrors: true,
        headers: adminHeaders,
        data: { zone_id: 'main', name: 'Listening room' },
      });
      expect(zoneCreated.status()).toBe(201);
      const zone = await zoneCreated.json();
      const rendererCodeResponse = await context.request.post(`${api.origin}/api/v1/pairing-codes`, {
        ignoreHTTPSErrors: true,
        headers: adminHeaders,
        data: { role: 'renderer' },
      });
      expect(rendererCodeResponse.status()).toBe(201);
      const rendererCode = await rendererCodeResponse.json();
      const rendererPairResponse = await context.request.post(`${api.origin}/api/v1/pairings`, {
        ignoreHTTPSErrors: true,
        data: { code: rendererCode.code, name: 'Fixture Renderer' },
      });
      expect(rendererPairResponse.status()).toBe(201);
    const rendererCredential = await rendererPairResponse.json();
    if (scenario === 'control-policy-failure') {
      const assigned = await context.request.put(`${api.origin}/api/v1/zones/main/renderer`, {
        ignoreHTTPSErrors: true,
        headers: { ...adminHeaders, 'if-match': String(zone.revision), 'idempotency-key': 'failure-assign-renderer' },
        data: { renderer_id: rendererCredential.device.id },
      });
      expect(assigned.status()).toBe(200);
    }
    await popup.close();
    await page.getByLabel('Controller token').focus();
    await page.screenshot({ path: join(output, `${scenario}-pairing-return-empty-focused.png`), fullPage: true });
    if (scenario === 'control-policy-failure') {
      await submitPairingToken(page, 'rejected-token');
      await expect(page.getByText('Server request failed: UNAUTHORIZED').last()).toBeVisible();
      const retryPairing = context.waitForEvent('page', { timeout: 10_000 });
      await activateFlutter(page, page.getByRole('button', { name: 'Open pairing page' }));
      await (await retryPairing).close();
      await page.bringToFront();
      await expect(page.getByText('Complete pairing return', { exact: true })).toBeVisible();
      await expect(page.getByLabel('Controller token')).toHaveValue('');
    }
    await submitPairingToken(page, token);
    const pairedStatus = page.getByText('Paired device', { exact: true });
    const errorStatus = page
      .getByText(/Server request failed|must be|identity changed/)
      .first();
    await pairedStatus.or(errorStatus).waitFor({ state: 'visible' });
    expect(await errorStatus.allTextContents(), JSON.stringify(apiTrace)).toEqual([]);
    await expect(pairedStatus).toBeVisible();

    if (scenario === 'control-policy-happy') {
      renderer = await startControlRendererFixture(server, rendererCredential);
      const assigned = await context.request.put(`${api.origin}/api/v1/zones/main/renderer`, {
        ignoreHTTPSErrors: true,
        headers: {
          ...adminHeaders,
          'if-match': String(zone.revision),
          'idempotency-key': 'happy-assign-renderer',
        },
        data: { renderer_id: rendererCredential.device.id },
      });
      expect(assigned.status()).toBe(200);
      const rendererRefresh = page.waitForResponse((response) =>
        response.url().endsWith('/api/v1/zones') && response.request().method() === 'GET',
      );
      await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server state' }));
      expect((await rendererRefresh).status()).toBe(200);
      await expect(page.getByRole('button', {
        name: /Assigned Renderer.*Fixture Renderer.*connected/,
      })).toBeVisible();
    } else {
      await expect(page.getByRole('button', { name: /Assigned Renderer.*Fixture Renderer.*unavailable/ })).toBeVisible();
    }

    await resizeFlutter(page, 1280, 3000);
    await page.screenshot({ path: join(output, `${scenario}-paired-top.png`), fullPage: true });
    if (scenario === 'control-policy-happy') {
      const search = page.getByLabel('Search title, artist, or album');
      await replaceFlutterText(page, search, 'first');
      const filtered = page.waitForResponse((response) => {
        const url = new URL(response.url());
        return url.pathname === '/api/v1/catalog/tracks' &&
          url.searchParams.get('query') === 'first';
      });
      await page.keyboard.press('Enter');
      expect((await filtered).status()).toBe(200);
      await expect(page.getByRole('group', { name: /No matching tracks.*Change the search/ })).toBeVisible();
      await page.screenshot({ path: join(output, 'control-happy-search-empty-desktop.png'), fullPage: true });
      await resizeFlutter(page, 390, 3000);
      await page.screenshot({ path: join(output, 'control-happy-search-empty-mobile.png'), fullPage: true });
      await resizeFlutter(page, 1280, 3000);
      await replaceFlutterText(page, search, '');
      const restored = page.waitForResponse((response) => {
        const url = new URL(response.url());
        return url.pathname === '/api/v1/catalog/tracks' && !url.searchParams.has('query');
      });
      await page.keyboard.press('Enter');
      expect((await restored).status()).toBe(200);

      const addButtons = page.getByRole('button', { name: /Add track .* to explicit queue/ });
      await expect(addButtons).toHaveCount(2);
      for (let index = 0; index < 2; index++) {
        const mutation = page.waitForResponse((response) =>
          response.url().endsWith('/api/v1/zones/main/queue') &&
          response.request().method() === 'POST',
        );
        await activateFlutter(page, addButtons.nth(index));
        expect((await mutation).status()).toBe(201);
      }
      const moveEarlier = page.getByRole('button', { name: 'Earlier' }).last();
      const reorder = page.waitForResponse((response) =>
        response.url().endsWith('/api/v1/zones/main/queue') &&
        response.request().method() === 'POST',
      );
      await activateFlutter(page, moveEarlier);
      expect((await reorder).status()).toBe(200);
      await page.screenshot({ path: join(output, 'control-functional-queue-reordered.png'), fullPage: true });
      await resizeFlutter(page, 390, 844);
      const mobileEarlier = page.getByRole('button', { name: 'Earlier' }).last();
      await revealFlutterAnchor(page, mobileEarlier);
      await expectTouchTarget(page, mobileEarlier);
      await expectTouchTarget(page, page.getByRole('button', { name: 'Remove' }).last());
      await page.screenshot({ path: join(output, 'control-functional-queue-reordered-mobile.png') });
      await resizeFlutter(page, 1280, 3000);

      const transport = async (name, expectedIntent) => {
        const responsePromise = page.waitForResponse((response) =>
          response.url().endsWith('/api/v1/zones/main/transport') &&
          response.request().method() === 'POST',
        );
        const button = page.getByRole('button', { name, exact: true });
        const rendererResult = renderer.nextResult();
        await activateFlutter(page, button);
        const response = await responsePromise;
        expect(response.status(), await response.text()).toBe(202);
        renderer.wake();
        await rendererResult;
        const refresh = page.getByRole('button', { name: 'Refresh Server state' });
        await expect(refresh).toBeEnabled({ timeout: 20_000 });
        const recovered = page.waitForResponse((candidate) =>
          candidate.url().endsWith('/api/v1/zones/main/playback-state') &&
          candidate.request().method() === 'GET',
        );
        await activateFlutter(page, refresh);
        expect((await recovered).status()).toBe(200);
        expect(
          renderer.records.some((record) => record.type === 'result.ack'),
          JSON.stringify(renderer.records),
        ).toBe(true);
        await expect(page.getByText(`Server intent: ${expectedIntent}`, { exact: true }).last()).toBeVisible();
        await expect(button).toBeEnabled();
      };
      await transport('Play', 'playing');
      await page.screenshot({ path: join(output, 'control-functional-play-requested.png'), fullPage: true });
      await resizeFlutter(page, 390, 844);
      const mobilePlay = page.getByRole('button', { name: 'Play', exact: true });
      await revealFlutterAnchor(page, mobilePlay);
      await expectTouchTarget(page, mobilePlay);
      await page.screenshot({ path: join(output, 'control-functional-play-requested-mobile.png') });
      await resizeFlutter(page, 1280, 3000);
      await transport('Pause', 'paused');
      await replaceFlutterText(
        page,
        page.getByLabel('Seek position (seconds)'),
        '1',
      );
      await transport('Seek', 'paused');
      await transport('Resume', 'playing');
      await transport('Next', 'suspended');
      await transport('Stop', 'idle');
    }
    if (scenario === 'control-policy-failure') {
      const controllerHeaders = { authorization: `Bearer ${token}`, 'content-type': 'application/json' };
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

      const catalogPage = await (await context.request.get(`${api.origin}/api/v1/catalog/tracks?limit=10`, { headers: controllerHeaders })).json();
      expect(catalogPage.tracks.length).toBeGreaterThanOrEqual(2);
      let serverState = await (await context.request.get(`${api.origin}/api/v1/zones/main/playback-state`, { headers: controllerHeaders })).json();
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
        page.getByText('RENDERER_OFFLINE').last(),
      );
      await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));

      serverState = await (await context.request.get(`${api.origin}/api/v1/zones/main/playback-state`, { headers: controllerHeaders })).json();
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
      const retryResponse = page.waitForResponse((response) =>
        response.url().endsWith('/api/v1/zones/main/queue') && response.request().method() === 'POST',
      );
      await activateFlutter(page, retryBlocked);
      expect((await retryResponse).status()).toBe(200);
      const retryRefresh = page.waitForResponse((response) =>
        response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
      );
      await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server state' }));
      expect((await retryRefresh).status()).toBe(200);
      await expect(retryBlocked).toBeHidden();
      await expect(page.getByRole('button', { name: 'Refresh Server state' })).toBeEnabled();

      markFixtureRendererConnected(server, rendererCredential.device.id);
      const blockedCommand = page.waitForResponse((response) =>
        response.url().endsWith('/api/v1/zones/main/transport') && response.request().method() === 'POST',
      );
      const blockedPlay = page.getByRole('button', { name: 'Play', exact: true });
      await expect(blockedPlay).toBeVisible();
      await expect(blockedPlay).toBeEnabled();
      await blockedPlay.press('Enter');
      expect((await blockedCommand).status()).toBe(409);
      await expect(page.getByText('BLOCKED_EXPLICIT_HEAD').last()).toBeVisible();
      await captureFailure(
        'control-failure-blocked-command',
        page.getByText('BLOCKED_EXPLICIT_HEAD').last(),
      );
      await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));

      serverState = await (await context.request.get(`${api.origin}/api/v1/zones/main/playback-state`, { headers: controllerHeaders })).json();
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
        page.getByText('INVALID_STATE').last(),
      );
      const commandRecovery = page.waitForResponse((response) =>
        response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
      );
      await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));
      expect((await commandRecovery).status()).toBe(200);
      await expect(page.getByRole('button', { name: 'Refresh Server state' })).toBeEnabled();

      serverState = await (await context.request.get(`${api.origin}/api/v1/zones/main/playback-state`, { headers: controllerHeaders })).json();
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
        page.getByText('STALE_REVISION').last(),
      );
      const staleRecovery = page.waitForResponse((response) =>
        response.url().endsWith('/api/v1/zones/main/playback-state') && response.request().method() === 'GET',
      );
      await activateFlutter(page, page.getByRole('button', { name: 'Refresh Server truth' }));
      expect((await staleRecovery).status()).toBe(200);
      await expect(page.getByRole('button', { name: 'Refresh Server state' })).toBeEnabled();

      const devices = await (await context.request.get(`${api.origin}/api/v1/devices`, { headers: adminHeaders })).json();
      const controller = devices.devices.find((device) => device.name === 'Fixture Control');
      expect(controller).toBeTruthy();
      const revoked = await context.request.delete(`${api.origin}/api/v1/devices/${controller.id}`, { headers: adminHeaders });
      expect(revoked.status()).toBe(204);
      const devicesAfter = await (await context.request.get(`${api.origin}/api/v1/devices`, { headers: adminHeaders })).json();
      const revokedDevice = devicesAfter.devices.find((device) => device.id === controller.id);
      expect(revokedDevice?.revoked, JSON.stringify(devicesAfter.devices)).toBe(true);
      const revokedDirectProbe = await context.request.post(`${server.origin}/api/v1/event-tickets`, {
        headers: controllerHeaders,
        data: {},
      });
      expect(revokedDirectProbe.status(), JSON.stringify(devicesAfter.devices)).toBe(401);
      const revokedProbe = await context.request.get(`${api.origin}/api/v1/catalog/tracks?limit=1`, { headers: controllerHeaders });
      expect(revokedProbe.status(), JSON.stringify(devicesAfter.devices)).toBe(401);
      const revokedRequest = page.waitForResponse((response) =>
        response.url().includes('/api/v1/catalog/tracks') && response.status() === 401,
      );
    await replaceFlutterText(
      page,
      page.getByLabel('Search title, artist, or album'),
      'revoked',
    );
      await activateFlutter(page, page.getByRole('button', { name: 'Search catalog' }));
      await revokedRequest;
      const revokedBanner = page.getByText('TOKEN_REVOKED').last();
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
    }
    if (scenario === 'control-policy-happy') {
      for (const label of ["재생 종료", "앨범 이어듣기", "비슷한 음악"]) {
        await activateFlutter(page, page.getByText(label, { exact: true }));
        await activateFlutter(page, page.getByRole("button", { name: "Save policy" }));
        await expect(page.getByText(/Server policy revision \d+ · saved/)).toBeVisible();
      }
    }
    await resizeFlutter(page, 1280, 900);
    await page.screenshot({ path: join(output, `${scenario}-desktop-viewport.png`) });
    await resizeFlutter(page, 1280, 3000);
    await page.screenshot({ path: join(output, `${scenario}-desktop.png`), fullPage: true });
    await resizeFlutter(page, 390, 6000);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: join(output, `${scenario}-mobile-full.png`), fullPage: true });
    await resizeFlutter(page, 390, 844);
    if (scenario === 'control-policy-happy') {
      await page.mouse.move(195, 422);
      await page.mouse.wheel(0, 100000);
      await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      await page.screenshot({ path: join(output, `${scenario}-mobile-bottom.png`) });
      await page.mouse.wheel(0, -100000);
      await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    }
    await expect(page.getByRole('heading', { name: 'Control room' })).toBeVisible();
    await page.screenshot({ path: join(output, `${scenario}-mobile-top.png`) });
    await writeFile(join(output, `${scenario}.json`), `${JSON.stringify({ scenario, status: "passed", token: "[REDACTED]", code: "[REDACTED]" }, null, 2)}\n`);
  } finally {
    await context.close();
    if (renderer) await renderer.close();
    if (failureProxy) {
      await failureProxy.close();
    }
    await stopChild(server.child);
    await rm(server.directory, { recursive: true, force: true });
    await new Promise((resolve, reject) => web.server.close((error) => error ? reject(error) : resolve()));
  }
});
