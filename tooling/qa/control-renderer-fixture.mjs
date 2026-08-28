import { randomBytes } from 'node:crypto';
import { connect as connectTls } from 'node:tls';

const encodeClientFrame = (opcode, payload = Buffer.alloc(0)) => {
  const body = Buffer.isBuffer(payload) ? payload : Buffer.from(payload);
  const mask = randomBytes(4);
  let header;
  if (body.length < 126) {
    header = Buffer.from([0x80 | opcode, 0x80 | body.length]);
  } else if (body.length <= 0xffff) {
    header = Buffer.alloc(4);
    header[0] = 0x80 | opcode;
    header[1] = 0x80 | 126;
    header.writeUInt16BE(body.length, 2);
  } else {
    throw new RangeError('Control Renderer fixture frame is too large');
  }
  const masked = Buffer.alloc(body.length);
  for (let index = 0; index < body.length; index++) {
    masked[index] = body[index] ^ mask[index % mask.length];
  }
  return Buffer.concat([header, mask, masked]);
};

const parseServerFrames = (buffer, accept) => {
  let remaining = buffer;
  while (remaining.length >= 2) {
    const opcode = remaining[0] & 0x0f;
    let length = remaining[1] & 0x7f;
    let header = 2;
    if (length === 126) {
      if (remaining.length < 4) break;
      length = remaining.readUInt16BE(2);
      header = 4;
    } else if (length === 127) {
      if (remaining.length < 10) break;
      const large = remaining.readBigUInt64BE(2);
      if (large > BigInt(Number.MAX_SAFE_INTEGER)) {
        throw new RangeError('Control Renderer fixture received an oversized frame');
      }
      length = Number(large);
      header = 10;
    }
    if (remaining.length < header + length) break;
    accept(opcode, remaining.subarray(header, header + length));
    remaining = remaining.subarray(header + length);
  }
  return remaining;
};

export const startControlRendererFixture = async (server, credential) => {
  const target = new URL(server.origin);
  const socket = connectTls({
    host: target.hostname,
    port: Number(target.port),
    rejectUnauthorized: false,
  });
  await new Promise((resolve, reject) => {
    socket.once('secureConnect', resolve);
    socket.once('error', reject);
  });
  const peer = socket.getPeerCertificate();
  const fingerprint = peer.fingerprint256?.replaceAll(':', '').toLowerCase();
  if (!fingerprint || fingerprint !== server.fingerprint.toLowerCase()) {
    socket.destroy();
    throw new Error('Control Renderer fixture rejected the Server fingerprint');
  }

  const key = randomBytes(16).toString('base64');
  socket.write(
    `GET /api/v1/renderers/${encodeURIComponent(credential.device.id)}/session HTTP/1.1\r\n` +
    `Host: ${target.host}\r\n` +
    `Authorization: Bearer ${credential.token}\r\n` +
    'Connection: Upgrade\r\n' +
    'Upgrade: websocket\r\n' +
    'Sec-WebSocket-Version: 13\r\n' +
    `Sec-WebSocket-Key: ${key}\r\n` +
    'Sec-WebSocket-Protocol: jastreamer.renderer.v3\r\n\r\n',
  );

  let buffer = Buffer.alloc(0);
  const headers = await new Promise((resolve, reject) => {
    const receive = (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      const end = buffer.indexOf('\r\n\r\n');
      if (end < 0) return;
      socket.off('data', receive);
      const value = buffer.subarray(0, end + 4).toString('utf8');
      buffer = buffer.subarray(end + 4);
      resolve(value);
    };
    socket.on('data', receive);
    socket.once('error', reject);
  });
  if (!headers.startsWith('HTTP/1.1 101 ') ||
      !/Sec-WebSocket-Protocol:\s*jastreamer\.renderer\.v3/i.test(headers)) {
    socket.destroy();
    throw new Error(`Control Renderer fixture upgrade failed: ${headers.split('\r\n')[0]}`);
  }

  const sendJSON = (value) => socket.write(encodeClientFrame(1, JSON.stringify(value)));
  const hello = {
    protocolMajor: 3,
    type: 'hello',
    rendererId: credential.device.id,
    supportedMajors: [3, 2],
    capabilities: {
      commands: ['play', 'pause', 'resume', 'stop', 'seek'],
      mediaTypes: ['audio/mpeg', 'audio/flac'],
      supportsRange: true,
      maxChannels: 2,
      maxSampleRateHz: 192000,
    },
    lastServerSequence: 0,
    pendingResults: [],
  };

  let resultSequence = 0;
  let observedState = 'idle';
  const records = [];
  const resultWaiters = [];
  let welcomeResolve;
  let welcomeReject;
  const ready = new Promise((resolve, reject) => {
    welcomeResolve = resolve;
    welcomeReject = reject;
  });
  const accept = (opcode, payload) => {
    if (opcode === 9) {
      socket.write(encodeClientFrame(10, payload));
      return;
    }
    if (opcode === 8) {
      socket.end(encodeClientFrame(8, payload));
      return;
    }
    if (opcode !== 1) return;
    const message = JSON.parse(payload.toString('utf8'));
    records.push({ type: message.type, kind: message.kind, code: message.code });
    if (message.type === 'welcome') {
      welcomeResolve(message);
      return;
    }
    if (message.type === 'result.ack') {
      resultWaiters.shift()?.(message);
      return;
    }
    if (message.type !== 'command') return;
    sendJSON({
      protocolMajor: 3,
      type: 'command.ack',
      commandId: message.commandId,
      sequence: message.sequence,
      status: 'received',
      error: null,
    });
    observedState = switchObservedState(message.kind, observedState);
    resultSequence++;
    sendJSON({
      protocolMajor: 3,
      type: 'command.result',
      commandId: message.commandId,
      resultId: `qa-result-${resultSequence}`,
      status: 'succeeded',
      observedState,
      positionMs: message.positionMs ?? 0,
      error: null,
    });
  };
  const receiveFrames = (chunk) => {
    try {
      buffer = parseServerFrames(Buffer.concat([buffer, chunk]), accept);
    } catch (error) {
      welcomeReject(error);
      socket.destroy(error);
    }
  };
  socket.on('data', receiveFrames);
  if (buffer.length > 0) {
    const initialFrames = buffer;
    buffer = Buffer.alloc(0);
    receiveFrames(initialFrames);
  }
  socket.once('error', welcomeReject);
  sendJSON(hello);
  const welcome = await ready;
  if (!welcome.sessionEpoch || welcome.selectedMajor !== 3) {
    socket.destroy();
    throw new Error('Control Renderer fixture received an invalid welcome');
  }
  return {
    ready: welcome,
    records,
    nextResult: () => new Promise((resolve) => { resultWaiters.push(resolve); }),
    wake: () => socket.write(encodeClientFrame(9, Buffer.from('dispatch'))),
    close: async () => {
      if (socket.destroyed) return;
      const closed = new Promise((resolve) => socket.once('close', resolve));
      socket.end(encodeClientFrame(8, Buffer.from([0x03, 0xe8])));
      await closed;
    },
  };
};

const switchObservedState = (kind, current) => {
  switch (kind) {
    case 'play': return 'playing';
    case 'pause': return 'paused';
    case 'resume': return 'playing';
    case 'stop': return 'stopped';
    case 'seek':
    default: return current;
  }
};
