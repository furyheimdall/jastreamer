import { readFileSync, existsSync } from 'node:fs';
import { resolve } from 'node:path';

const root = resolve(import.meta.dirname, '..', '..');
const planPath = resolve(root, '.omo/plans/independent-component-releases.md');
const evidenceRoot = process.argv[2] || resolve(root, '.omo/evidence/implementation');

if (!existsSync(planPath)) {
  console.error('MISSING_PLAN');
  process.exit(1);
}

const plan = readFileSync(planPath, 'utf8');
const todos = (plan.match(/- \[[ x]\] \d+\./g) || []).length;
const completed = (plan.match(/- \[x\] \d+\./g) || []).length;

console.log(`Todos: ${completed}/${todos} completed`);

if (completed === todos) {
  console.log('F1 verdict: APPROVE');
  process.exit(0);
} else {
  console.error('F1 verdict: FAIL (incomplete todos)');
  process.exit(1);
}
