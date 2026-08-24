import { expect, test } from "@playwright/test";
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { startEdgeApi, startTodo13, startWeb, stopChild } from './control-servers.mjs';

const root = join(import.meta.dirname, "../..");
const serverRoot = join(root, "apps/server");
const webRoot = join(root, "apps/control/build/web");
const fixturePath = process.env.CONTROL_FIXTURE;
const output = process.env.CONTROL_OUTPUT;
if (!fixturePath || !output) throw new TypeError("CONTROL_FIXTURE and CONTROL_OUTPUT are required");
const fixture = await readFile(fixturePath, "utf8");
const scenario = fixture.match(/^scenario:\s*(.+)$/m)?.[1];
if (scenario !== "control-policy-happy" && scenario !== "control-policy-failure") {
  throw new TypeError("unsupported Control QA scenario");
}
const expectedByScenario = {
  "control-policy-happy": ["real-todo13-authenticated-discovery", "server-advertised-https-pairing", "explicit-token-return", "stop-album-similar-save", "analysis-coverage-incomplete-metadata-fallback", "explicit-versus-automatic-preview", "desktop-mobile-screenshots"],
  "control-policy-failure": ["certificate-mismatch-rejected", "rejected-token-cleared", "stale-revision-intent-preserved", "analysis-coverage-incomplete-metadata-fallback", "unavailable-explicit-head-no-automatic-fallback", "revocable-preview", "committed-preview", "stop-similar-no-signal", "no-secret-evidence"],
};
const fixtureExpects = [...fixture.matchAll(/^\s+-\s+(.+)$/gm)].map((match) => match[1]);
if (JSON.stringify(fixtureExpects) !== JSON.stringify(expectedByScenario[scenario])) {
  throw new TypeError("Control fixture expects list drifted");
}

const enableSemantics = async (page) => {
  const button = page.getByRole("button", { name: "Enable accessibility" });
  const discover = page.getByRole('button', { name: 'Discover Server' });
  await button.or(discover).waitFor({ state: 'visible' });
  if (await discover.isVisible()) return;
  await button.focus();
  await page.keyboard.press("Enter");
  await discover.waitFor({ state: 'visible' });
};

const activateFlutter = async (page, locator) => {
  await expect(locator).toBeVisible();
  await expect(locator).toBeEnabled();
  if (!await locator.boundingBox()) throw new TypeError('Flutter control has no action target');
  await locator.focus();
  await expect(locator).toBeFocused();
  await page.keyboard.press('Enter');
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
};

const submitPairingToken = async (page, token) => {
  const input = page.getByLabel('Controller token');
  await input.focus();
  await expect(input).toBeFocused();
  await input.fill(token);
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(resolve)));
  await expect(page.getByLabel('Controller token')).toHaveValue(token);
  const responsePromise = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/discovery') &&
    response.request().method() === 'GET',
  );
  const submit = page.getByRole('button', { name: 'Complete pairing', exact: true });
  await expect(submit).toBeVisible();
  await expect(submit).toBeEnabled();
  await submit.focus();
  await expect(submit).toBeFocused();
  await page.keyboard.press('Enter');
  const response = await responsePromise;
  expect(response.status()).toBe(token === 'rejected-token' ? 401 : 200);
};

const registerController = async (popup) => {
  await popup.getByLabel("Device name").first().fill("Fixture Admin");
  await popup.getByLabel("One-time setup secret").fill("fixture-setup-secret");
  await popup.getByRole("button", { name: "Create administrator" }).click();
  await expect(popup.getByText("Administrator created.", { exact: false })).toBeVisible();
  await popup.getByRole("button", { name: "Generate code" }).click();
  await expect(popup.locator("#pairing-code")).toHaveText(/^\d{6}$/);
  const code = await popup.locator("#pairing-code").textContent();
  await popup.getByLabel("Device name").nth(1).fill("Fixture Control");
  await popup.getByLabel("Six-digit code").fill(code ?? "");
  await popup.getByRole("button", { name: "Register device" }).click();
  await expect(popup.getByText("Device registered.", { exact: false })).toBeVisible();
  const message = await popup.locator("#register-message").textContent();
  const token = message?.split(": ").at(-1) ?? "";
  expect(token).not.toBe("");
  await popup.locator("#register-message").evaluate((element) => { element.textContent = "Device registered. Token: [REDACTED]"; });
  await popup.locator("#pairing-code").evaluate((element) => { element.textContent = "[REDACTED]"; });
  await popup.screenshot({ path: join(output, `${scenario}-pairing-portal-redacted.png`), fullPage: true });
  return token;
};

await mkdir(output, { recursive: true });

