import { expect, test } from 'bun:test';
import { chmod, mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import {
  DRIVER_BUILD_COMMAND,
  requireGatewayDriver,
} from '../../apps/control/test_driver/gateway_driver_path.mjs';
import { withTemporaryControlGatewayDriver } from './control-gateway-driver.mjs';

const repository = resolve(import.meta.dirname, '../..');

const fixture = async (body) => {
  const root = await mkdtemp(join(tmpdir(), 'gateway-driver-path-'));
  try {
    return await body(root);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
};

test('real gateway rejects a missing driver with the exact self-contained build command', async () => {
  await expect(requireGatewayDriver({ environment: {}, repository })).rejects.toThrow(DRIVER_BUILD_COMMAND);
});

test('real gateway rejects the former repository build default as a source input', async () => {
  await expect(requireGatewayDriver({
    environment: { CONTROL_GATEWAY_DRIVER: 'apps/control/build/control-gateway-driver' },
    repository,
  })).rejects.toThrow('must not use apps/control/build');
});

test('real gateway validates an explicit executable before spawning', async () => fixture(async (root) => {
  const executable = join(root, 'driver');
  await writeFile(executable, '#!/bin/sh\nexit 0\n');
  await chmod(executable, 0o700);
  expect(await requireGatewayDriver({
    environment: { CONTROL_GATEWAY_DRIVER: executable },
    repository,
  })).toBe(executable);
  await expect(requireGatewayDriver({
    environment: { CONTROL_GATEWAY_DRIVER: join(root, 'missing') },
    repository,
  })).rejects.toThrow('is not an executable file');
}));

test('temporary gateway build cleans the package and binary after target failure', async () => {
  let observedRoot;
  const compile = async (_toolchain, _packageRoot, driverPath) => {
    await writeFile(driverPath, '#!/bin/sh\nexit 0\n');
    await chmod(driverPath, 0o700);
  };
  await expect(withTemporaryControlGatewayDriver(async (driverPath, temporaryRoot) => {
    observedRoot = temporaryRoot;
    expect(await Bun.file(driverPath).exists()).toBe(true);
    throw new Error('target failed');
  }, { toolchainProvider: async () => ({ kind: 'fixture' }), compile })).rejects.toThrow('target failed');
  expect(await Bun.file(observedRoot).exists()).toBe(false);
});
