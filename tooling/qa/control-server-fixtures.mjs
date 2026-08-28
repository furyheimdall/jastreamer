import { spawn, spawnSync } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';

export const markFixtureRendererConnected = (server, rendererID) => {
  const database = join(server.directory, 'playback.sqlite');
  const escapedID = rendererID.replaceAll("'", "''");
  const statement = `UPDATE renderer_registry SET state='connected',revision=revision+1
WHERE renderer_id='${escapedID}';`;
  const result = spawnSync('sqlite3', [database, statement], { encoding: 'utf8' });
  if (result.status !== 0) {
    throw new Error(`connected Renderer fixture cutpoint failed: ${result.stderr}`);
  }
};

export const markFixtureQueueHeadBlocked = (server) => {
  const database = join(server.directory, 'playback.sqlite');
  const statement = `BEGIN IMMEDIATE;
UPDATE playback_queue SET state='blocked'
WHERE entry_id=(SELECT entry_id FROM playback_queue
  WHERE zone_id='main' AND state='pending' ORDER BY position LIMIT 1);
UPDATE playback_zones SET revision=revision+1 WHERE zone_id='main';
COMMIT;`;
  const result = spawnSync('sqlite3', [database, statement], { encoding: 'utf8' });
  if (result.status !== 0) {
    throw new Error(`blocked queue fixture cutpoint failed: ${result.stderr}`);
  }
};

export const startFixtureRenderer = async (serverRoot, server, credential) => {
  const rendererRoot = join(serverRoot, '../renderer');
  const rendererBinary = join(rendererRoot, 'target/debug/jastreamer-renderer');
  const available = spawnSync('test', ['-x', rendererBinary]);
  if (available.status !== 0) {
    const built = spawnSync('cargo', ['build', '--quiet'], { cwd: rendererRoot, encoding: 'utf8' });
    if (built.status !== 0) throw new Error(`Renderer build failed: ${built.stderr}`);
  }
  const stateDirectory = join(server.directory, 'renderer-state');
  await mkdir(stateDirectory, { recursive: true });
  const child = spawn(rendererBinary, [
    '--server-origin', `${server.origin}/`,
    '--server-fingerprint', server.rendererFingerprint ?? server.fingerprint,
    '--renderer-id', credential.device.id,
    '--output-device', 'fixture',
    '--share-mode', 'shared',
    '--state-directory', stateDirectory,
    '--token-stdin',
  ], {
    cwd: rendererRoot,
    env: {
      ...process.env,
      SSL_CERT_FILE: join(server.directory, 'identity/tls-cert.pem'),
    },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  let stderr = '';
  child.stderr.on('data', (chunk) => {
    const message = chunk.toString();
    stderr += message;
    console.error(`Fixture Renderer: ${message.trim()}`);
  });
  child.stdin.end(`${credential.token}\n`);
  child.once('exit', (code) => {
    if (code !== 0 && code !== null) console.error(`Fixture Renderer exited ${code}: ${stderr}`);
  });
  return child;
};
