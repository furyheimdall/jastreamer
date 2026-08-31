import { expect } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { join } from "node:path";
import { output, scenario } from './control-scenario-fixture.mjs';

export const activateFlutter = async (page, locator) => {
  await expect(locator).toBeVisible();
  await expect(locator).toBeEnabled({ timeout: 20_000 });
  if (!await locator.boundingBox()) throw new TypeError('Flutter control has no action target');
  await locator.focus();
  await expect(locator).toBeFocused();
  await page.keyboard.press('Enter');
};

const observeFlutterKeyboard = async (locator, terminalKey, action) => {
  const observationKey = `__control_input_${randomUUID().replaceAll('-', '')}`;
  await locator.evaluate((input, { observationKey, terminalKey }) => {
    let complete;
    const done = new Promise((resolve) => { complete = resolve; });
    const observation = { done, inputs: [], keys: [] };
    const recordKey = (event) => {
      observation.keys.push(`${event.key}:${event.type}`);
      if (event.type !== 'keyup' || event.key !== terminalKey) return;
      input.removeEventListener('keydown', recordKey);
      input.removeEventListener('keyup', recordKey);
      input.removeEventListener('input', recordInput);
      complete();
    };
    const recordInput = (event) => {
      observation.inputs.push(`${event.inputType}:${event.data ?? ''}`);
    };
    globalThis[observationKey] = observation;
    input.addEventListener('keydown', recordKey);
    input.addEventListener('keyup', recordKey);
    input.addEventListener('input', recordInput);
  }, { observationKey, terminalKey });
  await action();
  return locator.evaluate(async (input, key) => {
    const observation = globalThis[key];
    await observation.done;
    delete globalThis[key];
    return {
      focused: document.activeElement === input,
      selection: [input.selectionStart, input.selectionEnd],
      value: input.value,
      keys: observation.keys,
      inputs: observation.inputs,
    };
  }, observationKey);
};

export const replaceFlutterText = async (locator, value) => {
  const semantics = [];
  await expect(locator).toHaveCount(1);
  await expect(locator).toBeVisible();
  await locator.click();
  await expect(locator).toBeFocused();
  await locator.press('End');
  semantics.push(await locator.evaluate((input) => ({
    phase: 'focused',
    focused: document.activeElement === input,
    selection: [input.selectionStart, input.selectionEnd],
    value: input.value,
  })));
  const current = await locator.inputValue();
  if (current.length > 0) {
    const selectedState = await observeFlutterKeyboard(
      locator,
      'Control',
      () => locator.press('Control+A'),
    );
    expect(selectedState.selection).toEqual([0, current.length]);
    semantics.push({ phase: 'selected', ...selectedState });

    const clearedState = await observeFlutterKeyboard(
      locator,
      'Backspace',
      () => locator.press('Backspace'),
    );
    expect(clearedState.value).toBe('');
    await expect(locator).toHaveValue('');
    semantics.push({ phase: 'cleared', ...clearedState });
  }
  if (value.length > 0) await locator.pressSequentially(value);
  await expect(locator).toHaveValue(value);
  semantics.push(await locator.evaluate((input) => ({
    phase: 'entered',
    focused: document.activeElement === input,
    selection: [input.selectionStart, input.selectionEnd],
    value: input.value,
  })));
  return semantics;
};

export const resizeFlutter = async (page, width, height) => {
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

export const revealFlutterAnchor = async (page, anchor, pages = 12) => {
  const isInViewport = async () => {
    if (!await anchor.isVisible()) return false;
    const bounds = await anchor.boundingBox();
    const viewport = page.viewportSize();
    if (!bounds || !viewport) return false;
    return bounds.x < viewport.width && bounds.y < viewport.height &&
      bounds.x + bounds.width > 0 && bounds.y + bounds.height > 0;
  };
  if (await isInViewport()) return;
  for (let pageDown = 0; pageDown < pages; pageDown++) {
    if (await isInViewport()) return;
    const viewport = page.viewportSize();
    if (!viewport) throw new TypeError('Flutter viewport is unavailable');
    await page.mouse.move(viewport.width / 2, viewport.height / 2);
    const observationKey = `__control_scroll_${randomUUID().replaceAll('-', '')}`;
    await page.evaluate(({ observationKey }) => {
      let complete;
      let settled = false;
      const done = new Promise((resolve) => { complete = resolve; });
      const record = (event) => requestAnimationFrame(() => requestAnimationFrame(() => {
        if (settled) return;
        settled = true;
        document.removeEventListener('scroll', record, true);
        document.removeEventListener('wheel', record, true);
        const target = event.target instanceof Element
          ? event.target
          : document.scrollingElement;
        complete(target?.scrollTop ?? window.scrollY);
      }));
      globalThis[observationKey] = done;
      document.addEventListener('scroll', record, { capture: true, once: true });
      document.addEventListener('wheel', record, { capture: true, once: true });
    }, { observationKey });
    await page.mouse.wheel(0, 700);
    await page.evaluate(async (key) => {
      const movement = await globalThis[key];
      delete globalThis[key];
      return movement;
    }, observationKey);
  }
  await expect(anchor).toBeInViewport();
};

export const expectTouchTarget = async (page, locator) => {
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

export const submitPairingToken = async (page, token) => {
  const input = page.getByLabel('Controller token');
  await replaceFlutterText(input, token);
  const responsePromise = page.waitForResponse((response) =>
    response.url().endsWith('/api/v1/discovery') &&
    response.request().method() === 'GET',
  );
  const submit = page.getByRole('button', { name: 'Complete pairing', exact: true });
  await activateFlutter(page, submit);
  const response = await responsePromise;
  expect(response.status()).toBe(token === 'rejected-token' ? 401 : 200);
};

export const redactPairingReceipt = async (popup, slug) => {
  await popup.locator("#register-message").evaluate((element) => {
    element.textContent = "Device registered. Token: [REDACTED]";
  });
  await popup.locator("#pairing-code").evaluate((element) => {
    element.textContent = "[REDACTED]";
  });
  await popup.screenshot({ path: join(output, `${slug}.png`), fullPage: true });
};

export const registerPairedDevice = async (popup, name, receiptSlug) => {
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

export const registerController = async (popup) => {
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
