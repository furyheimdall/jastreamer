import { spawn } from 'node:child_process';
import { join, resolve } from 'node:path';
import { stopChild } from './control-servers.mjs';
import { requireGatewayDriver } from '../../apps/control/test_driver/gateway_driver_path.mjs';
import { deadline } from './control-gateway-http.mjs';

const repository = resolve(import.meta.dirname, '../..');
const driverBinary = await requireGatewayDriver({ repository });

export const startDriver = (server, token, mode, extraEnvironment = {}) => {
  const child = spawn(driverBinary, [mode], {
    cwd: join(repository, 'apps/control'),
    env: {
      ...process.env,
      JASTREAMER_SERVER_URL: server.origin,
      JASTREAMER_CERTIFICATE_SHA256: server.fingerprint,
      JASTREAMER_CONTROL_TOKEN: token,
      JASTREAMER_RUN_ID: mode.slice(2),
      ...extraEnvironment,
    },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stderr = '';
  let stdoutBuffer = '';
  const records = [];
  const readyWaiters = [];
  child.stderr.on('data', (chunk) => { stderr += chunk.toString(); });
  child.stdout.on('data', (chunk) => {
    stdoutBuffer += chunk.toString();
    while (stdoutBuffer.includes('\n')) {
      const end = stdoutBuffer.indexOf('\n');
      const line = stdoutBuffer.slice(0, end).trim();
      stdoutBuffer = stdoutBuffer.slice(end + 1);
      if (!line) continue;
      const record = JSON.parse(line);
      records.push(record);
      if (record.ready) {
        for (const waiter of readyWaiters.filter((item) => item.expected === record.ready)) {
          waiter.resolve(record);
          readyWaiters.splice(readyWaiters.indexOf(waiter), 1);
        }
      }
    }
  });
  const waitForReady = (expected) => deadline(new Promise((resolveReady, rejectReady) => {
    const existing = records.find((record) => record.ready === expected);
    if (existing) {
      resolveReady(existing);
      return;
    }
    readyWaiters.push({ expected, resolve: resolveReady, reject: rejectReady });
  }), `${mode} ${expected} readiness`);
  child.once('exit', (code) => {
    for (const waiter of readyWaiters.splice(0)) {
      waiter.reject(new Error(`${mode} exited ${code} before ${waiter.expected}: ${stderr}`));
    }
  });
  const ready = mode.startsWith('--wait-')
    ? waitForReady(mode.slice('--wait-'.length))
    : Promise.resolve(null);
  const done = new Promise((resolveDone, rejectDone) => {
    let settled = false;
    const finish = (action) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      action();
    };
    const diagnostic = () => JSON.stringify({
      mode,
      pid: child.pid,
      exitCode: child.exitCode,
      signalCode: child.signalCode,
      records,
      stderr,
      partialStdout: stdoutBuffer,
    });
    const timeoutVariable = mode === '--event-gap'
      ? process.env.TASK15_EVENT_GAP_TIMEOUT_MS
      : process.env.TASK15_DRIVER_TIMEOUT_MS;
    const completionTimeout = Number.parseInt(timeoutVariable ?? '120000', 10);
    if (!Number.isSafeInteger(completionTimeout) || completionTimeout <= 0) {
      throw new Error('Task 15 driver timeout must be a positive integer');
    }
    const timeout = setTimeout(() => {
      void stopChild(child).finally(() => finish(() => rejectDone(
        new Error(`${mode} completion timed out; driver=${diagnostic()}`),
      )));
    }, completionTimeout);
    child.once('error', (error) => finish(() => rejectDone(
      new Error(`${mode} process error: ${error}; driver=${diagnostic()}`),
    )));
    child.once('exit', (code) => finish(() => {
      if (code !== 0) {
        rejectDone(new Error(`${mode} exited ${code}; driver=${diagnostic()}`));
        return;
      }
      const result = records.findLast((record) => record.scenario);
      if (!result) {
        rejectDone(new Error(`${mode} emitted no scenario result; driver=${diagnostic()}`));
        return;
      }
      resolveDone(result);
    }));
  });
  void done.catch(() => {});
  return { child, ready, done, waitForReady };
};
