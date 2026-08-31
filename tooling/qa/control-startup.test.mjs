import { expect, test } from 'bun:test';
import { chromium } from '@playwright/test';
import {
  createControlContext,
  createControlStartupNavigator,
  enableSemantics,
} from './control-startup.mjs';

test('captures worker navigation and semantics readiness when all fire during navigation', async () => {
  // Given
  const events = [];
  let acceptStartup;
  let acceptWorker;
  const worker = { url: () => 'https://control.test/flutter_service_worker.js' };
  const context = {
    addInitScript: async () => { events.push('observer-installed'); },
    exposeBinding: async (_name, callback) => {
      acceptStartup = callback;
      events.push('binding-installed');
    },
    off: (name) => { events.push(`unsubscribe-${name}`); },
    on: (name, callback) => {
      acceptWorker = callback;
      events.push(`subscribe-${name}`);
    },
    serviceWorkers: () => [],
  };
  const page = {
    context: () => context,
    goto: async () => {
      events.push('navigate');
      acceptWorker(worker);
      await acceptStartup({}, {
        href: 'https://control.test/',
        events: ['init', 'service-worker-active', 'semantics-placeholder'],
        serviceWorker: 'activated',
        semantics: 'placeholder',
      });
      return { status: () => 200 };
    },
    waitForEvent: (name) => {
      events.push(`subscribe-${name}`);
      return Promise.resolve();
    },
  };
  const navigate = await createControlStartupNavigator(context);

  // When
  const readiness = await navigate(page, 'https://control.test/');

  // Then
  expect(readiness.semantics).toBe('placeholder');
  expect(readiness.serviceWorker).toBe('activated');
  expect(events).toEqual([
    'binding-installed',
    'observer-installed',
    'subscribe-serviceworker',
    'subscribe-domcontentloaded',
    'navigate',
    'unsubscribe-serviceworker',
  ]);
});

test('enables Flutter semantics through the generated placeholder click handler', async () => {
  // Given
  const browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  await page.setContent(`<flt-semantics-placeholder tabindex="0" role="button">Enable accessibility</flt-semantics-placeholder><script>
    globalThis.clicks = 0;
    const placeholder = document.querySelector('flt-semantics-placeholder');
    placeholder.addEventListener('click', () => {
      clicks += 1;
      placeholder.outerHTML = '<button>Discover Server</button>';
    });
  </script>`);

  try {
    // When
    await enableSemantics(page);

    // Then
    expect(await page.evaluate(() => globalThis.clicks)).toBe(1);
    expect(await page.getByRole('button', { name: 'Discover Server' }).isEnabled()).toBe(true);
  } finally {
    await browser.close();
  }
});

test('creates each Control invocation with an empty isolated browser context', async () => {
  // Given
  const calls = [];
  const browser = {
    newContext: async (options) => {
      calls.push(options);
      return { serviceWorkers: () => [] };
    },
  };

  // When
  await createControlContext(browser);
  await createControlContext(browser);

  // Then
  expect(calls).toHaveLength(2);
  expect(calls[0]).not.toBe(calls[1]);
  expect(calls).toEqual(calls.map(() => ({
    serviceWorkers: 'allow',
    storageState: { cookies: [], origins: [] },
    viewport: { width: 1280, height: 900 },
  })));
});
