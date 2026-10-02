import { execFileSync } from 'node:child_process';
import { chmod, copyFile, mkdir, readFile, readdir } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('..', import.meta.url));
if (process.platform !== 'darwin' || process.arch !== 'arm64') {
  throw new Error('Native macOS audio requires Apple Silicon and arm64 Node.js; Intel, Universal and Rosetta builds are not supported.');
}
function run(tool, args, options = {}) {
  try { return execFileSync(tool, args, { cwd: root, stdio: 'inherit', ...options }); }
  catch (error) { throw new Error(`Required tool or build step failed: ${tool} ${args.join(' ')}. Install missing prerequisites explicitly; this script never installs software.`, { cause: error }); }
}
for (const [tool, args] of [['cmake', ['--version']], ['ninja', ['--version']], ['make', ['--version']], ['xcrun', ['--find', 'clang']], ['xcrun', ['--find', 'clang++']], ['xcrun', ['--find', 'install_name_tool']], ['xcrun', ['--find', 'lipo']], ['xcrun', ['--find', 'otool']], ['xcrun', ['--find', 'codesign']]]) run(tool, args);
if (run('uname', ['-m'], { encoding: 'utf8', stdio: 'pipe' }).trim() !== 'arm64') throw new Error('Rosetta builds are unsupported.');
const jobs = process.env.JOBS || '4';
if (!/^[1-9][0-9]*$/.test(jobs)) throw new Error('JOBS must be a positive integer.');
const lock = JSON.parse(await readFile(path.join(root, 'dependency-lock.json'), 'utf8'));
run(process.execPath, [path.join(root, 'scripts/fetch-native-deps.mjs')]);
const prefix = path.join(root, 'deps/ffmpeg-macos-arm64');
run('/bin/sh', [path.join(root, 'scripts/build-ffmpeg.sh'), path.join(root, 'deps/src', lock.ffmpeg.sourceDirectory), prefix], {
  env: { ...process.env, JOBS: jobs, JASTREAMER_FFMPEG_TOOLCHAIN: 'macos-arm64', MACOSX_DEPLOYMENT_TARGET: '13.0' },
});
const build = path.join(root, 'build-macos-arm64');
const dist = path.join(root, 'dist/macos-arm64');
run('cmake', ['-S', root, '-B', build, '-G', 'Ninja', '-DCMAKE_BUILD_TYPE=Release', '-DCMAKE_OSX_ARCHITECTURES=arm64', '-DCMAKE_OSX_DEPLOYMENT_TARGET=13.0', '-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON', '-DCMAKE_INSTALL_RPATH=@executable_path', `-DJASTREAMER_FFMPEG_ROOT=${prefix}`, `-DJASTREAMER_NLOHMANN_ROOT=${path.join(root, 'deps/src', lock.nlohmannJson.sourceDirectory)}`]);
run('cmake', ['--build', build, '--parallel', jobs]);
await mkdir(dist, { recursive: true });
const libraries = ['libavcodec.62.dylib', 'libavformat.62.dylib', 'libavutil.60.dylib', 'libswresample.6.dylib'];
for (const name of libraries) {
  await copyFile(path.join(prefix, 'lib', name), path.join(dist, name));
  await chmod(path.join(dist, name), 0o755);
  run('install_name_tool', ['-id', `@rpath/${name}`, path.join(dist, name)]);
}
// CMake emits the helper into this platform-specific staging directory.
for (const name of ['jastreamer-audio', ...libraries]) {
  const file = path.join(dist, name);
  const arch = run('lipo', ['-archs', file], { encoding: 'utf8', stdio: 'pipe' }).trim();
  if (arch !== 'arm64') throw new Error(`${name} must be thin arm64, found ${arch}`);
  const dependencies = run('otool', ['-L', file], { encoding: 'utf8', stdio: 'pipe' }).split('\n').slice(1).map(line => line.trim().split(' (')[0]).filter(Boolean);
  for (const dependency of dependencies) {
    const basename = path.basename(dependency);
    if (libraries.includes(basename) && dependency !== `@rpath/${basename}`) run('install_name_tool', ['-change', dependency, `@rpath/${basename}`, file]);
    else if (!libraries.includes(basename) && !dependency.startsWith('/usr/lib/') && !dependency.startsWith('/System/Library/')) throw new Error(`Non-distributable native dependency: ${name}: ${dependency}`);
  }
  run('codesign', ['--force', '--sign', '-', '--timestamp=none', file]);
}
const actual = (await readdir(dist)).sort();
if (JSON.stringify(actual) !== JSON.stringify(['jastreamer-audio', ...libraries].sort())) throw new Error(`Unexpected files in native staging directory: ${actual.join(', ')}. Move stale build outputs aside before rebuilding.`);
console.log(`Apple Silicon native helper and replaceable LGPL FFmpeg dylibs staged in ${dist}. No playback was started.`);
