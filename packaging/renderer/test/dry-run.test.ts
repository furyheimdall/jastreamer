import { afterEach, describe, expect, test } from "bun:test";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(new URL("../../..", import.meta.url).pathname);
const temporaryDirectories: string[] = [];

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) {
    rmSync(directory, { force: true, recursive: true });
  }
});

const temporaryDirectory = (): string => {
  const directory = mkdtempSync(join(tmpdir(), "renderer-release-test-"));
  temporaryDirectories.push(directory);
  return directory;
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
  test("rejects an unsupported protocol major atomically", async () => {
    const output = join(temporaryDirectory(), "release");
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
  });

  test.skipIf(process.platform === "win32")(
    "refuses to fabricate Windows artifacts on a non-Windows host",
    async () => {
      const output = join(temporaryDirectory(), "release");

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
    },
  );

  test("workflow signs the executable before packaging and publishes staged assets", () => {
    const workflow = readFileSync(join(root, ".github/workflows/renderer-release.yml"), "utf8");
    const release = readFileSync(join(root, "packaging/renderer/release.ps1"), "utf8");
    const signExecutable = release.indexOf("sign.ps1");
    const buildMsi = release.indexOf("build-msi.ps1");
    const promotion = workflow.indexOf("  promote:");

    expect(workflow).toContain("github.repository == 'furyheimdall/jastreamer'");
    expect(signExecutable).toBeGreaterThan(-1);
    expect(buildMsi).toBeGreaterThan(signExecutable);
    expect(promotion).toBeGreaterThan(-1);
    expect(workflow.slice(promotion)).toContain("gh release create");
    expect(workflow).not.toContain("placeholder");
  });
});
