import { packager } from '@electron/packager';
import { createHash } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { access, copyFile, mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { pipeline } from 'node:stream/promises';
import yazl from 'yazl';

const root = fileURLToPath(new URL('..', import.meta.url));
const output = path.join(root, 'dist');
const name = 'jastreamer-desktop';
const target = path.join(output, `${name}-win32-x64`);
const metadata = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8'));
try {
  await access(path.join(target, 'user-data'));
  throw new Error('Portable user-data exists in the build output. Move the entire used application folder before rebuilding; it will not be erased.');
} catch (error) {
  if (error.code !== 'ENOENT') throw error;
}
await mkdir(output, { recursive: true });
const [directory] = await packager({
  dir: root,
  out: output,
  name,
  executableName: name,
  platform: 'win32',
  arch: 'x64',
  electronVersion: metadata.devDependencies.electron,
  appVersion: metadata.version,
  appCopyright: 'Copyright jastreamer contributors',
  win32metadata: { CompanyName: 'jastreamer contributors', FileDescription: 'jastreamer desktop', ProductName: 'jastreamer' },
  asar: true,
  prune: true,
  overwrite: true,
  ignore: [
    /^\/(?:dist|scripts|tests?|\.user-data|user-data)(?:\/|$)/,
    /^\/.*\.(?:test|spec)\.[cm]?js$/,
    /^\/(?:\.env(?:\..*)?|package-lock\.json)$/,
  ],
});
await copyFile(path.resolve(root, '../../LICENSE'), path.join(directory, 'LICENSE.jastreamer'));
await writeFile(path.join(directory, 'START-HERE.txt'), [
  'jastreamer Windows portable desktop',
  '',
  'Extract the complete ZIP into a writable local folder, then run jastreamer-desktop.exe.',
  'Do not run inside the ZIP or move only the EXE. Windows 10/11 x64 is required.',
  'Select a discovered server or enter its HTTP(S) root address. Discovery requires LAN multicast UDP 5353.',
  'HTTP is only for a trusted private LAN; it does not encrypt credentials or traffic.',
  'Recent servers and OS-protected Chromium sessions are stored beside the EXE in user-data.',
  'The folder must remain writable. No AppData fallback or administrator launch is required.',
  'Saved sessions are not guaranteed to move across Windows accounts or machines; sign in again there.',
  'Closing this app or changing servers does not stop playback on a server.',
  'This private build is unsigned. No public release or production qualification is implied.',
  '',
].join('\r\n'));
async function* files(dir, prefix = '') {
  for (const entry of (await readdir(dir, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name, 'en'))) {
    const relative = prefix + entry.name;
    if (entry.isDirectory()) yield* files(path.join(dir, entry.name), relative + '/');
    else if (entry.isFile()) yield relative;
    else throw new Error(`Unexpected non-file entry: ${relative}`);
  }
}
async function digest(file) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest('hex');
}
const inventory = [];
const zip = new yazl.ZipFile();
const archiveName = `${name}_${metadata.version}_windows-x64.zip`;
const archivePath = path.join(output, archiveName);
const finished = pipeline(zip.outputStream, createWriteStream(archivePath));
zip.on('error', (error) => zip.outputStream.destroy(error));
try {
  for await (const relative of files(directory)) {
    const file = path.join(directory, relative);
    inventory.push({ path: relative, bytes: (await stat(file)).size, sha256: await digest(file) });
    zip.addFile(file, `${name}/${relative}`);
  }
  zip.end();
  await finished;
} catch (error) {
  zip.outputStream.destroy(error);
  await finished.catch(() => {});
  throw error;
}
const manifest = {
  product: 'jastreamer-desktop', version: metadata.version, platform: 'win32', arch: 'x64',
  electron: metadata.devDependencies.electron,
  sourceRevision: process.env.JASTREAMER_SOURCE_REVISION || 'worktree',
  signed: false, productionQualified: false,
  directory: path.basename(directory),
  archive: { path: archiveName, bytes: (await stat(archivePath)).size, sha256: await digest(archivePath) },
  files: inventory,
};
await writeFile(path.join(output, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
await writeFile(archivePath + '.sha256', `${manifest.archive.sha256}  ${archiveName}\n`);
console.log(JSON.stringify({ archive: archivePath, sha256: manifest.archive.sha256, files: inventory.length, signed: false }, null, 2));
