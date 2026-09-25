import { packager } from '@electron/packager';
import { createHash } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { access, copyFile, mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { pipeline } from 'node:stream/promises';
import yazl from 'yazl';

const root = fileURLToPath(new URL('..', import.meta.url));
const output = path.join(root, 'dist');
const name = 'jastreamer-desktop';
const target = path.join(output, `${name}-win32-x64`);
const metadata = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8'));
const nativeRoot = path.join(root, 'native/audio');
const nativeLock = JSON.parse(await readFile(path.join(nativeRoot, 'dependency-lock.json'), 'utf8'));
const sourceDateEpoch = Number(process.env.SOURCE_DATE_EPOCH || 315532800);
if (!Number.isSafeInteger(sourceDateEpoch) || sourceDateEpoch < 0) {
  throw new Error('SOURCE_DATE_EPOCH must be a non-negative integer');
}
const archiveTimestamp = new Date(Math.max(sourceDateEpoch, 315532800) * 1000);
try {
  await access(path.join(target, 'user-data'));
  throw new Error('Portable user-data exists in the build output. Move the entire used application folder before rebuilding; it will not be erased.');
} catch (error) {
  if (error.code !== 'ENOENT') throw error;
}
await mkdir(output, { recursive: true });
const [directory] = await packager({
  dir: root,
  out: output,
  name,
  executableName: name,
  icon: path.join(root, "assets/jastreamer.ico"),
  platform: 'win32',
  arch: 'x64',
  electronVersion: metadata.devDependencies.electron,
  appVersion: metadata.version,
  appCopyright: 'Copyright jastreamer contributors',
  win32metadata: { CompanyName: 'jastreamer contributors', FileDescription: 'jastreamer desktop', ProductName: 'jastreamer' },
  asar: true,
  prune: true,
  overwrite: true,
  ignore: [
    /^\/(?:dist|native|scripts|tests?|\.user-data|user-data)(?:\/|$)/,
    /^\/.*\.(?:test|spec)\.[cm]?js$/,
    /^\/(?:\.env(?:\..*)?|package-lock\.json)$/,
  ],
});
await copyFile(path.resolve(root, '../../LICENSE'), path.join(directory, 'LICENSE.jastreamer'));
const packagedNative = path.join(directory, 'resources/native-audio');
const correspondingSource = path.join(packagedNative, 'corresponding-source');
await mkdir(packagedNative, { recursive: true });
const runtimeFiles = ['jastreamer-audio.exe', ...nativeLock.ffmpeg.runtime];
for (const relative of runtimeFiles) {
  await copyFile(path.join(nativeRoot, 'dist', relative), path.join(packagedNative, relative));
}
await copyFile(path.join(nativeRoot, 'dependency-lock.json'), path.join(packagedNative, 'dependencies.json'));
await copyFile(path.join(nativeRoot, 'THIRD-PARTY-NOTICES.txt'), path.join(packagedNative, 'THIRD-PARTY-NOTICES.txt'));
await mkdir(path.join(packagedNative, 'legal/ffmpeg'), { recursive: true });
await mkdir(path.join(packagedNative, 'legal/nlohmann-json'), { recursive: true });
await mkdir(path.join(packagedNative, 'legal/jastreamer'), { recursive: true });
await copyFile(
  path.join(nativeRoot, 'deps/src', nativeLock.ffmpeg.sourceDirectory, nativeLock.ffmpeg.license),
  path.join(packagedNative, 'legal/ffmpeg', nativeLock.ffmpeg.license),
);
await copyFile(
  path.join(nativeRoot, 'deps/src', nativeLock.nlohmannJson.sourceDirectory, nativeLock.nlohmannJson.license),
  path.join(packagedNative, 'legal/nlohmann-json', nativeLock.nlohmannJson.license),
);
await copyFile(path.resolve(root, '../../LICENSE'), path.join(packagedNative, 'legal/jastreamer/LICENSE.Apache-2.0'));

await mkdir(path.join(correspondingSource, 'deps/downloads'), { recursive: true });
await mkdir(path.join(correspondingSource, 'scripts'), { recursive: true });
await mkdir(path.join(correspondingSource, 'tests'), { recursive: true });
for (const dependency of [nativeLock.ffmpeg, nativeLock.nlohmannJson]) {
  await copyFile(
    path.join(nativeRoot, 'deps/downloads', dependency.archive),
    path.join(correspondingSource, 'deps/downloads', dependency.archive),
  );
}
const nativeSources = [
  'CMakeLists.txt',
  'dependency-lock.json',
  'decoder.hpp',
  'decoder.cpp',
  'decoder_smoke.cpp',
  'decoder_behavior.cpp',
  'audio_engine.hpp',
  'audio_engine.cpp',
  'protocol.hpp',
  'protocol.cpp',
  'smtc.hpp',
  'smtc.cpp',
  'main.cpp',
  'tests/packing_test.cpp',
  'tests/protocol_smoke.cpp',
  'scripts/build-ffmpeg.sh',
  'scripts/build-native.ps1',
  'scripts/fetch-native-deps.mjs',
];
for (const relative of nativeSources) {
  await copyFile(path.join(nativeRoot, relative), path.join(correspondingSource, relative));
}
await writeFile(path.join(directory, 'START-HERE.txt'), [
  'jastreamer Windows portable desktop',
  '',
  'Extract the complete ZIP into a writable local folder, then run jastreamer-desktop.exe.',
  'Do not run inside the ZIP or move only the EXE. Windows 10/11 x64 is required.',
  'Select a discovered server or enter its HTTP(S) root address. Discovery requires LAN multicast UDP 5353.',
  'HTTP is only for a trusted private LAN; it does not encrypt credentials or traffic.',
  'Recent servers, language preference, and OS-protected Chromium sessions are stored beside the EXE in user-data.',
  'The folder must remain writable. No AppData fallback or administrator launch is required.',
  'Saved sessions are not guaranteed to move across Windows accounts or machines; sign in again there.',
  'Native audio is opt-in. Its helper and replaceable LGPL FFmpeg DLLs are under resources/native-audio.',
  'Corresponding source, verified source archives, build recipes, licenses, and third-party notices are packaged beside them.',
  'On Windows, X hides the window in the notification tray while local playback continues.',
  'Click the tray icon to reopen; right-click it and choose Exit to quit completely before updating.',
  'Exiting or changing servers does not send Stop to other network outputs.',
  'This build is unsigned and is not production-qualified.',
  '',
].join('\r\n'));
async function* files(dir, prefix = '') {
  for (const entry of (await readdir(dir, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name, 'en'))) {
    const relative = prefix + entry.name;
    if (entry.isDirectory()) yield* files(path.join(dir, entry.name), relative + '/');
    else if (entry.isFile()) yield relative;
    else throw new Error(`Unexpected non-file entry: ${relative}`);
  }
}
async function digest(file) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest('hex');
}
const inventory = [];
const zip = new yazl.ZipFile();
const archiveName = `${name}_${metadata.version}_windows-x64.zip`;
const archivePath = path.join(output, archiveName);
const finished = pipeline(zip.outputStream, createWriteStream(archivePath));
zip.on('error', (error) => zip.outputStream.destroy(error));
try {
  for await (const relative of files(directory)) {
    const file = path.join(directory, relative);
    inventory.push({ path: relative, bytes: (await stat(file)).size, sha256: await digest(file) });
    zip.addFile(file, `${name}/${relative}`, { mtime: archiveTimestamp });
  }
  zip.end();
  await finished;
} catch (error) {
  zip.outputStream.destroy(error);
  await finished.catch(() => {});
  throw error;
}
const manifest = {
  product: 'jastreamer-desktop', version: metadata.version, platform: 'win32', arch: 'x64',
  electron: metadata.devDependencies.electron,
  sourceRevision: process.env.JASTREAMER_SOURCE_REVISION || 'worktree',
  sourceDateEpoch,
  signed: false, productionQualified: false,
  directory: path.basename(directory),
  archive: { path: archiveName, bytes: (await stat(archivePath)).size, sha256: await digest(archivePath) },
  nativeAudio: {
    helper: 'resources/native-audio/jastreamer-audio.exe',
    runtime: runtimeFiles.map((file) => `resources/native-audio/${file}`),
    staticCrt: true,
    ffmpeg: {
      version: nativeLock.ffmpeg.version,
      source: `resources/native-audio/corresponding-source/deps/downloads/${nativeLock.ffmpeg.archive}`,
      sha256: nativeLock.ffmpeg.sha256,
    },
    nlohmannJson: {
      version: nativeLock.nlohmannJson.version,
      source: `resources/native-audio/corresponding-source/deps/downloads/${nativeLock.nlohmannJson.archive}`,
      sha256: nativeLock.nlohmannJson.sha256,
    },
  },
  files: inventory,
};
await writeFile(path.join(output, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
await writeFile(archivePath + '.sha256', `${manifest.archive.sha256}  ${archiveName}\n`);
console.log(JSON.stringify({ archive: archivePath, sha256: manifest.archive.sha256, files: inventory.length, signed: false }, null, 2));
