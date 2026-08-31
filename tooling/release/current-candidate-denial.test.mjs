import { expect, test } from "bun:test";
import { resolve } from "node:path";

const repository = resolve(import.meta.dirname, "../..");

test("accepts the authoritative pending receipt when production gate roots are unavailable", () => {
  // Given: the current local candidate has no authenticated production gate directory.
  // When: the current-candidate denial verifier runs through its public CLI.
  const result = Bun.spawnSync(["node", "tooling/release/current-candidate-denial.mjs"], { cwd: repository, stdout: "pipe", stderr: "pipe" });

  // Then: the verifier authenticates the local pending boundary without executing product writes.
  expect(result.exitCode).toBe(0);
  expect(JSON.parse(result.stdout.toString())).toEqual(expect.objectContaining({ denied: "SIGNED_MSIX_REQUIRED", productCommandsExecuted: 0, externalMutations: 0 }));
});
