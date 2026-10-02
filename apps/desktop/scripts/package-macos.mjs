import { packager } from '@electron/packager';
import { access, copyFile, mkdir, mkdtemp, readFile, readdir, rm, stat, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { command, digest, inventory, isMachO, packageIdentity, requireMac, runtimeNames, verifyBundle } from './macos-package-common.mjs';

requireMac();
const root = fileURLToPath(new URL('..', import.meta.url));
const output = path.join(root, 'dist/macos-arm64');
const nativeRoot = path.join(root, 'native/audio');
const metadata = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8'));
const packageLock = JSON.parse(await readFile(path.join(root, 'package-lock.json'), 'utf8'));
const lock = JSON.parse(await readFile(path.join(nativeRoot, 'dependency-lock.json'), 'utf8'));
const identity = packageIdentity(root, metadata.version);
for (const name of runtimeNames) await access(path.join(nativeRoot, 'dist/macos-arm64', name));
await mkdir(output, { recursive: true });
const temporary = await mkdtemp(path.join(os.tmpdir(), 'jastreamer-macos-package-'));
try {
  const iconset = path.join(temporary, 'jastreamer.iconset');
  await mkdir(iconset);
  for (const size of [16, 32, 128, 256, 512]) {
    for (const scale of [1, 2]) command('sips', ['-z', String(size * scale), String(size * scale), path.join(root, 'assets/jastreamer.png'), '--out', path.join(iconset, `icon_${size}x${size}${scale === 2 ? '@2x' : ''}.png`)]);
  }
  const icon = path.join(temporary, 'jastreamer.icns');
  command('iconutil', ['-c', 'icns', '-o', icon, iconset]);
  const allowedRoots = new Set(['main.mjs', 'preload.cjs', 'remote-preload.cjs', 'shell.js', 'shell.css', 'index.html', 'package.json', 'assets', 'lib', 'node_modules']);
  const [directory] = await packager({
    dir: root, out: temporary, name: 'jastreamer', executableName: 'jastreamer-desktop',
    platform: 'darwin', arch: 'arm64', electronVersion: metadata.devDependencies.electron,
    appVersion: metadata.version, appBundleId: 'org.jastreamer.desktop',
    appCategoryType: 'public.app-category.music', appCopyright: 'Copyright jastreamer contributors',
    icon, asar: true, prune: true, overwrite: false, darwinDarkModeSupport: true,
    extendInfo: { LSMinimumSystemVersion: '13.0', NSLocalNetworkUsageDescription: 'Discover and connect to your jastreamer Server on your local network.', NSBonjourServices: ['_jastreamer._tcp'] },
    ignore: relative => {
      const normalized = relative.replace(/^\//, '');
      if (!normalized) return false;
      return !allowedRoots.has(normalized.split('/')[0]) || /(^|\/)(?:\.env(?:\.[^/]*)?|\.git|\.DS_Store|user-data|\.user-data)(\/|$)/i.test(normalized) || /\.(?:pem|p12|p8|key|mobileprovision)$/i.test(normalized);
    },
  });
  const app = path.join(directory, 'jastreamer.app');
  const resources = path.join(app, 'Contents/Resources');
  const native = path.join(resources, 'native-audio');
  const source = path.join(native, 'corresponding-source');
  await mkdir(source, { recursive: true });
  for (const name of runtimeNames) await copyFile(path.join(nativeRoot, 'dist/macos-arm64', name), path.join(native, name));
  await copyFile(path.join(nativeRoot, 'dependency-lock.json'), path.join(native, 'dependencies.json'));
  await copyFile(path.join(nativeRoot, 'THIRD-PARTY-NOTICES.txt'), path.join(native, 'THIRD-PARTY-NOTICES.txt'));
  await copyFile(path.resolve(root, '../../LICENSE'), path.join(resources, 'LICENSE.jastreamer'));
  for (const [name, dependency] of [['ffmpeg', lock.ffmpeg], ['nlohmann-json', lock.nlohmannJson]]) {
    await mkdir(path.join(native, 'legal', name), { recursive: true });
    await copyFile(path.join(nativeRoot, 'deps/src', dependency.sourceDirectory, dependency.license), path.join(native, 'legal', name, dependency.license));
    await mkdir(path.join(source, 'deps/downloads'), { recursive: true });
    await copyFile(path.join(nativeRoot, 'deps/downloads', dependency.archive), path.join(source, 'deps/downloads', dependency.archive));
  }
  await mkdir(path.join(native, 'legal/jastreamer'), { recursive: true });
  await copyFile(path.resolve(root, '../../LICENSE'), path.join(native, 'legal/jastreamer/LICENSE.Apache-2.0'));
  // Copy only build inputs, never a development tree, cache, private profile or binary build output.
  for (const subdir of ['', 'tests', 'scripts']) {
    for (const item of await readdir(path.join(nativeRoot, subdir), { withFileTypes: true })) {
      if (!item.isFile() || !(subdir === 'scripts' ? /^(?:build-ffmpeg\.sh|build-native-macos\.mjs|fetch-native-deps\.mjs)$/ : /\.(?:cpp|hpp|h|mm)$|^(?:CMakeLists\.txt|dependency-lock\.json|media_controls_macos\.plist)$/).test(item.name)) continue;
      await mkdir(path.join(source, subdir), { recursive: true });
      await copyFile(path.join(nativeRoot, subdir, item.name), path.join(source, subdir, item.name));
    }
  }
  await writeFile(path.join(native, 'RELINKING.txt'), [
    'jastreamer macOS arm64 ad-hoc build: replaceable LGPL FFmpeg libraries', '',
    'FFmpeg is dynamically linked, LGPL v2.1-or-later only (GPL, nonfree and version3 disabled).',
    'The exact verified FFmpeg and nlohmann/json source archives, full helper source, CMake inputs,',
    'dependency lock and build recipes are in corresponding-source. No media/session credentials are included.',
    'Prerequisites: Apple Silicon macOS 13+, native arm64 Node.js 22.12+, CMake 3.25+, Ninja,',
    'Xcode Command Line Tools (clang, make, codesign). These recipes do not install host software.', '',
    'Rebuild unmodified helper and dependencies in a writable copy of corresponding-source:',
    '  node scripts/build-native-macos.mjs',
    'For modified FFmpeg, extract deps/downloads/ffmpeg-*.tar.xz into a writable directory,',
    'make your changes, then use the included recipe without re-fetching/replacing modified sources:',
    '  JASTREAMER_FFMPEG_TOOLCHAIN=macos-arm64 sh scripts/build-ffmpeg.sh /absolute/modified-source /absolute/prefix',
    'The configure recipe preserves LGPL-only components, --enable-shared, --disable-static and @rpath install names.',
    'To relink the helper against your prefix, use the CMake invocation in build-native-macos.mjs',
    'with JASTREAMER_FFMPEG_ROOT set to that prefix and JASTREAMER_NLOHMANN_ROOT to extracted JSON source.',
    'Stop and quit jastreamer before replacement. In a writable copy of jastreamer.app replace',
    'Contents/Resources/native-audio/libavcodec.62.dylib, libavformat.62.dylib, libavutil.60.dylib,',
    'libswresample.6.dylib with ABI-compatible arm64 builds; optionally replace jastreamer-audio.',
    'Keep @rpath library IDs and dependencies, and helper @executable_path RPATH.',
    'Changes invalidate the app signature. Re-sign replaced libraries/helper with:',
    '  codesign --force --sign - --timestamp=none /path/to/replaced-file',
    'Then re-seal the writable app, preserving the existing ad-hoc entitlements:',
    '  codesign --force --sign - --timestamp=none --preserve-metadata=entitlements /path/to/jastreamer.app',
    '  codesign --verify --deep --strict /path/to/jastreamer.app',
    'No signing key, vendor authorization or network service is required to relink these ad-hoc builds.',
    'The generated checksum/manifest applies only to the original unmodified artifact.', '',
    'This is ad-hoc signed, not Developer ID signed or notarized.',
    'Public ad-hoc distribution does not imply Apple identity signing, notarization or production qualification.', '',
  ].join('\n'));
  await writeFile(path.join(resources, 'START-HERE.txt'), [
    `jastreamer Desktop Controller — Apple Silicon ${identity.distribution} build`, '',
    'Drag jastreamer.app to Applications. Requires Apple Silicon macOS 13 or newer; no Intel/Rosetta support.',
    'Connect to an existing jastreamer Server. Browser output is the default; native CoreAudio is opt-in.',
    'No playback starts automatically. Select the intended USB DAC and mode while stopped.',
    'Preferences and sessions live in the macOS application-support profile, never inside this read-only app.',
    'Quit the app before replacing it; changing or reinstalling the app must preserve your profile.',
    'This artifact is ad-hoc signed only, not Developer ID signed, notarized or production-qualified.',
    'macOS may refuse this downloaded app. Do not disable Gatekeeper or system security.',
    'Use a local source build or an explicitly trusted per-app macOS approval where available.',
    'Corresponding source and FFmpeg replacement instructions are under Resources/native-audio.', '',
  ].join('\n'));
  const entitlements = path.join(temporary, 'development-entitlements.plist');
  await writeFile(entitlements, '<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>com.apple.security.cs.allow-jit</key><true/></dict></plist>\n');
  const entries = await inventory(app);
  for (const entry of entries.filter(item => item.type === 'file')) {
    const file = path.join(app, entry.path);
    if (await isMachO(file)) command('codesign', ['--force', '--sign', '-', '--timestamp=none', ...(entry.path.includes('/MacOS/') ? ['--entitlements', entitlements] : []), file]);
  }
  const bundles = new Set();
  for (const entry of entries) {
    const parts = entry.path.split('/');
    for (let i = 0; i < parts.length - 1; ++i) if (/\.(?:app|framework|xpc)$/.test(parts[i])) bundles.add(parts.slice(0, i + 1).join('/'));
  }
  for (const relative of [...bundles].sort((a, b) => b.split('/').length - a.split('/').length)) command('codesign', ['--force', '--sign', '-', '--timestamp=none', ...(relative.endsWith('.app') ? ['--entitlements', entitlements] : []), path.join(app, relative)]);
  command('codesign', ['--force', '--sign', '-', '--timestamp=none', '--entitlements', entitlements, app]);
  const verified = await verifyBundle(app, metadata, lock, packageLock);
  const finalApp = path.join(output, 'jastreamer.app');
  // Never overwrite a user's application/profile or an existing artifact implicitly.
  try { await access(finalApp); throw new Error(`Output already exists: ${finalApp}. Move the previous development artifact aside before packaging.`); }
  catch (error) { if (error.code !== 'ENOENT') throw error; }
  command('ditto', [app, finalApp]);
  const imageRoot = path.join(temporary, 'image');
  await mkdir(imageRoot);
  command('ditto', [finalApp, path.join(imageRoot, 'jastreamer.app')]);
  await symlink('/Applications', path.join(imageRoot, 'Applications'));
  const archiveName = identity.archiveName;
  const archive = path.join(output, archiveName);
  command('hdiutil', ['create', '-volname', 'jastreamer Ad-hoc', '-srcfolder', imageRoot, '-format', 'UDZO', '-fs', 'HFS+', archive]);
  const manifest = {
    product: 'jastreamer-desktop', version: metadata.version, platform: 'darwin', arch: 'arm64', minimumMacOS: '13.0',
    electron: metadata.devDependencies.electron, packager: metadata.devDependencies['@electron/packager'],
    sourceRevision: identity.sourceRevision, distribution: identity.distribution,
    developmentBuild: !identity.publicBuild, signed: false, signing: 'ad-hoc', developerIDSigned: false, notarized: false, productionQualified: false,
    directory: 'jastreamer.app', archive: { path: archiveName, bytes: (await stat(archive)).size, sha256: await digest(archive) },
    nativeAudio: { helper: 'Contents/Resources/native-audio/jastreamer-audio', runtime: runtimeNames, ffmpeg: lock.ffmpeg.version, dynamicLinking: true },
    files: verified.entries,
  };
  await writeFile(path.join(output, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
  await writeFile(`${archive}.sha256`, `${manifest.archive.sha256}  ${archiveName}\n`);
  console.log(JSON.stringify({ app: finalApp, archive, sha256: manifest.archive.sha256, machOCount: verified.machOCount, signing: manifest.signing, notarized: false }, null, 2));
} finally { await rm(temporary, { recursive: true, force: true }); }
