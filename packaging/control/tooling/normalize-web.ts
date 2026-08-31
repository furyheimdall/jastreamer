#!/usr/bin/env bun
import { createHash } from "node:crypto";
import { existsSync, lstatSync, readFileSync, readdirSync, renameSync, rmSync, utimesSync, writeFileSync } from "node:fs";
import { join, posix, relative, resolve } from "node:path";

export type WebNormalizationOptions = Readonly<{ version: string; sourceIdentity: string; toolchainIdentity: string }>;
export type WebNormalizationResult = Readonly<{ buildIdentity: string; serviceWorkerVersion: string; transformed: readonly string[]; removed: readonly string[] }>;
const bootstrapName = "flutter_bootstrap.js";
const workerName = "flutter_service_worker.js";
const metadataName = ".last_build_id";
const localCanvasKitFiles = Object.freeze(["canvaskit/canvaskit.js", "canvaskit/canvaskit.wasm", "canvaskit/chromium/canvaskit.js", "canvaskit/chromium/canvaskit.wasm"]);
const localFontFiles = Object.freeze(["assets/.release-web-fonts/Roboto-Regular.ttf", "assets/assets/fonts/noto_sans_kr/NotoSansKR-wght.ttf", "assets/assets/fonts/noto_sans_kr/OFL.txt", "assets/assets/fonts/noto_sans_kr/source.json"]);
const resourcesPrefix = "const RESOURCES = ";
const resourcesSuffix = "\n// The application shell files that are downloaded before a service worker can\n// start.";
const md5 = (bytes: Buffer): string => createHash("md5").update(bytes).digest("hex");
const packageTimestamp = new Date("1980-01-01T00:00:00.000Z");

const files = (root: string): string[] => {
  const result: string[] = [];
  const visit = (directory: string): void => {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const absolute = join(directory, entry.name);
      if (entry.isSymbolicLink()) throw new Error("CONTROL_WEB_NORMALIZE_SYMLINK");
      if (entry.isDirectory()) visit(absolute);
      else if (entry.isFile()) result.push(relative(root, absolute).split("\\").join("/"));
      else throw new Error("CONTROL_WEB_NORMALIZE_SPECIAL_FILE");
    }
  };
  visit(root);
  return result.sort();
};
const safeResourcePath = (path: string): boolean => path === "/" || path !== "" && !path.startsWith("/") && !path.includes("\\") && posix.normalize(path) === path && !path.split("/").includes("..");
const writeAtomic = (path: string, content: string): void => { const temporary = `${path}.normalize-${process.pid}`; writeFileSync(temporary, content, { mode: lstatSync(path).mode }); renameSync(temporary, path); };

