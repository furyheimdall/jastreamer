import { expect, test } from 'bun:test';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import Ajv2020 from './node_modules/ajv/dist/2020.js';
import {
  createZoneInventoryValidator,
  isGoServerRfc3339,
  validateZoneInventorySemantics,
  zoneInventorySemantics,
} from './schema-validation.mjs';

const root = join(import.meta.dirname, '../..');
const load = async (path) => JSON.parse(await readFile(join(root, path), 'utf8'));

const validTimes = [
  '2026-08-26T00:00:00Z',
  '2024-02-29T23:59:59.123456789Z',
  '2026-08-26T00:00:00.1+05:30',
  '2026-08-26T00:00:00.000001-07:45',
];

const invalidTimes = [
  '2026-02-29T00:00:00Z',
  '2026-13-01T00:00:00Z',
  '2026-04-31T00:00:00Z',
  '2026-08-26T24:00:00Z',
  '2026-08-26T00:60:00Z',
  '2026-08-26T00:00:60Z',
  '2026-08-26T00:00:00+24:00',
  '2026-08-26T00:00:00+05:60',
  '2026-08-26T00:00:00',
  '2026-08-26 00:00:00Z',
  '2026-08-26T00:00:00.1234567890Z',
];

test('every AJV boundary registers the shared machine formats', async () => {
  // Given: every repository module that constructs an AJV instance.
  const boundaries = [
    'contracts/tests/contracts.test.ts',
    'tooling/docs/verify.mjs',
    'tooling/qa/check-control-contract.mjs',
    'tooling/qa/task19/product-e2e-receipt.mjs',
    'tooling/release/product-gate-schema.mjs',
    'tooling/release/product-gate-supply-chain.mjs',
    'tooling/release/product-gate-trust.mjs',
  ];

  // When: their machine wiring is inspected.
  const sources = await Promise.all(
    boundaries.map((path) => readFile(join(root, path), 'utf8')),
  );

  // Then: no AJV boundary can silently fall back to built-in/no-op formats.
  expect(sources.every((source) =>
    source.includes('registerSchemaFormats') ||
    source.includes('createZoneInventoryValidator'))).toBe(true);
});

test('Go Server RFC3339 validator accepts only emitted calendar values', () => {
  // Given / When: valid and malformed timestamp classes are parsed.
  const valid = validTimes.map(isGoServerRfc3339);
  const invalid = invalidTimes.map(isGoServerRfc3339);

  // Then: timezone-bearing valid values pass and every malformed value fails.
  expect(valid).toEqual(validTimes.map(() => true));
  expect(invalid).toEqual(invalidTimes.map(() => false));
});

test('zone semantics require the versioned marker and unambiguous references', async () => {
  // Given: the real fixture, schema, and semantic drift variants.
  const schema = await load('contracts/control-api/v3/schema.json');
  const fixture = await load('contracts/control-api/v3/fixtures/zones-snapshot.json');
  const withoutMarker = { ...schema };
  delete withoutMarker['x-jastreamer-semantics'];
  const duplicateZone = structuredClone(fixture);
  duplicateZone.zones.push({ ...duplicateZone.zones[0], name: 'Different zone' });
  const duplicateRenderer = structuredClone(fixture);
  duplicateRenderer.renderers.push({ ...duplicateRenderer.renderers[0], name: 'Different Renderer' });
  const dangling = structuredClone(fixture);
  dangling.zones[0].renderer_id = 'renderer-missing';

  // When: marker and cross-array identity semantics are evaluated.
  const results = [
    validateZoneInventorySemantics(withoutMarker, fixture),
    validateZoneInventorySemantics(schema, duplicateZone),
    validateZoneInventorySemantics(schema, duplicateRenderer),
    validateZoneInventorySemantics(schema, dangling),
  ];

  // Then: every drift is rejected and the marker remains exact.
  expect(schema['x-jastreamer-semantics']).toEqual(zoneInventorySemantics);
  expect(results).toEqual([false, false, false, false]);
});

test('combined validator applies structure formats and inventory semantics', async () => {
  // Given: the canonical contract and real Server fixture.
  const schema = await load('contracts/control-api/v3/schema.json');
  const fixture = await load('contracts/control-api/v3/fixtures/zones-snapshot.json');
  const validate = createZoneInventoryValidator(
    new Ajv2020({ allErrors: true, strict: true }),
    schema,
  );
  const malformedTime = structuredClone(fixture);
  malformedTime.renderers[0].last_seen_at = '2026-02-29T00:00:00Z';
  const dangling = structuredClone(fixture);
  dangling.zones[0].renderer_id = 'renderer-missing';

  // When / Then: structural-format and post-structural semantic failures deny.
  expect(validate(fixture)).toBe(true);
  expect(validate(malformedTime)).toBe(false);
  expect(validate(dangling)).toBe(false);
});
