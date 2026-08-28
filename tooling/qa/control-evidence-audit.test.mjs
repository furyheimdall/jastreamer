import { expect, test } from "bun:test";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { auditControlEvidence } from "./control-evidence-audit.mjs";

const pngHeader = (width, height) => {
  const bytes = Buffer.alloc(24);
  Buffer.from("89504e470d0a1a0a", "hex").copy(bytes);
  bytes.writeUInt32BE(13, 8);
  bytes.write("IHDR", 12, "ascii");
  bytes.writeUInt32BE(width, 16);
  bytes.writeUInt32BE(height, 20);
  return bytes;
};

test("audits exact screenshot dimensions hashes freshness and declared alias", async () => {
  // Given
  const root = await mkdtemp(join(tmpdir(), "control-evidence-audit-"));
  const browser = join(root, "browser");
  const failure = join(root, "browser-failure");
  const source = join(root, "source.dart");
  await Promise.all([mkdir(browser), mkdir(failure)]);
  await writeFile(source, "source");
  await Promise.all([
    writeFile(join(browser, "desktop.png"), pngHeader(1280, 900)),
    writeFile(join(browser, "mobile.png"), pngHeader(390, 844)),
    writeFile(join(failure, "repaired.png"), pngHeader(390, 6000)),
    writeFile(join(failure, "final.png"), pngHeader(390, 6000)),
  ]);

  // When
  const manifest = await auditControlEvidence({
    root,
    sourceDigest: "a".repeat(64),
    sourcePaths: [source],
    expectedCount: 4,
    allowedAliases: [["browser-failure/final.png", "browser-failure/repaired.png"]],
  });

  // Then
  expect(manifest.capture_count).toBe(4);
  expect(manifest.unique_capture_count).toBe(3);
  expect(manifest.fresh_build).toBe(true);
  expect(manifest.source_digest).toBe("a".repeat(64));
  expect(
    manifest.captures.map(({ path, width, height }) => ({
      name: path.slice(path.lastIndexOf("/") + 1),
      width,
      height,
    })),
  ).toEqual(expect.arrayContaining([
    { name: "desktop.png", width: 1280, height: 900 },
    { name: "mobile.png", width: 390, height: 844 },
    { name: "repaired.png", width: 390, height: 6000 },
    { name: "final.png", width: 390, height: 6000 },
  ]));
  await rm(root, { recursive: true });
});

test("rejects undeclared duplicate screenshots", async () => {
  // Given
  const root = await mkdtemp(join(tmpdir(), "control-evidence-audit-"));
  const browser = join(root, "browser");
  await Promise.all([mkdir(browser), mkdir(join(root, "browser-failure"))]);
  await Promise.all([
    writeFile(join(browser, "one.png"), pngHeader(390, 844)),
    writeFile(join(browser, "two.png"), pngHeader(390, 844)),
  ]);

  // When
  const audit = auditControlEvidence({
    root,
    sourceDigest: "b".repeat(64),
    sourcePaths: [],
    expectedCount: 2,
    allowedAliases: [],
  });

  // Then
  await expect(audit).rejects.toThrow("SCREENSHOT_ALIAS_UNDECLARED");
  await rm(root, { recursive: true });
});
