import { expect, test } from 'bun:test';
import { spawn } from 'node:child_process';
import { resolve } from 'node:path';

const repository = resolve(import.meta.dirname, '../..');

test('pinned temporary Dart driver runs all real Go TLS and WSS scenarios', async () => {
  const child = spawn('bun', ['tooling/qa/control-gateway-driver.mjs'], {
    cwd: repository,
    env: process.env,
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', (chunk) => { stdout += chunk.toString(); });
  child.stderr.on('data', (chunk) => { stderr += chunk.toString(); });
  const code = await new Promise((resolveExit, reject) => {
    child.once('error', reject);
    child.once('exit', resolveExit);
  });

  expect(code, stderr).toBe(0);
  const receipt = JSON.parse(stdout);
  expect(receipt.results).toHaveLength(9);
  expect(receipt.results.find((result) => result.scenario === 'event-sequence-gap')).toMatchObject({
    full_resyncs: 1,
    bounded: true,
  });
  expect(receipt.cleanup).toEqual({
    server_stopped: true,
    renderer_stopped: true,
    proxies_closed: true,
    temporary_directory_removed: true,
  });
}, 300_000);
