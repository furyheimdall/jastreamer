import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { open, readFile, readdir, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const output = fileURLToPath(new URL('../dist/', import.meta.url));
const manifest = JSON.parse(await readFile(path.join(output, 'manifest.json'), 'utf8'));
assert.equal(manifest.product, 'jastreamer-desktop');
assert.equal(manifest.platform, 'win32');
assert.equal(manifest.arch, 'x64');
function child(root, relative) {
  assert.equal(typeof relative, 'string');
  assert(relative && !relative.includes('\\') && !path.isAbsolute(relative));
  assert(relative.split('/').every((part) => part && part !== '.' && part !== '..'));
  return path.join(root, relative);
}
async function digest(file) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest('hex');
}
const archive = child(output, manifest.archive.path);
assert.equal((await stat(archive)).size, manifest.archive.bytes);
assert.equal(await digest(archive), manifest.archive.sha256, 'ZIP SHA-256 mismatch');
const directory = child(output, manifest.directory);
const expected = new Set();
for (const entry of manifest.files) {
  assert(!expected.has(entry.path), 'Duplicate artifact path');
  assert(!/(^|\/)(?:user-data|\.user-data|recents\.json)(\/|$)/i.test(entry.path), 'Private runtime data in artifact');
  expected.add(entry.path);
  const file = child(directory, entry.path);
  assert.equal((await stat(file)).size, entry.bytes, entry.path);
  assert.equal(await digest(file), entry.sha256, entry.path);
}
async function inventory(dir, prefix = '') {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const relative = prefix + entry.name;
    if (entry.isDirectory()) await inventory(path.join(dir, entry.name), relative + '/');
    else { assert(entry.isFile()); assert(expected.delete(relative), `Unlisted file: ${relative}`); }
  }
}
await inventory(directory);
assert.equal(expected.size, 0, 'Missing artifact files');
for (const required of ['jastreamer-desktop.exe', 'resources/app.asar', 'LICENSE.jastreamer', 'START-HERE.txt']) {
  assert(manifest.files.some((entry) => entry.path === required), `Missing ${required}`);
}
const executable = await open(path.join(directory, 'jastreamer-desktop.exe'));
try {
  const header = Buffer.alloc(64);
  await executable.read(header, 0, header.length, 0);
  assert.equal(header.toString('ascii', 0, 2), 'MZ');
  const pe = Buffer.alloc(26);
  await executable.read(pe, 0, pe.length, header.readUInt32LE(60));
  assert.equal(pe.toString('ascii', 0, 4), 'PE\0\0');
  assert.equal(pe.readUInt16LE(4), 0x8664, 'Executable must target Windows x64');
  assert.equal(pe.readUInt16LE(24), 0x20b, 'Executable must be PE32+');
} finally { await executable.close(); }
console.log(`Verified Windows x64 portable ZIP and ${manifest.files.length} exact packaged files. Windows execution and signing are separate checks.`);
