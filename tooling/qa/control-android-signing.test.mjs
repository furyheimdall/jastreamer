import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { access, readFile, readdir } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import test from 'node:test';

const root = resolve(import.meta.dirname, '../..');
const helper = join(root, 'tooling/qa/control-android-signing.sh');
const harness = join(root, 'tooling/qa/control.sh');
const gradle = join(root, 'apps/control/android/app/build.gradle.kts');

const runLifecycle = (forcedFailure) => spawnSync('sh', ['-c', `
  set -eu
  . "$1"
  qa_signing_dir=
  ${forcedFailure ? 'trap cleanup_control_android_qa_signing EXIT' : ''}
  create_control_android_qa_signing
  printf '%s\\n' "$qa_signing_dir"
  test -f "$qa_keystore"
  ${forcedFailure ? 'exit 23' : 'cleanup_control_android_qa_signing'}
`, 'control-signing-test', helper], { encoding: 'utf8' });

const doesNotExist = async (path) => {
  try {
    await access(path);
    return false;
  } catch (error) {
    if (error && error.code === 'ENOENT') return true;
    throw error;
  }
};

const keyLikeFiles = async (directory) => {
  const found = [];
  const visit = async (path) => {
    let entries;
    try {
      entries = await readdir(path, { withFileTypes: true });
    } catch (error) {
      if (error && error.code === 'ENOENT') return;
      throw error;
    }
    for (const entry of entries) {
      const child = join(path, entry.name);
      if (entry.isDirectory()) await visit(child);
      else if (/\.(?:jks|keystore|p12|pfx|key)$/i.test(entry.name)) found.push(child);
    }
  };
  await visit(directory);
  return found;
};

test('production Android release remains fail-closed without all protected inputs', async () => {
  const source = await readFile(gradle, 'utf8');
  for (const name of [
    'CONTROL_ANDROID_KEYSTORE',
    'CONTROL_ANDROID_STORE_PASSWORD',
    'CONTROL_ANDROID_KEY_ALIAS',
    'CONTROL_ANDROID_KEY_PASSWORD',
  ]) {
    assert.match(source, new RegExp(`environmentVariable\\("${name}"\\)`));
  }
  assert.match(source, /releaseRequested\s*&&[\s\S]*any \{ it\.isNullOrBlank\(\) \}/);
  assert.match(source, /throw GradleException\("protected Control Android signing inputs are required"\)/);
  assert.match(source, /release \{\s*signingConfig = signingConfigs\.getByName\("controlRelease"\)/);
  assert.doesNotMatch(source, /signingConfigs\.getByName\("debug"\)/);
});

test('QA harness provisions four build-scoped inputs from a read-only ephemeral mount', async () => {
  const source = await readFile(harness, 'utf8');
  assert.match(source, /create_control_android_qa_signing/);
  assert.match(source, /"\$qa_signing_dir:\/qa-signing:ro"/);
  assert.match(source, /CONTROL_ANDROID_KEYSTORE=\/qa-signing\/control-qa\.jks/);
  for (const variable of ['STORE_PASSWORD', 'KEY_ALIAS', 'KEY_PASSWORD']) {
    assert.match(source, new RegExp(`-e CONTROL_ANDROID_${variable}=`));
  }
  assert.match(source, /cleanup_control_android_qa_signing[\s\S]*trap cleanup EXIT INT TERM/);
  assert.match(source, /find "\$screenshots"[\s\S]*'\*\.jks'/);
  assert.match(source, /-v "\$root:\/repo" -w \/repo\/apps\/control "\$image"[\s\S]*flutter test/);
});

test('QA harness reclaims prior container artifacts before deleting build state', async () => {
  // Given / When
  const source = await readFile(harness, 'utf8');
  const reclaim = source.indexOf('chown -R "$owner_uid:$owner_gid" /workspace');
  const remove = source.indexOf('rm -rf "$root/apps/control/.dart_tool"');

  // Then
  assert.ok(reclaim >= 0);
  assert.ok(remove > reclaim);
});

test('ephemeral QA key directory is unique and removed on success and failure', async () => {
  const success = runLifecycle(false);
  assert.equal(success.status, 0, success.stderr);
  const successPath = success.stdout.trim();
  assert.match(successPath, /jastreamer-control-android-qa-signing\.[A-Za-z0-9]+$/);
  assert.equal(await doesNotExist(successPath), true);

  const failure = runLifecycle(true);
  assert.equal(failure.status, 23, failure.stderr);
  const failurePath = failure.stdout.trim();
  assert.match(failurePath, /jastreamer-control-android-qa-signing\.[A-Za-z0-9]+$/);
  assert.notEqual(failurePath, successPath);
  assert.equal(await doesNotExist(failurePath), true);
  assert.deepEqual(await keyLikeFiles(join(root, '.omo/evidence')), []);
});
