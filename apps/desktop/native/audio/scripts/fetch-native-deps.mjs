import { createHash } from "node:crypto";
import { createReadStream, createWriteStream } from "node:fs";
import { access, mkdir, readFile, rename, rm } from "node:fs/promises";
import path from "node:path";
import { pipeline } from "node:stream/promises";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";

const audioRoot = path.resolve(fileURLToPath(new URL("..", import.meta.url)));
const lock = JSON.parse(await readFile(path.join(audioRoot, "dependency-lock.json"), "utf8"));
const downloads = path.join(audioRoot, "deps", "downloads");
const sources = path.join(audioRoot, "deps", "src");
const maximumArchiveBytes = 128 * 1024 * 1024;

async function sha256(file) {
  const hash = createHash("sha256");
  const input = createReadStream(file);
  for await (const chunk of input) hash.update(chunk);
  return hash.digest("hex");
}

async function download(dependency) {
  const archive = path.join(downloads, dependency.archive);
  try {
    await access(archive);
    const actual = await sha256(archive);
    if (actual !== dependency.sha256) {
      throw new Error(`${dependency.archive} exists but has SHA-256 ${actual}; refusing to replace an untrusted cache entry`);
    }
    return archive;
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
  }

  const response = await fetch(dependency.url, { redirect: "follow", signal: AbortSignal.timeout(120_000) });
  if (!response.ok || !response.body) throw new Error(`Download failed for ${dependency.url}: HTTP ${response.status}`);
  if (new URL(response.url).protocol !== "https:") throw new Error(`Dependency redirected outside HTTPS: ${response.url}`);
  const declaredLength = Number(response.headers.get("content-length"));
  if (Number.isFinite(declaredLength) && declaredLength > maximumArchiveBytes) {
    throw new Error(`${dependency.archive} exceeds the archive size limit`);
  }

  const temporary = `${archive}.partial-${process.pid}`;
  let received = 0;
  const limiter = new TransformStream({
    transform(chunk, controller) {
      received += chunk.byteLength;
      if (received > maximumArchiveBytes) throw new Error(`${dependency.archive} exceeds the archive size limit`);
      controller.enqueue(chunk);
    },
  });
  try {
    await pipeline(response.body.pipeThrough(limiter), createWriteStream(temporary, { flags: "wx", mode: 0o644 }));
    const actual = await sha256(temporary);
    if (actual !== dependency.sha256) {
      throw new Error(`${dependency.archive} SHA-256 mismatch: expected ${dependency.sha256}, got ${actual}`);
    }
    await rename(temporary, archive);
  } catch (error) {
    await rm(temporary, { force: true });
    throw error;
  }
  return archive;
}

function run(program, args, cwd) {
  return new Promise((resolve, reject) => {
    const child = spawn(program, args, { cwd, stdio: "inherit", windowsHide: true });
    child.on("error", reject);
    child.on("exit", (code, signal) => {
      if (code === 0) resolve();
      else reject(new Error(`${program} exited with ${code ?? signal}`));
    });
  });
}

async function extract(dependency, archive) {
  const destination = path.join(sources, dependency.sourceDirectory);
  const staging = `${destination}.extract-${process.pid}`;
  await rm(destination, { recursive: true, force: true });
  await rm(staging, { recursive: true, force: true });
  await mkdir(staging, { recursive: true });
  try {
    await run("cmake", ["-E", "tar", "xvf", archive], staging);
    const nested = path.join(staging, dependency.sourceDirectory);
    try {
      await access(nested);
      await rename(nested, destination);
      await rm(staging, { recursive: true, force: true });
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
      await rename(staging, destination);
    }
  } catch (error) {
    await rm(staging, { recursive: true, force: true });
    throw error;
  }
}

await mkdir(downloads, { recursive: true });
await mkdir(sources, { recursive: true });
for (const dependency of [lock.ffmpeg, lock.nlohmannJson]) {
  const archive = await download(dependency);
  await extract(dependency, archive);
}
console.log(`Verified and extracted FFmpeg ${lock.ffmpeg.version} and nlohmann/json ${lock.nlohmannJson.version}.`);
