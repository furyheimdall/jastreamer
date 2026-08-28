import { readFile } from 'node:fs/promises';
import { createServer as createSecureServer, request as secureRequest } from 'node:https';
import { join } from 'node:path';
import { trackServerConnections } from './control-servers.mjs';

export const startUnknownEnumProxy = async (server) => {
  const [certificate, key] = await Promise.all([
    readFile(join(server.directory, 'identity/tls-cert.pem')),
    readFile(join(server.directory, 'identity/tls-key.pem')),
  ]);
  const target = new URL(server.origin);
  let mutations = 0;
  const proxy = createSecureServer({ cert: certificate, key }, (request, response) => {
    connections.track(request.socket);
    if (request.method !== 'GET') mutations++;
    const upstream = secureRequest({
      hostname: target.hostname,
      port: target.port,
      path: request.url,
      method: request.method,
      headers: { ...request.headers, host: target.host },
      rejectUnauthorized: false,
    }, async (result) => {
      const chunks = [];
      for await (const chunk of result) chunks.push(chunk);
      let body = Buffer.concat(chunks);
      if (request.url?.startsWith('/api/v1/zones') && result.statusCode === 200) {
        const inventory = JSON.parse(body.toString('utf8'));
        inventory.zones[0].transport = 'future-logical';
        inventory.renderers[0].kind = 'future-renderer';
        inventory.renderers[0].status = 'future-status';
        body = Buffer.from(JSON.stringify(inventory));
      }
      response.writeHead(result.statusCode ?? 502, {
        ...result.headers,
        'content-length': body.length,
      });
      response.end(body);
    });
    upstream.on('error', () => {
      if (!response.headersSent) response.writeHead(502).end();
      else response.destroy();
    });
    upstream.on('socket', (socket) => connections.track(socket));
    request.on('error', () => upstream.destroy());
    request.pipe(upstream);
  });
  const connections = trackServerConnections(proxy);
  await new Promise((resolveListen) => proxy.listen(0, '127.0.0.1', resolveListen));
  const address = proxy.address();
  if (!address || typeof address === 'string') throw new Error('unknown-enum proxy did not bind');
  return {
    server: proxy,
    origin: `https://127.0.0.1:${address.port}`,
    fingerprint: server.fingerprint,
    mutationCount: () => mutations,
    close: connections.close,
  };
};
