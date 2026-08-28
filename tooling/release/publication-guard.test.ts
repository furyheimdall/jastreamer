import { describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(new URL("../..", import.meta.url).pathname);
const components = ["server", "control", "renderer"] as const;
const externalWritePatterns = [
  /\bgh release (?:create|upload|edit|delete)\b/,
  /\bskopeo (?:copy|delete)\b/,
  /\bdocker (?:login|push)\b/,
  /\bgit (?:tag|push)\b/,
] as const;

describe("publication workflow guard", () => {
  test("keeps default denial while exposing writes only behind the complete Todo 22 gate", () => {
    // Given: every public-facing component workflow.
    const workflows = components.map((component) =>
      readFileSync(join(root, `.github/workflows/${component}-release.yml`), "utf8"),
    );

    // When: shell-level publication commands and job permission boundaries are enumerated.
    const externalWrites = workflows.flatMap((workflow) =>
      externalWritePatterns.filter((pattern) => pattern.test(workflow)).map(String),
    );

    // Then: provider writes stay inside the typed driver and only Server/Control final jobs can invoke it.
    expect(externalWrites).toEqual([]);
    for (const workflow of workflows) expect(workflow).toContain("workflow_dispatch:");
    for (const workflow of workflows.slice(0, 2)) {
      const publishAt = workflow.indexOf("  publish-qualified:");
      const handoff = workflow.slice(workflow.indexOf("  product-qualification-handoff:"), publishAt);
      const candidate = workflow.slice(0, publishAt);
      const publish = workflow.slice(publishAt);
      expect(handoff).toContain("PRODUCT_QUALIFICATION_RUN_ID");
      expect(handoff).toContain("PRODUCT_GATE_ARTIFACT_ID");
      expect(handoff).toContain("PRODUCT_GATE_ARTIFACT_DIGEST");
      expect(handoff).toContain("product-gate-production-trust-v1.json");
      expect(candidate).not.toMatch(/^\s+contents:\s*write\s*$/m);
      expect(candidate).not.toMatch(/^\s+packages:\s*write\s*$/m);
      expect(publish).toContain("publication-cli.ts");
      expect(publish).toContain("artifact-ids:");
      expect(publish).toContain("run-id:");
      expect(publish).toContain("github.event_name == 'push'");
      expect(publish).toContain("github.ref_type == 'tag'");
      expect(publish).not.toContain("github.event.inputs");
    }
    const renderer = workflows[2];
    if (renderer === undefined) throw new Error("Renderer workflow missing");
    expect(renderer).not.toContain("publish-qualified:");
    expect(renderer).not.toMatch(/^\s+contents:\s*write\s*$/m);
    expect(renderer).not.toMatch(/^\s+packages:\s*write\s*$/m);
  });

  test("keeps Renderer output CI-only", () => {
    // Given: the Renderer candidate workflow.
    const workflow = readFileSync(join(root, ".github/workflows/renderer-release.yml"), "utf8");

    // When: artifact publication destinations are inspected.
    const artifactNames = [...workflow.matchAll(/^\s+name:\s*([^\n]+)$/gm)].map((match) => match[1]?.trim());

    // Then: only explicitly CI-scoped Renderer artifacts are retained.
    expect(artifactNames).not.toContain("renderer-windows-public");
    expect(workflow).toContain("renderer-candidate-ci");
    expect(workflow).toContain("candidate.json");
  });

  test("routes audio qualification exclusively by the default-false authorization output", () => {
    // Given / When
    const workflow = readFileSync(join(root, ".github/workflows/renderer-release.yml"), "utf8");

    // Then
    expect(workflow).toContain("authorize_windows_audio:");
    expect(workflow).toMatch(/authorize_windows_audio:[\s\S]*?default:\s*false/);
    expect(workflow).toContain("windows_audio_authorized: ${{ steps.version.outputs.windows_audio_authorized }}");
    expect(workflow).toContain("windows-audio-qualification-physical:");
    expect(workflow).toContain("runs-on: [self-hosted, windows, x64, jastreamer-audio]");
    expect(workflow).toContain("if: needs.validate.outputs.windows_audio_authorized == 'true'");
    expect(workflow).toContain("if: needs.validate.outputs.windows_audio_authorized != 'true'");
    expect(workflow).toContain("environment: renderer-audio-qa");
    expect(workflow).toContain("JASTREAMER_QA_ENDPOINT_ID: ${{ secrets.JASTREAMER_QA_ENDPOINT_ID }}");
    expect(workflow).toContain("IsNullOrWhiteSpace($env:JASTREAMER_QA_RUNNER_NAME)");
  });

  test("stages and binds the probe, scenario driver, and server peer without rebuilding in the physical job", () => {
    // Given / When
    const workflow = readFileSync(join(root, ".github/workflows/renderer-release.yml"), "utf8");
    const physical = workflow.slice(workflow.indexOf("windows-audio-qualification-physical:"), workflow.indexOf("windows-audio-qualification-pending:"));

    // Then
    expect(workflow).toContain("jastreamer-wasapi-loopback-probe.exe");
    expect(workflow).toContain("windows-audio-scenario-driver.mjs");
    expect(workflow).toContain("windows-audio-server-peer.json");
    expect(workflow).toContain("probe-executable.sha256");
    expect(workflow).toContain("scenario-driver.sha256");
    expect(workflow).toContain("server-peer.sha256");
    expect(workflow).toContain("media-fixtures.sha256");
    expect(workflow).toContain("media-manifest.sha256");
    expect(physical).toContain("-MediaFixturesSha256File stage/media-fixtures.sha256");
    expect(physical).toContain("-MediaManifestSha256File stage/media-manifest.sha256");
    expect(physical).toContain("provision.ps1");
    expect(physical).not.toMatch(/cargo (?:build|run)|go build|bun build/);
  });

  test("keeps an exact workflow-dispatch candidate non-promotable without external writes", async () => {
    // Given: a synthetic exact staged manifest.
    const directory = mkdtempSync(join(tmpdir(), "publication-candidate-"));
    const manifest = join(directory, "manifest.json");
    const output = join(directory, "receipt.json");
    writeFileSync(manifest, JSON.stringify({
      component: "server",
      source_revision: "0123456789abcdef",
      artifacts: [{ name: "server.tar", sha256: "a".repeat(64) }],
    }));

    try {
      // When: the local workflow harness evaluates a manual candidate event.
      const child = Bun.spawn([
        "bun", "tooling/release/publication-guard-cli.ts",
        "--component", "server", "--event", "workflow_dispatch",
        "--manifest", manifest, "--output", output,
      ], { cwd: root, stdout: "pipe", stderr: "pipe" });
      const code = await child.exited;

      // Then: dispatch remains a staged denial and can never authorize promotion.
      expect(code).toBe(65);
      expect(JSON.parse(readFileSync(output, "utf8"))).toEqual(expect.objectContaining({
        candidate: expect.objectContaining({ status: "blocked", component: "server" }),
        code: "NON_PROMOTABLE_EVENT", external_writes: [],
      }));
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  test("authorizes only the component manifest bound by a verified gate result", async () => {
    // Given: exact gate bytes, exact staged manifest bytes, and verifier output bound to both.
    const directory = mkdtempSync(join(tmpdir(), "publication-authorized-"));
    const manifest = join(directory, "manifest.json");
    const gate = join(directory, "product-gate.json");
    const verified = join(directory, "verified.json");
    const output = join(directory, "authorization.json");
    const stageRoot = join(directory, "stage"); mkdirSync(stageRoot); const artifact = join(stageRoot, "server.tar"); writeFileSync(artifact, "exact staged server bytes");
    const artifactSha256 = createHash("sha256").update(readFileSync(artifact)).digest("hex");
    writeFileSync(manifest, JSON.stringify({ component: "server", source_revision: "0123456789abcdef", artifacts: [{ name: "server.tar", sha256: artifactSha256 }] }));
    writeFileSync(gate, "exact signed gate bytes");
    const gateSha256 = createHash("sha256").update(readFileSync(gate)).digest("hex");
    const manifestSha256 = createHash("sha256").update(readFileSync(manifest)).digest("hex");
    writeFileSync(verified, JSON.stringify({
      schemaVersion: 1, kind: "product_gate_verification", status: "authorized", ok: true,
      productGateSha256: gateSha256, candidateManifests: { server: manifestSha256, control: "b".repeat(64) },
      selection: [{ kind: "server-test", path: "stage/server/server.tar", sha256: artifactSha256 }], rebuild: false, externalMutations: 0, rendererPublicAssets: [],
    }));
    try {
      // When: publication guard consumes the exact verifier output.
      const child = Bun.spawn(["bun", "tooling/release/publication-guard-cli.ts", "--component", "server", "--event", "push", "--manifest", manifest, "--stage-root", stageRoot, "--output", output, "--product-gate-receipt", gate, "--verified-product-gate", verified, "--expected-product-gate-sha256", gateSha256], { cwd: root, stdout: "pipe", stderr: "pipe" });
      const code = await child.exited;

      // Then: authorization names the exact candidate and still performs no external write.
      expect(code).toBe(0);
      expect(JSON.parse(readFileSync(output, "utf8"))).toEqual(expect.objectContaining({ candidate: expect.objectContaining({ status: "authorized", manifest_sha256: manifestSha256 }), product_gate_sha256: gateSha256, external_writes: [] }));
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });

  test.each(["missing", "altered", "symlink", "extra"]) ("rejects %s final stage bytes independently of the manifest hash", async (mode) => {
    const directory = mkdtempSync(join(tmpdir(), "publication-stage-bytes-"));
    try {
      const stageRoot = join(directory, "stage"); mkdirSync(stageRoot); const artifact = join(stageRoot, "server.tar"); writeFileSync(artifact, "certified"); const artifactSha256 = createHash("sha256").update(readFileSync(artifact)).digest("hex");
      const manifest = join(directory, "manifest.json"); writeFileSync(manifest, JSON.stringify({ component: "server", source_revision: "rev", artifacts: [{ name: "server.tar", sha256: artifactSha256 }] }));
      const gate = join(directory, "gate.json"); writeFileSync(gate, "gate"); const gateSha256 = createHash("sha256").update(readFileSync(gate)).digest("hex");
      const verified = join(directory, "verified.json"); writeFileSync(verified, JSON.stringify({ schemaVersion: 1, kind: "product_gate_verification", status: "authorized", productGateSha256: gateSha256, candidateManifests: { server: createHash("sha256").update(readFileSync(manifest)).digest("hex"), control: "b".repeat(64) }, selection: [{ kind: "server-test", path: "bound/server.tar", sha256: artifactSha256 }], rebuild: false, externalMutations: 0, rendererPublicAssets: [] }));
      if (mode === "missing") rmSync(artifact); if (mode === "altered") writeFileSync(artifact, "altered"); if (mode === "symlink") { rmSync(artifact); symlinkSync(gate, artifact); } if (mode === "extra") writeFileSync(join(stageRoot, "extra.bin"), "extra");
      const output = join(directory, "out.json"); const child = Bun.spawn(["bun", "tooling/release/publication-guard-cli.ts", "--component", "server", "--event", "push", "--manifest", manifest, "--stage-root", stageRoot, "--output", output, "--product-gate-receipt", gate, "--verified-product-gate", verified, "--expected-product-gate-sha256", gateSha256], { cwd: root, stdout: "pipe", stderr: "pipe" });
      expect(await child.exited).toBe(65); expect(JSON.parse(readFileSync(output, "utf8")).code).toBe("PRODUCT_GATE_INVALID");
    } finally { rmSync(directory, { recursive: true, force: true }); }
  });

  test("returns typed default-deny failures across the adversarial tag matrix", async () => {
    // Given: an exact candidate manifest and a receipt whose digest cannot authorize publication.
    const directory = mkdtempSync(join(tmpdir(), "publication-adversarial-"));
    const manifest = join(directory, "manifest.json");
    const receipt = join(directory, "product-gate.json");
    writeFileSync(manifest, JSON.stringify({
      component: "control",
      source_revision: "0123456789abcdef",
      artifacts: [{ name: "control.zip", sha256: "b".repeat(64) }],
    }));
    writeFileSync(receipt, "{}");
    const cases: readonly {
      readonly code: "PRODUCT_GATE_REQUIRED" | "PRODUCT_GATE_MISMATCH";
      readonly extra: readonly string[];
    }[] = [
      { code: "PRODUCT_GATE_REQUIRED", extra: [] },
      { code: "PRODUCT_GATE_MISMATCH", extra: ["--product-gate-receipt", receipt, "--expected-product-gate-sha256", "0".repeat(64)] },
    ];

    try {
      for (const [index, item] of cases.entries()) {
        // When: tag publication is attempted without an installed exact Todo 22 gate.
        const output = join(directory, `blocked-${index}.json`);
        const child = Bun.spawn([
          "bun", "tooling/release/publication-guard-cli.ts",
          "--component", "control", "--event", "push",
          "--manifest", manifest, "--output", output, ...item.extra,
        ], { cwd: root, stdout: "pipe", stderr: "pipe" });
        const [code, stderr] = await Promise.all([child.exited, new Response(child.stderr).text()]);

        // Then: the typed failure is emitted before any external write.
        expect(code).toBe(65);
        expect(stderr.trim()).toBe(item.code);
        expect(JSON.parse(readFileSync(output, "utf8"))).toEqual(expect.objectContaining({
          code: item.code,
          external_writes: [],
        }));
      }
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  });
});
