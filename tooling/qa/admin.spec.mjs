import { expect, test } from "@playwright/test";
import { spawn, spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";

const repository = join(import.meta.dirname, "../..");
const serverRoot = join(repository, "apps/server");
const output = process.env.ADMIN_OUTPUT;
const qaEnabled = output !== undefined;

const startServer = async () => {
  const directory = await mkdtemp(join(tmpdir(), "jastreamer-admin-"));
  const media = join(directory, "music");
  const secondRoot = join(media, "archive");
  await mkdir(secondRoot, { recursive: true });
  await writeFile(join(secondRoot, "sample.wav"), "not media; scanner must report deterministically");
  const binary = join(directory, "jastreamer-server");
  const built = spawnSync("go", ["build", "-o", binary, "./cmd/jastreamer-server"], { cwd: serverRoot, encoding: "utf8" });
  if (built.status !== 0) {
    await rm(directory, { recursive: true, force: true });
    throw new Error(`server build failed: ${built.stderr}`);
  }
  const child = spawn(binary, ["--config", "../../tooling/fixtures/e2e/local.yaml"], {
    cwd: serverRoot,
    env: {
      ...process.env,
      JASTREAMER_DATA_DIR: directory,
      JASTREAMER_CATALOG_ROOT: media,
      JASTREAMER_SETUP_SECRET: "admin-fixture-secret",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += chunk.toString(); });
  const origin = await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error(`server readiness timeout: ${stderr}`)), 30_000);
    child.once("exit", (code) => { clearTimeout(timeout); reject(new Error(`server exited ${code}: ${stderr}`)); });
    child.stdout.on("data", (chunk) => {
      const match = chunk.toString().match(/ready (https:\/\/[^ ]+) fingerprint=/);
      if (match?.[1]) { clearTimeout(timeout); resolve(match[1]); }
    });
  });
  return { binary, child, directory, media, origin, secondRoot };
};

const stopServer = async (server) => {
  if (server.child.exitCode === null) {
    const exited = new Promise((resolve) => server.child.once("exit", resolve));
    server.child.kill("SIGTERM");
    await Promise.race([exited, new Promise((_, reject) => setTimeout(() => reject(new Error("server shutdown timeout")), 10_000))]);
  }
  const cleanup = { processExited: server.child.exitCode !== null, directoryRemoved: false };
  await rm(server.directory, { recursive: true, force: true });
  cleanup.directoryRemoved = true;
  return cleanup;
};

const json = async (request, method, url, token = "", data, headers = {}) => request.fetch(url, {
  method,
  data,
  headers: { ...headers, ...(token ? { Authorization: `Bearer ${token}` } : {}) },
});

