import { createHash, generateKeyPairSync, randomBytes, sign } from "node:crypto";
import { mkdirSync, readFileSync, renameSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { createInstalledProductFixture } from "../qa/task19/installed-product-fixture.mjs";

const json = (value) => Buffer.from(`${JSON.stringify(value, null, 2)}\n`);
const canonical = (value) => Buffer.from(JSON.stringify(value));
const pae = (type, payload) => Buffer.concat([Buffer.from(`DSSEv1 ${Buffer.byteLength(type)} ${type} ${payload.length} `), payload]);
const sha256 = (value) => createHash("sha256").update(value).digest("hex");
// Fixture-only: shipped workflows construct this document with qualification-authoritative-reducer.ts.
export const createFixtureOnlyAuthoritativeReduction = (caller, server, control) => ({ schemaVersion: 1, kind: "authoritative_product_qualification", status: "satisfied", caller, children: { server: { result: "success", outputs: { fixtureOnly: true } }, control: { result: "success", outputs: { fixtureOnly: true } } }, promotableInput: true, retryDispatches: 0, candidates: { server, control } });

export const createPromotionFixture = async (root, recordedAt, observedInventory) => {
  const installed = await createInstalledProductFixture(join(root, "installed"));
  const trustPolicyVersion = "product-gate-trust-v1"; const rotationEpoch = 1;
  const { privateKey, publicKey } = generateKeyPairSync("ed25519");
  const { privateKey: artifactPrivateKey, publicKey: artifactPublicKey } = generateKeyPairSync("ed25519");
  const artifactKeyId = sha256(artifactPublicKey.export({ type: "spki", format: "der" }));
  const publicationReceiptKey = randomBytes(32);
  const publicationReceiptKeyId = sha256(publicationReceiptKey);
  const publicBytes = publicKey.export({ type: "spki", format: "pem" });
  const keyId = sha256(publicKey.export({ type: "spki", format: "der" }));
  const publicKeyPath = join(root, "trust/product-gate-public.pem");
  mkdirSync(dirname(publicKeyPath), { recursive: true });
  writeFileSync(publicKeyPath, publicBytes);
  const trustConfigPath = join(root, "trust/fixture-trust-v1.json");
  const harnessPublic = installed.publicKey.export({ type: "spki", format: "pem" });
  writeFileSync(join(root, "trust/installed-harness-public.pem"), harnessPublic);
  writeFileSync(join(root, "trust/artifact-signing-public.pem"), artifactPublicKey.export({ type: "spki", format: "pem" }));
  writeFileSync(join(root, "trust/source-policy-v1.json"), json({ schemaVersion: 1, policyId: "fixture-source-v1", includeRoots: [], includeRootFiles: [".gitignore", "source.txt"], excludePrefixes: ["bound/"], inventoryCommand: ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"] }));

  const authenticated = (path, value) => {
    const bytes = Buffer.isBuffer(value) ? value : json(value);
    const absolute = join(root, path);
    mkdirSync(dirname(absolute), { recursive: true });
    writeFileSync(absolute, bytes);
    return { path, sha256: sha256(bytes), keyId, signature: sign(null, bytes, privateKey).toString("base64"), trustPolicyVersion, rotationEpoch };
  };
  const artifact = (kind, path, installedRole) => {
    const installedArtifact = installedRole === undefined ? undefined : installed.receipt.artifacts.find((item) => item.role === installedRole);
    const bytes = installedArtifact === undefined ? Buffer.from(`exact synthetic candidate bytes: ${kind}\n`) : readFileSync(join(installed.root, installedArtifact.path));
    const absolute = join(root, path);
    mkdirSync(dirname(absolute), { recursive: true });
    writeFileSync(absolute, bytes);
    return { kind, path, sha256: sha256(bytes) };
  };

  const serverArtifacts = [
    artifact("server-linux-amd64-deb", "stage/server/jastreamer-server_1.2.3_linux_amd64.deb", "server"), artifact("server-linux-amd64-rpm", "stage/server/jastreamer-server_1.2.3_linux_amd64.rpm"),
    artifact("server-linux-arm64-deb", "stage/server/jastreamer-server_1.2.3_linux_arm64.deb"), artifact("server-linux-arm64-rpm", "stage/server/jastreamer-server_1.2.3_linux_arm64.rpm"),
    artifact("server-windows-amd64-exe", "stage/server/jastreamer-server_1.2.3_windows_amd64.exe"), artifact("server-windows-amd64-msi", "stage/server/jastreamer-server_1.2.3_windows_amd64.msi"),
    artifact("server-oci", "stage/server/jastreamer-server_1.2.3_linux_amd64-arm64.oci"),
  ];
  const ociIndex = { schemaVersion: 2, mediaType: "application/vnd.oci.image.index.v1+json", manifests: [
    { mediaType: "application/vnd.oci.image.manifest.v1+json", digest: `sha256:${sha256("amd64-manifest")}`, size: 64, platform: { os: "linux", architecture: "amd64" } },
    { mediaType: "application/vnd.oci.image.manifest.v1+json", digest: `sha256:${sha256("arm64-manifest")}`, size: 64, platform: { os: "linux", architecture: "arm64" } },
  ] };
  const ociBytes = canonical(ociIndex); writeFileSync(join(root, serverArtifacts[6].path), ociBytes); serverArtifacts[6].sha256 = sha256(ociBytes); serverArtifacts[6].indexDigest = `sha256:${sha256(ociBytes)}`;
  const controlArtifacts = [
    artifact("control-web", "stage/control/jastreamer-control_1.2.3_web.zip", "control-web"), artifact("control-windows", "stage/control/jastreamer-control_1.2.3_windows.msix", "control-windows"),
    artifact("control-android", "stage/control/jastreamer-control_1.2.3_android_universal.apk", "control-android"),
  ];
  const rendererArtifacts = [artifact("renderer-ci-peer", "stage/renderer/renderer-diagnostic.zip", "renderer")];
  const allArtifacts = [...serverArtifacts, ...controlArtifacts, ...rendererArtifacts];
  const artifactSetSha256 = sha256(canonical(allArtifacts.map(({ kind, sha256: digest }) => ({ kind, sha256: digest }))));
  const sourceSnapshot = authenticated("evidence/source-snapshot.json", installed.receipt.source);
  const dirtySha256 = installed.receipt.source.sha256;
  const bindings = {
    artifactSetSha256, peerSetSha256: installed.receipt.bindings.peerSetSha256, controlContractSha256: installed.receipt.bindings.controlContractSha256,
    rendererContractSha256: installed.receipt.bindings.rendererContractSha256,
  };
  const identity = { sourceRevision: installed.receipt.source.revision, sourceDirtySha256: dirtySha256, ...bindings };
  const manifest = (component, artifacts) => authenticated(`stage/${component}/manifest.json`, {
    schemaVersion: 1, component, tag: `${component}-v1.2.3`, ...identity, artifacts: artifacts.map(({ kind, path, sha256: digest }) => ({ kind, path, sha256: digest })),
  });
  const serverManifest = manifest("server", serverArtifacts);
  const controlManifest = manifest("control", controlArtifacts);
  const rendererManifest = manifest("renderer", rendererArtifacts);
  const dsse = (path, statement) => {
    const payload = canonical(statement); const type = "application/vnd.in-toto+json";
    return authenticated(path, { payloadType: type, payload: payload.toString("base64"), signatures: [{ keyid: artifactKeyId, sig: sign(null, pae(type, payload), artifactPrivateKey).toString("base64") }] });
  };
  const ociSubject = [{ name: "server.oci", digest: { sha256: serverArtifacts[6].sha256 } }];
  const attestations = [
    dsse("stage/server/oci-index-provenance.json", { _type: "https://in-toto.io/Statement/v1", subject: ociSubject, predicateType: "https://slsa.dev/provenance/v1", predicate: { buildDefinition: { buildType: "https://jastreamer.invalid/build/server", resolvedDependencies: [{ uri: "git+fixture", digest: { gitCommit: identity.sourceRevision } }, { uri: "git+fixture#source-input", digest: { sha256: identity.sourceDirtySha256 } }] }, runDetails: { builder: { id: "https://jastreamer.invalid/builder" } } } }),
    dsse("stage/server/oci-index-sbom.json", { _type: "https://in-toto.io/Statement/v1", subject: ociSubject, predicateType: "https://spdx.dev/Document", predicate: { spdxVersion: "SPDX-2.3", SPDXID: "SPDXRef-DOCUMENT", packages: [{ name: "server.oci", SPDXID: "SPDXRef-Package", checksums: [{ algorithm: "SHA256", checksumValue: serverArtifacts[6].sha256 }] }] } }),
  ];
  const predicateTypes = ["https://slsa.dev/provenance/v1", "https://spdx.dev/Document"];
  const referrers = authenticated("stage/server/oci-referrers.json", { schemaVersion: 2, mediaType: "application/vnd.oci.image.index.v1+json", subject: `sha256:${serverArtifacts[6].sha256}`, manifests: attestations.map((item, index) => ({ mediaType: "application/vnd.dsse.envelope.v1+json", artifactType: "application/vnd.in-toto+json", digest: `sha256:${item.sha256}`, size: readFileSync(join(root, item.path)).length, annotations: { "in-toto.io/predicate-type": predicateTypes[index] } })) });
  serverArtifacts[6].platforms = ["linux/amd64", "linux/arm64"];
  serverArtifacts[6].attestations = attestations; serverArtifacts[6].referrers = referrers;

  const protocolInfo = ["http-get:*:audio/flac:*", "http-get:*:audio/mpeg:*", "http-get:*:audio/ogg:*", "http-get:*:audio/wav:*", "http-get:*:audio/L16;rate=44100;channels=2:*"];
  const k17Physical = {
    evidenceSource: "physical", artifactSha256: artifactSetSha256, identitySha256: sha256("k17-identity"), model: "FiiO K17", firmware: 261,
    runnerLabel: "jastreamer-k17-lab-a", protocolInfo, protocolInfoSha256: sha256(JSON.stringify(protocolInfo)),
    representations: [["flac", "original"], ["mp3", "original"], ["vorbis", "original"], ["opus", "original"], ["wav", "original"], ["l16-fallback", "l16"]].map(([format, selected], index) => ({ format, advertised: true, selected, audioProofSha256: String(index + 1).repeat(64) })),
    transport: { pause: "passed", seek: "passed", stop: "passed", naturalEndCount: 1 }, lifecycle: { disappearance: "passed", reappearance: "passed" },
    externalOverride: { observed: true, adopted: false }, network: { https: "passed", explicitMediaOnlyHttp: "passed", privateNetworkOnly: true, hostileLocationRejected: true, redirectsRejected: true, expiredUrlRejected: true },
    audioProof: { captureSha256: sha256("k17-capture"), method: "automated_capture", manualListening: false }, cleanup: { rawIdentityRetained: false, firmwareMutated: false, resourcesReleased: true, processesTerminated: true }, recordedAt,
  };
  const k17 = { schema_version: 1, kind: "k17_qualification", recorded_at: recordedAt, candidate_sha256: artifactSetSha256, qualification_status: "qualified", physical: k17Physical };
  const wasapiBinding = {
    renderer_executable_sha256: rendererArtifacts[0].sha256, probe_executable_sha256: sha256("probe"), scenario_driver_sha256: sha256("driver"), server_peer_sha256: sha256("server-peer"), server_peer_input_sha256: sha256("server-peer-input"), media_fixture_archive_sha256: sha256("media"), media_fixture_manifest_sha256: sha256("media-manifest"), source_sha256: dirtySha256, renderer_contract_sha256: bindings.rendererContractSha256, peer_set_sha256: bindings.peerSetSha256, candidate_sha256: artifactSetSha256, endpoint_identity_sha256: sha256("endpoint"),
  };
  const wasapi = {
    schema_version: 1, kind: "windows_wasapi_qualification", recorded_at: recordedAt, qualification_status: "qualified", runner_labels: ["self-hosted", "windows", "x64", "jastreamer-audio"], binding: wasapiBinding,
    endpoint: { identity_sha256: wasapiBinding.endpoint_identity_sha256, data_flow: "render", capture_mode: "wasapi_loopback" },
    capture: { sha256: sha256("wasapi-capture"), encoding: "normalized_f32le", sample_rate_hz: 48000, channels: 2, tone: { peak_frequency_hz: 1000, steady_rms: 0.18, absolute_max: 0.251, duration_ms: 2000 }, pause_rms: 0.0004, post_stop_500ms_rms: 0.0003, seek: { requested_position_ms: 1000, dominance_latency_ms: 120, frequency_hz: 1000, rejected_frequency_hz: 440, rejection_db: 44 } },
    formats: ["flac", "mp3", "vorbis", "opus", "wav"].map((format) => ({ format, result: "passed", capture_sha256: sha256(format) })),
    scenarios: ["play-pause-resume-seek-stop", "endpoint-absent", "endpoint-busy", "endpoint-invalidated-restored", "duplicate-conflict", "revocation", "server-restart", "renderer-restart", "disconnect-before-ack", "disconnect-after-ack", "disconnect-before-result", "disconnect-after-result", "corrupted-media", "truncated-media", "cleanup"].map((id) => ({ id, result: "passed" })),
    cleanup: { resources_released: true, processes_terminated: true, temporary_files_removed: true, raw_endpoint_retained: false, external_writes: 0 },
  };
  const qualify = (name, value, evidenceSource) => ({ ...authenticated(`evidence/${name}.json`, value), status: "qualified", evidenceSource });
  const spdx = (item) => ({ spdxVersion: "SPDX-2.3", dataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT", name: `${item.kind} SBOM`, documentNamespace: `https://jastreamer.invalid/spdx/${item.sha256}`, creationInfo: { created: recordedAt, creators: ["Tool: jastreamer-product-gate"] }, packages: [{ name: item.kind, SPDXID: "SPDXRef-Package", downloadLocation: "NOASSERTION", filesAnalyzed: true, licenseConcluded: "Apache-2.0", licenseDeclared: "Apache-2.0", copyrightText: "NOASSERTION", checksums: [{ algorithm: "SHA256", checksumValue: item.sha256 }] }], files: [{ fileName: item.path, SPDXID: "SPDXRef-File", checksums: [{ algorithm: "SHA256", checksumValue: item.sha256 }], licenseConcluded: "Apache-2.0", copyrightText: "NOASSERTION" }], relationships: [{ spdxElementId: "SPDXRef-DOCUMENT", relationshipType: "DESCRIBES", relatedSpdxElement: "SPDXRef-Package" }, { spdxElementId: "SPDXRef-Package", relationshipType: "CONTAINS", relatedSpdxElement: "SPDXRef-File" }] });
  const supply = (lane) => allArtifacts.map((item) => {
    if (lane === "sbom") return authenticated(`evidence/${lane}/${item.kind}.json`, spdx(item));
    if (lane === "provenance") return dsse(`evidence/${lane}/${item.kind}.json`, { _type: "https://in-toto.io/Statement/v1", subject: [{ name: item.kind, digest: { sha256: item.sha256 } }], predicateType: "https://slsa.dev/provenance/v1", predicate: { buildDefinition: { buildType: "https://jastreamer.invalid/build/product", externalParameters: { artifactKind: item.kind, platforms: item.kind === "server-oci" ? ["linux/amd64", "linux/arm64"] : [] }, resolvedDependencies: [{ uri: "git+fixture", digest: { gitCommit: identity.sourceRevision } }, { uri: "git+fixture#source-input", digest: { sha256: identity.sourceDirtySha256 } }] }, runDetails: { builder: { id: "https://jastreamer.invalid/builder" } } } });
    const signature = sign(null, Buffer.from(item.sha256), artifactPrivateKey).toString("base64");
    return authenticated(`evidence/${lane}/${item.kind}.json`, { schemaVersion: 1, kind: "artifact_signature", subjectKind: item.kind, subjectSha256: item.sha256, keyId: artifactKeyId, signature });
  });
  const inventory = observedInventory ?? ["process", "container", "temporary", "listener", "builder", "browser"].map((type) => ({ type, ids: [] }));
  const inventoryBefore = authenticated("evidence/cleanup/inventory-before.json", inventory);
  const inventoryAfter = authenticated("evidence/cleanup/inventory-after.json", inventory);
  const operations = [{ sequence: 1, operation: "local-stage", externallyObserved: false }, { sequence: 2, operation: "local-select", externallyObserved: false }];
  const attemptedOperations = authenticated("evidence/cleanup/attempted-operations.json", operations);
  let previousSha256 = "0".repeat(64); const ledger = operations.map((operation) => { const entrySha256 = sha256(canonical({ ...operation, previousSha256 })); const entry = { ...operation, previousSha256, entrySha256 }; previousSha256 = entrySha256; return entry; });
  const mutationLedger = authenticated("evidence/cleanup/mutation-ledger.json", ledger);
  const mutationLedgerPath = join(root, "evidence/cleanup/external-mutation-ledger.jsonl"); writeFileSync(mutationLedgerPath, `${ledger.map(JSON.stringify).join("\n")}\n`);
  const priorPath = "prior-published/server-v0.0.9.bin"; mkdirSync(dirname(join(root, priorPath)), { recursive: true }); writeFileSync(join(root, priorPath), "prior published immutable bytes\n");
  const priorPublished = authenticated("evidence/cleanup/prior-published.json", [{ path: priorPath, sha256: sha256(readFileSync(join(root, priorPath))) }]);

  const staging = (component, runId, artifactId) => ({
    repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/product-qualification-dispatch.yml", eventName: "workflow_dispatch", headSha: installed.receipt.source.revision,
    callerRunId: runId, callerRunAttempt: 1, callerRef: "refs/heads/main", callerSha: installed.receipt.source.revision, calledWorkflowPath: `.github/workflows/${component}-qualification-staging.yml`, calledJob: component, calledJobResult: "success",
    runId, runAttempt: 1, artifactId, artifactName: `${component}-publication-stage-${runId}-1`, artifactDigest: sha256(`${component}-publication-stage-archive`),
    artifactAttemptProvenance: "caller-run+upload-output+embedded-manifest", artifactManifestSha256: sha256(`${component}-candidate-binding`), artifactContentManifestSha256: sha256(`${component}-content-manifest`),
  });
  const serverStaging = staging("server", "1001", "2001"); const controlStaging = staging("control", "1001", "2002");
  const reducerPath = "authoritative/authoritative-qualification.json"; const reducerBytes = json(createFixtureOnlyAuthoritativeReduction({ repository: "furyheimdall/jastreamer", runId: "1001", runAttempt: 1, ref: "refs/heads/main", sha: installed.receipt.source.revision, workflowPath: ".github/workflows/product-qualification-dispatch.yml" }, serverStaging, controlStaging)); mkdirSync(dirname(join(root, reducerPath)), { recursive: true }); writeFileSync(join(root, reducerPath), reducerBytes);
  const receipt = {
    schemaVersion: 1, kind: "product_promotion_gate", recordedAt, trustPolicyVersion, rotationEpoch, authoritativeReducer: { path: reducerPath, sha256: sha256(reducerBytes), result: "success" },
    source: { revision: installed.receipt.source.revision, dirtySha256, snapshot: sourceSnapshot }, bindings,
    publication: { repository: "furyheimdall/jastreamer", environment: "product-promotion", receiptKeyId: publicationReceiptKeyId },
    candidates: {
      server: { component: "server", manifest: serverManifest, floating: false, releaseTag: "server-v1.2.3", staging: serverStaging, artifacts: serverArtifacts },
      control: { component: "control", manifest: controlManifest, floating: false, releaseTag: "control-v1.2.3", staging: controlStaging, artifacts: controlArtifacts },
      rendererPeer: { component: "renderer", manifest: rendererManifest, floating: false, artifacts: rendererArtifacts },
    },
    qualifications: { todo19: qualify("todo19", installed.receipt, "installed"), k17: qualify("k17", k17, "physical"), wasapi: qualify("wasapi", wasapi, "native") },
    supplyChain: {
      sbom: supply("sbom"), provenance: supply("provenance"), signing: supply("signing"),
      security: authenticated("evidence/security.json", { kind: "security", secretScan: "passed", privateKeysRetained: false, externalWrites: 0, ...identity }),
    },
    cleanup: { inventoryBefore, inventoryAfter, attemptedOperations, mutationLedger, priorPublished, stagingTemporaryRemoved: true, priorPublishedTouched: false },
    signature: { algorithm: "Ed25519", keyId, value: "pending" },
  };
  writeFileSync(trustConfigPath, json({
    schemaVersion: 1, profile: "fixture", trustPolicyVersion, rotationEpoch, gate: { keyId, publicKeyPath: "trust/product-gate-public.pem", algorithm: "Ed25519" },
    artifactSigning: { keyIds: [artifactKeyId], publicKeys: [{ keyId: artifactKeyId, path: "trust/artifact-signing-public.pem" }] },
    builders: ["https://jastreamer.invalid/builder"], materialPolicy: { buildType: "https://jastreamer.invalid/build/product", sourceUri: "git+fixture" },
    publication: { repository: "furyheimdall/jastreamer", environment: "product-promotion", receiptKeyId: publicationReceiptKeyId },
    qualification: {
      todo19: { root: "installed", harnessKeyId: installed.keyId, harnessPublicKeyPath: "trust/installed-harness-public.pem", trustedCandidates: installed.trustedCandidates, expectedBindings: installed.receipt.bindings },
      k17: { artifactSha256: artifactSetSha256, runnerLabel: "jastreamer-k17-lab-a" }, wasapi: { expectedBinding: wasapiBinding },
    },
    canonical: { sourceRoot: "installed", sourcePolicyPath: "trust/source-policy-v1.json", sourceRevision: installed.receipt.source.revision, contracts: installed.receipt.contracts, peers: installed.receipt.peers },
  }));
  const receiptPath = join(root, "product-gate.json");
  const writeReceipt = () => writeFileSync(receiptPath, json(receipt));
  const resignReceipt = () => {
    const currentReducerBytes = json(createFixtureOnlyAuthoritativeReduction({ repository: "furyheimdall/jastreamer", runId: receipt.candidates.server.staging.callerRunId, runAttempt: receipt.candidates.server.staging.callerRunAttempt, ref: receipt.candidates.server.staging.callerRef, sha: receipt.candidates.server.staging.callerSha, workflowPath: ".github/workflows/product-qualification-dispatch.yml" }, receipt.candidates.server.staging, receipt.candidates.control.staging)); writeFileSync(join(root, reducerPath), currentReducerBytes); receipt.authoritativeReducer = { path: reducerPath, sha256: sha256(currentReducerBytes), result: "success" };
    const { signature: _signature, ...unsigned } = receipt;
    receipt.signature = { algorithm: "Ed25519", keyId, value: sign(null, canonical(unsigned), privateKey).toString("base64") };
    writeReceipt();
  };
  resignReceipt();
  return {
    receipt, receiptPath, publicKeyPath, trustConfigPath, mutationLedgerPath, publicationReceiptKey, writeReceipt, resignReceipt, sha256,
    authenticate(path, value) { return authenticated(path, value); },
    mutateOciReferrers(mutate) {
      const reference = receipt.candidates.server.artifacts.find((item) => item.kind === "server-oci").referrers; const value = JSON.parse(readFileSync(join(root, reference.path), "utf8")); mutate(value);
      const bytes = json(value); writeFileSync(join(root, reference.path), bytes); reference.sha256 = sha256(bytes); reference.signature = sign(null, bytes, privateKey).toString("base64"); resignReceipt();
    },
    mutateCleanup(name, mutate) {
      const reference = receipt.cleanup[name]; const value = JSON.parse(readFileSync(join(root, reference.path), "utf8")); mutate(value);
      const bytes = json(value); writeFileSync(join(root, reference.path), bytes); reference.sha256 = sha256(bytes); reference.signature = sign(null, bytes, privateKey).toString("base64"); resignReceipt();
    },
    mutateDsse(lane, index, mutate) {
      const reference = receipt.supplyChain[lane][index]; const envelope = JSON.parse(readFileSync(join(root, reference.path), "utf8")); const payload = JSON.parse(Buffer.from(envelope.payload, "base64")); mutate(payload);
      const payloadBytes = canonical(payload); envelope.payload = payloadBytes.toString("base64"); envelope.signatures[0].sig = sign(null, pae(envelope.payloadType, payloadBytes), artifactPrivateKey).toString("base64");
      const bytes = json(envelope); writeFileSync(join(root, reference.path), bytes); reference.sha256 = sha256(bytes); reference.signature = sign(null, bytes, privateKey).toString("base64"); resignReceipt();
    },
    mutateSupply(lane, index, mutate) {
      const reference = receipt.supplyChain[lane][index]; const value = JSON.parse(readFileSync(join(root, reference.path), "utf8")); mutate(value);
      const bytes = json(value); writeFileSync(join(root, reference.path), bytes); reference.sha256 = sha256(bytes); reference.signature = sign(null, bytes, privateKey).toString("base64"); resignReceipt();
    },
    mutateQualification(name, mutate) {
      const reference = receipt.qualifications[name]; const value = JSON.parse(readFileSync(join(root, reference.path), "utf8")); mutate(value);
      const bytes = json(value); writeFileSync(join(root, reference.path), bytes); reference.sha256 = sha256(bytes); reference.signature = sign(null, bytes, privateKey).toString("base64"); resignReceipt();
    },
    replaceWithSymlink(path, target) { const absolute = join(root, path); const moved = `${absolute}.replaced`; renameSync(absolute, moved); symlinkSync(target, absolute); },
    cleanup() { rmSync(root, { recursive: true, force: true }); },
  };
};
