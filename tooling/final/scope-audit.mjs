import { execSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..', '..');

let ok = true;

try {
  execSync('git status --short', { cwd: root, stdio: 'pipe' });
  console.log('Git status: clean');
} catch (e) {
  console.error('Git status: dirty (uncommitted changes)');
  ok = false;
}

if (ok) {
  console.log('F4 verdict: APPROVE (scope clean)');
  process.exit(0);
} else {
  console.error('F4 verdict: FAIL');
  process.exit(1);
}
