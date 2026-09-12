import assert from "node:assert/strict";
import { execFile as execFileCallback } from "node:child_process";
import { createHash } from "node:crypto";
import { createReadStream } from "node:fs";
import {
  lstat,
  mkdtemp,
  open,
  readFile,
  readdir,
  readlink,
  rm,
  stat,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const execFile = promisify(execFileCallback);
const root = fileURLToPath(new URL("..", import.meta.url));
const output = path.join(root, "dist");
const metadata = JSON.parse(await readFile(path.join(root, "package.json"), "utf8"));
const manifest = JSON.parse(await readFile(path.join(output, "manifest.json"), "utf8"));

assert.equal(manifest.product, "jastreamer-desktop");
assert.equal(manifest.version, metadata.version);
assert.equal(manifest.electron, metadata.devDependencies.electron);
assert.equal(typeof manifest.sourceRevision, "string");
assert(manifest.sourceRevision.length > 0, "Missing source revision");
if (process.env.JASTREAMER_SOURCE_REVISION) {
  assert.equal(manifest.sourceRevision, process.env.JASTREAMER_SOURCE_REVISION);
}
assert.equal(manifest.signed, false);
assert.equal(manifest.productionQualified, false);
assert(Array.isArray(manifest.files) && manifest.files.length > 0, "Missing exact file inventory");

function child(directory, relative) {
  assert.equal(typeof relative, "string");
  assert(relative && !relative.includes("\\") && !path.isAbsolute(relative));
  assert(relative.split("/").every((part) => part && part !== "." && part !== ".."));
  return path.join(directory, relative);
}

async function digest(file) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(file)) hash.update(chunk);
  return hash.digest("hex");
}

const archive = child(output, manifest.archive.path);
assert.equal((await stat(archive)).size, manifest.archive.bytes);
assert.equal(await digest(archive), manifest.archive.sha256, "Archive SHA-256 mismatch");
assert.equal(
  await readFile(`${archive}.sha256`, "utf8"),
  `${manifest.archive.sha256}  ${manifest.archive.path}\n`,
  "Detached SHA-256 record does not match the manifest",
);

async function verifyWindows() {
  assert.equal(manifest.arch, "x64");
  assert.equal(manifest.archive.path, `jastreamer-desktop_${metadata.version}_windows-x64.zip`);
  const directory = child(output, manifest.directory);
  const expected = new Set();
  for (const entry of manifest.files) {
    assert(!expected.has(entry.path), "Duplicate artifact path");
    assert(!/(^|\/)(?:user-data|\.user-data|recents\.json|preferences\.json)(\/|$)/i.test(entry.path), "Private runtime data in artifact");
    expected.add(entry.path);
    const file = child(directory, entry.path);
    assert.equal((await stat(file)).size, entry.bytes, entry.path);
    assert.equal(await digest(file), entry.sha256, entry.path);
  }
  async function inventory(dir, prefix = "") {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const relative = prefix + entry.name;
      if (entry.isDirectory()) await inventory(path.join(dir, entry.name), `${relative}/`);
      else {
        assert(entry.isFile());
        assert(expected.delete(relative), `Unlisted file: ${relative}`);
      }
    }
  }
  await inventory(directory);
  assert.equal(expected.size, 0, "Missing artifact files");
  for (const required of ["jastreamer-desktop.exe", "resources/app.asar", "LICENSE.jastreamer", "START-HERE.txt"]) {
    assert(manifest.files.some((entry) => entry.path === required), `Missing ${required}`);
  }
  const executable = await open(path.join(directory, "jastreamer-desktop.exe"));
  try {
    const header = Buffer.alloc(64);
    await executable.read(header, 0, header.length, 0);
    assert.equal(header.toString("ascii", 0, 2), "MZ");
    const pe = Buffer.alloc(26);
    await executable.read(pe, 0, pe.length, header.readUInt32LE(60));
    assert.equal(pe.toString("ascii", 0, 4), "PE\0\0");
    assert.equal(pe.readUInt16LE(4), 0x8664, "Executable must target Windows x64");
    assert.equal(pe.readUInt16LE(24), 0x20b, "Executable must be PE32+");
  } finally {
    await executable.close();
  }
  console.log(`Verified Windows x64 portable ZIP and ${manifest.files.length} exact packaged files. Windows execution and signing are separate checks.`);
}

async function command(program, args) {
  try {
    return await execFile(program, args, { maxBuffer: 32 * 1024 * 1024 });
  } catch (error) {
    throw new Error(`${program} failed: ${error.stderr || error.message}`, { cause: error });
  }
}

