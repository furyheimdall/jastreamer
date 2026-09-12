import { packager } from "@electron/packager";
import { createHash } from "node:crypto";
import { execFile as execFileCallback } from "node:child_process";
import { createReadStream } from "node:fs";
import {
  chmod,
  copyFile,
  cp,
  lstat,
  mkdir,
  readFile,
  readdir,
  readlink,
  rm,
  stat,
  writeFile,
} from "node:fs/promises";
import path from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const execFile = promisify(execFileCallback);
const root = fileURLToPath(new URL("..", import.meta.url));
const output = path.join(root, "dist");
const name = "jastreamer-desktop";
const applicationDirectory = `/usr/lib/${name}`;
const metadata = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
const packageDirectory = path.join(output, `${name}-deb-amd64`);
const archiveName = `${name}_${metadata.version}_linux-amd64.deb`;
const archivePath = path.join(output, archiveName);

async function digest(file) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest("hex");
}

async function normalizePermissions(directory) {
  await chmod(directory, 0o755);
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      await chmod(file, 0o755);
      await normalizePermissions(file);
    } else if (entry.isFile()) {
      const mode = (await stat(file)).mode;
      await chmod(file, mode & 0o111 ? 0o755 : 0o644);
    } else if (!entry.isSymbolicLink()) {
      throw new Error(`Unexpected package entry: ${file}`);
    }
  }
}

async function* files(directory, prefix = "") {
  for (const entry of (await readdir(directory, { withFileTypes: true })).sort((left, right) => left.name.localeCompare(right.name, "en"))) {
    const relative = prefix + entry.name;
    const file = path.join(directory, entry.name);
    if (entry.isDirectory()) {
      yield* files(file, `${relative}/`);
    } else if (entry.isFile()) {
      const details = await lstat(file);
      yield {
        path: relative,
        type: "file",
        bytes: details.size,
        sha256: await digest(file),
        mode: (details.mode & 0o7777).toString(8).padStart(4, "0"),
        uid: 0,
        gid: 0,
      };
    } else if (entry.isSymbolicLink()) {
      const details = await lstat(file);
      yield {
        path: relative,
        type: "symlink",
        target: await readlink(file),
        mode: (details.mode & 0o7777).toString(8).padStart(4, "0"),
        uid: 0,
        gid: 0,
      };
    } else {
      throw new Error(`Unexpected package entry: ${relative}`);
    }
  }
}

await mkdir(output, { recursive: true });
await rm(packageDirectory, { recursive: true, force: true });
await rm(archivePath, { force: true });

const [packagedDirectory] = await packager({
  dir: root,
  out: output,
  name,
  executableName: name,
  icon: path.join(root, "assets/jastreamer.png"),
  platform: "linux",
  arch: "x64",
  electronVersion: metadata.devDependencies.electron,
  appVersion: metadata.version,
  appCopyright: "Copyright jastreamer contributors",
  asar: true,
  prune: true,
  overwrite: true,
  ignore: [
    /^\/(?:dist|scripts|tests?|\.user-data|user-data)(?:\/|$)/,
    /^\/.*\.(?:test|spec)\.[cm]?js$/,
    /^\/(?:\.env(?:\..*)?|package-lock\.json)$/,
  ],
});

const dataRoot = path.join(packageDirectory, "usr");
const installedApplication = path.join(dataRoot, "lib", name);
const desktopEntry = path.join(dataRoot, "share", "applications", `${name}.desktop`);
const installedIcon = path.join(dataRoot, "share", "icons", "hicolor", "512x512", "apps", `${name}.png`);
const policyDirectory = path.join(dataRoot, "share", name, "apparmor");
const policySource = path.join(policyDirectory, name);
const controlDirectory = path.join(packageDirectory, "DEBIAN");

await mkdir(path.dirname(installedApplication), { recursive: true });
await cp(packagedDirectory, installedApplication, { recursive: true, verbatimSymlinks: true });
await copyFile(path.resolve(root, "../../LICENSE"), path.join(installedApplication, "LICENSE.jastreamer"));
await mkdir(path.dirname(desktopEntry), { recursive: true });
await writeFile(desktopEntry, [
  "[Desktop Entry]",
  "Type=Application",
  "Name=JASTREAMER",
  "Comment=Connect to jastreamer servers",
  `Exec=${applicationDirectory}/${name}`,
  `Icon=${name}`,
  "Terminal=false",
  "Categories=AudioVideo;Audio;Network;",
  `StartupWMClass=${name}`,
  "",
].join("\n"));
await mkdir(path.dirname(installedIcon), { recursive: true });
await copyFile(path.join(root, "assets", "jastreamer.png"), installedIcon);
await mkdir(policyDirectory, { recursive: true });
await writeFile(policySource, [
  "# Managed by the jastreamer-desktop package; use /etc/apparmor.d/local/jastreamer-desktop for local additions.",
  "abi <abi/4.0>,",
  "include <tunables/global>",
  "",
  `${applicationDirectory}/${name} flags=(unconfined) {`,
  "  userns,",
  "  include if exists <local/jastreamer-desktop>",
  "}",
  "",
].join("\n"));

await normalizePermissions(dataRoot);
await chmod(path.join(installedApplication, name), 0o755);
await chmod(path.join(installedApplication, "chrome-sandbox"), 0o4755);
await mkdir(controlDirectory, { recursive: true });

