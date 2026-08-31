import { createHash, createPublicKey, verify } from "node:crypto";
import { closeSync, constants, fstatSync, lstatSync, openSync, readFileSync, realpathSync } from "node:fs";
import { isAbsolute, relative, resolve, sep } from "node:path";
import { validateInstalledProductReceipt } from "../qa/task19/product-e2e-receipt.mjs";
import { validateQualificationReceipt } from "../qa/k17/receipt.mjs";
import { validateWindowsAudioReceipt } from "../qa/windows-audio/receipt.mjs";
import { verifyDsse, verifySupplyChain } from "./product-gate-supply-chain.mjs";
import { verifyCanonicalSource } from "./product-gate-source.mjs";
import { loadTrustConfig } from "./product-gate-trust.mjs";
import { observeExternalMutations } from "./product-gate-mutations.mjs";
import { consumeAuthoritativeReduction } from "./qualification-authoritative-input.ts";
import { validateK17Schema, validateObservationSchema, validateSchema, validateSecuritySchema, validateWasapiSchema } from "./product-gate-schema.mjs";

const SERVER_KINDS = [
  "server-linux-amd64-deb", "server-linux-amd64-rpm", "server-linux-arm64-deb", "server-linux-arm64-rpm",
  "server-windows-amd64-exe", "server-windows-amd64-msi", "server-oci",
];
const CONTROL_KINDS = ["control-web", "control-windows", "control-android"];
const RENDERER_KINDS = ["renderer-ci-peer"];
const PUBLICATION_WORKFLOWS = { server: ".github/workflows/server-qualification-staging.yml", control: ".github/workflows/control-qualification-staging.yml" };
const MAX_AGE_MS = 24 * 60 * 60 * 1000;
const MAX_FUTURE_MS = 5 * 60 * 1000;
const sha256 = (value) => createHash("sha256").update(value).digest("hex");
const canonical = (value) => Buffer.from(JSON.stringify(value));
const denied = (code, path) => ({ ok: false, code, path });

const safePath = (root, path) => {
  if (typeof path !== "string" || path === "" || isAbsolute(path) || path.split(/[\\/]/).includes("..")) return { issue: denied("PATH_INVALID", path) };
  const absolute = resolve(root, path);
  const inside = relative(root, absolute);
  if (inside === "" || inside === ".." || inside.startsWith(`..${sep}`) || isAbsolute(inside)) return { issue: denied("PATH_INVALID", path) };
  let current = realpathSync(root);
  for (const part of inside.split(sep)) {
    current = resolve(current, part);
    try {
      if (lstatSync(current).isSymbolicLink()) return { issue: denied("SYMLINK_REJECTED", path) };
    } catch {
      return { issue: denied("FILE_MISSING", path) };
    }
  }
  return { absolute };
};

