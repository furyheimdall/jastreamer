import { expect } from '@playwright/test';

const STARTUP_BINDING = '__controlQaStartupReady';
const STARTUP_TIMEOUT_MS = 20_000;

const bounded = async (signal, label) => {
  let timeout;
  try {
    return await Promise.race([
      signal,
      new Promise((_, reject) => {
        timeout = setTimeout(() => reject(new Error(`${label} timed out`)), STARTUP_TIMEOUT_MS);
      }),
    ]);
  } finally {
    clearTimeout(timeout);
  }
};

export const createControlContext = async (browser) => {
  const context = await browser.newContext({
    serviceWorkers: 'allow',
    storageState: { cookies: [], origins: [] },
    viewport: { width: 1280, height: 900 },
  });
  if (context.serviceWorkers().length !== 0) {
    await context.close();
    throw new Error('Control browser context inherited a service worker');
  }
  return context;
};

export const createControlStartupNavigator = async (context) => {
  const pending = [];
  const buffered = [];
  await context.exposeBinding(STARTUP_BINDING, (_source, readiness) => {
    const resolve = pending.shift();
    if (resolve) resolve(readiness);
    else buffered.push(readiness);
  });
  await context.addInitScript(({ binding }) => {
    const events = [];
    const record = (type, detail = '') => {
      events.push({ detail, time: performance.now(), type });
    };
    record('init', location.href);
    const serviceWorkers = navigator.serviceWorker;
    if (!serviceWorkers) return;
    document.addEventListener('readystatechange', () => record('document', document.readyState));
    addEventListener('DOMContentLoaded', () => record('domcontentloaded'), { once: true });
    addEventListener('load', () => record('load'), { once: true });
    addEventListener('pageshow', (event) => record('pageshow', event.persisted ? 'persisted' : 'fresh'), { once: true });
    serviceWorkers.addEventListener('controllerchange', () => {
      record('service-worker-controller', serviceWorkers.controller?.scriptURL ?? 'none');
    });
    let resolveServiceWorker;
    const serviceWorker = new Promise((resolve) => { resolveServiceWorker = resolve; });
    const register = serviceWorkers.register.bind(serviceWorkers);
    serviceWorkers.register = (...arguments_) => {
      const registration = register(...arguments_);
      void registration.then((value) => {
        const worker = value.installing ?? value.waiting ?? value.active;
        const complete = () => {
          if (worker?.state !== 'activated') return;
          record('service-worker-active', `${worker.state}:${value.scope}`);
          resolveServiceWorker(worker.state);
        };
        if (worker?.state === 'activated') complete();
        else if (worker) worker.addEventListener('statechange', complete);
        else {
          record('service-worker-missing', value.scope);
          resolveServiceWorker('missing');
        }
      }, (error) => {
        record('service-worker-registration-failed', `${error.name}:${error.message}`);
        resolveServiceWorker('registration-failed');
      });
      return registration;
    };
    const semantics = new Promise((resolve) => {
      const inspect = () => {
        const placeholder = document.querySelector('flt-semantics-placeholder');
        if (placeholder) {
          record('semantics', 'placeholder');
          resolve('placeholder');
          return true;
        }
        const discover = [...document.querySelectorAll('[role="button"],button')]
          .find((element) => element.textContent?.trim() === 'Discover Server');
        if (!discover) return false;
        record('semantics', 'discover');
        resolve('discover');
        return true;
      };
      if (inspect()) return;
      const observer = new MutationObserver(() => {
        if (inspect()) observer.disconnect();
      });
      observer.observe(document, { childList: true, subtree: true });
    });
    void Promise.all([serviceWorker, semantics]).then(([workerState, semanticsState]) => {
      globalThis[binding]({
        events,
        href: location.href,
        semantics: semanticsState,
        serviceWorker: workerState,
      });
    });
  }, { binding: STARTUP_BINDING });

  return async (page, url) => {
    const startup = buffered.length > 0
      ? Promise.resolve(buffered.shift())
      : new Promise((resolve) => pending.push(resolve));
    let createdWorker;
    const observeWorker = (worker) => { createdWorker = worker; };
    context.on('serviceworker', observeWorker);
    const domReady = page.waitForEvent('domcontentloaded', { timeout: STARTUP_TIMEOUT_MS });
    let response;
    let readiness;
    try {
      response = await page.goto(url);
      readiness = await bounded(startup, 'Control Flutter/service-worker startup');
      await domReady;
    } finally {
      context.off('serviceworker', observeWorker);
    }
    const result = {
      ...readiness,
      navigationStatus: response?.status() ?? null,
      workerUrl: createdWorker?.url() ?? null,
    };
    process.stdout.write(`CONTROL_STARTUP ${JSON.stringify(result)}\n`);
    return result;
  };
};

export const enableSemantics = async (page) => {
  const placeholder = page.locator('flt-semantics-placeholder');
  const discover = page.getByRole('button', { name: 'Discover Server' });
  const discoverReady = discover.waitFor({ state: 'visible', timeout: STARTUP_TIMEOUT_MS });
  if (!await discover.isVisible()) {
    await placeholder.evaluate((element) => element.click());
  }
  await discoverReady;
  await expect(discover).toBeEnabled();
  if (!await discover.boundingBox()) throw new TypeError('Discover Server has no action target');
};
