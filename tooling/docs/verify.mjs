import { readFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..', '..');
const sources = [
  'apps/server/component.yaml',
  'apps/control/component.yaml',
  'apps/renderer/component.yaml',
  'packaging/server/manifest.json',
  'packaging/control/manifest.json',
  'packaging/renderer/config.json'
];
const guides = [
  'docs/synology.md',
  'docs/server-pairing.md',
  'docs/control-windows.md',
  'docs/control-web.md',
  'docs/control-android.md',
  'docs/renderer-windows.md',
  'docs/releasing.md'
];

let ok = true;
for (const s of [...sources, ...guides]) {
  if (!existsSync(resolve(root, s))) {
    console.error(`MISSING_SOURCE: ${s}`);
    ok = false;
  }
}

const readme = readFileSync(resolve(root, 'README.md'), 'utf8');
if (!readme.includes('jastreamer-server') || !readme.includes('jastreamer-control') || !readme.includes('jastreamer-renderer')) {
  console.error('README.md missing product references');
  ok = false;
}
for (const guide of guides) {
  if (!readme.includes(`(${guide})`)) {
    console.error(`README.md missing guide link: ${guide}`);
    ok = false;
  }
}

if (ok) {
  console.log('Documentation verification PASSED');
  process.exit(0);
} else {
  console.error('Documentation verification FAILED');
  process.exit(1);
}
