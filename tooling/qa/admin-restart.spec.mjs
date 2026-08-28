import { expect, test } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { subscribeAdminEvents } from "./admin-events.mjs";
import { AdminServerFixture } from "./admin-server.mjs";
import { awaitSubscribedMutation } from "./subscribe-before-mutate.mjs";

const output = process.env.ADMIN_RESTART_OUTPUT;
const qaEnabled = output !== undefined;

const requestJSON = async (request, input) => request.fetch(input.url, {
  method: input.method,
  data: input.data,
  headers: input.token ? { Authorization: `Bearer ${input.token}` } : {},
});

const pairRole = async (request, registration) => {
  const generated = await requestJSON(request, {
    method: "POST",
    url: `${registration.origin}/api/v1/pairing-codes`,
    token: registration.adminToken,
    data: { role: registration.role },
  });
  expect(generated.status()).toBe(201);
  const code = await generated.json();
  const paired = await requestJSON(request, {
    method: "POST",
    url: `${registration.origin}/api/v1/pairings`,
    data: { code: code.code, name: registration.name },
  });
  expect(paired.status()).toBe(201);
  return await paired.json();
};

const redactRestartEvidence = async (page) => page.evaluate(() => {
  for (const id of ["listen-address", "certificate-fingerprint", "certificate-sans", "data-directory"]) {
    const control = document.getElementById(id);
    if (control) control.value = "[REDACTED]";
  }
  for (const detail of document.querySelectorAll("#catalog-roots .inventory-copy p, #device-list .inventory-copy p")) {
    detail.textContent = "[REDACTED IDENTIFIER]";
  }
  const active = document.getElementById("active-device-name");
  if (active) active.textContent = "[ACTIVE ADMIN REDACTED]";
});

if (qaEnabled) await mkdir(output, { recursive: true });