let installedBytes = 0;
for await (const entry of files(dataRoot)) {
  if (entry.type === "file") installedBytes += entry.bytes;
}
const dependencies = [
  "apparmor",
  "libasound2t64 | libasound2",
  "libatk-bridge2.0-0t64 | libatk-bridge2.0-0",
  "libatk1.0-0t64 | libatk1.0-0",
  "libatspi2.0-0t64 | libatspi2.0-0",
  "libc6 (>= 2.35)",
  "libcairo2",
  "libcups2t64 | libcups2",
  "libdbus-1-3",
  "libdrm2",
  "libexpat1",
  "libgbm1",
  "libglib2.0-0t64 | libglib2.0-0",
  "libgtk-3-0t64 | libgtk-3-0",
  "libnspr4",
  "libnotify4",
  "libnss3",
  "libsecret-1-0",
  "libpango-1.0-0",
  "libudev1",
  "libx11-6",
  "libx11-xcb1",
  "libxcb1",
  "libxcomposite1",
  "libxdamage1",
  "libxext6",
  "libxfixes3",
  "libxkbcommon0",
  "libuuid1",
  "libxrandr2",
  "libxss1",
  "libxtst6",
  "hicolor-icon-theme",
  "xdg-utils",
];
await writeFile(path.join(controlDirectory, "control"), [
  `Package: ${name}`,
  `Version: ${metadata.version}`,
  "Architecture: amd64",
  "Section: sound",
  "Priority: optional",
  `Installed-Size: ${Math.ceil(installedBytes / 1024)}`,
  `Depends: ${dependencies.join(", ")}`,
  "Maintainer: jastreamer contributors",
  "Description: Desktop connection shell for jastreamer servers",
  " A sandboxed Electron shell for discovering and connecting to jastreamer servers on a trusted LAN.",
  "",
].join("\n"));

const postinst = `#!/bin/sh
set -e
profile=/etc/apparmor.d/jastreamer-desktop
source=/usr/share/jastreamer-desktop/apparmor/jastreamer-desktop
marker='# Managed by the jastreamer-desktop package; use /etc/apparmor.d/local/jastreamer-desktop for local additions.'
managed=false
if [ -f "$profile" ] && [ "$(sed -n '1p' "$profile")" = "$marker" ]; then
  managed=true
fi
if command -v apparmor_parser >/dev/null 2>&1 && apparmor_parser --skip-kernel-load --skip-cache "$source" >/dev/null 2>&1; then
  if [ ! -e "$profile" ] || [ "$managed" = true ]; then
    install -m 0644 "$source" "$profile"
    if [ -r /sys/module/apparmor/parameters/enabled ] && grep -q '^[Yy]' /sys/module/apparmor/parameters/enabled; then
      apparmor_parser --replace "$profile"
    fi
  else
    echo "jastreamer-desktop: preserving unmanaged $profile; ensure it grants userns to ${applicationDirectory}/${name}" >&2
  fi
elif [ "$managed" = true ]; then
  if command -v apparmor_parser >/dev/null 2>&1; then
    apparmor_parser --remove "$profile" >/dev/null 2>&1 || true
  fi
  rm -f "$profile"
  echo 'jastreamer-desktop: AppArmor parser does not support the application userns profile; the setuid sandbox remains installed' >&2
fi
exit 0
`;
const postrm = `#!/bin/sh
set -e
profile=/etc/apparmor.d/jastreamer-desktop
marker='# Managed by the jastreamer-desktop package; use /etc/apparmor.d/local/jastreamer-desktop for local additions.'
case "$1" in
  remove|purge)
    if [ -f "$profile" ] && [ "$(sed -n '1p' "$profile")" = "$marker" ]; then
      if command -v apparmor_parser >/dev/null 2>&1; then
        apparmor_parser --remove "$profile" >/dev/null 2>&1 || true
      fi
      rm -f "$profile"
    fi
    ;;
esac
exit 0
`;
await writeFile(path.join(controlDirectory, "postinst"), postinst, { mode: 0o755 });
await writeFile(path.join(controlDirectory, "postrm"), postrm, { mode: 0o755 });
await chmod(controlDirectory, 0o755);
await chmod(path.join(controlDirectory, "control"), 0o644);
await chmod(path.join(controlDirectory, "postinst"), 0o755);
await chmod(path.join(controlDirectory, "postrm"), 0o755);

try {
  await execFile("dpkg-deb", ["--root-owner-group", "--build", packageDirectory, archivePath], { maxBuffer: 16 * 1024 * 1024 });
} catch (error) {
  throw new Error(`dpkg-deb failed: ${error.stderr || error.message}`, { cause: error });
}

const inventory = [];
for await (const entry of files(dataRoot, "usr/")) inventory.push(entry);
const manifest = {
  product: name,
  version: metadata.version,
  platform: "linux",
  arch: "amd64",
  electron: metadata.devDependencies.electron,
  sourceRevision: process.env.JASTREAMER_SOURCE_REVISION || "worktree",
  signed: false,
  productionQualified: false,
  archive: {
    path: archiveName,
    bytes: (await stat(archivePath)).size,
    sha256: await digest(archivePath),
  },
  files: inventory,
};
await writeFile(path.join(output, "manifest.json"), `${JSON.stringify(manifest, null, 2)}\n`);
await writeFile(`${archivePath}.sha256`, `${manifest.archive.sha256}  ${archiveName}\n`);
console.log(JSON.stringify({
  archive: archivePath,
  sha256: manifest.archive.sha256,
  files: inventory.length,
  signed: false,
}, null, 2));
