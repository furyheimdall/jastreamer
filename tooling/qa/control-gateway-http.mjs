import { request as secureRequest } from 'node:https';

export const deadline = (promise, label, milliseconds = 30_000) => {
  let timer;
  return Promise.race([
    promise,
    new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error(`${label} timed out`)), milliseconds);
    }),
  ]).finally(() => clearTimeout(timer));
};

export const requestJSON = async (origin, method, path, token = '', body, extraHeaders = {}) => {
  const target = new URL(path, origin);
  const payload = body === undefined ? undefined : JSON.stringify(body);
  const response = await new Promise((resolveResponse, reject) => {
    const request = secureRequest({
      hostname: target.hostname,
      port: target.port,
      path: `${target.pathname}${target.search}`,
      method,
      rejectUnauthorized: false,
      headers: {
        accept: 'application/json',
        ...(token ? { authorization: `Bearer ${token}` } : {}),
        ...(payload ? {
          'content-type': 'application/json',
          'content-length': Buffer.byteLength(payload),
        } : {}),
        ...extraHeaders,
      },
    }, resolveResponse);
    request.on('error', reject);
    if (payload) request.end(payload); else request.end();
  });
  let text = '';
  for await (const chunk of response) text += chunk;
  const decoded = text ? JSON.parse(text) : null;
  return { status: response.statusCode, headers: response.headers, body: decoded };
};

export const requireStatus = (response, expected, action) => {
  if (response.status !== expected) {
    throw new Error(`${action} returned ${response.status}: ${JSON.stringify(response.body)}`);
  }
  return response.body;
};

export const pairRole = async (server, adminToken, role, name) => {
  const generated = requireStatus(
    await requestJSON(server.origin, 'POST', '/api/v1/pairing-codes', adminToken, { role }),
    201,
    `generate ${role} pairing code`,
  );
  return requireStatus(
    await requestJSON(server.origin, 'POST', '/api/v1/pairings', '', { code: generated.code, name }),
    201,
    `pair ${role}`,
  );
};
