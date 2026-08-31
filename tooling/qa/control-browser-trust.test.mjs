import { expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import config from './playwright.config.mjs';
import { certificateSpkiPin } from './control-server-process.mjs';
import { controlFixtureRoot } from './control-scenario-fixture.mjs';

test('launches Chromium with the exact Control Web certificate SPKI exception', () => {
  expect(config.testMatch.test('control.playwright.mjs')).toBe(true);
  expect(config.testMatch.test('control.spec.mjs')).toBe(false);
  expect(config.use).toBeUndefined();
  const pin = certificateSpkiPin(readFileSync(join(controlFixtureRoot, 'control_gateway_tls_cert.pem')));
  expect(Buffer.from(pin, 'base64')).toHaveLength(32);
  expect(pin).toBe('/aY/krwn/0LwGBQjHt6CJT+5wU+qPzzPuC5hfV6Xobs=');
});
