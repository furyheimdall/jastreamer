import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { constants, copyFileSync, createReadStream, linkSync, mkdtempSync, rmSync } from 'node:fs';
import { access, lstat, open, readFile, readdir, readlink, realpath } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { listPackage, extractFile } from '@electron/asar';

export const runtimeNames = ['jastreamer-audio', 'libavcodec.62.dylib', 'libavformat.62.dylib', 'libavutil.60.dylib', 'libswresample.6.dylib'];
export function command(tool, args) {
  try { return execFileSync(tool, args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 16 * 1024 * 1024 }).trim(); }
  catch (error) { throw new Error(`${tool} ${args.join(' ')} failed: ${error.stderr || error.message}`, { cause: error }); }
}
export function requireMac() {
  assert(process.platform === 'darwin' && process.arch === 'arm64', 'Packaging/verification requires Apple Silicon macOS with native arm64 Node.js; no Intel, Universal or Rosetta fallback.');
  assert.equal(command('uname', ['-m']), 'arm64', 'Rosetta is unsupported');
  for (const tool of ['clang', 'lipo', 'otool', 'codesign', 'sips', 'iconutil', 'hdiutil', 'ditto', 'plutil']) command('/usr/bin/xcrun', ['--find', tool]);
}
export function packageIdentity(root, version) {
  const publicBuild = process.env.JASTREAMER_MACOS_DISTRIBUTION === 'public-adhoc';
  assert(!process.env.JASTREAMER_MACOS_DISTRIBUTION || publicBuild, 'Unknown macOS distribution classification');
  const revision = command('git', ['-C', root, 'rev-parse', 'HEAD']);
  const dirty = command('git', ['-C', root, 'status', '--porcelain', '--untracked-files=normal']) !== '';
  if (publicBuild) {
    assert.equal(process.env.GITHUB_ACTIONS, 'true', 'Public packages must be built by CI');
    assert.equal(process.env.GITHUB_SHA, revision, 'CI checkout differs from source revision');
    assert.equal(process.env.JASTREAMER_SOURCE_REVISION, revision, 'Public source revision must identify the checkout');
    assert(!dirty, 'Public packages require a clean source tree without untracked inputs');
  }
  const sourceRevision = dirty ? 'modified-worktree' : revision;
  if (process.env.JASTREAMER_SOURCE_REVISION) assert.equal(process.env.JASTREAMER_SOURCE_REVISION, sourceRevision, 'Source revision must describe the actual worktree');
  return {
    publicBuild, sourceRevision,
    distribution: publicBuild ? 'public-adhoc' : 'local-development',
    archiveName: `jastreamer-desktop_${version}_macos-arm64-${publicBuild ? 'adhoc' : 'development'}.dmg`,
  };
}
export async function digest(file) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest('hex');
}
export async function inventory(root, prefix = '') {
  const result = [];
  for (const item of (await readdir(path.join(root, prefix), { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name, 'en'))) {
    const relative = prefix + item.name;
    const file = path.join(root, relative);
    const details = await lstat(file);
    if (item.isDirectory()) result.push(...await inventory(root, `${relative}/`));
    else if (item.isSymbolicLink()) {
      const target = await readlink(file);
      assert(!path.isAbsolute(target), `Absolute symlink in bundle: ${relative}`);
      const resolved = await realpath(file);
      assert(resolved.startsWith(`${await realpath(root)}${path.sep}`), `Escaping symlink: ${relative}`);
      result.push({ path: relative, type: 'symlink', target });
    } else {
      assert(item.isFile(), `Unexpected bundle entry: ${relative}`);
      result.push({ path: relative, type: 'file', mode: details.mode & 0o777, bytes: details.size, sha256: await digest(file) });
    }
  }
  return result;
}
export async function isMachO(file) {
  const handle = await open(file);
  try {
    const header = Buffer.alloc(4);
    if ((await handle.read(header, 0, 4, 0)).bytesRead !== 4) return false;
    return [0xfeedface, 0xfeedfacf, 0xcefaedfe, 0xcffaedfe, 0xcafebabe, 0xbebafeca, 0xcafebabf, 0xbfbafeca].includes(header.readUInt32BE());
  } finally { await handle.close(); }
}
function inspectMachO(file, option) {
  // Apple otool interprets a trailing "(...)" in a filename as an archive
  // member selector, including Electron Helper (GPU)/(Renderer) executables.
  // Inspect the same bytes under a safe basename; resolve load paths against
  // the original bundle location, never this temporary inspection directory.
  const temporary = mkdtempSync(path.join(os.tmpdir(), 'jastreamer-macho-'));
  const alias = path.join(temporary, 'binary');
  try {
    try { linkSync(file, alias); }
    catch (error) {
      if (error.code !== 'EXDEV') throw error;
      copyFileSync(file, alias, constants.COPYFILE_FICLONE);
    }
    return command('otool', [option, alias]);
  } finally { rmSync(temporary, { recursive: true, force: true }); }
}
function loadPaths(file) {
  return [...inspectMachO(file, '-l').matchAll(/cmd LC_RPATH\s+cmdsize \d+\s+path (.+?) \(offset \d+\)/g)].map(match => match[1]);
}
export async function verifyBundle(app, metadata, lock, packageLock) {
  const plist = JSON.parse(command('plutil', ['-convert', 'json', '-o', '-', path.join(app, 'Contents/Info.plist')]));
  assert.equal(plist.CFBundleIdentifier, 'org.jastreamer.desktop');
  assert.equal(plist.CFBundleShortVersionString, metadata.version);
  assert.equal(plist.LSMinimumSystemVersion, '13.0');
  assert.equal(plist.CFBundleExecutable, 'jastreamer-desktop');
  await access(path.join(app, 'Contents/Resources', plist.CFBundleIconFile));
  const entries = await inventory(app);
  const forbidden = /(^|\/)(?:\.env(?:\.[^/]*)?|\.git|user-data|\.user-data|recents\.json|preferences\.json|Cookies|Login Data|Local State|\.DS_Store)(\/|$)|\.(?:pem|p12|p8|key|mobileprovision)$/i;
  for (const entry of entries) assert(!forbidden.test(entry.path), `Private/development data in bundle: ${entry.path}`);
  const resources = path.join(app, 'Contents/Resources');
  const asar = path.join(resources, 'app.asar');
  const packagedMetadata = JSON.parse(extractFile(asar, 'package.json').toString());
  assert.equal(packagedMetadata.version, metadata.version);
  assert.equal(packagedMetadata.main, 'main.mjs');
  const allowedRoots = new Set(['main.mjs', 'preload.cjs', 'remote-preload.cjs', 'shell.js', 'shell.css', 'index.html', 'package.json', 'assets', 'lib', 'node_modules']);
  const devPackages = Object.entries(packageLock.packages).filter(([, value]) => value.dev).map(([key]) => key);
  for (const entry of listPackage(asar)) {
    const relative = entry.replace(/^\//, '');
    assert(allowedRoots.has(relative.split('/')[0]), `Unexpected app payload: ${relative}`);
    assert(!forbidden.test(relative), `Private app payload: ${relative}`);
    assert(!devPackages.some(name => relative === name || relative.startsWith(`${name}/`)), `Development dependency in app: ${relative}`);
  }
  for (const name of ['main.mjs', 'preload.cjs', 'remote-preload.cjs', 'index.html', 'lib/native-controller.mjs', 'lib/native-process.mjs', 'assets/jastreamer.png']) extractFile(asar, name);
  const native = path.join(resources, 'native-audio');
  for (const name of runtimeNames) await access(path.join(native, name));
  assert.deepEqual(JSON.parse(await readFile(path.join(native, 'dependencies.json'), 'utf8')), lock);
  for (const dependency of [lock.ffmpeg, lock.nlohmannJson]) assert.equal(await digest(path.join(native, 'corresponding-source/deps/downloads', dependency.archive)), dependency.sha256);
  for (const required of ['THIRD-PARTY-NOTICES.txt', 'RELINKING.txt', `legal/ffmpeg/${lock.ffmpeg.license}`, `legal/nlohmann-json/${lock.nlohmannJson.license}`, 'legal/jastreamer/LICENSE.Apache-2.0', 'corresponding-source/CMakeLists.txt', 'corresponding-source/audio_engine_coreaudio.cpp', 'corresponding-source/scripts/build-native-macos.mjs', 'corresponding-source/scripts/build-ffmpeg.sh', 'corresponding-source/scripts/fetch-native-deps.mjs']) await access(path.join(native, required));
  const main = path.join(app, 'Contents/MacOS/jastreamer-desktop');
  const mainRpaths = loadPaths(main);
  const machOFiles = [];
  for (const entry of entries.filter(item => item.type === 'file')) {
    const file = path.join(app, entry.path);
    if (!await isMachO(file)) continue;
    machOFiles.push({ path: entry.path, arch: 'arm64', sha256: entry.sha256 });
    assert.equal(command('lipo', ['-archs', file]), 'arm64', `Not thin arm64: ${entry.path}`);
    command('codesign', ['--verify', '--strict', file]);
    const enclosingApp = file.slice(0, file.lastIndexOf('.app/') + 4);
    const executableDirectory = entry.path.startsWith('Contents/Resources/native-audio/') ? native : path.join(enclosingApp, 'Contents/MacOS');
    const expand = value => value.replace(/^@loader_path/, path.dirname(file)).replace(/^@executable_path/, executableDirectory);
    const rpaths = [...loadPaths(file), ...(entry.path.startsWith('Contents/Resources/native-audio/') ? loadPaths(path.join(native, runtimeNames[0])) : mainRpaths)];
    for (const rpath of rpaths) assert(rpath.startsWith('@loader_path') || rpath.startsWith('@executable_path'), `Non-relocatable RPATH ${rpath}: ${entry.path}`);
    const ids = inspectMachO(file, '-D').split('\n').slice(1).map(line => line.trim());
    const dependencies = inspectMachO(file, '-L').split('\n').slice(1).map(line => line.trim().split(' (')[0]).filter(Boolean);
    for (const dependency of dependencies.filter(item => !ids.includes(item))) {
      if (dependency.startsWith('/System/Library/') || dependency.startsWith('/usr/lib/')) continue;
      assert(dependency.startsWith('@'), `External dependency ${dependency}: ${entry.path}`);
      const candidates = dependency.startsWith('@rpath/') ? rpaths.map(rpath => path.join(expand(rpath), dependency.slice(7))) : [expand(dependency)];
      let found = false;
      for (const candidate of candidates) {
        try {
          const resolved = await realpath(candidate);
          assert(resolved.startsWith(`${await realpath(app)}/`), `Dependency escapes app: ${dependency}`);
          found = true;
          break;
        } catch (error) { if (error.code !== 'ENOENT') throw error; }
      }
      assert(found, `Unresolved dylib ${dependency}: ${entry.path}`);
    }
  }
  assert.equal(machOFiles.length, 18, 'Pinned Electron/native Mach-O inventory changed');
  for (const name of runtimeNames.slice(1)) assert.equal(inspectMachO(path.join(native, name), '-D').split('\n')[1]?.trim(), `@rpath/${name}`);
  assert(loadPaths(path.join(native, runtimeNames[0])).includes('@executable_path'), 'Native helper lacks relocatable runtime search path');
  command('codesign', ['--verify', '--deep', '--strict', app]);
  const signature = spawnSync('codesign', ['--display', '--verbose=4', app], { encoding: 'utf8' });
  assert.equal(signature.status, 0, 'Unable to inspect application signature');
  assert.match(signature.stderr, /^Signature=adhoc$/m, 'Artifact must be explicitly ad-hoc signed');
  return { entries, machOCount: machOFiles.length, machOFiles };
}
