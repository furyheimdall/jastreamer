#!/usr/bin/env node
import { createHash } from "node:crypto";
import { readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const jsonBytes = (value) => Buffer.from(`${JSON.stringify(value, null, 2)}\n`);
const DEFAULT_IMPLEMENTATION_PATHS = [
  "tooling/qa/task19/evidence-signer.mjs", "tooling/qa/task19/installed-product-harness.mjs", "tooling/qa/task19/scenario-contract.mjs", "tooling/qa/task19/task19-scenario-provisioner.mjs", "tooling/qa/task19/task19-native-capture.mjs", "tooling/qa/task19/task19-capture-sidecar.mjs", "tooling/qa/task19/task19-diagnostic-sanitizer.mjs", "tooling/qa/task19/task19-sidecar-proxy.mjs", "tooling/qa/task19/task19-sidecar-protocol.mjs", "tooling/qa/task19/task19-frame-controller.mjs", "tooling/qa/task19/task19-websocket-diagnostic.mjs", "tooling/qa/task19/task19-web-origin.mjs", "tooling/qa/task19/task19-tls-identity.mjs", "tooling/qa/task19/installed-product-harness.mjs", "tooling/qa/task19/task19-operation-adapter.mjs", "tooling/qa/task19/task19-inventory-adapter.mjs", "tooling/qa/task19/task19-process-adapter.mjs", "tooling/qa/task19/installed-runner-policy.mjs", "tooling/qa/task19/installed-runner.mjs", "tooling/qa/task19/product-e2e-receipt.mjs", "tooling/qa/task19/protected-lifecycle.mjs", "tooling/qa/task19/protected-runner.ps1", "tooling/qa/task19/scenario-driver.ps1", "tooling/qa/task19/scenario-runtime.mjs", "tooling/qa/task19/task19-production-trust-v1.json", "tooling/qa/task19/task19-trust-evidence-generator.mjs",
];
const reference = async (root, path) => ({ path, sha256: sha256(await readFile(resolve(root, path))) });

export const reconcileTask19Trust = async ({ root, trustPath, evidencePath, write = false }) => {
  const repository = resolve(root); const absoluteTrust = resolve(trustPath); const original = await readFile(absoluteTrust); const trust = JSON.parse(original);
  const driverPath = resolve(repository, trust.driver.path); const runtimePath = resolve(repository, trust.driver.runtimePath ?? "tooling/qa/task19/scenario-runtime.mjs"); const runtime = trust.qualification.runtime;
  trust.driver.sha256 = sha256(await readFile(driverPath)); trust.driver.runtimeSha256 = sha256(await readFile(runtimePath));
  for (const pathKey of Object.keys(runtime).filter((key) => key.endsWith("Path"))) { const key = pathKey.slice(0, -4); runtime[`${key}Sha256`] = sha256(await readFile(resolve(repository, runtime[pathKey]))); }
  const trustBytes = jsonBytes(trust); let changed = !original.equals(trustBytes); if (write && changed) await writeFile(absoluteTrust, trustBytes);
  if (evidencePath !== undefined) {
    const absoluteEvidence = resolve(evidencePath); const evidence = JSON.parse(await readFile(absoluteEvidence)); const current = new Map(evidence.hashManifest.implementationFiles.filter((item) => !/^tooling\/qa\/task19\/fixtures\/task19-web-origin-(?:key|cert)\.pem$/.test(item.path)).map((item) => [item.path, item])); for (const path of DEFAULT_IMPLEMENTATION_PATHS) current.set(path, await reference(repository, path));
    evidence.hashManifest.implementationFiles = await Promise.all([...current.keys()].sort().map((path) => reference(repository, path)));
    for (const group of ["priorEvidence", "candidateRecords"]) evidence.hashManifest[group] = await Promise.all(evidence.hashManifest[group].map((item) => reference(repository, item.path)));
    const evidenceBytes = jsonBytes(evidence); const evidenceOriginal = await readFile(absoluteEvidence); changed ||= !evidenceOriginal.equals(evidenceBytes); if (write && !evidenceOriginal.equals(evidenceBytes)) await writeFile(absoluteEvidence, evidenceBytes);
  }
  return { changed, driverSha256: trust.driver.sha256, runtimeSha256: trust.driver.runtimeSha256, harnessSha256: trust.qualification.runtime.harnessSha256 };
};

if (process.argv[1] && resolve(process.argv[1]) === new URL(import.meta.url).pathname) {
  const mode = process.argv[2]; if (mode !== "--write" && mode !== "--check") throw new Error("TASK19_TRUST_GENERATOR_USAGE"); const root = resolve(dirname(new URL(import.meta.url).pathname), "../../.."); const result = await reconcileTask19Trust({ root, trustPath: resolve(root, "tooling/qa/task19/task19-production-trust-v1.json"), evidencePath: resolve(root, ".omo/evidence/functional-jastreamer-products/final/task19-verifier-blocker-remediation.json"), write: mode === "--write" }); process.stdout.write(`${JSON.stringify(result)}\n`); if (mode === "--check" && result.changed) process.exitCode = 1;
}