test("real Todo13 Control policy flow", async ({ browser }) => {
  test.setTimeout(180_000);
  const web = await startWeb(webRoot);
  const server = await startTodo13(serverRoot, web.origin);
  const edge = scenario === "control-policy-failure"
    ? await startEdgeApi(server, web.origin)
    : undefined;
  const api = edge ?? server;
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
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
    const token = await registerController(popup);
    await popup.close();
    await page.getByLabel('Controller token').focus();
    await page.screenshot({ path: join(output, `${scenario}-pairing-return-empty-focused.png`), fullPage: true });
    let unauthorized;
    if (scenario === 'control-policy-failure') {
      await submitPairingToken(page, 'rejected-token');
      unauthorized = page.getByText('Server request failed: UNAUTHORIZED').last();
      await expect(unauthorized).toBeVisible();
      await page.close();
      unauthorized = undefined;
      page = await context.newPage();
      page.setDefaultTimeout(20_000);
      await page.goto(`${controlUrl}&fingerprint=${api.fingerprint}`);
      await enableSemantics(page);
      await activateFlutter(page, page.getByRole('button', { name: 'Discover Server' }));
      const redundantPopup = context.waitForEvent('page', { timeout: 10_000 });
      await activateFlutter(page, page.getByRole('button', { name: 'Open pairing page' }));
      await (await redundantPopup).close();
      await page.bringToFront();
      await expect(page.getByText('Complete pairing return', { exact: true })).toBeVisible();
      await expect(page.getByLabel('Controller token')).toBeVisible();
    }
    await submitPairingToken(page, token);
    if (unauthorized) await expect(unauthorized).toBeHidden();
    const pairedStatus = page.getByText('Paired device', { exact: true });
    const errorStatus = page.getByText(/Server request failed|must be|identity changed/);
    await pairedStatus.or(errorStatus).waitFor({ state: 'visible' });
    expect(await errorStatus.count(), JSON.stringify(apiTrace)).toBe(0);
    await expect(pairedStatus).toBeVisible();
    await expect(page.getByText(/Analysis incomplete/)).toBeVisible();
    await page.screenshot({ path: join(output, `${scenario}-paired-top.png`), fullPage: true });
    if (scenario === "control-policy-failure") {
      const edgeHeaders = { authorization: `Bearer ${token}` };
      const queueState = await (await context.request.get(`${api.origin}/api/v1/zones/main/queue`, { headers: edgeHeaders })).json();
      const previewState = await (await context.request.get(`${api.origin}/api/v1/zones/main/automatic-preview`, { headers: edgeHeaders })).json();
      expect(queueState.queue[0].state).toBe('blocked');
      expect(previewState.replaceable).toBe(true);
      expect(previewState.decision.reason).toBe('BLOCK_EXPLICIT');
      await resizeFlutter(page, 1280, 3000);
      await expect(page.getByRole('heading', { name: 'Explicit queue' })).toBeVisible();
      const noFallback = page.getByLabel('No automatic fallback while the explicit head is blocked.');
      await expect(noFallback).toBeVisible();
      await expect(page.getByText(/^Unavailable explicit head\b/)).toBeVisible();
      await expect(page.getByText(/^Revocable preview\b/)).toBeVisible();
      await page.screenshot({ path: join(output, 'control-policy-failure-blocked.png'), fullPage: true });
    }
    for (const label of ["재생 종료", "앨범 이어듣기", "비슷한 음악"]) {
      await activateFlutter(page, page.getByText(label, { exact: true }));
      await activateFlutter(page, page.getByRole("button", { name: "Save policy" }));
      await expect(page.getByText(/Server policy revision \d+ · saved/)).toBeVisible();
    }
    if (scenario === "control-policy-failure") {
      const changed = await context.request.patch(`${api.origin}/api/v1/zones/main/continuation-policy`, {
        ignoreHTTPSErrors: true,
        headers: { authorization: `Bearer ${token}`, "content-type": "application/json", "if-match": "3" },
        data: { mode: "album", artist_gap: 4, album_gap: 10, session_override: "" },
      });
      expect(changed.status()).toBe(200);
      await activateFlutter(page, page.getByText("재생 종료", { exact: true }));
      await activateFlutter(page, page.getByRole("button", { name: "Save policy" }));
      await expect(page.getByText(/desired intent preserved for retry/)).toBeVisible();
      await activateFlutter(page, page.getByRole("button", { name: "Refresh Server state" }));
      const refreshedPreview = await (await context.request.get(`${api.origin}/api/v1/zones/main/automatic-preview`, {
        headers: { authorization: `Bearer ${token}` },
      })).json();
      expect(refreshedPreview.committed).toBe(true);
      expect(refreshedPreview.decision.reason).toBe('STOP_SIMILAR_NO_SIGNAL');
    }
    await resizeFlutter(page, 1280, 3000);
    const explicitQueue = page.getByRole('heading', { name: 'Explicit queue' });
    await expect(explicitQueue).toBeVisible();
    const automaticPreview = page.getByRole('heading', { name: 'Automatic next preview' });
    await expect(automaticPreview).toBeVisible();
    if (scenario === 'control-policy-failure') {
      const committed = page.getByText(/^Committed preview\b/);
      await expect(committed).toBeVisible();
      const noSignal = page.getByLabel(/No signal → Server stops playback\./);
      await expect(noSignal).toBeVisible();
    }
    await page.screenshot({ path: join(output, `${scenario}-desktop.png`), fullPage: true });
    await resizeFlutter(page, 390, 3000);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: join(output, `${scenario}-mobile-full.png`), fullPage: true });
    await resizeFlutter(page, 390, 844);
    await expect(page.getByRole('heading', { name: 'Control room' })).toBeVisible();
    await page.screenshot({ path: join(output, `${scenario}-mobile-top.png`), fullPage: true });
    await writeFile(join(output, `${scenario}.json`), `${JSON.stringify({ scenario, status: "passed", token: "[REDACTED]", code: "[REDACTED]" }, null, 2)}\n`);
  } finally {
    await context.close();
    if (edge) {
      await new Promise((resolve, reject) => edge.server.close((error) => error ? reject(error) : resolve()));
    }
    await stopChild(server.child);
    await rm(server.directory, { recursive: true, force: true });
    await new Promise((resolve, reject) => web.server.close((error) => error ? reject(error) : resolve()));
  }
});
