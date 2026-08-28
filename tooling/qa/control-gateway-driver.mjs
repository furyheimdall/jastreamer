#!/usr/bin/env bun
import { constants } from 'node:fs';
import { access, cp, mkdir, mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path';
import { spawn } from 'node:child_process';
import { pathToFileURL } from 'node:url';

export const FLUTTER_VERSION = '3.35.0';
export const FLUTTER_IMAGE =
  'ghcr.io/cirruslabs/flutter:3.35.0@sha256:114f14a7cf973b08e4607d3e2fb4a3b2dc977c08877e651743f8cbed0e971046';
const repository = resolve(import.meta.dirname, '../..');

const execute = (command, args, options = {}) => new Promise((resolveExit, reject) => {
  const child = spawn(command, args, { stdio: ['ignore', 'pipe', 'pipe'], ...options });
  let stdout = '';
  let stderr = '';
  child.stdout?.on('data', (chunk) => { stdout += chunk; });
  child.stderr?.on('data', (chunk) => { stderr += chunk; });
  child.once('error', reject);
  child.once('exit', (code, signal) => resolveExit({ code, signal, stdout, stderr }));
});

const successful = async (command, args, options) => {
  const result = await execute(command, args, options);
  if (result.code !== 0) {
    throw new Error(`${command} ${args.join(' ')} failed (${result.code ?? result.signal}): ${result.stderr || result.stdout}`);
  }
  return result;
};

const localFlutter = async () => {
  const located = await execute('sh', ['-lc', 'command -v flutter']);
  if (located.code !== 0) return undefined;
  const flutter = located.stdout.trim();
  const version = await execute(flutter, ['--version', '--machine']);
  if (version.code !== 0) return undefined;
  try {
    if (JSON.parse(version.stdout).frameworkVersion !== FLUTTER_VERSION) return undefined;
  } catch {
    return undefined;
  }
  return { flutter, dart: join(dirname(flutter), 'dart') };
};

const cachedDocker = async () => {
  const inspected = await execute('docker', ['image', 'inspect', FLUTTER_IMAGE]).catch(() => ({ code: 1 }));
  return inspected.code === 0;
};

export const qualificationToolchain = async () => {
  const local = await localFlutter();
  if (local) return { kind: 'local', ...local };
  if (await cachedDocker()) return { kind: 'docker', image: FLUTTER_IMAGE };
  throw new Error(
    `CONTROL_GATEWAY_TOOLCHAIN_UNAVAILABLE: require Flutter ${FLUTTER_VERSION} in PATH ` +
    `or cached pinned image ${FLUTTER_IMAGE}; no image pull is performed`,
  );
};

const copyPackage = async (packageRoot) => {
  await mkdir(packageRoot, { recursive: true });
  for (const directory of ['bin', 'lib']) {
    await cp(join(repository, 'apps/control', directory), join(packageRoot, directory), { recursive: true });
  }
  for (const file of ['pubspec.yaml', 'pubspec.lock']) {
    await cp(
      join(repository, 'apps/control/test_driver', `driver_${file}`),
      join(packageRoot, file),
    );
  }
};

const compileDriver = async (toolchain, packageRoot, driverPath) => {
  if (toolchain.kind === 'local') {
    await successful(toolchain.dart, ['pub', 'get', '--offline', '--enforce-lockfile'], { cwd: packageRoot });
    await successful(toolchain.dart, ['compile', 'exe', 'bin/control_gateway_driver.dart', '-o', driverPath], { cwd: packageRoot });
    return;
  }
  const uid = process.getuid?.() ?? 0;
  const gid = process.getgid?.() ?? 0;
  const script = [
    'set -eu',
    'dart pub get --offline --enforce-lockfile',
    'dart compile exe bin/control_gateway_driver.dart -o /workspace/control-gateway-driver',
    `chown -R ${uid}:${gid} /workspace`,
  ].join('\n');
  await successful('docker', [
    'run', '--rm', '-v', `${packageRoot}:/workspace`, '-w', '/workspace',
    toolchain.image, 'sh', '-lc', script,
  ]);
};

export const withTemporaryControlGatewayDriver = async (run, {
  toolchainProvider = qualificationToolchain,
  compile = compileDriver,
} = {}) => {
  const temporaryRoot = await mkdtemp(join(tmpdir(), 'jastreamer-control-driver-'));
  const packageRoot = join(temporaryRoot, 'control');
  const driverPath = join(packageRoot, 'control-gateway-driver');
  try {
    await copyPackage(packageRoot);
    await compile(await toolchainProvider(), packageRoot, driverPath);
    await access(driverPath, constants.X_OK);
    return await run(driverPath, temporaryRoot);
  } finally {
    await rm(temporaryRoot, { recursive: true, force: true });
  }
};

const runTarget = async (driverPath, target, targetArguments) => {
  const child = spawn('bun', [target, ...targetArguments], {
    cwd: repository,
    env: { ...process.env, CONTROL_GATEWAY_DRIVER: driverPath },
    stdio: 'inherit',
  });
  const forward = (signal) => child.kill(signal);
  process.once('SIGINT', forward);
  process.once('SIGTERM', forward);
  try {
    const { code, signal } = await new Promise((resolveExit, reject) => {
      child.once('error', reject);
      child.once('exit', (code, signal) => resolveExit({ code, signal }));
    });
    if (code !== 0) throw new Error(`real gateway target exited ${code ?? signal}`);
  } finally {
    process.off('SIGINT', forward);
    process.off('SIGTERM', forward);
  }
};

const main = async () => {
  const args = process.argv.slice(2);
  const separator = args.indexOf('--');
  const targetIndex = args.indexOf('--target');
  const target = targetIndex < 0 ? 'tooling/qa/control-gateway-real.mjs' : args[targetIndex + 1];
  if (!target || (targetIndex >= 0 && targetIndex + 1 >= (separator < 0 ? args.length : separator))) {
    throw new Error('USAGE: control-gateway-driver.mjs [--target <repository-script>] [-- <arguments>]');
  }
  const absoluteTarget = resolve(repository, target);
  const targetLocation = relative(repository, absoluteTarget);
  if (targetLocation === '' || isAbsolute(targetLocation) || targetLocation === '..' ||
      targetLocation.startsWith(`..${sep}`)) throw new Error(`gateway target is outside the repository: ${target}`);
  await access(absoluteTarget).catch(() => { throw new Error(`gateway target does not exist: ${target}`); });
  await withTemporaryControlGatewayDriver((driverPath) =>
    runTarget(driverPath, target, separator < 0 ? [] : args.slice(separator + 1)));
};

if (import.meta.url === pathToFileURL(process.argv[1] ?? '').href) await main();
