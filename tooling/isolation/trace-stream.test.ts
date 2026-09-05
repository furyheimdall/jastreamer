import { expect, test } from "bun:test";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { normalizeTraceFile, outsideAllowlist } from "./trace.ts";

test("file traces preserve split UTF-8, process state, and unfinished calls across chunks", () => {
  const directory = mkdtempSync(join(tmpdir(), "isolation-stream-"));
  try {
    const path = join(directory, "trace");
    const first = '100 chdir("apps/server") = 0\n';
    const call = '100 openat(AT_FDCWD, "../../apps/control/lib/';
    const padding = " ".repeat(65535 - Buffer.byteLength(first + call));
    writeFileSync(path, first + padding + call +
      '\uac00.dart", O_RDONLY <unfinished ...>\n' +
      '100 <... openat resumed>) = -1 ENOENT (No such file or directory)');
    const result = normalizeTraceFile(path, {
      repositoryRoot: directory,
      worktree: directory,
      initialDirectory: directory,
      namespaceRoot: join(directory, "namespaces"),
      aliases: [],
    });
    expect(result.accessedPaths).toContain("apps/control/lib/\uac00.dart");
    expect(result.missingPaths).toEqual(["apps/control/lib/\uac00.dart"]);
    expect(outsideAllowlist(result.accessedPaths, ["apps/server"], result.missingPaths, "server"))
      .toEqual(["apps/control/lib/\uac00.dart"]);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
