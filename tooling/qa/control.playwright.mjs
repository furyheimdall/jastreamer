import { chromium, expect, test } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { mkdir, rename, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import {
  activateFlutter,
  registerController,
  replaceFlutterText,
  resizeFlutter,
  submitPairingToken,
} from './control-playwright-actions.mjs';
import { runFailureFlow } from './control-failure-flow.mjs';
import { runHappyFlow } from './control-happy-flow.mjs';
import {
  controlFixtureRoot,
  output,
  qaEnabled,
  root,
  scenario,
  serverRoot,
} from './control-scenario-fixture.mjs';
import {
  cleanupControlResources,
  releaseTodo13,
  startEventGapProxy,
  startTodo13,
  startWeb,
} from './control-servers.mjs';
import { startControlRendererFixture } from './control-renderer-fixture.mjs';
import { createControlContext, createControlStartupNavigator, enableSemantics } from './control-startup.mjs';
import { captureControlViewports } from './control-viewport-evidence.mjs';

const configuredWebRoot = process.env.CONTROL_WEB_ROOT; const inputRegression = process.env.CONTROL_INPUT_REGRESSION === '1';
const webRoot = configuredWebRoot === undefined
  ? join(root, "apps/control/build/web")
  : resolve(configuredWebRoot);

if (qaEnabled) await mkdir(output, { recursive: true });

if (qaEnabled) test("real Todo13 Control policy flow", async () => {
  test.setTimeout(300_000);
  let web, server, failureProxy, browser;
  let context, page, renderer, primaryFailure;
  let contextClosed = false;
  try {
    web = await startWeb(webRoot, {
      certificatePath: join(controlFixtureRoot, "control_gateway_tls_cert.pem"),
      keyPath: join(controlFixtureRoot, "control_gateway_tls_key.pem"),
    });
    server = await startTodo13(serverRoot, web.origin);
    failureProxy = scenario === 'control-policy-failure'
      ? await startEventGapProxy(server, web.origin)
      : undefined;
    const api = failureProxy ?? server;
    process.stdout.write(`CONTROL_SERVER_READY ${JSON.stringify({ proxy: failureProxy?.origin ?? null, server: server.origin, web: web.origin })}\n`);
    const spkiPins = [web.spkiPinBase64, api.spkiPinBase64];
    if (new Set(spkiPins).size !== 2) throw new Error('Control browser trust pins are not distinct');
    browser = await chromium.launch({ headless: true, args: [`--ignore-certificate-errors-spki-list=${spkiPins.join(',')}`] });
    process.stdout.write(`CONTROL_BROWSER_TRUST ${JSON.stringify({ exactSpkiPins: spkiPins.length, ignoreHTTPSErrors: false })}\n`);
    context = await createControlContext(browser);
    context.once('close', () => { contextClosed = true; });
    const navigateControl = await createControlStartupNavigator(context);
    page = await context.newPage();
    const apiTrace = [];
    page.on('response', (response) => {
      const url = new URL(response.url());
      if (url.pathname.startsWith('/api/v1/')) {
        apiTrace.push({ path: url.pathname, status: response.status() });
      }
    });
    page.setDefaultTimeout(20_000);
    const controlUrl = `${web.origin}/?server=${encodeURIComponent(api.origin)}&qa=${randomUUID()}`;
    const startup = await navigateControl(page, scenario === "control-policy-failure"
      ? `${controlUrl}&fingerprint=${"00".repeat(32)}`
      : controlUrl);
    expect([startup.navigationStatus, startup.serviceWorker, new URL(startup.workerUrl).pathname]).toEqual([200, 'activated', '/flutter_service_worker.js']);
    await enableSemantics(page);
    if (scenario === "control-policy-failure") {
      await activateFlutter(page, page.getByRole("button", { name: "Discover Server" }));
      await expect(page.getByText("Advertised fingerprint does not match Server identity.")).toBeVisible();
      await navigateControl(page, `${controlUrl}&fingerprint=${api.fingerprint}`);
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
    const flow = {
      page,
      context,
      api,
      server,
      failureProxy,
      token,
      rendererCredential,
      adminHeaders,
      recoveryAdmin,
      renderer,
    };
    if (inputRegression) {
      const search = page.getByLabel('Search title, artist, or album');
      const entered = await replaceFlutterText(search, 'first');
      const cleared = await replaceFlutterText(search, '');
      console.log(`CONTROL_INPUT_SEMANTICS ${JSON.stringify({
        process: process.pid,
        scenario,
        entered,
        cleared,
      })}`);
    } else if (scenario === 'control-policy-happy') {
      await runHappyFlow(flow);
    } else if (scenario === 'control-policy-failure') {
      await runFailureFlow(flow);
    }
    await captureControlViewports(page);
  } catch (error) {
    primaryFailure = error;
  }
  await cleanupControlResources({
    primaryFailure,
    operations: [
      ...(page ? [{ name: 'page', state: page.isClosed() ? 'absent' : 'present', close: () => page.close() }] : []),
      ...(context ? [{ name: 'context', state: contextClosed ? 'absent' : 'present', close: () => context.close() }] : []),
      ...(browser ? [{ name: 'browser', state: browser.isConnected() ? 'present' : 'absent', close: () => browser.close() }] : []),
      ...(renderer ? [{ name: 'renderer', state: 'present', close: () => renderer.close() }] : []),
      ...(failureProxy ? [{ name: 'failure-proxy', state: 'present', close: () => failureProxy.close() }] : []),
      ...(server ? [{ name: 'server', state: 'present', close: () => releaseTodo13(server) }] : []),
      ...(web ? [{ name: 'web', state: 'present', close: () => web.close() }] : []),
    ],
  });
  if (!inputRegression) {
    const resultPath = join(output, `${scenario}.json`);
    const temporary = `${resultPath}.${randomUUID()}.tmp`;
    const result = { scenario, status: "passed", token: "[REDACTED]", code: "[REDACTED]" };
    await writeFile(temporary, `${JSON.stringify(result, null, 2)}\n`, { flag: "wx" });
    await rename(temporary, resultPath);
  }
});
