import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, test } from "bun:test";

const repository = resolve(import.meta.dirname, "../../..");
const releaseInputs = [
  ".github/workflows/server-release.yml",
  ".github/workflows/control-release.yml",
  ".github/workflows/product-qualification-dispatch.yml",
  ".github/workflows/task19-installed-qualification.yml",
  "tooling/release/product-gate-production-trust-v1.json",
  "tooling/qa/task19/task19-production-trust-v1.json",
  "tooling/qa/task19/installed-runner.mjs",
];

describe("Task19 remediation evidence byte bindings", () => {
  test("never treats ignored local remediation records as release authorization", () => {
    const inputs = releaseInputs.map(path => readFileSync(resolve(repository, path), "utf8")).join("\n");
    const productTrust = JSON.parse(readFileSync(
      resolve(repository, "tooling/release/product-gate-production-trust-v1.json"),
      "utf8",
    ));
    const task19Trust = JSON.parse(readFileSync(
      resolve(repository, "tooling/qa/task19/task19-production-trust-v1.json"),
      "utf8",
    ));

    expect(inputs).not.toContain(".omo/evidence");
    expect(readFileSync(resolve(repository, ".gitignore"), "utf8")).toContain(".omo/evidence/");
    expect(productTrust.artifactSigning.keyIds).toEqual([]);
    expect(task19Trust.qualification.ready).toBe(false);
  });
});
