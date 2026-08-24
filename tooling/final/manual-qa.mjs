import { execSync } from 'node:child_process';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..', '..');

let ok = true;

try {
  execSync('node --test tooling/qa/check-control-contract.test.mjs', { cwd: root, stdio: 'pipe' });
  console.log('Control contract tests: PASS');
} catch (e) {
  console.error('Control contract tests: FAIL');
  ok = false;
}

if (ok) {
  console.log('F3 verdict: APPROVE (core surfaces pass)');
  process.exit(0);
} else {
  console.error('F3 verdict: FAIL');
  process.exit(1);
}