async function verifyLinux() {
  assert.equal(manifest.arch, "amd64");
  assert.equal(manifest.archive.path, `jastreamer-desktop_${metadata.version}_linux-amd64.deb`);
  assert.equal((await command("dpkg-deb", ["--field", archive, "Package"])).stdout.trim(), "jastreamer-desktop");
  assert.equal((await command("dpkg-deb", ["--field", archive, "Version"])).stdout.trim(), metadata.version);
  assert.equal((await command("dpkg-deb", ["--field", archive, "Architecture"])).stdout.trim(), "amd64");
  const dependencies = (await command("dpkg-deb", ["--field", archive, "Depends"])).stdout;
  assert.match(dependencies, /\bapparmor\b/, "AppArmor parser dependency is required");
  assert.doesNotMatch(dependencies, /\b(?:ffmpeg|mpv|vlc)\b/i, "Desktop package must not require host media software");

  const contents = (await command("dpkg-deb", ["--contents", archive])).stdout
    .split("\n")
    .filter(Boolean);
  assert(contents.length > manifest.files.length, "DEB contents listing is unexpectedly short");
  for (const line of contents) {
    assert.match(line, /^\S+\s+root\/root\s+/, `DEB entry is not root-owned: ${line}`);
  }
  const sandboxListing = contents.find((line) => line.endsWith(" ./usr/lib/jastreamer-desktop/chrome-sandbox"));
  assert(sandboxListing, "Missing chrome-sandbox from DEB");
  assert.match(sandboxListing, /^-rwsr-xr-x\s+root\/root\s+/, "chrome-sandbox must be root:root mode 4755");

  const temporary = await mkdtemp(path.join(os.tmpdir(), "jastreamer-deb-verify-"));
  try {
    const extracted = path.join(temporary, "data");
    const control = path.join(temporary, "control");
    await command("dpkg-deb", ["--extract", archive, extracted]);
    await command("dpkg-deb", ["--control", archive, control]);

    const expected = new Map();
    for (const entry of manifest.files) {
      child(extracted, entry.path);
      assert(!expected.has(entry.path), `Duplicate artifact path: ${entry.path}`);
      assert(!/(^|\/)(?:user-data|\.user-data|recents\.json|preferences\.json)(\/|$)/i.test(entry.path), "Private runtime data in artifact");
      assert.equal(entry.uid, 0, `${entry.path} uid`);
      assert.equal(entry.gid, 0, `${entry.path} gid`);
      expected.set(entry.path, entry);
    }

    async function inspect(directory, prefix = "") {
      for (const item of await readdir(directory, { withFileTypes: true })) {
        const relative = prefix + item.name;
        const file = path.join(directory, item.name);
        const details = await lstat(file);
        if (item.isDirectory()) {
          assert.equal(details.mode & 0o022, 0, `Writable installation directory: ${relative}`);
          await inspect(file, `${relative}/`);
          continue;
        }
        const entry = expected.get(relative);
        assert(entry, `Unlisted DEB file: ${relative}`);
        expected.delete(relative);
        assert.equal((details.mode & 0o7777).toString(8).padStart(4, "0"), entry.mode, `${relative} mode`);
        if (item.isFile()) {
          assert.equal(entry.type, "file", `${relative} type`);
          assert.equal(details.mode & 0o022, 0, `Writable installed file: ${relative}`);
          assert.equal(details.size, entry.bytes, `${relative} bytes`);
          assert.equal(await digest(file), entry.sha256, `${relative} SHA-256`);
        } else if (item.isSymbolicLink()) {
          assert.equal(entry.type, "symlink", `${relative} type`);
          assert.equal(await readlink(file), entry.target, `${relative} link target`);
        } else {
          assert.fail(`Unexpected DEB entry type: ${relative}`);
        }
      }
    }
    await inspect(extracted);
    assert.equal(expected.size, 0, `Missing DEB files: ${[...expected.keys()].join(", ")}`);

    const required = new Map([
      ["usr/lib/jastreamer-desktop/jastreamer-desktop", "0755"],
      ["usr/lib/jastreamer-desktop/chrome-sandbox", "4755"],
      ["usr/lib/jastreamer-desktop/resources/app.asar", "0644"],
      ["usr/lib/jastreamer-desktop/LICENSE.jastreamer", "0644"],
      ["usr/share/applications/jastreamer-desktop.desktop", "0644"],
      ["usr/share/icons/hicolor/512x512/apps/jastreamer-desktop.png", "0644"],
      ["usr/share/jastreamer-desktop/apparmor/jastreamer-desktop", "0644"],
    ]);
    for (const [requiredPath, mode] of required) {
      const entry = manifest.files.find((candidate) => candidate.path === requiredPath);
      assert(entry, `Missing ${requiredPath}`);
      assert.equal(entry.mode, mode, `${requiredPath} mode`);
    }

    const executable = await open(path.join(extracted, "usr/lib/jastreamer-desktop/jastreamer-desktop"));
    try {
      const elf = Buffer.alloc(20);
      await executable.read(elf, 0, elf.length, 0);
      assert.deepEqual([...elf.subarray(0, 4)], [0x7f, 0x45, 0x4c, 0x46], "Executable must be ELF");
      assert.equal(elf[4], 2, "Executable must be ELF64");
      assert.equal(elf[5], 1, "Executable must be little-endian");
      assert.equal(elf.readUInt16LE(18), 0x3e, "Executable must target Linux amd64");
    } finally {
      await executable.close();
    }

    const desktop = await readFile(path.join(extracted, "usr/share/applications/jastreamer-desktop.desktop"), "utf8");
    assert.match(desktop, /^Exec=\/usr\/lib\/jastreamer-desktop\/jastreamer-desktop$/m);
    assert.match(desktop, /^Icon=jastreamer-desktop$/m);
    assert.match(desktop, /^Terminal=false$/m);
    const profile = await readFile(path.join(extracted, "usr/share/jastreamer-desktop/apparmor/jastreamer-desktop"), "utf8");
    assert.match(profile, /^\/usr\/lib\/jastreamer-desktop\/jastreamer-desktop flags=\(unconfined\) \{$/m);
    assert.match(profile, /^\s+userns,$/m);
    assert.equal((await lstat(path.join(control, "postinst"))).mode & 0o777, 0o755);
    assert.equal((await lstat(path.join(control, "postrm"))).mode & 0o777, 0o755);
  } finally {
    await rm(temporary, { recursive: true, force: true });
  }
  console.log(`Verified Linux amd64 DEB, root-owned installation permissions, Chromium sandbox helper and ${manifest.files.length} exact packaged files. Installed execution is a separate smoke check.`);
}

if (manifest.platform === "win32") {
  await verifyWindows();
} else if (manifest.platform === "linux") {
  await verifyLinux();
} else {
  assert.fail(`Unsupported manifest platform: ${manifest.platform}`);
}