if (qaEnabled) test("admin state persists through a real same-directory Server restart", async ({ browser }) => {
  const server = await AdminServerFixture.create();
  let context;
  let cleanup;
  try {
    const firstLifecycle = await server.start();
    context = await browser.newContext({
      ignoreHTTPSErrors: true,
      viewport: { width: 1280, height: 900 },
      colorScheme: "light",
    });
    const page = await context.newPage();
    const pageErrors = [];
    page.on("pageerror", (error) => { pageErrors.push(error.message); });
    await page.goto(`${server.origin}/admin/`);

    const bootstrap = await requestJSON(context.request, {
      method: "POST",
      url: `${server.origin}/api/v1/bootstrap`,
      data: { setup_secret: "admin-restart-fixture-secret", name: "Persistent Admin" },
    });
    expect(bootstrap.status()).toBe(201);
    const admin = await bootstrap.json();
    const pairedRegistration = { origin: server.origin, adminToken: admin.token };
    const recoveryAdmin = await pairRole(context.request, {
      ...pairedRegistration,
      role: "admin",
      name: "Recovery Admin",
    });
    const controller = await pairRole(context.request, {
      ...pairedRegistration,
      role: "controller",
      name: "Revoked Controller",
    });
    const renderer = await pairRole(context.request, {
      ...pairedRegistration,
      role: "renderer",
      name: "Persistent Renderer",
    });
    const rendererSession = await requestJSON(context.request, {
      method: "GET",
      url: `${server.origin}/api/v1/renderers/${renderer.device.id}/session`,
      token: renderer.token,
    });
    expect(rendererSession.status()).toBe(204);
    const zone = await requestJSON(context.request, {
      method: "POST",
      url: `${server.origin}/api/v1/zones`,
      token: admin.token,
      data: { zone_id: "restart-zone", name: "Restart Zone" },
    });
    expect(zone.status()).toBe(201);

    await page.getByLabel("Administrator token").fill(admin.token);
    await page.getByRole("button", { name: "Open administration" }).click();
    await expect(page.locator("#admin-shell")).toBeVisible();
    const firstEvents = await subscribeAdminEvents(page);

    await page.getByLabel("Server display name").fill("Persistent Restart Server");
    await page.getByLabel("Pairing TTL in seconds").fill("601");
    await page.getByLabel("Exact Control origins").fill("https://restart-control.fixture.invalid");
    const firstConfigEvent = await awaitSubscribedMutation({
      subscribe: () => firstEvents.subscribe("config"),
      mutate: () => page.getByRole("button", { name: "Save settings" }).click(),
      timeoutMs: 10_000,
    });
    await expect(page.locator("#restart-banner")).toBeVisible();
    await expect(page.locator("#settings-message")).toContainText("saved");
    expect(firstConfigEvent.server_epoch).toBe(firstEvents.initial.server_epoch);
    expect(firstConfigEvent.sequence).toBe(firstEvents.initial.sequence + 1);

    await page.getByLabel("Catalog root path").fill(server.secondRoot);
    await page.getByLabel("Catalog root name").fill("Restart Archive");
    await page.getByRole("button", { name: "Add root" }).click();
    await expect(page.locator("#catalog-roots")).toContainText("Restart Archive");

    await page.getByLabel("Renderer for Restart Zone").selectOption(renderer.device.id);
    await page.getByRole("button", { name: "Assign Restart Zone" }).click();
    await expect(page.locator("#zones-list")).toContainText("Persistent Renderer");

    await page.getByRole("button", { name: "Revoke Revoked Controller" }).click();
    await expect(page.locator("#device-message")).toContainText("Revoked Controller was revoked");
    await expect(page.getByRole("button", { name: "Revoke Revoked Controller" })).toBeDisabled();

    const shutdownSignal = firstEvents.waitForClose();
    const secondLifecycle = await server.restart();
    expect(secondLifecycle.origin).toBe(firstLifecycle.origin);
    expect(secondLifecycle.fingerprint).toBe(firstLifecycle.fingerprint);
    await expect(shutdownSignal).resolves.toEqual({ code: 1001, reason: "server shutting down", clean: true });

    await page.reload();
    await expect(page.locator("#admin-shell")).toBeVisible();
    expect(await page.evaluate(() => sessionStorage.getItem("jastreamer-admin-token"))).toBe(admin.token);
    await expect(page.getByLabel("Server display name")).toHaveValue("Persistent Restart Server");
    await expect(page.getByLabel("Pairing TTL in seconds")).toHaveValue("601");
    await expect(page.getByLabel("Exact Control origins")).toHaveValue("https://restart-control.fixture.invalid");
    await expect(page.locator("#restart-banner")).toBeHidden();
    await expect(page.locator("#catalog-roots")).toContainText("Restart Archive");
    await expect(page.locator("#zones-list")).toContainText("Persistent Renderer");
    await expect(page.locator("#device-list")).toContainText("Revoked Controller");
    await expect(page.getByRole("button", { name: "Revoke Revoked Controller" })).toBeDisabled();

    expect((await requestJSON(context.request, {
      method: "GET",
      url: `${server.origin}/api/v1/config`,
      token: recoveryAdmin.token,
    })).status()).toBe(200);
    expect((await requestJSON(context.request, {
      method: "GET",
      url: `${server.origin}/api/v1/config`,
      token: controller.token,
    })).status()).toBe(401);
    const secondEvents = await subscribeAdminEvents(page);
    expect(secondEvents.initial.server_epoch).not.toBe(firstEvents.initial.server_epoch);
    await page.getByLabel("Server display name").fill("Persistent Restart Server Verified");
    const secondConfigEvent = await awaitSubscribedMutation({
      subscribe: () => secondEvents.subscribe("config"),
      mutate: () => page.getByRole("button", { name: "Save settings" }).click(),
      timeoutMs: 10_000,
    });
    expect(secondConfigEvent.server_epoch).toBe(secondEvents.initial.server_epoch);
    expect(secondConfigEvent.sequence).toBeGreaterThan(secondEvents.initial.sequence);
    await expect(page.locator("#settings-message")).toContainText("saved");

    await redactRestartEvidence(page);
    await page.screenshot({ path: join(output, "admin-restart-persisted-redacted.png"), fullPage: true });
    expect(pageErrors).toEqual([]);
    await writeFile(join(output, "browser-result.json"), `${JSON.stringify({
      status: "passed",
      server: "real-ephemeral-tls-same-origin-restart",
      dataDirectory: "same-temporary-directory",
      startupLifecycles: 2,
      checks: {
        settingsPersistence: "pass",
        rootPersistence: "pass",
        rendererAssignmentPersistence: "pass",
        activeAuthenticationPersistence: "pass",
        revocationPersistence: "pass",
        browserSessionPersistence: "pass",
        gracefulEventShutdown: "pass",
        eventEpochRotation: "pass",
        orderedEventsBeforeAndAfterRestart: "pass",
      },
      pageErrors,
      secretsRecorded: false,
      absolutePathsRecorded: false,
    }, null, 2)}\n`);
    await secondEvents.close();
  } finally {
    if (context) await context.close();
    cleanup = await server.cleanup();
    await writeFile(join(output, "cleanup.json"), `${JSON.stringify(cleanup, null, 2)}\n`);
  }
});
