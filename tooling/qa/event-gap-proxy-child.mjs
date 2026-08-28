import { createInterface } from 'node:readline';
import { startEventGapProxy } from './control-servers.mjs';

const [origin, directory, fingerprint, controlOrigin = ''] = process.argv.slice(2);
if (!origin || !directory || !fingerprint) {
  throw new Error('event-gap proxy child requires origin, directory, and fingerprint');
}

const proxy = await startEventGapProxy(
  { origin, directory, fingerprint },
  controlOrigin || undefined,
);
let dropIndex = 0;
const observeDrop = (promise) => {
  const index = dropIndex++;
  void promise.then((event) => {
    process.stdout.write(`${JSON.stringify({ type: 'drop', index, event })}\n`);
  });
};
observeDrop(proxy.dropped);
process.stdout.write(`${JSON.stringify({ type: 'ready', origin: proxy.origin })}\n`);

const input = createInterface({ input: process.stdin });
input.on('line', (line) => {
  if (line === 'drop') observeDrop(proxy.dropNextInvalidation());
});

let closing = false;
const close = async () => {
  if (closing) return;
  closing = true;
  input.close();
  await proxy.close();
  process.exit(0);
};
process.on('SIGTERM', () => { void close(); });
process.on('SIGINT', () => { void close(); });
