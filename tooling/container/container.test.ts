import { describe, expect, test } from "bun:test";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { GateError, UsageError, parseArgs, scanContext } from "./args";
import { classifyDescriptors } from "./oci";
import { publishAtomically } from "./qa";
import { cleanupTargets } from "./runtime";
import type { Descriptor } from "./types";

function rootFixture(): string {
  const root = `${process.env.TMPDIR ?? "/tmp"}/container-test-${crypto.randomUUID()}`;
  mkdirSync(join(root, "apps/server"), { recursive: true }); mkdirSync(join(root, "packaging/container"), { recursive: true }); mkdirSync(join(root, "deploy"), { recursive: true });
  writeFileSync(join(root, "LICENSE"), "Apache-2.0\n");
  writeFileSync(join(root, "packaging/container/health-entrypoint.sh"), "#!/bin/sh\n");
  writeFileSync(join(root, "apps/server/VERSION"), "1.2.3\n"); writeFileSync(join(root, "apps/server/Dockerfile"), "COPY apps/server /src\n");
  writeFileSync(join(root, "apps/server/.dockerignore"), "apps/control\napps/renderer\n"); writeFileSync(join(root, "apps/server/Dockerfile.dockerignore"), "apps/control\napps/renderer\n");
  writeFileSync(join(root, "deploy/compose.yaml"), "services: {}\n"); return root;
}
describe("argument parser and context gate", () => {
  test("accepts only the exact platform and scenario", () => {
    const root = rootFixture(); const args = ["--platform", "linux/amd64,linux/arm64", "--compose", "deploy/compose.yaml", "--scenario", "replacement-persistence", "--oci-layout", "/tmp/x.oci", "--output", "/tmp/x.json"];
    expect(parseArgs(args, root).version).toBe("1.2.3"); expect(() => parseArgs(args.with(1, "linux/arm64"), root)).toThrow(UsageError);
  });
  test("rejects a forbidden fixture before build", () => {
    const root = rootFixture(); mkdirSync(join(root, "fixture/flutter_assets"), { recursive: true }); writeFileSync(join(root, "fixture/flutter_assets/AssetManifest.json"), "{}");
    expect(() => scanContext(root, "fixture")).toThrow(new GateError("FORBIDDEN_CONTROL_ASSET"));
  });
});
test("index classification requires exactly two runnable target manifests", () => {
  const descriptor = (architecture: string, attestation = false): Descriptor => ({ mediaType: "application/vnd.oci.image.manifest.v1+json", digest: `sha256:${architecture.padEnd(64, "0")}`, size: 1,
    platform: { os: attestation ? "unknown" : "linux", architecture: attestation ? "unknown" : architecture }, annotations: attestation ? { "vnd.docker.reference.type": "attestation-manifest" } : undefined });
  expect(classifyDescriptors([descriptor("amd64"), descriptor("arm64"), descriptor("attestation", true)]).runnable).toHaveLength(2);
  expect(() => classifyDescriptors([descriptor("arm64")])).toThrow("RUNNABLE_MANIFEST_SET_INVALID");
});
test("publication rolls back every existing output on a promotion error", () => {
  const root = rootFixture(); const one = join(root, "one"); const two = join(root, "two"); const staged = join(root, "staged");
  writeFileSync(one, "old-one"); writeFileSync(two, "old-two"); writeFileSync(staged, "new-one");
  expect(() => publishAtomically([{ staged, final: one }, { staged: join(root, "missing"), final: two }])).toThrow();
  expect(readFileSync(one, "utf8")).toBe("old-one"); expect(readFileSync(two, "utf8")).toBe("old-two");
});
test("publication rolls back when final cleanup fails", () => {
  const root = rootFixture(); const final = join(root, "final"); const staged = join(root, "staged");
  writeFileSync(final, "old"); writeFileSync(staged, "new");
  expect(() => publishAtomically([{ staged, final }], () => { throw new Error("cleanup failed"); })).toThrow("cleanup failed");
  expect(readFileSync(final, "utf8")).toBe("old");
});
test("cleanup is scoped to names and Compose projects created by this run", () => {
  const commands = cleanupTargets(new Set(["jastreamer-task17-owned"]), new Set(["jastreamert17123"]), "/compose.yaml");
  expect(commands).toEqual([["rm", "-f", "jastreamer-task17-owned"], ["ps", "-aq", "--filter", "label=com.docker.compose.project=jastreamert17123"], ["compose", "-p", "jastreamert17123", "-f", "/compose.yaml", "down", "--remove-orphans"]]);
  expect(JSON.stringify(commands)).not.toContain("unrelated");
});
