import { expect } from '@playwright/test';
import { join } from 'node:path';
import { resizeFlutter } from './control-playwright-actions.mjs';
import { output, scenario } from './control-scenario-fixture.mjs';

export const captureControlViewports = async (page) => {
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
};
