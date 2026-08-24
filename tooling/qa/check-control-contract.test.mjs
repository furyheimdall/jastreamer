import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";

const checker = join(import.meta.dirname, "check-control-contract.mjs");

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
