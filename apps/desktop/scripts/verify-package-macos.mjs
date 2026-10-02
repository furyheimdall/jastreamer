import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, readdir, readlink, rm, stat, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, digest, inventory, packageIdentity, requireMac, runtimeNames, verifyBundle } from './macos-package-common.mjs';

requireMac();
const root = fileURLToPath(new URL('..', import.meta.url));
const output = path.join(root, 'dist/macos-arm64');
const metadata = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8'));
const lock = JSON.parse(await readFile(path.join(root, 'native/audio/dependency-lock.json'), 'utf8'));
const packageLock = JSON.parse(await readFile(path.join(root, 'package-lock.json'), 'utf8'));
const identity = packageIdentity(root, metadata.version);
const manifest = JSON.parse(await readFile(path.join(output, 'manifest.json'), 'utf8'));
assert.equal(manifest.product, 'jastreamer-desktop');
assert.equal(manifest.version, metadata.version);
assert.equal(manifest.platform, 'darwin');
assert.equal(manifest.arch, 'arm64');
assert.equal(manifest.minimumMacOS, '13.0');
assert.equal(manifest.electron, metadata.devDependencies.electron);
assert.equal(manifest.packager, metadata.devDependencies['@electron/packager']);
assert.equal(manifest.developmentBuild, !identity.publicBuild);
assert.equal(manifest.distribution, identity.distribution);
assert.equal(manifest.signed, false);
assert.equal(manifest.signing, 'ad-hoc');
assert.equal(manifest.developerIDSigned, false);
assert.equal(manifest.notarized, false);
assert.equal(manifest.productionQualified, false);
assert.equal(manifest.directory, 'jastreamer.app');
assert.equal(manifest.nativeAudio.helper, 'Contents/Resources/native-audio/jastreamer-audio');
assert.deepEqual(manifest.nativeAudio.runtime, runtimeNames);
assert.equal(manifest.nativeAudio.ffmpeg, lock.ffmpeg.version);
assert.equal(manifest.nativeAudio.dynamicLinking, true);
assert.equal(manifest.sourceRevision, identity.sourceRevision);
assert.equal(manifest.archive.path, identity.archiveName);
const archive = path.join(output, manifest.archive.path);
assert.equal((await stat(archive)).size, manifest.archive.bytes);
assert.equal(await digest(archive), manifest.archive.sha256);
assert.equal(await readFile(`${archive}.sha256`, 'utf8'), `${manifest.archive.sha256}  ${manifest.archive.path}\n`);
const app = path.join(output, manifest.directory);
const verified = await verifyBundle(app, metadata, lock, packageLock);
assert.deepEqual(verified.entries, manifest.files, 'Application differs from the complete signed file/symlink inventory');
command('hdiutil', ['verify', archive]);
const temporary = await mkdtemp(path.join(os.tmpdir(), 'jastreamer-dmg-verify-'));
const mount = path.join(temporary, 'mount');
await mkdir(mount);
let attached = false;
try {
  command('hdiutil', ['attach', '-readonly', '-nobrowse', '-noautoopen', '-mountpoint', mount, archive]);
  attached = true;
  assert.equal(await readlink(path.join(mount, 'Applications')), '/Applications');
  const top = await readdir(mount);
  for (const name of top) assert(['jastreamer.app', 'Applications', '.fseventsd', '.Trashes', '.HFS+ Private Directory Data\r'].includes(name), `Unexpected DMG payload: ${name}`);
  assert.deepEqual(await inventory(path.join(mount, manifest.directory)), manifest.files, 'DMG application differs from signed source bundle');
  command('codesign', ['--verify', '--deep', '--strict', path.join(mount, manifest.directory)]);
} finally {
  // Never delete a live mount point if detach fails.
  if (attached) command('hdiutil', ['detach', mount]);
  await rm(temporary, { recursive: true, force: true });
}
if (identity.publicBuild) {
  const build = path.join(root, 'native/audio/build-macos-arm64');
  const nativeTests = ['coreaudio-format', 'coreaudio-pcm', 'decoder', 'packing', 'protocol'];
  const discovered = JSON.parse(command('ctest', ['--test-dir', build, '--show-only=json-v1']));
  assert.deepEqual(discovered.tests.map(test => test.name).sort(), nativeTests, 'Native test suite is missing or changed');
  console.log(command('ctest', ['--test-dir', build, '--output-on-failure', '--no-tests=error']));
  const manifestPath = path.join(output, 'manifest.json');
  const receipt = {
    schema: 1, product: manifest.product, version: manifest.version, platform: 'darwin', arch: 'arm64',
    sourceRevision: identity.sourceRevision, distribution: identity.distribution,
    signing: 'ad-hoc', developerIDSigned: false, notarized: false, productionQualified: false,
    archive: manifest.archive,
    packageManifest: { path: 'manifest.json', bytes: (await stat(manifestPath)).size, sha256: await digest(manifestPath) },
    machOFiles: verified.machOFiles, nativeTests,
    checks: ['exact-package-inventory', 'readonly-dmg-mount', 'thin-arm64-mach-o', 'relocatable-dylib-closure', 'ad-hoc-signatures', 'corresponding-source-and-licenses', 'native-coreaudio-decoder-packing-protocol-tests'],
  };
  await writeFile(path.join(output, 'native-verification.json'), JSON.stringify(receipt, null, 2) + '\n');
}
console.log(`Verified Apple Silicon ${identity.distribution} app and read-only DMG: ${verified.machOCount} thin-arm64 Mach-O files, relocatable dylib closure, ad-hoc signatures, exact bundle inventory, LGPL source/license/relink materials and Applications link. No app or audio was started. Not Developer ID signed or notarized.`);
