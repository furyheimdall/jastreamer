import { spawn } from 'node:child_process';
import { request as secureRequest, createServer as createSecureServer } from 'node:https';
import { connect as connectTls } from 'node:tls';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { stopChild, trackServerConnections } from './control-server-process.mjs';

export const startEventGapProxySidecar = async (todo13, controlOrigin) => {
  const child = spawn('node', [
    join(import.meta.dirname, 'event-gap-proxy-child.mjs'),
    todo13.origin,
    todo13.directory,
    todo13.fingerprint,
    controlOrigin ?? '',
  ], { stdio: ['pipe', 'pipe', 'pipe'] });
  let stderr = '';
  let stdout = '';
  const records = [];
  const dropWaiters = [];
  const pendingDrops = [];
  let resolveReady;
  let rejectReady;
  const readyRecord = new Promise((resolve, reject) => {
    resolveReady = resolve;
    rejectReady = reject;
  });
  child.stderr.on('data', (chunk) => { stderr += chunk.toString(); });
  const accept = (record) => {
    records.push(record);
    if (record.type === 'ready') resolveReady(record);
    if (record.type === 'drop') {
      const waiter = dropWaiters.shift();
      if (waiter) waiter(record.event);
      else pendingDrops.push(record.event);
    }
  };
  child.stdout.on('data', (chunk) => {
    stdout += chunk.toString();
    while (stdout.includes('\n')) {
      const end = stdout.indexOf('\n');
      const line = stdout.slice(0, end).trim();
      stdout = stdout.slice(end + 1);
      if (line) accept(JSON.parse(line));
    }
  });
  child.once('exit', (code) => {
    rejectReady(new Error(`event-gap Node sidecar exited ${code}: ${stderr}`));
  });
  let readinessTimeout;
  const ready = await Promise.race([
    readyRecord,
    new Promise((_, reject) => {
      readinessTimeout = setTimeout(() => {
        void stopChild(child).finally(() => reject(
          new Error(`event-gap Node sidecar readiness timed out: ${stderr}`),
        ));
      }, 30_000);
    }),
  ]).finally(() => clearTimeout(readinessTimeout));
  const nextDrop = () => new Promise((resolve) => {
    if (pendingDrops.length > 0) resolve(pendingDrops.shift());
    else dropWaiters.push(resolve);
  });
  const dropped = nextDrop();
  return {
    origin: ready.origin,
    fingerprint: todo13.fingerprint,
    close: () => stopChild(child),
    dropped,
    dropNextInvalidation: () => {
      const result = nextDrop();
      child.stdin.write('drop\n');
      return result;
    },
    diagnostics: () => ({
      runtime: 'node-sidecar', pid: child.pid, exitCode: child.exitCode,
      records: structuredClone(records), stderr, partialStdout: stdout,
    }),
  };
};

