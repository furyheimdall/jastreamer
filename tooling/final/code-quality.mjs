import { execSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..', '..');

let ok = true;

try {
  execSync('go test -race ./internal/playback/... ./internal/catalog/... ./internal/curation/... -count=1', { cwd: resolve(root, 'apps/server'), stdio: 'pipe' });
  console.log('Go race gates: PASS');
} catch (e) {
  console.error('Go race gates: FAIL');
  ok = false;
}

try {
  execSync('bun test tooling/policy/tests/policy.test.ts', { cwd: root, stdio: 'pipe' });
  console.log('Policy tests: PASS');
} catch (e) {
  console.error('Policy tests: FAIL');
  ok = false;
}

if (ok) {
  console.log('F2 verdict: APPROVE');
  process.exit(0);
} else {
  console.error('F2 verdict: FAIL');
  process.exit(1);
}
