import { execFileSync } from 'node:child_process';
import { resolve } from 'node:path';
import { isGitStatusClean } from './scope-audit-status.mjs';

const root = resolve(import.meta.dirname, '..', '..');

let ok = true;

try {
  const status = execFileSync('git', ['status', '--short'], {
    cwd: root,
    encoding: 'utf8',
  });
  ok = isGitStatusClean(status);
  if (ok) console.log('Git status: clean');
  else console.error('Git status: dirty (uncommitted changes)');
} catch (e) {
  console.error('Git status: unavailable');
  ok = false;
}

if (ok) {
  console.log('F4 verdict: APPROVE (scope clean)');
  process.exit(0);
} else {
  console.error('F4 verdict: FAIL');
  process.exit(1);
}
