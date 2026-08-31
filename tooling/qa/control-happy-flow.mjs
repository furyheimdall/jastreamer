import { expect } from "@playwright/test";
import { join } from "node:path";
import {
  activateFlutter,
  expectTouchTarget,
  replaceFlutterText,
  resizeFlutter,
  revealFlutterAnchor,
} from './control-playwright-actions.mjs';
import { output } from './control-scenario-fixture.mjs';
import { waitForPlaybackState } from './control-gateway-waits.mjs';

export const runHappyFlow = async ({ page, renderer, server, token }) => {
  const search = page.getByLabel('Search title, artist, or album');
  await replaceFlutterText(search, 'first');
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
  await replaceFlutterText(search, '');
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
    const authoritativeState = waitForPlaybackState(
      server,
      token,
      (state) => state.transport === expectedIntent && state.pending_command_id === '',
      `${name} authoritative transport state`,
    );
    const rendererResult = renderer.nextResult();
    await activateFlutter(page, button);
    const response = await responsePromise;
    expect(response.status(), await response.text()).toBe(202);
    renderer.wake();
    await rendererResult;
    await authoritativeState;
    const refresh = page.getByRole('button', { name: 'Refresh Server state' });
    await expect(refresh).toBeEnabled({ timeout: 20_000 });
    const recovered = page.waitForResponse((candidate) =>
      candidate.url().endsWith('/api/v1/zones/main/playback-state') &&
      candidate.request().method() === 'GET',
    );
    const rendered = expect(
      page.getByText(`Server intent: ${expectedIntent}`, { exact: true }).last(),
    ).toBeVisible({ timeout: 20_000 });
    await activateFlutter(page, refresh);
    expect((await recovered).status()).toBe(200);
    expect(
      renderer.records.some((record) => record.type === 'result.ack'),
      JSON.stringify(renderer.records),
    ).toBe(true);
    await rendered;
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
    page.getByLabel('Seek position (seconds)'),
    '1',
  );
  await transport('Seek', 'paused');
  await transport('Resume', 'playing');
  await transport('Next', 'suspended');
  await transport('Stop', 'idle');
  for (const label of ["재생 종료", "앨범 이어듣기", "비슷한 음악"]) {
    await activateFlutter(page, page.getByText(label, { exact: true }));
    await activateFlutter(page, page.getByRole("button", { name: "Save policy" }));
    await expect(page.getByText(/Server policy revision \d+ · saved/)).toBeVisible();
  }
};
