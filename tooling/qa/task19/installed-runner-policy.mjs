import { createHash } from "node:crypto";
import { lstatSync, mkdirSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { secureWindowsSnapshot } from "./windows-snapshot-acl.mjs";

const SHA256 = /^[0-9a-f]{64}$/;
const REVISION = /^[0-9a-f]{40}$/;
const POSITIVE_ID = /^[1-9][0-9]*$/;
const PLATFORMS = ["web", "windows", "android"];
const ORDERS = ["server_first", "control_first"];
const failure = (code, path) => ({ ok: false, code, path });
const digest = (bytes) => createHash("sha256").update(bytes).digest("hex");
const exact = (value, keys, code, path) => {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return failure(code, path);
  const actual = Object.keys(value).sort();
  return JSON.stringify(actual) === JSON.stringify([...keys].sort()) ? undefined : failure(code, path);
};

const readBound = (root, reference, path) => {
  if (exact(reference, ["kind", "path", "sha256", "size"], "CANDIDATE_REFERENCE_INVALID", path)
      || typeof reference.path !== "string" || !SHA256.test(reference.sha256)
      || !Number.isSafeInteger(reference.size) || reference.size < 1 || isAbsolute(reference.path)
      || reference.path.split(/[\\/]/).includes("..")) return { issue: failure("CANDIDATE_REFERENCE_INVALID", path) };
  const absolute = resolve(root, reference.path);
  const inside = relative(root, absolute);
  if (inside === "" || inside === ".." || inside.startsWith(`..${sep}`) || isAbsolute(inside)) return { issue: failure("CANDIDATE_REFERENCE_INVALID", path) };
  let current = realpathSync(root);
  for (const part of inside.split(sep)) {
    current = resolve(current, part);
    let metadata;
    try { metadata = lstatSync(current); } catch { return { issue: failure("CANDIDATE_FILE_MISSING", path) }; }
    if (metadata.isSymbolicLink()) return { issue: failure("CANDIDATE_REPARSE_POINT_REJECTED", path) };
  }
  const bytes = readFileSync(absolute);
  return bytes.length === reference.size && digest(bytes) === reference.sha256
    ? { absolute, bytes, reference }
    : { issue: failure("CANDIDATE_DIGEST_MISMATCH", path) };
};

const processPlan = (files, platform) => {
  const controlKey = `control${platform[0].toUpperCase()}${platform.slice(1)}`; const control = files[controlKey];
  const controls = {
    web: { installCommand: ["unzip", control.path], packageArgumentIndex: 1, launchCommand: ["chromium", "app-mode"] },
    windows: { installCommand: ["Add-AppxPackage", control.path], packageArgumentIndex: 1, launchCommand: ["jastreamer-control"] },
    android: { installCommand: ["adb", "install", control.path], packageArgumentIndex: 2, launchCommand: ["adb", "shell", "am", "start"] },
  };
  return [
    { role: "server", packageKey: "server", packagePath: files.server.path, installCommand: ["wsl.exe", "--exec", "sudo", "dpkg", "-i", files.server.path], packageArgumentIndex: 5, launchCommand: ["wsl.exe", "--exec", "systemctl", "start", "jastreamer-server.service"] },
    { role: `control-${platform}`, packageKey: controlKey, packagePath: control.path, ...controls[platform] },
    { role: "renderer", packageKey: "renderer", packagePath: files.renderer.path, installCommand: ["msiexec", "/i", files.renderer.path], packageArgumentIndex: 2, launchCommand: ["jastreamer-renderer"] },
  ];
};

const receiptFromRoots = (manifest, trust) => {
  const qualification = trust.qualification; const source = qualification.source; const bindings = qualification.expectedBindings; const contracts = qualification.contracts; const peers = qualification.peers;
  if (!source || !bindings || !Array.isArray(contracts) || contracts.length !== 2 || !Array.isArray(peers) || peers.length !== 3) return undefined;
  const roles = [["server", "server"], ["controlWeb", "control-web"], ["controlWindows", "control-windows"], ["controlAndroid", "control-android"], ["renderer", "renderer"]];
  const artifacts = roles.map(([key, role]) => ({ role, candidateComponent: role.startsWith("control-") ? "control" : role, path: manifest.files[key].path, sha256: manifest.files[key].sha256 }));
  return { source, bindings, artifacts, contracts, peers };
};

export const buildQualificationPlan = (manifest, trust, snapshotRoot) => ({
  schemaVersion: 2,
  kind: "task19_trusted_execution_plan",
  qualificationReady: trust.qualification.ready,
  sourceRevision: manifest.source.revision,
  snapshotRoot,
  driver: { ...trust.driver, ...trust.qualification.runtime },
  receipt: receiptFromRoots(manifest, trust),
  identities: trust.identities,
  scenarioCount: 30,
  probeCount: 7,
  files: manifest.files,
  receipts: manifest.receipts,
  stagedManifest: manifest.stagedManifest,
  runs: PLATFORMS.flatMap((platform) => ORDERS.map((startupOrder) => ({
    runId: `task19-${platform}-${startupOrder.replace("_", "-")}`,
    platform,
    startupOrder,
    processes: processPlan(manifest.files, platform),
  }))),
});

const repositoryRoot = resolve(dirname(new URL(import.meta.url).pathname), "../../..");
const productionTrustPath = resolve(repositoryRoot, "tooling/qa/task19/task19-production-trust-v1.json");
export const loadProductionTrust = () => {
  const trust = JSON.parse(readFileSync(productionTrustPath));
  if (trust.schemaVersion !== 1 || trust.kind !== "task19_production_trust" || trust.repository !== "furyheimdall/jastreamer") throw new Error("TASK19_PRODUCTION_TRUST_INVALID");
  const driver = readFileSync(resolve(repositoryRoot, trust.driver.path)); const runtime = readFileSync(resolve(repositoryRoot, "tooling/qa/task19/scenario-runtime.mjs"));
  if (!SHA256.test(trust.driver.sha256) || digest(driver) !== trust.driver.sha256) throw new Error("TASK19_REPOSITORY_DRIVER_DIGEST_MISMATCH");
  if (!SHA256.test(trust.driver.runtimeSha256) || digest(runtime) !== trust.driver.runtimeSha256) throw new Error("TASK19_SCENARIO_RUNTIME_DIGEST_MISMATCH");
  const runtimeBindings = [["harness", "TASK19_SCENARIO_HARNESS_DIGEST_MISMATCH"], ["scenarioContract", "TASK19_SCENARIO_CONTRACT_DIGEST_MISMATCH"], ["scenarioProvisioner", "TASK19_SCENARIO_PROVISIONER_DIGEST_MISMATCH"], ["nativeCapture", "TASK19_NATIVE_CAPTURE_DIGEST_MISMATCH"], ["webOrigin", "TASK19_WEB_ORIGIN_DIGEST_MISMATCH"], ["tlsIdentityGenerator", "TASK19_TLS_IDENTITY_GENERATOR_DIGEST_MISMATCH"], ["operationAdapter", "TASK19_OPERATION_ADAPTER_DIGEST_MISMATCH"], ["inventoryAdapter", "TASK19_INVENTORY_ADAPTER_DIGEST_MISMATCH"], ["processAdapter", "TASK19_PROCESS_ADAPTER_DIGEST_MISMATCH"]];
  for (const [key, code] of runtimeBindings) { const path = trust.qualification?.runtime?.[`${key}Path`]; const sha256 = trust.qualification?.runtime?.[`${key}Sha256`]; if (typeof path !== "string" || !SHA256.test(sha256) || digest(readFileSync(resolve(repositoryRoot, path))) !== sha256) throw new Error(code); }
  if (JSON.stringify(trust.qualification.runtime.tlsConstraints) !== JSON.stringify({ kind: "task19-run-ephemeral-tls", originHost: "loopback_dns", originPort: "ephemeral", minimumTlsVersion: "TLSv1.3", privateKeyPersistence: "protected-run-only", browserTrust: "exact-spki" })) throw new Error("TASK19_TLS_CONSTRAINTS_INVALID");
  return trust;
};

export const validateCandidateManifest = (manifestPath, expectedRevision) => {
  let manifest;
  try { manifest = JSON.parse(readFileSync(manifestPath)); } catch { return failure("CANDIDATE_MANIFEST_INVALID", "candidates"); }
  if (manifest?.schemaVersion === 1 && manifest?.task === "Stage exact Server and Control candidates" && manifest?.control?.pendingArtifact?.kind === "control-windows") return failure("SIGNED_MSIX_REQUIRED", "control.pendingArtifact");
  if (manifest?.schemaVersion !== 2 || manifest?.kind !== "task19_exact_candidate_closure") return failure("CANDIDATE_SCHEMA_UNSUPPORTED", "candidates.schemaVersion");
  const top = exact(manifest, ["schemaVersion", "kind", "source", "provider", "producer", "files", "receipts", "stagedManifest"], "CANDIDATE_MANIFEST_INVALID", "candidates"); if (top) return top;
  if (exact(manifest.source, ["revision"], "CANDIDATE_SOURCE_INVALID", "source") || !REVISION.test(manifest.source.revision) || (expectedRevision !== undefined && manifest.source.revision !== expectedRevision)) return failure("CANDIDATE_SOURCE_INVALID", "source.revision");
  const trust = loadProductionTrust();
  if (exact(manifest.producer, ["driverSha256"], "CANDIDATE_PRODUCER_INVALID", "producer") || manifest.producer.driverSha256 !== trust.driver.sha256) return failure("CANDIDATE_PRODUCER_INVALID", "producer.driverSha256");
  const providerKeys = ["repository", "workflowPath", "eventName", "runId", "runAttempt", "headSha", "artifactId", "artifactName", "artifactDigest", "archiveSha256", "size", "createdAt", "expiresAt", "observedAt"];
  if (exact(manifest.provider, providerKeys, "PROVIDER_PROVENANCE_INVALID", "provider") || manifest.provider.repository !== trust.repository || manifest.provider.workflowPath !== trust.providerWorkflowPath || manifest.provider.eventName !== trust.providerEvent || !POSITIVE_ID.test(manifest.provider.runId) || !Number.isSafeInteger(manifest.provider.runAttempt) || manifest.provider.runAttempt < 1 || manifest.provider.headSha !== manifest.source.revision || !POSITIVE_ID.test(manifest.provider.artifactId) || manifest.provider.artifactName !== `${trust.candidateArtifactPrefix}${manifest.provider.runId}-${manifest.provider.runAttempt}` || !SHA256.test(manifest.provider.artifactDigest) || manifest.provider.archiveSha256 !== manifest.provider.artifactDigest || !Number.isSafeInteger(manifest.provider.size) || manifest.provider.size < 1) return failure("PROVIDER_PROVENANCE_INVALID", "provider");
  const observed = Date.parse(manifest.provider.observedAt); if (!["createdAt", "expiresAt"].every((key) => Number.isFinite(Date.parse(manifest.provider[key]))) || Date.parse(manifest.provider.createdAt) > observed + 300_000 || Date.parse(manifest.provider.expiresAt) <= observed) return failure("PROVIDER_ARTIFACT_EXPIRED", "provider.expiresAt");
  const fileKeys = ["server", "controlWeb", "controlWindows", "controlAndroid", "renderer"];
  if (exact(manifest.files, fileKeys, "CANDIDATE_INVENTORY_INVALID", "files")) return failure("CANDIDATE_INVENTORY_INVALID", "files");
  const expectedKinds = ["server-linux-deb", "control-web", "control-windows", "control-android", "renderer-windows-msi"];
  const root = dirname(resolve(manifestPath)); const reads = [];
  for (const [index, key] of fileKeys.entries()) { const reference = manifest.files[key]; if (reference?.kind !== expectedKinds[index]) return failure("CANDIDATE_INVENTORY_INVALID", `files.${key}`); const read = readBound(root, reference, `files.${key}`); if (read.issue) return read.issue; reads.push(read); }
  const receiptKeys = ["k17", "wasapi"];
  if (exact(manifest.receipts, receiptKeys, "PHYSICAL_GATE_RECEIPTS_REQUIRED", "receipts")) return failure("PHYSICAL_GATE_RECEIPTS_REQUIRED", "receipts");
  for (const key of receiptKeys) { const read = readBound(root, manifest.receipts[key], `receipts.${key}`); if (read.issue) return read.issue; let value; try { value = JSON.parse(read.bytes); } catch { return failure("PHYSICAL_GATE_RECEIPT_INVALID", `receipts.${key}`); } if (value.qualification_status !== "qualified") return failure("PHYSICAL_GATE_PENDING", `receipts.${key}`); reads.push(read); }
  const staged = readBound(root, manifest.stagedManifest, "stagedManifest"); if (staged.issue) return staged.issue; reads.push(staged);
  const plan = buildQualificationPlan(manifest, trust, root);
  const roots = trust.qualification;
  const qualificationReady = trust.status === "ready" && roots.ready === true && SHA256.test(trust.identities.msixCertificateSha256) && SHA256.test(trust.identities.apkLineageSha256)
    && roots.k17?.sha256 === manifest.receipts.k17.sha256 && roots.wasapi?.sha256 === manifest.receipts.wasapi.sha256
    && roots.stagedManifest?.sha256 === manifest.stagedManifest.sha256 && roots.driver?.sha256 === trust.driver.sha256
    && plan.receipt !== undefined && ["harness", "scenarioContract", "scenarioProvisioner", "nativeCapture", "webOrigin", "tlsIdentityGenerator", "operationAdapter", "inventoryAdapter", "processAdapter"].every((key) => typeof roots.runtime?.[`${key}Path`] === "string" && SHA256.test(roots.runtime?.[`${key}Sha256`]));
  return { ok: true, root, manifest, trust, reads, plan, qualificationReady };
};

export const snapshotCandidateClosure = (candidate, parent) => {
  const parentPath = resolve(parent); let current = realpathSync(dirname(parentPath));
  for (const part of relative(current, parentPath).split(sep).filter(Boolean)) {
    current = resolve(current, part);
    if (lstatSync(current).isSymbolicLink()) throw new Error("TASK19_SNAPSHOT_REPARSE_POINT_REJECTED");
  }
  if (realpathSync(parentPath) !== parentPath) throw new Error("TASK19_SNAPSHOT_REPARSE_POINT_REJECTED");
  const root = resolve(parentPath, `task19-snapshot-${process.pid}-${crypto.randomUUID()}`);
  mkdirSync(root, { recursive: false });
  secureWindowsSnapshot(root);
  const references = [...Object.values(candidate.manifest.files), ...Object.values(candidate.manifest.receipts), candidate.manifest.stagedManifest];
  const authenticated = Object.fromEntries(candidate.reads.map((read) => [read.reference.kind, read.bytes])); const names = new Set(); const files = {};
  for (const reference of references) {
    const name = basename(reference.path); if (names.has(name)) throw new Error("TASK19_SNAPSHOT_NAME_COLLISION"); names.add(name);
    const bytes = authenticated[reference.kind]; if (!(bytes instanceof Uint8Array) || bytes.length !== reference.size || digest(bytes) !== reference.sha256) throw new Error("TASK19_SNAPSHOT_BINDING_FAILED");
    const target = join(root, name); writeFileSync(target, bytes, { flag: "wx" });
    const immediate = readFileSync(target); if (immediate.length !== reference.size || digest(immediate) !== reference.sha256 || lstatSync(target).isSymbolicLink()) throw new Error("TASK19_SNAPSHOT_BINDING_FAILED"); files[reference.kind] = { ...reference, path: target };
  }
  const manifest = { ...candidate.manifest, files: Object.fromEntries(Object.entries(candidate.manifest.files).map(([key, value]) => [key, files[value.kind]])), receipts: Object.fromEntries(Object.entries(candidate.manifest.receipts).map(([key, value]) => [key, files[value.kind]])), stagedManifest: files[candidate.manifest.stagedManifest.kind] };
  return { root, files, plan: buildQualificationPlan(manifest, candidate.trust, root) };
};

export const validateExecutionResult = (value) => {
  if (value?.schemaVersion !== 1 || value.kind !== "task19_protected_execution" || value.evidenceRoot !== ".") return failure("EVIDENCE_ROOT_INVALID", "evidenceRoot");
  const receipt = value.installedProductReceipt;
  const expected = PLATFORMS.flatMap((platform) => ORDERS.map((startupOrder) => `${platform}:${startupOrder}`));
  const actual = Array.isArray(receipt?.runs) ? receipt.runs.map((run) => `${run.platform}:${run.startupOrder}`) : [];
  if (JSON.stringify(actual) !== JSON.stringify(expected)) return failure("RUN_MATRIX_INCOMPLETE", "installedProductReceipt.runs");
  if (!receipt.performance || !receipt.probes) return failure("RUN_EVIDENCE_INCOMPLETE", "installedProductReceipt");
  if (value.cleanup?.complete !== true || !SHA256.test(value.cleanup.cleanupEvidenceSha256)) return failure("CLEANUP_INCOMPLETE", "cleanup");
  return { ok: true };
};
