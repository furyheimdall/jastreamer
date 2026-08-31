import { afterEach, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { reconcileTask19Trust } from "./task19-trust-evidence-generator.mjs";

const roots = [];
afterEach(async () => Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))));
const sha256 = (value) => createHash("sha256").update(value).digest("hex");

describe("Task19 authoritative trust and evidence generator", () => {
  test("derives frozen driver runtime and harness digests from repository bytes", async () => {
    const root = await mkdtemp(join(tmpdir(), "task19-trust-generator-")); roots.push(root); await mkdir(join(root, "task19"));
    for (const [name, bytes] of [["driver.ps1", "driver"], ["runtime.mjs", "runtime"], ["harness.mjs", "harness"], ["scenario.mjs", "scenario"], ["operation.mjs", "operation"], ["inventory.mjs", "inventory"], ["process.mjs", "process"]]) await writeFile(join(root, "task19", name), bytes);
    const trustPath = join(root, "task19/trust.json"); await writeFile(trustPath, `${JSON.stringify({ driver: { path: "task19/driver.ps1", sha256: "0", runtimePath: "task19/runtime.mjs", runtimeSha256: "0" }, qualification: { ready: false, runtime: { harnessPath: "task19/harness.mjs", harnessSha256: "0", scenarioContractPath: "task19/scenario.mjs", scenarioContractSha256: "0", operationAdapterPath: "task19/operation.mjs", operationAdapterSha256: "0", inventoryAdapterPath: "task19/inventory.mjs", inventoryAdapterSha256: "0", processAdapterPath: "task19/process.mjs", processAdapterSha256: "0" } } })}\n`);
    const result = await reconcileTask19Trust({ root, trustPath, write: true }); const trust = JSON.parse(await readFile(trustPath, "utf8"));
    expect(result.changed).toBe(true); expect(trust.driver.sha256).toBe(sha256("driver")); expect(trust.driver.runtimeSha256).toBe(sha256("runtime")); expect(trust.qualification.runtime.harnessSha256).toBe(sha256("harness")); expect(trust.qualification.runtime.scenarioContractSha256).toBe(sha256("scenario")); expect(trust.qualification.runtime.operationAdapterSha256).toBe(sha256("operation")); expect(trust.qualification.runtime.inventoryAdapterSha256).toBe(sha256("inventory")); expect(trust.qualification.runtime.processAdapterSha256).toBe(sha256("process")); expect(trust.qualification.ready).toBe(false);
  });
});