export function normalizeWebBuild(rootPath: string, options: WebNormalizationOptions): WebNormalizationResult {
  if (!/^\d+\.\d+\.\d+$/.test(options.version) || !/^sha256:[0-9a-f]{64}$/.test(options.sourceIdentity) || !/^sha256:[0-9a-f]{64}$/.test(options.toolchainIdentity)) throw new Error("CONTROL_WEB_NORMALIZE_INPUT_INVALID");
  const root = resolve(rootPath); if (!lstatSync(root).isDirectory()) throw new Error("CONTROL_WEB_NORMALIZE_ROOT_INVALID");
  const bootstrapPath = join(root, bootstrapName); const workerPath = join(root, workerName); const metadataPath = join(root, metadataName);
  const bootstrap = readFileSync(bootstrapPath, "utf8"); const versions = [...bootstrap.matchAll(/serviceWorkerVersion:\s*"([0-9]+)"/g)]; if (versions.length !== 1) throw new Error("CONTROL_WEB_NORMALIZE_BOOTSTRAP_FORMAT"); if ([...bootstrap.matchAll(/fontFallbackBaseUrl:\s*"assets\/font-fallback-disabled\/"/g)].length !== 1 || /fontFallbackBaseUrl:\s*"https?:/.test(bootstrap)) throw new Error("CONTROL_WEB_NORMALIZE_EXTERNAL_FONT_FALLBACK"); const versionMatch = versions[0]; if (versionMatch === undefined || versionMatch[1] === undefined || versionMatch.index === undefined) throw new Error("CONTROL_WEB_NORMALIZE_BOOTSTRAP_FORMAT"); const localCanvasKit = [...bootstrap.matchAll(/"useLocalCanvasKit"\s*:\s*(true|false)/g)]; if (localCanvasKit.length !== 1 || localCanvasKit[0]?.[1] !== "true" || /"canvasKitBaseUrl"\s*:\s*"https?:/.test(bootstrap)) throw new Error("CONTROL_WEB_NORMALIZE_CDN_FALLBACK");
  const worker = readFileSync(workerPath, "utf8"); const prefixIndex = worker.indexOf(resourcesPrefix); if (prefixIndex < 0 || worker.indexOf(resourcesPrefix, prefixIndex + 1) >= 0) throw new Error("CONTROL_WEB_NORMALIZE_WORKER_FORMAT"); const jsonStart = prefixIndex + resourcesPrefix.length; const suffixIndex = worker.indexOf(resourcesSuffix, jsonStart); if (suffixIndex < 0 || worker.indexOf(resourcesSuffix, suffixIndex + 1) >= 0) throw new Error("CONTROL_WEB_NORMALIZE_WORKER_FORMAT"); const encoded = worker.slice(jsonStart, suffixIndex); if (!encoded.endsWith(";")) throw new Error("CONTROL_WEB_NORMALIZE_WORKER_FORMAT");
  let resources: Record<string, unknown>; try { resources = JSON.parse(encoded.slice(0, -1)); } catch { throw new Error("CONTROL_WEB_NORMALIZE_WORKER_FORMAT"); } if (resources === null || Array.isArray(resources) || typeof resources !== "object") throw new Error("CONTROL_WEB_NORMALIZE_WORKER_FORMAT"); const encodedKeys = [...encoded.matchAll(/"((?:[^"\\]|\\.)*)"\s*:/g)]; if (encodedKeys.length !== Object.keys(resources).length) throw new Error("CONTROL_WEB_NORMALIZE_WORKER_FORMAT");
  const actualFiles = files(root); const packageFiles = actualFiles.filter((path) => path !== metadataName && path !== workerName); const resourcePaths = Object.keys(resources); for (const path of resourcePaths) { if (!safeResourcePath(path) || typeof resources[path] !== "string" || !/^[0-9a-f]{32}$/.test(resources[path] as string)) throw new Error("CONTROL_WEB_NORMALIZE_RESOURCE_FORMAT"); }
  for (const path of localCanvasKitFiles) if (!packageFiles.includes(path)) throw new Error("CONTROL_WEB_NORMALIZE_LOCAL_CANVASKIT_REQUIRED"); for (const path of localFontFiles) if (!packageFiles.includes(path)) throw new Error("CONTROL_WEB_NORMALIZE_LOCAL_FONT_REQUIRED");
  const represented = resourcePaths.filter((path) => path !== "/").sort(); if (represented.join("\0") !== packageFiles.join("\0")) throw new Error("CONTROL_WEB_NORMALIZE_RESOURCE_SET");
  for (const path of resourcePaths) { const file = path === "/" ? "index.html" : path; const expectedDigest = resources[path]; if (typeof expectedDigest !== "string" || md5(readFileSync(join(root, file))) !== expectedDigest) throw new Error("CONTROL_WEB_NORMALIZE_RESOURCE_DIGEST"); }
  const buildIdentity = createHash("sha256").update("jastreamer-control-web-v1\0").update(options.version).update("\0").update(options.sourceIdentity).update("\0").update(options.toolchainIdentity).digest("hex"); const serviceWorkerVersion = createHash("sha256").update(buildIdentity).digest().readUInt32BE(0).toString();
  if (existsSync(metadataPath)) { const prior = readFileSync(metadataPath, "utf8").trim(); if (!/^[0-9a-f]{32,64}$/.test(prior)) throw new Error("CONTROL_WEB_NORMALIZE_BUILD_METADATA"); const needle = Buffer.from(prior); for (const path of packageFiles) if (readFileSync(join(root, path)).includes(needle)) throw new Error("CONTROL_WEB_NORMALIZE_RUNTIME_BUILD_ID_REFERENCE"); } else if (versionMatch[1] !== serviceWorkerVersion) throw new Error("CONTROL_WEB_NORMALIZE_ALREADY_MUTATED");
  const normalizedBootstrap = bootstrap.slice(0, versionMatch.index) + versionMatch[0].replace(versionMatch[1], serviceWorkerVersion) + bootstrap.slice(versionMatch.index + versionMatch[0].length); resources[bootstrapName] = md5(Buffer.from(normalizedBootstrap)); const canonicalResources = Object.fromEntries(Object.keys(resources).sort().map((key) => [key, resources[key]])); const normalizedWorker = worker.slice(0, jsonStart) + `${JSON.stringify(canonicalResources)};` + worker.slice(suffixIndex);
  if (normalizedBootstrap !== bootstrap) writeAtomic(bootstrapPath, normalizedBootstrap); if (normalizedWorker !== worker) writeAtomic(workerPath, normalizedWorker); if (existsSync(metadataPath)) rmSync(metadataPath);
  for (const path of files(root)) utimesSync(join(root, path), packageTimestamp, packageTimestamp);
  const directories = readdirSync(root, { recursive: true, withFileTypes: true }).filter((entry) => entry.isDirectory()).map((entry) => join(entry.parentPath, entry.name)).sort((left, right) => right.length - left.length); for (const directory of directories) utimesSync(directory, packageTimestamp, packageTimestamp); utimesSync(root, packageTimestamp, packageTimestamp);
  return { buildIdentity, serviceWorkerVersion, transformed: [bootstrapName, workerName], removed: [metadataName] };
}

if (import.meta.main) {
  const [root, version, sourceIdentity, toolchainIdentity] = process.argv.slice(2); if (!root || !version || !sourceIdentity || !toolchainIdentity) throw new Error("Usage: normalize-web <root> <version> <source-identity> <toolchain-identity>"); console.log(JSON.stringify(normalizeWebBuild(root, { version, sourceIdentity, toolchainIdentity })));
}
