import { existsSync, readFileSync, readdirSync } from 'node:fs';
import { join, relative } from 'node:path';

const root = process.cwd();
const configuration = JSON.parse(readFileSync(join(root, 'tooling/boundaries.json'), 'utf8'));
const components = Object.entries(configuration.components);
const failures = [];
const sourceExtensions = new Set(['.go', '.dart', '.rs', '.yaml', '.yml', '.toml', '.json']);

function walk(directory) {
  if (!existsSync(directory)) return [];
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? walk(path) : [path];
  });
}

for (const [name, componentRoot] of components) {
  const manifest = join(root, componentRoot, 'component.yaml');
  if (!existsSync(manifest)) failures.push(`${name}: missing ${relative(root, manifest)}`);
  for (const file of walk(join(root, componentRoot))) {
    const extension = file.slice(file.lastIndexOf('.'));
    if (!sourceExtensions.has(extension)) continue;
    const text = readFileSync(file, 'utf8');
    for (const [otherName, otherRoot] of components) {
      if (otherName === name) continue;
      if (text.includes(otherRoot)) failures.push(`${relative(root, file)} references ${otherRoot}`);
    }
  }
}

if (failures.length > 0) {
  console.error('Boundary verification failed');
  failures.forEach((failure) => console.error(`- ${failure}`));
  process.exitCode = 1;
} else {
  console.log(`Boundary verification passed for ${components.length} components`);
}
