import { createServer as createSecureServer } from 'node:https';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';

export const startEdgeApi = async (todo13, controlOrigin) => {
  const certificate = await readFile(join(todo13.directory, 'identity/tls-cert.pem'));
  const key = await readFile(join(todo13.directory, 'identity/tls-key.pem'));
  let revision = 0;
  let mode = 'stop';
  let catalogReads = 0;
  const decision = (reason = 'BLOCK_EXPLICIT') => ({
    decision_id: 'edge-decision', kind: reason === 'BLOCK_EXPLICIT' ? 'block' : 'stop',
    reason, source: reason === 'BLOCK_EXPLICIT' ? 'explicit' : '', track_id: 'unavailable-edge',
    algorithm_revision: 'policy-v1', catalog_revision: 7, policy_revision: revision,
    contract_revision: 'http-api-v1', signal_coverage: 61,
  });
  const server = createSecureServer({ cert: certificate, key }, async (request, response) => {
    const headers = { 'content-type': 'application/json', 'access-control-allow-origin': controlOrigin, vary: 'Origin' };
    if (request.method === 'OPTIONS') {
      response.writeHead(204, { ...headers, 'access-control-allow-methods': 'GET,PATCH,OPTIONS', 'access-control-allow-headers': 'authorization,content-type,if-match,x-jake-protocol-major,x-jake-supported-protocol-majors' }).end();
      return;
    }
    const path = new URL(request.url ?? '/', 'https://edge.local').pathname;
    if (path.startsWith('/api/v1/') && path !== '/api/v1/identity' && request.headers.authorization === 'Bearer rejected-token') {
      response.writeHead(401, headers).end(JSON.stringify({ code: 'UNAUTHORIZED' })); return;
    }
    if (path === '/pair/') { response.writeHead(302, { location: `${todo13.origin}/pair/` }).end(); return; }
    let status = 200;
    let body;
    if (path === '/api/v1/identity') body = { common_name: 'jastreamer Edge Fixture', sha256_fingerprint: todo13.fingerprint, pairing_url: '/pair/' };
    else if (path === '/api/v1/discovery') body = {
      protocol_major: Number(request.headers['x-jake-protocol-major']),
      supported_protocol_majors: [2, 1],
      capabilities: ['catalog-status', 'queue', 'continuation-policy', 'automatic-preview', 'decision-explanation'],
      pairing_url: '/pair/',
      certificate_sha256: todo13.fingerprint,
      contract_revision: 'http-api-v1',
      algorithm_revision: 'policy-v1',
      analysis_revision: 1,
      catalog_revision: 7,
    };
    else if (path.endsWith('/continuation-policy') && request.method === 'GET') body = { mode, artist_gap: 4, album_gap: 10, session_override: '', revision };
    else if (path.endsWith('/continuation-policy') && request.method === 'PATCH') {
      if (Number(request.headers['if-match']) !== revision) { status = 412; body = { code: 'STALE_POLICY_REVISION' }; }
      else { let raw = ''; for await (const chunk of request) raw += chunk; mode = JSON.parse(raw).mode; revision++; body = { mode, artist_gap: 4, album_gap: 10, session_override: '', revision }; }
    } else if (path === '/api/v1/catalog/status') { catalogReads++; body = { scan_status: 'ready', catalog_revision: 7, track_count: 100, analysis_complete: 61, analysis_queued: 32, analysis_failed: 7, analysis_coverage: 61, analysis_revision: 1 }; }
    else if (path.endsWith('/queue')) body = { zone_id: 'main', revision, transport: 'idle', queue: catalogReads > 1 ? [] : [{ entry_id: 'edge-1', track_id: 'unavailable-edge', state: 'blocked', position: 1 }] };
    else if (path.endsWith('/automatic-preview')) body = catalogReads > 1 ? { active: true, replaceable: false, committed: true, decision: decision('STOP_SIMILAR_NO_SIGNAL') } : { active: false, replaceable: true, committed: false, decision: decision() };
    else if (path.endsWith('/decision-explanation')) body = catalogReads > 1 ? decision('STOP_SIMILAR_NO_SIGNAL') : decision();
    else { status = 404; body = { code: 'NOT_FOUND' }; }
    response.writeHead(status, headers).end(JSON.stringify(body));
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  if (!address || typeof address === 'string') throw new TypeError('edge API did not bind TCP');
  return { server, origin: `https://127.0.0.1:${address.port}`, fingerprint: todo13.fingerprint };
};
