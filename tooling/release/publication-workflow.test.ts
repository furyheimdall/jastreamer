import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(new URL("../..", import.meta.url).pathname);
const workflow = (component: "server" | "control" | "renderer"): string =>
  readFileSync(join(root, `.github/workflows/${component}-release.yml`), "utf8");
const job = (source: string, name: string, next?: string): string => {
  const start = source.indexOf(`  ${name}:`);
  const end = next === undefined ? source.length : source.indexOf(`  ${next}:`, start + 1);
  return start < 0 || end < 0 ? "" : source.slice(start, end);
};

describe("qualified publication workflow policy", () => {
  test.each(["server", "control"] as const)("publishes %s only from the immutable tag gate", (component) => {
    // Given: the component release workflow.
    const source = workflow(component);

    // When: the final publication job is isolated from candidate jobs.
    const publish = job(source, "publish-qualified");
    const beforePublish = source.slice(0, source.indexOf("  publish-qualified:"));

    // Then: only the final protected job can write and every immutable context is explicit.
    expect(publish).toContain("environment: product-promotion");
    expect(publish).toContain("github.event_name == 'push'");
    expect(publish).toContain("github.ref_type == 'tag'");
    expect(publish).toContain("publication-cli.ts");
    expect(publish).toContain("product-gate-production-trust-v1.json");
    expect(publish).toContain("artifact-ids:");
    expect(publish).toContain("run-id:");
    expect(publish).toMatch(/^\s+contents:\s*write\s*$/m);
    expect(beforePublish).not.toMatch(/^\s+contents:\s*write\s*$/m);
    expect(beforePublish).not.toMatch(/^\s+packages:\s*write\s*$/m);
    expect(publish).not.toContain("workflow_dispatch");
    expect(publish).not.toMatch(/\b(?:go|cargo|flutter|docker) build\b/);
  });

  test("grants package write only to the final Server publication job", () => {
    // Given: all public-facing component workflows.
    const server = workflow("server");
    const control = workflow("control");
    const renderer = workflow("renderer");

    // When: package-write grants and publication jobs are counted.
    const packageWrites = [server, control, renderer]
      .flatMap((source) => [...source.matchAll(/^\s+packages:\s*write\s*$/gm)]);

    // Then: Server owns the sole GHCR grant and Renderer remains CI-only.
    expect(packageWrites).toHaveLength(1);
    expect(job(server, "publish-qualified")).toContain("packages: write");
    expect(job(control, "publish-qualified")).not.toContain("packages: write");
    expect(renderer).not.toContain("publish-qualified:");
    expect(renderer).not.toMatch(/^\s+contents:\s*write\s*$/m);
    expect(renderer).not.toMatch(/^\s+packages:\s*write\s*$/m);
  });
});
