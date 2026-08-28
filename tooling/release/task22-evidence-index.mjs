import { createHash, randomUUID } from "node:crypto";
import { lstat, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import { isAbsolute, join, relative, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";

export const INDEX_NAME = "evidence-index.json";
export const CANONICAL_COMMAND = ["bun", "test", "contracts/tests", "tooling/compatibility", "tooling/docs", "tooling/qa", "packaging/server", "packaging/control", "packaging/renderer", "tooling/release"];

const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");
const slash = (path) => path.split(sep).join("/");
const sameStat = (a, b) => a.isFile() && b.isFile() && a.dev === b.dev && a.ino === b.ino && a.size === b.size && a.mtimeNs === b.mtimeNs;

export const stableRead = async (path) => {
  const before = await lstat(path, { bigint: true });
  if (before.isSymbolicLink()) throw new Error(`SYMLINK ${path}`);
  if (!before.isFile()) throw new Error(`NOT_REGULAR_FILE ${path}`);
  const bytes = await readFile(path);
  const after = await lstat(path, { bigint: true });
  if (!sameStat(before, after) || BigInt(bytes.byteLength) !== after.size) throw new Error(`UNSTABLE_FILE ${path}`);
  return bytes;
};

const walk = async (root, directory = root, files = [], symlinks = []) => {
  const entries = await readdir(directory, { withFileTypes: true });
  entries.sort((a, b) => a.name.localeCompare(b.name));
  for (const entry of entries) {
    const absolute = join(directory, entry.name);
    const path = slash(relative(root, absolute));
    if (path === INDEX_NAME) continue;
    if (entry.isSymbolicLink()) symlinks.push(path);
    else if (entry.isDirectory()) await walk(root, absolute, files, symlinks);
    else if (entry.isFile()) files.push(path);
    else throw new Error(`NOT_REGULAR_FILE ${path}`);
  }
  return { files, symlinks };
};

const snapshot = async (root) => {
  const inventory = await walk(root);
  if (inventory.symlinks.length) throw new Error(`SYMLINK ${inventory.symlinks.join(",")}`);
  inventory.files.sort((a, b) => a.localeCompare(b));
  const files = [];
  for (const path of inventory.files) {
    const bytes = await stableRead(join(root, path));
    files.push({ path, sha256: digest(bytes), size: bytes.byteLength });
  }
  return files;
};

export const generateEvidenceIndex = async (evidenceRoot) => {
  const root = resolve(evidenceRoot);
  const first = await snapshot(root);
  const second = await snapshot(root);
  if (JSON.stringify(first) !== JSON.stringify(second)) throw new Error("UNSTABLE_EVIDENCE_CLOSURE");
  const index = {
    schemaVersion: 3,
    task: 22,
    excluded: [{ path: INDEX_NAME, reason: "self-referential index" }],
    files: second,
  };
  const output = `${JSON.stringify(index, null, 2)}\n`;
  const target = join(root, INDEX_NAME);
  const temporary = join(root, `.${INDEX_NAME}.${randomUUID()}.tmp`);
  try {
    await writeFile(temporary, output, { flag: "wx" });
    await rename(temporary, target);
  } finally {
    await rm(temporary, { force: true });
  }
  return index;
};

export const auditEvidenceIndex = async (evidenceRoot) => {
  const root = resolve(evidenceRoot);
  const symlinks = [];
  let actualPaths = [];
  try {
    const inventory = await walk(root);
    actualPaths = inventory.files;
    symlinks.push(...inventory.symlinks);
  } catch (error) {
    return { files: 0, bad: 1, symlinks: 0, missing: 0, extra: 0, errors: [error.message] };
  }

  const errors = [];
  let index;
  try {
    index = JSON.parse((await stableRead(join(root, INDEX_NAME))).toString("utf8"));
  } catch (error) {
    return { files: 0, bad: 1, symlinks: symlinks.length, missing: 0, extra: actualPaths.length, errors: [error.message] };
  }
  if (index.schemaVersion !== 3 || index.task !== 22) errors.push("INDEX_HEADER_INVALID");
  if (JSON.stringify(index.excluded) !== JSON.stringify([{ path: INDEX_NAME, reason: "self-referential index" }])) errors.push("INDEX_EXCLUSION_INVALID");
  if (!Array.isArray(index.files)) errors.push("INDEX_FILES_INVALID");

  const retained = Array.isArray(index.files) ? index.files : [];
  const indexedPaths = retained.map(({ path }) => path);
  const indexedSet = new Set(indexedPaths);
  const actualSet = new Set(actualPaths);
  const missing = indexedPaths.filter((path) => !actualSet.has(path));
  const extra = actualPaths.filter((path) => !indexedSet.has(path));
  if (indexedSet.size !== indexedPaths.length) errors.push("DUPLICATE_INDEX_PATH");
  if (JSON.stringify(indexedPaths) !== JSON.stringify([...indexedPaths].sort((a, b) => a.localeCompare(b)))) errors.push("INDEX_ORDER_INVALID");

  for (const entry of retained) {
    if (!actualSet.has(entry.path)) continue;
    try {
      const bytes = await stableRead(join(root, entry.path));
      if (entry.size !== bytes.byteLength || entry.sha256 !== digest(bytes)) errors.push(`MISMATCH ${entry.path}`);
    } catch (error) {
      errors.push(error.message);
    }
  }
  try {
    const claim = JSON.parse((await stableRead(join(root, "DoneClaim.json"))).toString("utf8"));
    if (claim.verification !== undefined) {
      const identity = (value) => typeof value === "object" && value !== null && typeof value.path === "string" && /^[0-9a-f]{64}$/.test(value.sha256) && Number.isSafeInteger(value.size) && value.size >= 0;
      const pathOf = (value) => typeof value === "string" ? value : value?.path;
      const readLinked = async (value, code) => {
        const path = pathOf(value);
        if (!identity(value) || typeof path !== "string") errors.push(code);
        if (typeof path !== "string") return undefined;
        const absolute = resolve(root, path); const location = relative(root, absolute);
        if (location === "" || location === ".." || location.startsWith(`..${sep}`) || isAbsolute(location)) { errors.push(code); return undefined; }
        const bytes = await stableRead(absolute); const indexed = retained.find((entry) => entry.path === slash(path));
        if (!identity(value) || indexed === undefined || indexed.sha256 !== value.sha256 || indexed.size !== value.size || digest(bytes) !== value.sha256 || bytes.byteLength !== value.size) errors.push(code);
        return { bytes };
      };
      const runLink = await readLinked(claim.verification.canonicalRun, "LINKED_EVIDENCE_IDENTITY_INVALID");
      const summaryLink = await readLinked(claim.evidence, "LINKED_EVIDENCE_IDENTITY_INVALID");
      if (runLink !== undefined && summaryLink !== undefined) {
        const run = JSON.parse(runLink.bytes.toString("utf8")); const summary = JSON.parse(summaryLink.bytes.toString("utf8"));
        const logLink = await readLinked(run.log, "CANONICAL_ARTIFACT_IDENTITY_INVALID");
        const exitCodeLink = await readLinked(run.exitCode, "CANONICAL_ARTIFACT_IDENTITY_INVALID");
        const manifestLink = await readLinked(run.sourceManifest, "CANONICAL_ARTIFACT_IDENTITY_INVALID");
        if (claim.sourceDigest !== run.sourceDigest || summary.sourceDigest !== run.sourceDigest) errors.push("LINKED_SOURCE_DIGEST_MISMATCH");
        const summaryCanonical = summary.verification?.canonical;
        const summaryResult = summaryCanonical === undefined ? undefined : { passed: summaryCanonical.passed, skipped: summaryCanonical.skipped, failed: summaryCanonical.failed };
        if (JSON.stringify(summaryResult) !== JSON.stringify(run.result)) errors.push("LINKED_CANONICAL_RESULT_MISMATCH");
        if (JSON.stringify(summaryCanonical?.command) !== JSON.stringify(run.command)) errors.push("LINKED_CANONICAL_COMMAND_MISMATCH");
        if (JSON.stringify(summary.verification?.canonicalRun) !== JSON.stringify(claim.verification.canonicalRun)) errors.push("LINKED_EVIDENCE_IDENTITY_INVALID");
        if (JSON.stringify(run.command) !== JSON.stringify(CANONICAL_COMMAND)) errors.push("CANONICAL_COMMAND_MISMATCH");
        if (exitCodeLink === undefined || exitCodeLink.bytes.toString("utf8").trim() !== "0") errors.push("CANONICAL_EXIT_CODE_MISMATCH");
        if (logLink !== undefined) {
          const log = logLink.bytes.toString("utf8");
          const count = (name) => { const match = new RegExp(`(?:^|\\n)\\s*([0-9]+) ${name}(?:ed)?(?:\\n|$)`).exec(log); return match === null ? 0 : Number(match[1]); };
          const observed = { passed: count("pass"), skipped: count("skip"), failed: count("fail") };
          if (JSON.stringify(run.result) !== JSON.stringify(observed)) errors.push("CANONICAL_RESULT_MISMATCH");
        }
        if (!/^[0-9a-f]{64}$/.test(run.sourceDigest) || manifestLink === undefined) errors.push("CANONICAL_SOURCE_INVALID");
        else {
          const records = JSON.parse(manifestLink.bytes.toString("utf8")); const repository = resolve(root, "../../../..");
          if (!Array.isArray(records) || records.length === 0) errors.push("CANONICAL_SOURCE_INVALID");
          else {
            for (const record of records) {
              const absolute = resolve(repository, record.path); const location = relative(repository, absolute);
              if (location === "" || location === ".." || location.startsWith(`..${sep}`) || digest(await stableRead(absolute)) !== record.sha256) errors.push("CANONICAL_SOURCE_MISMATCH");
            }
            if (digest(JSON.stringify(records)) !== run.sourceDigest) errors.push("CANONICAL_SOURCE_MISMATCH");
          }
        }
      }
    }
  } catch (error) { errors.push(error instanceof Error ? `CANONICAL_VERIFICATION_INVALID:${error.message}` : "CANONICAL_VERIFICATION_INVALID"); }
  return {
    files: retained.length,
    bad: errors.length + missing.length + extra.length + symlinks.length,
    symlinks: symlinks.length,
    missing: missing.length,
    extra: extra.length,
    errors: [...errors, ...missing.map((path) => `MISSING ${path}`), ...extra.map((path) => `EXTRA ${path}`), ...symlinks.map((path) => `SYMLINK ${path}`)],
  };
};

const main = async () => {
  const args = process.argv.slice(2);
  const write = args[0] === "--write";
  const root = write ? args[1] : args[0];
  if (!root) throw new Error("usage: task22-evidence-index.mjs [--write] <evidence-root>");
  if (write) await generateEvidenceIndex(root);
  const result = await auditEvidenceIndex(root);
  console.log(`FILES ${result.files} BAD ${result.bad} SYMLINKS ${result.symlinks} MISSING ${result.missing} EXTRA ${result.extra}`);
  for (const error of result.errors) console.error(error);
  if (result.bad) process.exitCode = 1;
};

if (import.meta.url === pathToFileURL(process.argv[1] ?? "").href) await main();
