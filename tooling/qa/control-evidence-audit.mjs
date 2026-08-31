#!/usr/bin/env bun
import { createHash, randomUUID } from "node:crypto";
import {
  readFile,
  readdir,
  rename,
  stat,
  writeFile,
} from "node:fs/promises";
import { join, relative, resolve, sep } from "node:path";
import { pathToFileURL } from "node:url";

const SHA256 = /^[0-9a-f]{64}$/;
const PNG_SIGNATURE = "89504e470d0a1a0a";

const filesBelow = async (path) => {
  const metadata = await stat(path, { bigint: true });
  if (metadata.isFile()) return [{ path, mtimeNs: metadata.mtimeNs }];
  const entries = await readdir(path, { withFileTypes: true });
  const nested = await Promise.all(
    entries.map((entry) => filesBelow(join(path, entry.name))),
  );
  return nested.flat();
};

const normalizedPair = (pair) => [...pair].sort().join("\u0000");

export const auditControlEvidence = async ({
  root,
  sourceDigest,
  sourcePaths,
  expectedCount,
  allowedAliases,
}) => {
  if (!SHA256.test(sourceDigest)) throw new Error("SOURCE_DIGEST_INVALID");
  const absoluteRoot = resolve(root);
  const laneNames = ["browser", "browser-failure"];
  const laneFiles = await Promise.all(
    laneNames.map(async (lane) => {
      const directory = join(absoluteRoot, lane);
      const entries = await readdir(directory, { withFileTypes: true });
      return entries
        .filter((entry) => entry.isFile() && entry.name.endsWith(".png"))
        .map((entry) => ({ lane, path: join(directory, entry.name) }));
    }),
  );
  const screenshots = laneFiles.flat().sort((left, right) =>
    left.path.localeCompare(right.path),
  );
  if (screenshots.length !== expectedCount) {
    throw new Error(`SCREENSHOT_COUNT_MISMATCH:${screenshots.length}`);
  }

  const captures = await Promise.all(
    screenshots.map(async ({ lane, path }) => {
      const [bytes, metadata] = await Promise.all([
        readFile(path),
        stat(path, { bigint: true }),
      ]);
      if (bytes.length < 24 || bytes.subarray(0, 8).toString("hex") !== PNG_SIGNATURE) {
        throw new Error(`SCREENSHOT_SIGNATURE_INVALID:${path}`);
      }
      return {
        path: relative(process.cwd(), path).split(sep).join("/"),
        alias_path: relative(absoluteRoot, path).split(sep).join("/"),
        lane,
        width: bytes.readUInt32BE(16),
        height: bytes.readUInt32BE(20),
        sha256: createHash("sha256").update(bytes).digest("hex"),
        mtime_ns: metadata.mtimeNs.toString(),
      };
    }),
  );

  const aliases = new Set(allowedAliases.map(normalizedPair));
  const digestGroups = Map.groupBy(captures, (capture) => capture.sha256);
  for (const group of digestGroups.values()) {
    if (group.length === 1) continue;
    const paths = group.map(({ alias_path }) => alias_path);
    for (let left = 0; left < paths.length; left++) {
      for (let right = left + 1; right < paths.length; right++) {
        if (!aliases.has(normalizedPair([paths[left], paths[right]]))) {
          throw new Error("SCREENSHOT_ALIAS_UNDECLARED");
        }
      }
    }
  }

  const sourceFiles = (
    await Promise.all(sourcePaths.map((path) => filesBelow(resolve(path))))
  ).flat();
  const latestSource = sourceFiles.reduce(
    (latest, file) => file.mtimeNs > latest ? file.mtimeNs : latest,
    0n,
  );
  const earliestCapture = captures.reduce((earliest, capture) => {
    const value = BigInt(capture.mtime_ns);
    return value < earliest ? value : earliest;
  }, BigInt(captures[0].mtime_ns));
  if (latestSource > earliestCapture) throw new Error("SCREENSHOT_SOURCE_STALE");

  return {
    schema_version: 1,
    generated_at: new Date().toISOString(),
    source_digest: sourceDigest,
    capture_count: captures.length,
    unique_capture_count: digestGroups.size,
    fresh_build: true,
    allowed_aliases: allowedAliases,
    captures: captures.map(({ alias_path: _, ...capture }) => capture),
  };
};

const parseArguments = (values) => {
  const result = new Map();
  for (let index = 0; index < values.length; index += 2) {
    const key = values[index];
    const value = values[index + 1];
    if (key === undefined || value === undefined || !key.startsWith("--")) {
      throw new Error("ARGUMENT_INVALID");
    }
    const name = key.slice(2);
    result.set(name, [...(result.get(name) ?? []), value]);
  }
  return result;
};

if (process.argv[1] !== undefined &&
    pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const args = parseArguments(process.argv.slice(2));
  const one = (name) => {
    const values = args.get(name);
    if (values?.length !== 1) throw new Error(`ARGUMENT_REQUIRED:${name}`);
    return values[0];
  };
  const output = resolve(one("output"));
  const manifest = await auditControlEvidence({
    root: one("root"),
    sourceDigest: one("source-digest"),
    sourcePaths: args.get("source") ?? [],
    expectedCount: Number(one("expected-count")),
    allowedAliases: (args.get("allow-alias") ?? []).map((value) => value.split("=")),
  });
  const temporary = `${output}.${randomUUID()}.tmp`;
  await writeFile(temporary, `${JSON.stringify(manifest, null, 2)}\n`, {
    flag: "wx",
  });
  await rename(temporary, output);
  process.stdout.write(`${manifest.capture_count} captures audited\n`);
}
