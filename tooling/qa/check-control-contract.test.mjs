import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";

const checker = join(import.meta.dirname, "check-control-contract.mjs");
const root = join(import.meta.dirname, "../..");

const scanFixture = async ({ dependency = '', source = '' }) => {
  const directory = await mkdtemp(join(tmpdir(), 'control-forbidden-'));
  try {
    await mkdir(join(directory, 'lib'));
    await writeFile(join(directory, 'pubspec.yaml'), `name: forbidden_control\ndependencies:\n${dependency}`);
    await writeFile(join(directory, 'pubspec.lock'), 'packages: {}\n');
    await writeFile(join(directory, 'lib/bad.dart'), source);
    return spawnSync(process.execPath, [checker, '--scan', directory], { encoding: 'utf8' });
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
};

test('generated Control contract is bound to protocol major 3', async () => {
  // Given: the canonical v3 HTTP contract and generated Dart constants.
  const contract = await readFile(join(root, 'contracts/control-api/v3/http-api.json'));
  const generated = await readFile(join(root, 'apps/control/lib/generated/control_contract.dart'), 'utf8');
  const checkerSource = await readFile(checker, 'utf8');

  // When: the production contract checker runs.
  const result = spawnSync(process.execPath, [checker], { encoding: 'utf8' });

  // Then: the checker and generated machine values select this exact v3 contract.
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.trim(), createHash('sha256').update(contract).digest('hex'));
  assert.match(generated, /controlProtocolMajor = 3;/);
  assert.match(generated, /controlContractRevision = 'control-api-v3';/);
  assert.match(checkerSource, /createZoneInventoryValidator/);
  assert.match(checkerSource, /validateZones\(zoneFixture\)/);
});

test('Control boundary scan rejects forbidden dependencies', async () => {
  const result = await scanFixture({ dependency: '  sqflite: 2.4.0\n' });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /forbidden Control dependency sqflite/);
});

test('Control boundary scan independently rejects recommendation logic', async () => {
  const result = await scanFixture({
    source: 'double cosineSimilarity(List<int> left, List<int> right) => 0;\n',
  });
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /forbidden Server-only logic\/storage term cosineSimilarity/);
});