const stableRead = (root, path) => {
  const safe = safePath(root, path);
  if (safe.issue) return safe;
  let descriptor;
  try {
    descriptor = openSync(safe.absolute, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    const before = fstatSync(descriptor);
    if (!before.isFile()) return { issue: denied("FILE_INVALID", path) };
    const bytes = readFileSync(descriptor);
    const after = fstatSync(descriptor);
    const named = lstatSync(safe.absolute);
    if (before.dev !== after.dev || before.ino !== after.ino || before.size !== after.size || before.mtimeMs !== after.mtimeMs || named.dev !== before.dev || named.ino !== before.ino || named.isSymbolicLink()) {
      return { issue: denied("FILE_REPLACED", path) };
    }
    return { bytes };
  } catch {
    return { issue: denied("FILE_INVALID", path) };
  } finally {
    if (descriptor !== undefined) closeSync(descriptor);
  }
};

const parseJson = (bytes, path) => {
  try {
    return { value: JSON.parse(bytes) };
  } catch {
    return { issue: denied("JSON_INVALID", path) };
  }
};

const verifyGateSignature = (trust, bytes, signature) => verify(trust.algorithm === "RSA-SHA256" ? "sha256" : null, bytes, trust.publicKey, signature);
const referenceFields = (reference) => ({ path: reference.path, sha256: reference.sha256, keyId: reference.keyId, signature: reference.signature, trustPolicyVersion: reference.trustPolicyVersion, rotationEpoch: reference.rotationEpoch });
const readAuthenticated = (context, reference, code = "TRANSCRIPT_AUTHENTICATION_FAILED") => {
  const read = stableRead(context.root, reference.path);
  if (read.issue) return read;
  if (reference.trustPolicyVersion !== context.trust.config.trustPolicyVersion || reference.rotationEpoch !== context.trust.config.rotationEpoch) return { issue: denied("TRUST_POLICY_MISMATCH", reference.path) };
  if (sha256(read.bytes) !== reference.sha256) return { issue: denied("DIGEST_MISMATCH", reference.path) };
  if (reference.keyId !== context.trust.keyId || !verifyGateSignature(context.trust, read.bytes, Buffer.from(reference.signature, "base64"))) return { issue: denied(code, reference.path) };
  const parsed = parseJson(read.bytes, reference.path);
  return parsed.issue ? parsed : { bytes: read.bytes, value: parsed.value };
};

const identityMatches = (value, receipt) => value?.sourceRevision === receipt.source.revision
  && value?.sourceDirtySha256 === receipt.source.dirtySha256
  && value?.artifactSetSha256 === receipt.bindings.artifactSetSha256
  && value?.peerSetSha256 === receipt.bindings.peerSetSha256
  && value?.controlContractSha256 === receipt.bindings.controlContractSha256
  && value?.rendererContractSha256 === receipt.bindings.rendererContractSha256;

const gateTools = { stableRead, readAuthenticated, denied, identityMatches, validateSecuritySchema, sha256 };

const exactKinds = (candidate, expected) => {
  const actual = candidate.artifacts.map((item) => item.kind);
  return actual.length === expected.length && expected.every((kind, index) => actual[index] === kind);
};

const verifyCandidate = (context, candidate, expectedKinds) => {
  if (candidate.floating) return denied("FLOATING_ARTIFACT", candidate.component);
  if (!exactKinds(candidate, expectedKinds)) return denied("ARTIFACT_ALLOWLIST_MISMATCH", candidate.component);
  const manifest = readAuthenticated(context, candidate.manifest);
  if (manifest.issue) return manifest.issue;
  if (manifest.value?.component !== candidate.component || !identityMatches(manifest.value, context.receipt)) return denied("EVIDENCE_BINDING_MISMATCH", candidate.manifest.path);
  const expectedManifestArtifacts = candidate.artifacts.map(({ kind, path, sha256: digest }) => ({ kind, path, sha256: digest }));
  if (JSON.stringify(manifest.value.artifacts) !== JSON.stringify(expectedManifestArtifacts)) return denied("MANIFEST_MISMATCH", candidate.component);
  for (const artifact of candidate.artifacts) {
    const read = stableRead(context.root, artifact.path);
    if (read.issue) return read.issue;
    if (sha256(read.bytes) !== artifact.sha256) return denied("DIGEST_MISMATCH", artifact.path);
  }
};

const verifyOci = (context) => {
  const oci = context.receipt.candidates.server.artifacts.find((item) => item.kind === "server-oci"); if (JSON.stringify(oci?.platforms) !== JSON.stringify(["linux/amd64", "linux/arm64"])) return denied("OCI_PLATFORM_MISMATCH", "candidates.server.server-oci.platforms");
  if (context.trust.config.profile !== "current-audit" && oci.indexDigest !== `sha256:${oci.sha256}`) return denied("OCI_INDEX_DIGEST_MISMATCH", "candidates.server.server-oci.indexDigest");
  const indexRead = stableRead(context.root, oci.path); if (indexRead.issue) return indexRead.issue;
  const index = JSON.parse(indexRead.bytes); const platforms = index.manifests?.map((item) => `${item.platform?.os}/${item.platform?.architecture}`);
  if (index.schemaVersion !== 2 || index.mediaType !== "application/vnd.oci.image.index.v1+json" || JSON.stringify(platforms) !== JSON.stringify(oci.platforms) || index.manifests.some((item) => item.mediaType !== "application/vnd.oci.image.manifest.v1+json" || !/^sha256:[0-9a-f]{64}$/.test(item.digest))) return denied("OCI_INDEX_INVALID", oci.path);
  if (!Array.isArray(oci.attestations) || oci.attestations.length !== 2) return denied("OCI_ATTESTATION_MISSING", "candidates.server.server-oci.attestations");
  const referrers = readAuthenticated(context, oci.referrers); if (referrers.issue) return referrers.issue;
  const predicates = ["https://slsa.dev/provenance/v1", "https://spdx.dev/Document"];
  if (referrers.value?.schemaVersion !== 2 || referrers.value?.mediaType !== "application/vnd.oci.image.index.v1+json" || referrers.value?.subject !== `sha256:${oci.sha256}` || referrers.value.manifests?.length !== 2 || referrers.value.manifests.some((item, index) => item.mediaType !== "application/vnd.dsse.envelope.v1+json" || item.artifactType !== "application/vnd.in-toto+json" || item.digest !== `sha256:${oci.attestations[index].sha256}` || item.annotations?.["in-toto.io/predicate-type"] !== predicates[index])) return denied("OCI_REFERRERS_INVALID", "candidates.server.server-oci.referrers");
  for (const [index, reference] of oci.attestations.entries()) {
    const result = readAuthenticated(context, reference); if (result.issue) return result.issue;
    const attestation = verifyDsse(context, { envelope: result.value, digest: oci.sha256, predicateType: predicates[index] }, gateTools); if (attestation?.ok === false) return attestation;
    if (predicates[index] === "https://spdx.dev/Document" && (attestation.statement.predicate?.spdxVersion !== "SPDX-2.3" || attestation.statement.predicate?.SPDXID !== "SPDXRef-DOCUMENT")) return denied("OCI_ATTESTATION_MISMATCH", reference.path);
  }
};

const verifyQualifications = (context) => {
  const values = {};
  for (const name of ["todo19", "k17", "wasapi"]) {
    const qualification = context.receipt.qualifications[name];
    if (qualification.status !== "qualified") return denied("QUALIFICATION_PENDING", `qualifications.${name}`);
    const result = readAuthenticated(context, referenceFields(qualification));
    if (result.issue) return result.issue;
    values[name] = result.value;
  }
  const config = context.trust.config.qualification;
  const installedRoot = resolve(context.root, config.todo19.root);
  const harnessRead = stableRead(context.root, config.todo19.harnessPublicKeyPath);
  if (harnessRead.issue) return harnessRead.issue;
  const installed = validateInstalledProductReceipt(values.todo19, { now: context.now, root: installedRoot, trustedCandidates: config.todo19.trustedCandidates, harnessTrust: { keyId: config.todo19.harnessKeyId, publicKey: createPublicKey(harnessRead.bytes) }, expectedBindings: config.todo19.expectedBindings });
  if (!installed.ok) return denied(`TODO19_${installed.code}`, installed.path);
  if (!validateK17Schema(values.k17)) return denied("K17_SCHEMA_INVALID", validateK17Schema.errors?.[0]?.instancePath ?? "$");
  const k17 = validateQualificationReceipt(values.k17, { now: context.now, candidateSha256: config.k17.artifactSha256, runnerLabel: config.k17.runnerLabel });
  if (!k17.ok) return denied(`K17_${k17.code}`, k17.path);
  if (!validateWasapiSchema(values.wasapi)) return denied("WASAPI_SCHEMA_INVALID", validateWasapiSchema.errors?.[0]?.instancePath ?? "$");
  const wasapi = validateWindowsAudioReceipt(values.wasapi, { now: context.now, expectedBinding: config.wasapi.expectedBinding });
  if (!wasapi.ok) return denied(`WASAPI_${wasapi.code}`, wasapi.path);
};

const publicationNames = (component, releaseTag) => { const version = releaseTag.slice(`${component}-v`.length); return component === "control"
  ? [`jastreamer-control_${version}_web.zip`, `jastreamer-control_${version}_windows.msix`, `jastreamer-control_${version}_android_universal.apk`]
  : [`jastreamer-server_${version}_linux_amd64.deb`, `jastreamer-server_${version}_linux_amd64.rpm`, `jastreamer-server_${version}_linux_arm64.deb`, `jastreamer-server_${version}_linux_arm64.rpm`, `jastreamer-server_${version}_windows_amd64.exe`, `jastreamer-server_${version}_windows_amd64.msi`, `jastreamer-server_${version}_linux_amd64-arm64.oci`]; };
const verifyPublication = (context) => {
  const reducerReference = context.receipt.authoritativeReducer;
  if (reducerReference?.result !== "success" || typeof reducerReference.path !== "string" || !/^[0-9a-f]{64}$/.test(reducerReference.sha256)) return denied("AUTHORITATIVE_REDUCER_REQUIRED", "authoritativeReducer");
  const reducerRead = stableRead(context.root, reducerReference.path); if (reducerRead.issue) return reducerRead.issue;
  if (sha256(reducerRead.bytes) !== reducerReference.sha256) return denied("AUTHORITATIVE_REDUCER_DIGEST_MISMATCH", "authoritativeReducer");
  let authoritativeInput; try { authoritativeInput = consumeAuthoritativeReduction(reducerRead.bytes); } catch { return denied("AUTHORITATIVE_REDUCER_INVALID", "authoritativeReducer"); }
  if (authoritativeInput.reducerResult !== "success" || authoritativeInput.reducerSha256 !== reducerReference.sha256) return denied("AUTHORITATIVE_REDUCER_INVALID", "authoritativeReducer");
  if (JSON.stringify(authoritativeInput.candidates.server) !== JSON.stringify(context.receipt.candidates.server.staging) || JSON.stringify(authoritativeInput.candidates.control) !== JSON.stringify(context.receipt.candidates.control.staging)) return denied("AUTHORITATIVE_REDUCER_CANDIDATE_MISMATCH", "candidates");
  if (JSON.stringify(context.receipt.publication) !== JSON.stringify(context.trust.config.publication)) return denied("PUBLICATION_BINDING_MISMATCH", "publication");
  for (const component of ["server", "control"]) {
    const candidate = context.receipt.candidates[component]; const staging = candidate.staging;
    if (staging?.repository !== context.receipt.publication.repository || staging.workflowPath !== ".github/workflows/product-qualification-dispatch.yml" || staging.calledWorkflowPath !== PUBLICATION_WORKFLOWS[component] || staging.calledJob !== component || staging.calledJobResult !== "success" || staging.eventName !== "workflow_dispatch" || staging.headSha !== context.receipt.source.revision || staging.callerSha !== context.receipt.source.revision || staging.callerRunId !== staging.runId || staging.callerRunAttempt !== staging.runAttempt || !/^refs\/heads\/[A-Za-z0-9._/-]{1,200}$/.test(staging.callerRef) || staging.artifactName !== `${component}-publication-stage-${staging.callerRunId}-${staging.callerRunAttempt}` || staging.artifactAttemptProvenance !== "caller-run+upload-output+embedded-manifest" || !/^[0-9a-f]{64}$/.test(staging.artifactManifestSha256) || !/^[0-9a-f]{64}$/.test(staging.artifactContentManifestSha256)) return denied("PUBLICATION_STAGE_INVALID", `candidates.${component}.staging`);
    const manifest = readAuthenticated(context, candidate.manifest); if (manifest.issue) return manifest.issue; if (manifest.value?.tag !== candidate.releaseTag) return denied("PUBLICATION_MANIFEST_MISMATCH", `candidates.${component}.manifest`);
    const names = candidate.artifacts.map((item) => item.path.split("/").at(-1)); if (JSON.stringify(names) !== JSON.stringify(publicationNames(component, candidate.releaseTag))) return denied("PUBLICATION_MANIFEST_MISMATCH", `candidates.${component}.artifacts`);
  }
  const renderer = context.receipt.candidates.rendererPeer; if (renderer.releaseTag !== undefined || renderer.staging !== undefined) return denied("RENDERER_PUBLICATION_FORBIDDEN", "candidates.rendererPeer");
};

const verifyCleanup = (context, mutationLedgerPath) => {
  if (!context.receipt.cleanup.stagingTemporaryRemoved || context.receipt.cleanup.priorPublishedTouched) return denied("CLEANUP_INCOMPLETE", "cleanup");
  const before = readAuthenticated(context, context.receipt.cleanup.inventoryBefore);
  const after = readAuthenticated(context, context.receipt.cleanup.inventoryAfter);
  const operations = readAuthenticated(context, context.receipt.cleanup.attemptedOperations);
  const ledgerReceipt = readAuthenticated(context, context.receipt.cleanup.mutationLedger);
  const prior = readAuthenticated(context, context.receipt.cleanup.priorPublished);
  for (const result of [before, after, operations, ledgerReceipt, prior]) if (result.issue) return result.issue;
  const observation = { before: before.value, after: after.value, operations: operations.value, ledger: ledgerReceipt.value, priorPublished: prior.value, stagingTemporaryRemoved: context.receipt.cleanup.stagingTemporaryRemoved, priorPublishedTouched: context.receipt.cleanup.priorPublishedTouched };
  if (!validateObservationSchema(observation)) return denied("OBSERVATION_SCHEMA_INVALID", validateObservationSchema.errors?.[0]?.instancePath ?? "$");
  if (JSON.stringify(before.value) !== JSON.stringify(after.value)) return denied("CLEANUP_INCOMPLETE", "cleanup.inventoryAfter");
  if (!Array.isArray(operations.value) || operations.value.some((item, index) => item?.sequence !== index + 1 || item.externallyObserved !== false)) return denied("EXTERNAL_MUTATION_OBSERVED", "cleanup.attemptedOperations");
  let previousSha256 = "0".repeat(64);
  for (const [index, entry] of ledgerReceipt.value.entries()) { const expected = sha256(canonical({ sequence: entry.sequence, operation: entry.operation, externallyObserved: entry.externallyObserved, previousSha256 })); if (entry.sequence !== index + 1 || entry.previousSha256 !== previousSha256 || entry.entrySha256 !== expected || entry.externallyObserved !== false) return denied("MUTATION_LEDGER_INVALID", "cleanup.mutationLedger"); previousSha256 = entry.entrySha256; }
  if (JSON.stringify(operations.value) !== JSON.stringify(ledgerReceipt.value.map(({ sequence, operation, externallyObserved }) => ({ sequence, operation, externallyObserved })))) return denied("MUTATION_LEDGER_INVALID", "cleanup.attemptedOperations");
  for (const item of prior.value) { const read = stableRead(context.root, item.path); if (read.issue || sha256(read.bytes) !== item.sha256) return denied("PRIOR_PUBLISHED_CHANGED", item.path); }
  const ledgerAbsolute = isAbsolute(mutationLedgerPath) ? mutationLedgerPath : resolve(context.root, mutationLedgerPath);
  const ledger = stableRead(context.root, relative(context.root, ledgerAbsolute));
  if (ledger.issue) return ledger.issue;
  const observedLedger = [];
  for (const line of ledger.bytes.toString("utf8").split("\n").filter(Boolean)) {
    const parsed = parseJson(Buffer.from(line), "mutation-ledger");
    if (parsed.issue || parsed.value?.externallyObserved !== false) return denied("EXTERNAL_MUTATION_OBSERVED", "mutation-ledger"); observedLedger.push(parsed.value);
  }
  if (JSON.stringify(observedLedger) !== JSON.stringify(ledgerReceipt.value)) return denied("MUTATION_LEDGER_INVALID", "mutation-ledger");
};

const verifyCore = (receiptPath, options) => {
  const root = resolve(options.root);
  const receiptAbsolute = isAbsolute(receiptPath) ? receiptPath : resolve(root, receiptPath);
  const receiptRelative = relative(root, receiptAbsolute);
  const receiptRead = stableRead(root, receiptRelative);
  if (receiptRead.issue) return receiptRead.issue;
  const parsed = parseJson(receiptRead.bytes, receiptRelative);
  if (parsed.issue) return parsed.issue;
  const receipt = parsed.value;
  if (!validateSchema(receipt)) return denied("SCHEMA_INVALID", validateSchema.errors?.[0]?.instancePath ?? "$");
  const repositoryRoot = resolve(options.repositoryRoot ?? process.cwd());
  const loaded = loadTrustConfig(options, { bundle: root, repository: repositoryRoot }, { denied, readFile: (path) => readFileSync(path, "utf8") }); if (loaded.issue) return loaded.issue;
  const { config: trustConfig, profile } = loaded;
  const verificationRoot = profile === "fixture" ? root : repositoryRoot;
  const publicRead = stableRead(verificationRoot, trustConfig.gate.publicKeyPath);
  if (publicRead.issue) return publicRead.issue;
  const publicKey = createPublicKey(publicRead.bytes);
  const keyId = sha256(publicKey.export({ type: "spki", format: "der" }));
  if (keyId !== trustConfig.gate.keyId) return denied("TRUST_KEY_ID_MISMATCH", "trustConfig.gate.keyId");
  const trust = { publicKey, keyId, algorithm: trustConfig.gate.algorithm, config: trustConfig };
  const { signature, ...unsigned } = receipt;
  if (signature.keyId !== keyId || signature.algorithm !== trust.algorithm || !verifyGateSignature(trust, canonical(unsigned), Buffer.from(signature.value, "base64"))) return denied("SIGNATURE_INVALID", "signature");
  const age = Date.parse(options.now) - Date.parse(receipt.recordedAt);
  if (!Number.isFinite(age) || age > MAX_AGE_MS || age < -MAX_FUTURE_MS) return denied("RECEIPT_STALE", "recordedAt");
  if (receipt.trustPolicyVersion !== trustConfig.trustPolicyVersion || receipt.rotationEpoch !== trustConfig.rotationEpoch) return denied("TRUST_POLICY_MISMATCH", "trustPolicy");
  const context = { root, policyRoot: profile === "production" ? repositoryRoot : root, trust, receipt, now: options.now };
  if (profile === "current-audit") { if (receipt.currentAudit === undefined) return denied("CURRENT_AUDIT_REQUIRED", "currentAudit"); const audit = readAuthenticated(context, receipt.currentAudit); if (audit.issue) return audit.issue; if (audit.value?.kind !== "current_candidate_scan") return denied("CURRENT_AUDIT_INVALID", "currentAudit"); }
  const canonicalConfig = trustConfig.canonical;
  const source = verifyCanonicalSource(context, canonicalConfig, gateTools); if (source.ok === false) return source;
  const sourceRoot = resolve(root, canonicalConfig.sourceRoot);
  if (source.revision !== canonicalConfig.sourceRevision || source.revision !== receipt.source.revision || source.digest !== receipt.source.dirtySha256) return denied("SOURCE_BINDING_MISMATCH", "source");
  for (const contract of canonicalConfig.contracts) {
    const read = stableRead(sourceRoot, contract.path); if (read.issue) return read.issue;
    if (sha256(read.bytes) !== contract.sha256 || receipt.bindings[`${contract.component}ContractSha256`] !== contract.sha256) return denied("CONTRACT_RECOMPUTATION_MISMATCH", contract.path);
  }
  const peerIdentities = [];
  for (const peer of canonicalConfig.peers) { const read = stableRead(sourceRoot, peer.path); if (read.issue) return read.issue; const digest = sha256(read.bytes); if (digest !== peer.sha256) return denied("PEER_RECOMPUTATION_MISMATCH", peer.path); peerIdentities.push({ component: peer.component, sha256: digest }); }
  if (sha256(JSON.stringify(peerIdentities)) !== receipt.bindings.peerSetSha256) return denied("PEER_RECOMPUTATION_MISMATCH", "bindings.peerSetSha256");
  const candidates = [receipt.candidates.server, receipt.candidates.control, receipt.candidates.rendererPeer];
  for (const [candidate, kinds] of [[candidates[0], SERVER_KINDS], [candidates[1], CONTROL_KINDS], [candidates[2], RENDERER_KINDS]]) {
    if (!exactKinds(candidate, kinds)) return denied("ARTIFACT_ALLOWLIST_MISMATCH", candidate.component);
  }
  const artifacts = candidates.flatMap((candidate) => candidate.artifacts);
  const computedSet = sha256(canonical(artifacts.map(({ kind, sha256: digest }) => ({ kind, sha256: digest }))));
  if (computedSet !== receipt.bindings.artifactSetSha256) return denied("EVIDENCE_BINDING_MISMATCH", "bindings.artifactSetSha256");
  for (const [candidate, kinds] of [[candidates[0], SERVER_KINDS], [candidates[1], CONTROL_KINDS], [candidates[2], RENDERER_KINDS]]) {
    const issue = verifyCandidate(context, candidate, kinds);
    if (issue) return issue;
  }
  const publication = profile === "current-audit" ? undefined : verifyPublication(context); for (const check of [verifyOci(context), verifyQualifications(context), verifySupplyChain(context, artifacts, gateTools), publication, verifyCleanup(context, options.mutationLedgerPath)]) if (check) return check;
  if (profile === "current-audit") return denied("CURRENT_AUDIT_NON_PROMOTABLE", "profile");
  const publicationCandidate = (candidate) => ({ releaseTag: candidate.releaseTag, manifest: { path: candidate.manifest.path, sha256: candidate.manifest.sha256 }, staging: candidate.staging }); const serverOci = receipt.candidates.server.artifacts.find((item) => item.kind === "server-oci");
  return { ok: true, productGateSha256: sha256(receiptRead.bytes), rebuild: false, externalMutations: 0, authoritativeReducer: { sha256: receipt.authoritativeReducer.sha256, result: receipt.authoritativeReducer.result },
    trust: { profile, trustPolicyVersion: trustConfig.trustPolicyVersion, rotationEpoch: trustConfig.rotationEpoch, gateKeyId: keyId }, publication: { ...receipt.publication, artifactSetSha256: receipt.bindings.artifactSetSha256, candidates: { server: publicationCandidate(receipt.candidates.server), control: publicationCandidate(receipt.candidates.control) } },
    serverOci: { artifactSha256: serverOci.sha256, indexDigest: serverOci.indexDigest, platforms: serverOci.platforms, attestations: serverOci.attestations.map((item) => item.sha256) }, candidateManifests: { server: receipt.candidates.server.manifest.sha256, control: receipt.candidates.control.manifest.sha256 }, selection: [...receipt.candidates.server.artifacts, ...receipt.candidates.control.artifacts].map(({ kind, path, sha256: digest }) => ({ kind, path, sha256: digest })), rendererPublicAssets: [] };
};

export const verifyProductGate = (receiptPath, options) => ({ ...verifyCore(receiptPath, options), externalMutations: observeExternalMutations(receiptPath, options) });
