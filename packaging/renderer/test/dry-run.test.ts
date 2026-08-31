import { describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(new URL("../../..", import.meta.url).pathname);

const withTemporaryDirectory = async (testBody: (directory: string) => Promise<void>): Promise<void> => {
  const directory = mkdtempSync(join(tmpdir(), "renderer-release-test-"));
  try {
    await testBody(directory);
  } finally {
    rmSync(directory, { force: true, recursive: true });
  }
};

const run = async (
  args: readonly string[],
): Promise<Readonly<{ code: number; stdout: string; stderr: string }>> => {
  const child = Bun.spawn(["./tooling/componentctl", "release", "dry-run", ...args], {
    cwd: root,
    stdout: "pipe",
    stderr: "pipe",
  });
  const [code, stdout, stderr] = await Promise.all([
    child.exited,
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]);
  return { code, stdout, stderr };
};

describe("Renderer release dry-run", () => {
  test("keeps package protocol metadata aligned with the runtime hello", () => {
    const config = JSON.parse(
      readFileSync(join(root, "packaging/renderer/config.json"), "utf8"),
    );
    const cli = readFileSync(
      join(root, "packaging/renderer/tooling/cli.ts"),
      "utf8",
    );
    const runtime = readFileSync(
      join(root, "apps/renderer/src/session_messages.rs"),
      "utf8",
    );

    expect(config.protocol.supportedMajors).toEqual([3, 2]);
    expect(cli).toContain("new Set([3, 2])");
    expect(runtime).toContain("supported_majors: [3, 2]");
  });

  test("rejects an unsupported protocol major atomically", async () => withTemporaryDirectory(async (directory) => {
    const output = join(directory, "release");
    writeFileSync(output, "immutable");
    const before = readFileSync(output);

    const result = await run([
      "--component",
      "renderer",
      "--tag",
      "renderer-v1.2.3",
      "--no-publish",
      "--scenario",
      "clean-windows-vm",
      "--output",
      output,
      "--fixture",
      "packaging/renderer/fixtures/unsupported-protocol-major.json",
    ]);

    expect(result.code).toBe(78);
    expect(result.stderr).toContain("UNSUPPORTED_PROTOCOL_MAJOR");
    expect(readFileSync(output)).toEqual(before);
  }));

  test.skipIf(process.platform === "win32")(
    "refuses to fabricate Windows artifacts on a non-Windows host",
    async () => withTemporaryDirectory(async (directory) => {
      const output = join(directory, "release");

      const result = await run([
        "--component",
        "renderer",
        "--tag",
        "renderer-v1.2.3",
        "--no-publish",
        "--scenario",
        "clean-windows-vm",
        "--output",
        output,
      ]);

      expect(result.code).toBe(69);
      expect(result.stderr).toContain("WINDOWS_RUNNER_REQUIRED");
      expect(existsSync(output)).toBe(false);
    }),
  );

  test("workflow signs the executable before packaging and retains only a CI candidate", () => {
    // Given: the Renderer packaging script and candidate workflow.
    const workflow = readFileSync(join(root, ".github/workflows/renderer-release.yml"), "utf8");
    const release = readFileSync(join(root, "packaging/renderer/release.ps1"), "utf8");

    // When: signing order and publication reachability are inspected.
    const signExecutable = release.indexOf("sign.ps1");
    const buildMsi = release.indexOf("build-msi.ps1");

    // Then: signed outputs stage as CI artifacts and no public promotion exists.
    expect(workflow).toContain("github.repository == 'furyheimdall/jastreamer'");
    expect(signExecutable).toBeGreaterThan(-1);
    expect(buildMsi).toBeGreaterThan(signExecutable);
    expect(workflow).not.toContain("  promote:");
    expect(workflow).not.toContain("gh release");
    expect(workflow).toContain("renderer-candidate-ci");
    expect(workflow).toContain('retention:"ci-artifact-only"');
    expect(workflow).not.toContain("placeholder");
  });
});