const redactEvidence = async (page) => page.evaluate(() => {
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

const pairRole = async (request, origin, adminToken, role, name) => {
  const generated = await json(request, "POST", `${origin}/api/v1/pairing-codes`, adminToken, { role });
  expect(generated.status()).toBe(201);
  const code = await generated.json();
  const paired = await json(request, "POST", `${origin}/api/v1/pairings`, "", { code: code.code, name });
  expect(paired.status()).toBe(201);
  return await paired.json();
};

if (qaEnabled) await mkdir(output, { recursive: true });

if (qaEnabled) test("admin management application", async ({ browser }) => {
  const server = await startServer();
  let cleanup;
  try {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 }, colorScheme: "light" });
    const page = await context.newPage();
    const pageErrors = [];
    page.on("pageerror", (error) => { pageErrors.push(error.message); console.error(`pageerror: ${error.message}`); });
    page.on("console", (message) => { if (message.type() === "error") console.error(`console: ${message.text()}`); });
    await page.goto(`${server.origin}/admin/`);
    await expect(page.getByRole("heading", { name: "Server administration" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Administrator session" })).toBeVisible();
    await page.emulateMedia({ reducedMotion: "reduce" });
    const skipTransform = await page.locator(".skip-link").evaluate((element) => getComputedStyle(element).transform);
    expect(skipTransform).not.toBe("none");
    await page.locator(".skip-link").focus();
    expect(["none", "matrix(1, 0, 0, 1, 0, 0)"]).toContain(
      await page.locator(".skip-link").evaluate((element) => getComputedStyle(element).transform),
    );
    await page.emulateMedia({ reducedMotion: "no-preference" });

    await page.getByLabel("Administrator token").fill("invalid-token");
    await page.getByRole("button", { name: "Open administration" }).click();
    await expect(page.locator("#login-error")).toBeVisible();
    await expect(page.locator("#login-error")).toBeFocused();
    await expect(page.locator("#admin-token")).toHaveValue("");
    await expect(page.locator("#admin-shell")).toBeHidden();
    const invalidCredentialCleared = await page.locator("#admin-token").inputValue() === "";
    await page.screenshot({ path: join(output, "admin-login-error-redacted.png"), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: join(output, "admin-login-error-mobile-redacted.png"), fullPage: true });
    await page.setViewportSize({ width: 1280, height: 900 });

    const bootstrap = await json(context.request, "POST", `${server.origin}/api/v1/bootstrap`, "", {
      setup_secret: "admin-fixture-secret",
      name: "Browser Admin",
    });
    expect(bootstrap.status()).toBe(201);
    const admin = await bootstrap.json();
    const secondAdmin = await pairRole(context.request, server.origin, admin.token, "admin", "Recovery Admin");
    const controller = await pairRole(context.request, server.origin, admin.token, "controller", "Secondary Controller");
    const renderer = await pairRole(context.request, server.origin, admin.token, "renderer", "Studio Renderer");
    const zone = await json(context.request, "POST", `${server.origin}/api/v1/zones`, admin.token, { zone_id: "studio", name: "Studio" });
    expect(zone.status()).toBe(201);

    await page.getByLabel("Administrator token").fill(controller.token);
    await page.getByRole("button", { name: "Open administration" }).click();
    await expect(page.locator("#login-error")).toContainText("ADMIN_REQUIRED");
    await expect(page.locator("#login-error")).toBeFocused();
    await expect(page.locator("#admin-token")).toHaveValue("");
    const wrongRoleCredentialCleared = await page.locator("#admin-token").inputValue() === "";

    await page.getByLabel("Administrator token").fill(admin.token);
    await page.getByRole("button", { name: "Open administration" }).click();
    await expect(page.locator("#admin-shell")).toBeVisible();
    expect(await page.evaluate(() => sessionStorage.getItem("jastreamer-admin-token"))).toBe(admin.token);
    expect(await page.evaluate(() => localStorage.length)).toBe(0);
    await expect(page.locator("#admin-token")).toHaveValue("");
    await expect(page.locator('.section-nav [aria-current="location"]')).toHaveCount(1);
    await expect(page.locator('#nav-settings[aria-current="location"]')).toBeVisible();
    await page.locator('a[href="#catalog"]').click();
    await expect(page.locator('a[href="#catalog"][aria-current="location"]')).toBeVisible();
    await page.locator('a[href="#renderers"]').focus();
    await page.keyboard.press("Enter");
    await expect(page.locator('a[href="#renderers"][aria-current="location"]')).toBeVisible();
    await page.locator('a[href="#settings"]').click();

    await expect(page.getByLabel("Listen address")).toBeDisabled();
    await expect(page.getByLabel("Certificate fingerprint")).toBeDisabled();
    await expect(page.getByLabel("Data directory")).toBeDisabled();

    await page.getByLabel("Pairing TTL in seconds").fill("600");
    await page.getByLabel("Exact Control origins").fill("https://control.fixture.invalid\nhttps://tablet.fixture.invalid");
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect(page.locator("#restart-banner")).toBeVisible();
    await expect(page.locator("#settings-message")).toContainText("saved");

    await page.getByLabel("Catalog root path").fill(server.secondRoot);
    await page.getByLabel("Catalog root name").fill("Archive");
    await page.getByRole("button", { name: "Add root" }).click();
    await expect(page.locator("#root-message")).toContainText("Catalog root added");
    await expect(page.locator("#catalog-roots")).toContainText("Archive");
    await page.getByRole("button", { name: "Scan Archive" }).click();
    await expect(page.locator("#scan-jobs")).toContainText("Archive");

    await expect(page.locator("#renderer-inventory")).toContainText("Studio Renderer");
    await expect(page.locator("#upnp-status")).toBeVisible();
    await expect(page.locator("#pcm-status")).toBeVisible();
    await page.getByRole("button", { name: "Assign Studio" }).click();
    await expect(page.locator("#renderer-message")).toContainText("Choose a Renderer");
    await page.getByLabel("Renderer for Studio").selectOption(renderer.device.id);
    await page.getByRole("button", { name: "Assign Studio" }).click();
    await expect(page.locator("#zones-list")).toContainText("Studio Renderer");
    await expect(page.locator("#renderer-message")).toContainText("assigned");

    const firstName = "Preserve my intent";
    const externalConfig = await json(context.request, "GET", `${server.origin}/api/v1/config`, secondAdmin.token);
    const externalETag = externalConfig.headers()["etag"];
    const externalPatch = await json(context.request, "PATCH", `${server.origin}/api/v1/config`, secondAdmin.token,
      { display_name: "External update" }, { "If-Match": externalETag, "Idempotency-Key": "external-update" });
    expect(externalPatch.status()).toBe(200);
    await page.getByLabel("Server display name").fill(firstName);
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect(page.locator("#conflict-panel")).toBeVisible();
    await expect(page.getByLabel("Server display name")).toHaveValue(firstName);
    await redactEvidence(page);
    await page.screenshot({ path: join(output, "admin-conflict-redacted.png") });
    await page.getByRole("button", { name: "Refresh and reapply" }).click();
    await expect(page.getByLabel("Server display name")).toHaveValue(firstName);
    await page.getByRole("button", { name: "Save settings" }).click();
    await expect(page.locator("#settings-message")).toContainText("saved");

    const accessibility = await page.evaluate(() => {
      const controls = [...document.querySelectorAll("input, textarea, select")];
      const unlabeled = controls.filter((control) => !control.labels?.length && !control.getAttribute("aria-label")).map((control) => control.id);
      const undersized = [...document.querySelectorAll("button, input:not([type=checkbox]), textarea, select")]
        .filter((control) => {
          const bounds = control.getBoundingClientRect();
          return bounds.width > 0 && bounds.height > 0 && bounds.height < 44;
        }).map((control) => control.id || control.textContent.trim());
      return { unlabeled, undersized };
    });
    expect(accessibility).toEqual({ unlabeled: [], undersized: [] });

    await page.getByRole("button", { name: "Revoke Secondary Controller" }).click();
    await expect(page.locator("#device-message")).toContainText("Secondary Controller was revoked");
    await expect(page.getByRole("button", { name: "Revoke Secondary Controller" })).toBeDisabled();
    await page.locator("#devices").evaluate((element) => element.scrollIntoView({ block: "start" }));
    await expect(page.locator('a[href="#devices"][aria-current="location"]')).toBeVisible();
    await redactEvidence(page);
    await page.screenshot({ path: join(output, "admin-secondary-device-revoked-redacted.png") });

    await redactEvidence(page);
    await page.locator("#main-content").evaluate((element) => element.scrollTo({ top: 0 }));
    await expect(page.locator('#nav-settings[aria-current="location"]')).toBeVisible();
    await page.screenshot({ path: join(output, "admin-desktop-redacted.png") });
    for (const section of ["catalog", "renderers", "devices"]) {
      await page.locator(`#${section}`).evaluate((element) => element.scrollIntoView({ block: "start" }));
      await expect(page.locator(`a[href="#${section}"][aria-current="location"]`)).toBeVisible();
      await redactEvidence(page);
      await page.screenshot({ path: join(output, `admin-desktop-${section}-redacted.png`) });
    }

    await page.setViewportSize({ width: 390, height: 844 });
    await page.evaluate(() => window.scrollTo({ top: 0 }));
    await redactEvidence(page);
    await page.locator(".skip-link").focus();
    await page.keyboard.press("Tab");
    await expect(page.locator("#nav-settings")).toBeFocused();
    const focusStyle = await page.locator("#nav-settings").evaluate((element) => {
      const style = getComputedStyle(element);
      return { width: style.outlineWidth, style: style.outlineStyle, color: style.outlineColor };
    });
    expect(focusStyle.width).toBe("3px");
    expect(focusStyle.style).toBe("solid");
    expect(focusStyle.color).not.toBe("rgba(0, 0, 0, 0)");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
    await page.screenshot({ path: join(output, "admin-mobile-focus-redacted.png") });
    await page.screenshot({ path: join(output, "admin-mobile-redacted.png"), fullPage: true });

    const revoke = await json(context.request, "DELETE", `${server.origin}/api/v1/devices/${admin.device.id}`, secondAdmin.token);
    expect(revoke.status()).toBe(204);
    await page.getByRole("button", { name: "Refresh devices" }).click();
    await expect(page.locator("#login-panel")).toBeVisible();
    await expect(page.locator("#login-error")).toContainText("ended");
    expect(await page.evaluate(() => sessionStorage.getItem("jastreamer-admin-token"))).toBeNull();

    const browserResult = {
      status: "passed",
      server: "real-ephemeral-tls",
      viewports: [{ width: 1280, height: 900 }, { width: 390, height: 844 }],
      checks: {
        authentication: "pass",
        invalidCredentialCleared,
        wrongRoleCredentialCleared,
        sessionStorageOnly: "pass",
        staleETagIntentPreserved: "pass",
        activeAdminRevocationTerminatesSession: "pass",
        lockedFieldsReadOnly: "pass",
        restartRequired: "pass",
        rootsAndJobs: "pass",
        rendererAssignment: "pass",
        noHorizontalOverflow: "pass",
        keyboardFocus: "pass",
        labelsAndTouchTargets: "pass",
        activeNavigation: "pass",
        rendererLocalStatus: "pass",
        reducedMotionSkipLink: "pass",
        secondaryDeviceUIRevocation: "pass",
      },
      accessibility: { ...accessibility, focusStyle },
      pageErrors,
      secretsRecorded: !(invalidCredentialCleared && wrongRoleCredentialCleared),
    };
    expect(pageErrors).toEqual([]);
    await writeFile(join(output, "browser-result.json"), `${JSON.stringify(browserResult, null, 2)}\n`);
    await context.close();
  } finally {
    cleanup = await stopServer(server);
    if (output) await writeFile(join(output, "cleanup.json"), `${JSON.stringify(cleanup, null, 2)}\n`);
  }
});
