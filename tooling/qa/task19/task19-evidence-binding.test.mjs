import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, test } from "bun:test";

const repository = resolve(import.meta.dirname, "../../.."); const evidencePath = resolve(repository, ".omo/evidence/functional-jastreamer-products/final/task19-verifier-blocker-remediation.json"); const sha256 = (path) => createHash("sha256").update(readFileSync(resolve(repository, path))).digest("hex");

describe("Task19 remediation evidence byte bindings", () => {
  test("binds current HEAD implementation prior evidence and candidate records", () => {
    const evidence = JSON.parse(readFileSync(evidencePath, "utf8")); const manifest = evidence.hashManifest;
    expect(manifest.algorithm).toBe("sha256"); expect(manifest.head).toBe(execFileSync("git", ["-C", repository, "rev-parse", "HEAD"], { encoding: "utf8" }).trim());
    for (const key of ["implementationFiles", "priorEvidence", "candidateRecords"]) { expect(manifest[key].length).toBeGreaterThan(0); for (const item of manifest[key]) expect(sha256(item.path), item.path).toBe(item.sha256); }
    expect(manifest.implementationFiles.map((item) => item.path)).toEqual(expect.arrayContaining(["tooling/release/task19-physical-provider-cli.ts", "tooling/qa/task19/protected-runner.ps1", "tooling/qa/task19/windows-snapshot-acl.mjs"]));
    expect(manifest.priorEvidence.map((item) => item.path)).toContain(".omo/evidence/functional-jastreamer-products/final/task19-security-core-remediation.json"); expect(manifest.candidateRecords.map((item) => item.path)).toContain(".omo/evidence/functional-jastreamer-products/final/stage-exact-server-control-candidates.json");
  });
});