export const startEventGapProxy = async (todo13, controlOrigin) => {
  if (process.versions.bun) {
    return startEventGapProxySidecar(todo13, controlOrigin);
  }
  const dropSignals = [];
  const dropped = new Promise((resolve) => { dropSignals.push(resolve); });
  const diagnostics = {
    httpRequests: [], httpResponses: [], upgrades: 0, upgraded: false,
    frames: 0, textEvents: [], dropped: [], forwarded: [],
    upstreamErrors: 0, clientErrors: 0,
  };
  const certificate = await readFile(join(todo13.directory, 'identity/tls-cert.pem'));
  const key = await readFile(join(todo13.directory, 'identity/tls-key.pem'));
  const target = new URL(todo13.origin);
  const server = createSecureServer({ cert: certificate, key }, (request, response) => {
    connections.track(request.socket);
    diagnostics.httpRequests.push(`${request.method} ${request.url}`);
    const upstream = secureRequest({
      hostname: target.hostname,
      port: target.port,
      path: request.url,
      method: request.method,
      headers: {
        ...request.headers,
        host: target.host,
        ...(request.headers.origin ? { origin: controlOrigin } : {}),
      },
      rejectUnauthorized: false,
    }, (result) => {
      diagnostics.httpResponses.push(`${result.statusCode ?? 502} ${request.url}`);
      response.writeHead(result.statusCode ?? 502, result.headers);
      result.pipe(response);
    });
    upstream.on('socket', (upstreamSocket) => connections.track(upstreamSocket));
    upstream.on('error', () => {
      diagnostics.upstreamErrors++;
      if (!response.headersSent) response.writeHead(502).end();
      else response.destroy();
    });
    request.on('error', () => {
      diagnostics.clientErrors++;
      upstream.destroy();
    });
    request.pipe(upstream);
  });
  const connections = trackServerConnections(server);
  server.on('upgrade', (request, socket, head) => {
    diagnostics.upgrades++;
    connections.track(socket);
    const upstream = connectTls({
      host: target.hostname,
      port: Number(target.port),
      rejectUnauthorized: false,
    }, () => {
      const headers = Object.entries({
        ...request.headers,
        host: target.host,
        ...(request.headers.origin ? { origin: controlOrigin } : {}),
      })
        .map(([name, value]) => `${name}: ${Array.isArray(value) ? value.join(', ') : value}`)
        .join('\r\n');
      upstream.write(`${request.method} ${request.url} HTTP/1.1\r\n${headers}\r\n\r\n`);
      if (head.length > 0) upstream.write(head);
    });
    connections.track(upstream);
    socket.on('error', () => {
      diagnostics.clientErrors++;
      upstream.destroy();
    });
    upstream.on('error', () => {
      diagnostics.upstreamErrors++;
      socket.destroy();
    });
    socket.pipe(upstream);
    let buffer = Buffer.alloc(0);
    let upgraded = false;
    upstream.on('data', (chunk) => {
      const outbound = [];
      const flush = () => {
        if (outbound.length > 0) socket.write(Buffer.concat(outbound));
      };
      buffer = Buffer.concat([buffer, chunk]);
      if (!upgraded) {
        const end = buffer.indexOf('\r\n\r\n');
        if (end < 0) return;
        outbound.push(Buffer.from(buffer.subarray(0, end + 4)));
        buffer = buffer.subarray(end + 4);
        upgraded = true;
        diagnostics.upgraded = true;
      }
      while (buffer.length >= 2) {
        let payloadLength = buffer[1] & 0x7f;
        let headerLength = 2;
        if (payloadLength === 126) {
          if (buffer.length < 4) {
            flush();
            return;
          }
          payloadLength = buffer.readUInt16BE(2);
          headerLength = 4;
        } else if (payloadLength === 127) {
          if (buffer.length < 10) {
            flush();
            return;
          }
          const large = buffer.readBigUInt64BE(2);
          if (large > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error('WebSocket frame too large');
          payloadLength = Number(large);
          headerLength = 10;
        }
        const frameLength = headerLength + payloadLength;
        if (buffer.length < frameLength) {
          flush();
          return;
        }
        const frame = Buffer.from(buffer.subarray(0, frameLength));
        buffer = buffer.subarray(frameLength);
        const opcode = frame[0] & 0x0f;
        diagnostics.frames++;
        let discard = false;
        let event;
        if (opcode === 1) {
          try {
            event = JSON.parse(frame.subarray(headerLength).toString('utf8'));
            diagnostics.textEvents.push({
              type: event.type,
              sequence: event.sequence,
              resource: event.resource,
            });
            if (event.type === 'invalidation' && dropSignals.length > 0) {
              discard = true;
              const droppedEvent = { sequence: event.sequence, resource: event.resource };
              diagnostics.dropped.push(droppedEvent);
              dropSignals.shift()(droppedEvent);
            }
          } catch {}
        }
        if (!discard) {
          if (event?.type === 'invalidation') {
            diagnostics.forwarded.push({ sequence: event.sequence, resource: event.resource });
          }
          outbound.push(frame);
        }
      }
      flush();
    });
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new TypeError('event-gap proxy did not bind TCP');
  return {
    server,
    origin: `https://127.0.0.1:${address.port}`,
    fingerprint: todo13.fingerprint,
    close: connections.close,
    dropped,
    dropNextInvalidation: () => new Promise((resolve) => {
      dropSignals.push(resolve);
    }),
    diagnostics: () => structuredClone(diagnostics),
  };
};
