import { describe, expect, test } from "bun:test";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { distributables, finalize } from "../tooling/finalize";

const root = resolve(new URL("../../..", import.meta.url).pathname);

const withTemporaryDirectory = async (testBody: (directory: string) => void | Promise<void>): Promise<void> => {
  const directory = mkdtempSync(join(tmpdir(), "control-release-test-"));
  try {
    await testBody(directory);
  } finally {
    rmSync(directory, { force: true, recursive: true });
  }
};

const run = async (args: readonly string[]): Promise<Readonly<{ code: number; stdout: string; stderr: string }>> => {
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

describe("Control release dry-run", () => {
  test("finalizes exactly the public Control allowlist without publication", async () => withTemporaryDirectory(async (directory) => {
    const output = join(directory, "release");
    mkdirSync(output);
    const records = [
      "Android-CERT-SHA256.txt",
      "Apache-2.0.txt",
      "THIRD_PARTY_NOTICES",
      "android-upgrade-inspection.json",
      "control-windows.cer",
      "Windows-CERT-SHA256.txt",
      "trust.md",
      "remove-trust.md",
    ];
    for (const name of [...distributables("1.2.3"), ...records]) {
      writeFileSync(join(output, name), name);
    }

    finalize(output, "1.2.3", "control-v1.2.3", "sha256:0123456789abcdef");

    const manifest = JSON.parse(readFileSync(join(output, "manifest.json"), "utf8")) as {
      readonly component: string;
      readonly sourceRevision: string;
      readonly publicArtifacts: readonly string[];
      readonly publishReachable: boolean;
    };
    expect(manifest.component).toBe("control");
    expect(manifest.sourceRevision).toBe("sha256:0123456789abcdef");
    expect(manifest.publicArtifacts).toEqual([
      "jastreamer-control_1.2.3_android_universal.apk",
      "jastreamer-control_1.2.3_web.zip",
      "jastreamer-control_1.2.3_windows.msix",
    ]);
    expect(manifest.publishReachable).toBe(false);
    expect(readdirSync(output).some((name) => name.endsWith(".aab"))).toBe(false);
  }));

  test("rejects changed signing lineage and public AAB atomically", async () => withTemporaryDirectory(async (directory) => {
    const output = join(directory, "release");
    writeFileSync(output, "immutable");
    const before = readFileSync(output);
    const result = await run([
      "--component", "control",
      "--tag", "control-v1.2.3",
      "--no-publish",
      "--scenario", "android-in-place-upgrade",
      "--output", output,
      "--fixture", "tooling/fixtures/releases/invalid/control-key-change-and-aab-publication.json",
    ]);
    expect(result.code).toBe(65);
    expect(result.stderr).toContain("FORBIDDEN_AAB_ASSET");
    expect(result.stderr).toContain("SIGNING_LINEAGE_CHANGED");
    expect(result.stderr).toContain('"server":"passed"');
    expect(result.stderr).toContain('"renderer":"passed"');
    expect(readFileSync(output)).toEqual(before);
  }));

  test("workflow builds exact candidates and gates the three-asset publication job", () => {
    // Given: the complete Control candidate workflow.
    const workflow = readFileSync(join(root, ".github/workflows/control-release.yml"), "utf8");

    // When: build and publication surfaces are inspected.
    const stage = workflow.slice(workflow.indexOf("  stage:"));

    // Then: real signed builds remain and only the protected typed driver can promote them.
    expect(workflow).not.toContain("placeholder");
    expect(workflow).toContain("github.repository == 'furyheimdall/jastreamer'");
    expect(workflow).toContain("persist-credentials: false");
    expect(workflow).toContain("flutter build web");
    expect(workflow).toContain("flutter build apk");
    expect(workflow).toContain("flutter build appbundle");
    expect(workflow).toContain("MakeAppx");
    expect(workflow).toContain("SignTool");
    expect(workflow).toContain("if: always()");
    expect(workflow).not.toContain("  promote:");
    expect(workflow).not.toContain("gh release");
    expect(workflow).toContain("  publish-qualified:");
    expect(workflow).toContain("environment: product-promotion");
    expect(workflow).toContain("publication-cli.ts");
    expect(workflow).toContain("control-publication-stage");
    expect(stage).not.toContain("CONTROL_ANDROID_");
    expect(stage).not.toContain("CONTROL_WINDOWS_");
    expect(stage).not.toContain("control-aab-validation");
    expect(stage).toContain("control-candidate-ci");
    expect(stage).toContain("external_writes:[]");
  });
});
