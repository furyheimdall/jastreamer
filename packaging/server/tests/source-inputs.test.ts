import { describe, expect, test } from "bun:test";
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { sourceIdentity } from "../tooling/identity";

const generatedExecutableNames = ["jastreamer-server", "jastreamer-server.exe"] as const;

function sourceRoot(): string {
  const root = mkdtempSync(join(tmpdir(), "server-source-inputs-"));
  mkdirSync(join(root, "apps/server"), { recursive: true });
  writeFileSync(join(root, "apps/server/main.go"), "package main\nfunc main() {}\n");
  return root;
}

describe("Server packaging source inputs", () => {
  test("derives first-run identity from a clean source tree without a prebuilt executable", () => {
    // Given: current Server source in a tree that has never been built.
    const root = sourceRoot();
    try {
      // When: packaging derives the source identity.
      const identity = sourceIdentity(root, ["apps/server"]);

      // Then: source alone is a complete packaging input.
      expect(identity).toMatch(/^sha256:[0-9a-f]{64}$/);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  test("ignores missing and stale default Go executable outputs while retaining current source", async () => {
    // Given: a source identity captured before stale default Go outputs appear.
    const repository = resolve(new URL("../../..", import.meta.url).pathname);
    const root = sourceRoot();
    const staged = join(root, "staged");
    try {
      const clean = sourceIdentity(root, ["apps/server"]);
      for (const name of generatedExecutableNames) writeFileSync(join(root, "apps/server", name), `stale-${name}`);

      // When: packaging derives identity and stages source with stale generated executables present.
      const stale = sourceIdentity(root, ["apps/server"]);
      const child = Bun.spawn(["bash", "packaging/server/stage-source.sh", join(root, "apps/server"), staged], {
        cwd: repository,
        stdout: "pipe",
        stderr: "pipe",
      });
      const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text()]);
      writeFileSync(join(root, "apps/server/main.go"), "package main\nfunc main() { println(\"current\") }\n");

      // Then: stale outputs are neither identity nor staged inputs, while current source remains authoritative.
      expect({ code, stderr }).toEqual({ code: 0, stderr: "" });
      expect(stale).toBe(clean);
      expect(generatedExecutableNames.some((name) => existsSync(join(staged, name)))).toBe(false);
      expect(sourceIdentity(root, ["apps/server"])).not.toBe(clean);
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  });

  test("retains zero generated Server executables in the repository source directory", () => {
    // Given: the repository Server source directory.
    const repository = resolve(new URL("../../..", import.meta.url).pathname);

    // When: default Go output locations are inspected.
    const retained = generatedExecutableNames.filter((name) => existsSync(join(repository, "apps/server", name)));

    // Then: packaging has retained no generated executable in source.
    expect(retained).toEqual([]);
  });
});
