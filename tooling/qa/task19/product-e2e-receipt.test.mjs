import { afterEach, describe, expect, test } from "bun:test";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, symlink, utimes, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { createSyntheticBundle } from "../synthetic-bundle.mjs";
import { validateProductBundle } from "../product-receipt.mjs";
import { findUnsafeEvidence } from "../receipt-redaction.mjs";
import { compileReceiptSchemas, validateInstalledProductReceipt, validatePackageStructure } from "./product-e2e-receipt.mjs";

const NOW = "2026-08-26T12:00:00.000Z";
const BUILD_AT = "2026-08-26T11:50:00.000Z";
const OBSERVED_AT = "2026-08-26T11:59:30.000Z";
const CAPTURE_AT = "2026-08-26T11:58:00.000Z";
const PACKAGE_AT = "2026-08-26T11:49:00.000Z";
const REQUIRED_SCENARIOS = [
  ["pair", 200, "PAIRED"], ["admin", 200, "AUTHENTICATED"], ["settings-restart", 200, "REVISION_PERSISTED"],
  ["multi-root-scan", 200, "COMPLETE"], ["browse-search", 200, "CATALOG_AUTHORITATIVE"], ["secure-token-restart", 200, "TOKEN_RESTORED"],
  ["queue-add", 201, "QUEUED"], ["queue-reorder", 200, "QUEUED"], ["queue-remove", 200, "QUEUED"], ["queue-clear", 200, "QUEUED"],
  ["queue-retry", 200, "QUEUED"], ["queue-skip", 200, "QUEUED"], ["transport-start", 202, "PLAYING"], ["transport-pause", 202, "PAUSED"],
  ["transport-resume", 202, "PLAYING"], ["transport-stop", 202, "IDLE"], ["transport-skip", 202, "SUSPENDED"], ["transport-seek", 202, "PLAYING"],
  ["transport-next", 202, "SUSPENDED"], ["transport-previous", 202, "PLAYING"], ["policy", 200, "SERVER_AUTHORITY"], ["event-gap-resync", 200, "RESYNCED"],
  ["renderer-assignment-status", 200, "CONNECTED"], ["revocation", 401, "TOKEN_REVOKED"], ["invalid-config", 422, "CONFIG_VALIDATION_FAILED"],
  ["stale-revision", 412, "STALE_REVISION"], ["certificate-change", 495, "CERTIFICATE_MISMATCH"], ["unavailable-explicit-head", 409, "BLOCKED_EXPLICIT_HEAD"],
  ["offline-renderer", 409, "RENDERER_OFFLINE"], ["interrupted-restart", 200, "PRIOR_STATE_PRESERVED"],
];
const REQUIRED_PROBES = ["malformed-input", "stale-state", "dirty-worktree", "hung-command", "flaky-test", "misleading-output", "repeated-interruption"];
const PLATFORMS = ["web", "windows", "android"];
const ORDERS = ["server_first", "control_first"];
const roots = [];
const sha = (value) => createHash("sha256").update(value).digest("hex");
const shaFile = async (path) => sha(await readFile(path));
const shaJson = (value) => sha(JSON.stringify(value));
const json = (path, value) => writeFile(path, `${JSON.stringify(value, null, 2)}\n`);
const git = (root, ...args) => execFileSync("git", ["-C", root, ...args], { encoding: "utf8" }).trim();
const touch = (path, date) => utimes(path, new Date(date), new Date(date));
const writeFixtureAt = async (path, value, recordedAt) => { await writeFile(path, value); await touch(path, recordedAt); };
const writeJsonAt = (path, value, recordedAt) => writeFixtureAt(path, `${JSON.stringify(value, null, 2)}\n`, recordedAt);
afterEach(async () => Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true }))));

const createZip = async (root, output, entries) => {
  const stage = join(root, `${output}.stage`); await mkdir(stage, { recursive: true });
  for (const [name, content] of entries) { const path = join(stage, name); await mkdir(dirname(path), { recursive: true }); await writeFile(path, content); }
  execFileSync("zip", ["-q", "-r", join(root, output), "."], { cwd: stage }); await rm(stage, { recursive: true, force: true });
};
const createDeb = async (root, output) => {
  const stage = join(root, `${output}.stage`); await mkdir(join(stage, "control"), { recursive: true }); await mkdir(join(stage, "data/usr/bin"), { recursive: true });
  await writeFile(join(stage, "debian-binary"), "2.0\n"); await writeFile(join(stage, "control/control"), "Package: jastreamer-server\nVersion: 1.2.3\nArchitecture: amd64\n"); await writeFile(join(stage, "data/usr/bin/jastreamer-server"), "ELF-runtime");
  execFileSync("tar", ["-czf", "control.tar.gz", "-C", "control", "."], { cwd: stage }); execFileSync("tar", ["-czf", "data.tar.gz", "-C", "data", "."], { cwd: stage }); execFileSync("ar", ["r", join(root, output), "debian-binary", "control.tar.gz", "data.tar.gz"], { cwd: stage, stdio: "ignore" }); await rm(stage, { recursive: true, force: true });
};

const makeFixture = async () => {
  const root = await mkdtemp(join(tmpdir(), "task19-secure-receipt-")); roots.push(root);
  await writeFile(join(root, ".gitignore"), "bound/\n"); await writeFile(join(root, "source.txt"), "current source\n");
  git(root, "init", "-q"); git(root, "config", "user.email", "qa@example.invalid"); git(root, "config", "user.name", "QA");
  git(root, "add", ".gitignore", "source.txt"); git(root, "commit", "-qm", "fixture");
  await touch(join(root, ".gitignore"), "2026-08-26T11:40:00Z"); await touch(join(root, "source.txt"), "2026-08-26T11:40:00Z");
  const revision = git(root, "rev-parse", "HEAD"); await Bun.write(join(root, "bound/.keep"), "");
  const { privateKey, publicKey } = generateKeyPairSync("ed25519"); const keyId = sha(publicKey.export({ type: "spki", format: "der" }));
  const packages = new Map([
    ["server", "bound/jastreamer-server_1.2.3_linux_amd64.deb"], ["control-web", "bound/jastreamer-control_1.2.3_web.zip"],
    ["control-windows", "bound/jastreamer-control_1.2.3_windows.msix"], ["control-android", "bound/jastreamer-control_1.2.3_android_universal.apk"],
    ["renderer", "bound/jastreamer-renderer_1.2.3_windows_amd64.msi"],
  ]);
  await createDeb(root, packages.get("server"));
  await createZip(root, packages.get("control-web"), [["index.html", "<html>Jake Streamer</html>"], ["assets/main.js", "console.log('control')"], ["manifest.json", "{\"name\":\"Jake Streamer\"}"]]);
  await createZip(root, packages.get("control-windows"), [["AppxManifest.xml", "<Package><Identity Name=\"JakeStreamer\"/></Package>"], ["AppxBlockMap.xml", "<BlockMap/ >"], ["AppxSignature.p7x", Buffer.concat([Buffer.from("PKCX"), Buffer.alloc(252, 7)])], ["control.exe", Buffer.alloc(512, 8)]]);
  await createZip(root, packages.get("control-android"), [["AndroidManifest.xml", Buffer.alloc(256, 1)], ["classes.dex", Buffer.alloc(512, 2)], ["META-INF/MANIFEST.MF", "Manifest-Version: 1.0\nName: classes.dex\nSHA-256-Digest: fixture"], ["META-INF/CERT.SF", "Signature-Version: 1.0\nSHA-256-Digest-Manifest: fixture"], ["META-INF/CERT.RSA", Buffer.concat([Buffer.from([0x30, 0x82, 0x00, 0xfc]), Buffer.alloc(252, 3)])]]);
  const msi = Buffer.alloc(1024); Buffer.from([0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1]).copy(msi); msi.writeUInt16LE(9, 30); msi.writeUInt32LE(1, 44); Buffer.from("Property ProductName JakeStreamer ProductVersion 1.2.3").copy(msi, 512); await writeFile(join(root, packages.get("renderer")), msi);
  const artifacts = [];
  for (const [role, path] of packages) { await touch(join(root, path), PACKAGE_AT); artifacts.push({ role, candidateComponent: role.startsWith("control-") ? "control" : role, path, sha256: await shaFile(join(root, path)) }); }
  const trustedCandidates = {};
  for (const component of ["server", "control", "renderer"]) {
    const selected = artifacts.filter((item) => item.candidateComponent === component);
    const manifestPath = `bound/${component}-candidate-manifest.json`; const provenancePath = `bound/${component}-candidate-provenance.json`;
    const manifest = { schema: 1, component, tag: `${component}-v1.2.3`, built_at: BUILD_AT, promotionReady: false, artifacts: selected.map(({ path, sha256 }) => ({ name: basename(path), sha256 })) };
    if (component === "server") manifest.sourceRevision = revision; else manifest.source_revision = revision;
    await json(join(root, manifestPath), manifest);
    await json(join(root, provenancePath), { _type: "https://in-toto.io/Statement/v1", predicateType: "https://slsa.dev/provenance/v1", subject: manifest.artifacts.map(({ name, sha256 }) => ({ name, digest: { sha256 } })), predicate: { runDetails: { metadata: { sourceRevision: revision, startedOn: "2026-08-26T11:45:00.000Z", finishedOn: BUILD_AT } } } });
    await touch(join(root, manifestPath), BUILD_AT); await touch(join(root, provenancePath), BUILD_AT);
    trustedCandidates[component] = { manifestPath, manifestSha256: await shaFile(join(root, manifestPath)), provenancePath, provenanceSha256: await shaFile(join(root, provenancePath)) };
  }
  const contracts = [];
  for (const component of ["control", "renderer"]) { const path = `bound/${component}.contract.json`; await json(join(root, path), { schema: 3, component }); await touch(join(root, path), BUILD_AT); contracts.push({ component, path, sha256: await shaFile(join(root, path)) }); }
  const peers = [];
  for (const [component, artifactRole] of [["server", "server"], ["control", "control-web"], ["renderer", "renderer"]]) { const artifact = artifacts.find((item) => item.role === artifactRole); const path = `bound/${component}.peer.json`; await json(join(root, path), { schema: 1, component, artifact_role: artifactRole, artifact_sha256: artifact.sha256 }); await touch(join(root, path), BUILD_AT); peers.push({ component, artifactRole, path, sha256: await shaFile(join(root, path)) }); }
  const candidateArtifacts = ["server", "control", "renderer"].map((component) => ({ component, sha256: trustedCandidates[component].manifestSha256 }));
  const artifactSetSha256 = shaJson(candidateArtifacts);
  const signedReference = async (path, value) => { await json(join(root, path), value); await touch(join(root, path), OBSERVED_AT); const content = await readFile(join(root, path)); return { path, sha256: sha(content), keyId, signature: sign(null, content, privateKey).toString("base64") }; };
  const captured = async (path, value, capturedAt = CAPTURE_AT) => { await json(join(root, path), { ...value, capturedAt }); await touch(join(root, path), capturedAt); return { path, sha256: await shaFile(join(root, path)) }; };
  const launch = {
    server: { installCommand: ["dpkg", "-i"], launchCommand: ["jastreamer-server", "serve"] },
    "control-web": { installCommand: ["unzip"], launchCommand: ["chromium", "app-mode"] },
    "control-windows": { installCommand: ["Add-AppxPackage"], launchCommand: ["jastreamer-control"] },
    "control-android": { installCommand: ["adb", "install"], launchCommand: ["adb", "shell", "am", "start"] },
    renderer: { installCommand: ["msiexec", "/i"], launchCommand: ["jastreamer-renderer"] },
  };
  const runs = []; let nextPid = 2000;
  await mkdir(join(root, "bound/capture"));
  for (const platform of PLATFORMS) for (const startupOrder of ORDERS) {
    const runId = `task19-${platform}-${startupOrder.replace("_", "-")}`; const controlRole = `control-${platform}`;
    const processRoles = ["server", controlRole, "renderer"];
    const scenarios = [];
    for (const [index, [id, status, code]] of REQUIRED_SCENARIOS.entries()) {
      const prefix = `bound/capture/${runId}-${id}`;
      const before = await captured(`${prefix}-before.json`, { scenarioId: id, phase: "before", revision: index * 2 + 1, authority: "server", state: { queueRevision: index * 2 + 1 } }, "2026-08-26T11:57:50.000Z");
      const requestBody = await captured(`${prefix}-request.json`, { scenarioId: id, operation: id, sequence: index + 1 }, "2026-08-26T11:57:55.000Z");
      const responseBody = await captured(`${prefix}-response.json`, { scenarioId: id, status, code, revision: index * 2 + 2 }, "2026-08-26T11:58:00.000Z");
      const eventBody = await captured(`${prefix}-event.json`, { scenarioId: id, type: "authoritative_state", revision: index * 2 + 2 }, "2026-08-26T11:58:01.000Z");
      const after = await captured(`${prefix}-after.json`, { scenarioId: id, phase: "after", revision: index * 2 + 2, authority: "server", state: { queueRevision: index * 2 + 2 } }, "2026-08-26T11:58:02.000Z");
      scenarios.push({ id, request: { processId: `${runId}-control`, sequence: index + 1, method: "POST", target: `qa/${id}`, body: requestBody }, response: { status, code, body: responseBody }, event: { sequence: index + 1, type: "authoritative_state", stateRevision: index * 2 + 2, body: eventBody }, before: { revision: index * 2 + 1, snapshot: before }, after: { revision: index * 2 + 2, snapshot: after } });
    }
    const startSequence = startupOrder === "server_first" ? ["server", "renderer", controlRole] : [controlRole, "server", "renderer"];
    const processes = processRoles.map((role, index) => { const artifact = artifacts.find((item) => item.role === role); const orderIndex = startSequence.indexOf(role); return { id: index === 1 ? `${runId}-control` : `${runId}-${role}`, role, packagePath: artifact.path, packageSha256: artifact.sha256, installCommand: [...launch[role].installCommand, artifact.path], launchCommand: launch[role].launchCommand, pid: nextPid++, startedAt: `2026-08-26T11:55:0${orderIndex}.000Z`, endedAt: "2026-08-26T11:59:00.000Z", exitCode: 0 }; });
    const allocated = [...processes.map(({ id, pid }) => ({ type: "process", id, pid, observedBy: "os-enumerator" })), ...["container", "browser", "emulator", "temporary_directory", "port"].map((type, index) => ({ type, id: `${runId}-${type.replaceAll("_", "-")}-${index}`, observedBy: "os-enumerator" }))];
    const transcript = { schemaVersion: 2, kind: "installed_product_run_transcript", recordedAt: OBSERVED_AT, runId, platform, startupOrder, artifactSetSha256, processes,
      inventories: { before: [], allocated, after: [] }, scenarios };
    runs.push({ runId, platform, startupOrder, ...(await signedReference(`bound/${runId}.transcript.json`, transcript)) });
  }
  const performanceValue = { schemaVersion: 2, kind: "installed_product_performance_transcript", recordedAt: OBSERVED_AT, artifactSetSha256, runIds: runs.map(({ runId }) => runId), tracks: 100000, zones: Array.from({ length: 8 }, (_, i) => ({ id: `zone-${i + 1}`, queueEntries: 10000 })), browseObservations: [["first", 0, "track-000000"], ["middle", 50000, "track-050000"], ["last", 99999, "track-099999"]].map(([page, offset, trackId]) => ({ page, offset, trackId })), mutationLatenciesMs: Array.from({ length: 160 }, (_, i) => 300 + i % 100) };
  const performance = await signedReference("bound/performance.transcript.json", performanceValue);
  const probeRecords = [];
  for (const [index, id] of REQUIRED_PROBES.entries()) { const stdout = await captured(`bound/probe-${id}-stdout.json`, { line: id === "misleading-output" ? "qualification succeeded" : "rejected" }); const stderr = await captured(`bound/probe-${id}-stderr.json`, { code: id === "misleading-output" ? "MISLEADING_SUCCESS_REJECTED" : "INPUT_REJECTED" }); probeRecords.push({ id, runId: runs[index % runs.length].runId, command: ["receipt-validator", id], startedAt: "2026-08-26T11:57:30.000Z", endedAt: "2026-08-26T11:59:00.000Z", exitCode: 1, stdout, stderr }); }
  const probesValue = { schemaVersion: 2, kind: "installed_product_probe_transcript", recordedAt: OBSERVED_AT, artifactSetSha256, records: probeRecords };
  const probes = await signedReference("bound/probes.transcript.json", probesValue);
  const sourceFiles = [".gitignore", "source.txt"]; const sourceSha256 = shaJson(await Promise.all(sourceFiles.sort().map(async (path) => ({ path, sha256: await shaFile(join(root, path)) }))));
  const receipt = { schemaVersion: 3, kind: "installed_server_control_e2e", recordedAt: NOW, runId: "task19-installed-product", source: { revision, sha256: sourceSha256, dirty: false, files: sourceFiles }, bindings: { sourceSha256, artifactSetSha256, controlContractSha256: contracts[0].sha256, rendererContractSha256: contracts[1].sha256, peerSetSha256: shaJson(peers.map(({ component, sha256 }) => ({ component, sha256 }))) }, artifacts, contracts, peers, runs, performance, probes };
  const options = { now: NOW, root, trustedCandidates, harnessTrust: { keyId, publicKey }, expectedBindings: { ...receipt.bindings } };
  return { root, receipt, options, privateKey, performanceValue, probesValue, trustedCandidates, candidateArtifacts };
};

const validate = (fixture) => validateInstalledProductReceipt(fixture.receipt, fixture.options);
const reject = (fixture, code) => expect(validate(fixture)).toEqual(expect.objectContaining({ ok: false, code }));
const resign = async (fixture, reference, value) => { await json(join(fixture.root, reference.path), value); await touch(join(fixture.root, reference.path), OBSERVED_AT); const content = await readFile(join(fixture.root, reference.path)); reference.sha256 = sha(content); reference.signature = sign(null, content, fixture.privateKey).toString("base64"); };

describe("Todo 19 trusted installed-product qualification", () => {
  test("accepts a real-manifest-shaped candidate-bound authenticated receipt", async () => { const fixture = await makeFixture(); expect(validate(fixture)).toEqual({ ok: true, computedMutationP95Ms: 391 }); });
  test("rejects the old fabricated positive receipt without trusted candidates or harness trust", async () => { const fixture = await makeFixture(); expect(validateInstalledProductReceipt(fixture.receipt, { now: NOW, root: fixture.root })).toEqual(expect.objectContaining({ ok: false, code: "TRUST_REQUIRED" })); });
  test("rejects self-authored candidate manifest trust", async () => { const fixture = await makeFixture(); fixture.options.trustedCandidates = undefined; reject(fixture, "TRUST_REQUIRED"); });
  test("rejects candidate A plus receipt B", async () => { const fixture = await makeFixture(); fixture.options.expectedBindings.artifactSetSha256 = "a".repeat(64); reject(fixture, "EXPECTED_BINDING_MISMATCH"); });
  test("rejects an unauthenticated or forged harness transcript", async () => { const fixture = await makeFixture(); fixture.receipt.runs[0].signature = Buffer.alloc(64).toString("base64"); reject(fixture, "TRANSCRIPT_AUTHENTICATION_FAILED"); });
  test("rejects placeholder package bytes deterministically beyond verifier NOW", async () => { const fixture = await makeFixture(); const item = fixture.receipt.artifacts[0]; const path = join(fixture.root, item.path); await touch(path, "2035-01-01T00:00:00.000Z"); await writeFixtureAt(path, "plain text placeholder", PACKAGE_AT); reject(fixture, "CANDIDATE_ARTIFACT_MISMATCH"); });
  test("rejects package paths used as runtime executables", async () => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const value = JSON.parse(await readFile(join(fixture.root, run.path))); value.processes[1].launchCommand = [value.processes[1].packagePath]; await writeJsonAt(join(fixture.root, run.path), value, OBSERVED_AT); run.sha256 = await shaFile(join(fixture.root, run.path)); reject(fixture, "TRANSCRIPT_AUTHENTICATION_FAILED"); });
  test("rejects arbitrary request body hashes and missing captured bodies", async () => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const value = JSON.parse(await readFile(join(fixture.root, run.path))); value.scenarios[0].request.body.sha256 = "a".repeat(64); await resign(fixture, run, value); reject(fixture, "DIGEST_MISMATCH"); });
  test("rejects identical generic before and after authority snapshots", async () => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const value = JSON.parse(await readFile(join(fixture.root, run.path))); value.scenarios[0].after.snapshot = value.scenarios[0].before.snapshot; await resign(fixture, run, value); reject(fixture, "CAPTURE_REUSED"); });
  test("rejects symlinks for bound inputs", async () => { const fixture = await makeFixture(); const item = fixture.receipt.contracts[0]; const target = `${item.path}.target`; await writeFixtureAt(join(fixture.root, target), await readFile(join(fixture.root, item.path)), BUILD_AT); await rm(join(fixture.root, item.path)); await symlink(basename(target), join(fixture.root, item.path)); reject(fixture, "BOUND_SYMLINK_REJECTED"); });
  test("rejects replacement detected during stable read", async () => { const fixture = await makeFixture(); let replaced = false; fixture.options.readObserver = ({ path }) => { if (!replaced && path.endsWith("control.contract.json")) { replaced = true; const full = join(fixture.root, path); const replacement = `${full}.replacement`; execFileSync("cp", [full, replacement]); execFileSync("mv", [replacement, full]); } }; reject(fixture, "BOUND_FILE_REPLACED"); });
  test("rejects touched artifacts despite unchanged bytes", async () => { const fixture = await makeFixture(); await touch(join(fixture.root, fixture.receipt.artifacts[0].path), "2026-08-26T11:51:00Z"); reject(fixture, "ARTIFACT_TIMESTAMP_INVALID"); });
  test("rejects stale and future transcript timestamps", async () => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const value = JSON.parse(await readFile(join(fixture.root, run.path))); value.recordedAt = "2026-08-26T12:06:00.000Z"; await resign(fixture, run, value); reject(fixture, "TIMESTAMP_INVALID"); });
  test("rejects fabricated misleading-output probe and non-integer exit code", async () => { const fixture = await makeFixture(); const value = JSON.parse(await readFile(join(fixture.root, fixture.receipt.probes.path))); value.records.find(({ id }) => id === "misleading-output").exitCode = "1"; await resign(fixture, fixture.receipt.probes, value); reject(fixture, "PROBE_FAILED"); });
  test("derives cleanup from authenticated pre/post inventories", async () => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const value = JSON.parse(await readFile(join(fixture.root, run.path))); value.inventories.after.push(value.inventories.allocated[0]); await resign(fixture, run, value); reject(fixture, "CLEANUP_INCOMPLETE"); });
  test.each([
    ["negative latency", (value) => { value.mutationLatenciesMs[0] = -1; }, "LATENCY_SAMPLES_INVALID"],
    ["one latency", (value) => { value.mutationLatenciesMs = [1]; }, "LATENCY_SAMPLES_INVALID"],
    ["slow p95", (value) => { value.mutationLatenciesMs.fill(500); }, "PERFORMANCE_THRESHOLD_FAILED"],
    ["wrong per-zone scale", (value) => { value.zones[0].queueEntries = 9999; }, "PERFORMANCE_SCALE_INVALID"],
    ["track count", (value) => { value.tracks = 99999; }, "PERFORMANCE_SCALE_INVALID"],
    ["zone count", (value) => { value.zones.pop(); }, "PERFORMANCE_SCALE_INVALID"],
    ["browse first", (value) => { value.browseObservations[0].trackId = "wrong"; }, "PERFORMANCE_BROWSE_INVALID"],
    ["browse middle", (value) => { value.browseObservations[1].offset = 2; }, "PERFORMANCE_BROWSE_INVALID"],
    ["browse last", (value) => { value.browseObservations.pop(); }, "PERFORMANCE_BROWSE_INVALID"],
  ])("rejects %s in authenticated performance evidence", async (_name, mutate, code) => { const fixture = await makeFixture(); const value = JSON.parse(await readFile(join(fixture.root, fixture.receipt.performance.path))); mutate(value); await resign(fixture, fixture.receipt.performance, value); reject(fixture, code); });
  test("rejects stale performance and probe transcripts", async () => { const fixture = await makeFixture(); const value = JSON.parse(await readFile(join(fixture.root, fixture.receipt.performance.path))); value.recordedAt = "2026-08-25T10:00:00.000Z"; await resign(fixture, fixture.receipt.performance, value); reject(fixture, "TIMESTAMP_INVALID"); });
  test("rejects future bound-file mtimes", async () => { const fixture = await makeFixture(); await touch(join(fixture.root, fixture.receipt.probes.path), "2026-08-26T12:06:00Z"); reject(fixture, "BOUND_FILE_FUTURE"); });
  test("rejects touched trusted manifests independently of their digest", async () => { const fixture = await makeFixture(); await touch(join(fixture.root, fixture.trustedCandidates.control.manifestPath), "2026-08-26T11:51:00Z"); reject(fixture, "CANDIDATE_TIMESTAMP_INVALID"); });
  test("rejects years-old provenance startedOn paired with a fresh finish", async () => { const fixture = await makeFixture(); const trusted = fixture.trustedCandidates.server; const value = JSON.parse(await readFile(join(fixture.root, trusted.provenancePath))); value.predicate.runDetails.metadata.startedOn = "2020-01-01T00:00:00.000Z"; await json(join(fixture.root, trusted.provenancePath), value); await touch(join(fixture.root, trusted.provenancePath), BUILD_AT); trusted.provenanceSha256 = await shaFile(join(fixture.root, trusted.provenancePath)); reject(fixture, "CANDIDATE_PROVENANCE_INVALID"); });
  test("rejects invalid provenance startedOn ordering", async () => { const fixture = await makeFixture(); const trusted = fixture.trustedCandidates.server; const value = JSON.parse(await readFile(join(fixture.root, trusted.provenancePath))); value.predicate.runDetails.metadata.startedOn = "2026-08-26T11:51:00.000Z"; await json(join(fixture.root, trusted.provenancePath), value); await touch(join(fixture.root, trusted.provenancePath), BUILD_AT); trusted.provenanceSha256 = await shaFile(join(fixture.root, trusted.provenancePath)); reject(fixture, "CANDIDATE_PROVENANCE_INVALID"); });
  test("rejects stale probe subprocess timestamps", async () => { const fixture = await makeFixture(); const value = JSON.parse(await readFile(join(fixture.root, fixture.receipt.probes.path))); value.records[0].startedAt = "2026-08-26T11:40:00.000Z"; await resign(fixture, fixture.receipt.probes, value); reject(fixture, "PROBE_FAILED"); });
  test.each([
    ["equal capture timestamps", (items) => { items.request.capturedAt = items.before.capturedAt; }],
    ["out-of-order response capture", (items) => { items.response.capturedAt = "2026-08-26T11:57:00.000Z"; }],
  ])("rejects %s", async (_name, mutate) => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const transcript = JSON.parse(await readFile(join(fixture.root, run.path))); const scenario = transcript.scenarios[0]; const refs = { before: scenario.before.snapshot, request: scenario.request.body, response: scenario.response.body, event: scenario.event.body, after: scenario.after.snapshot }; const items = {}; for (const [kind, ref] of Object.entries(refs)) items[kind] = JSON.parse(await readFile(join(fixture.root, ref.path))); mutate(items); for (const [kind, ref] of Object.entries(refs)) { await json(join(fixture.root, ref.path), items[kind]); await touch(join(fixture.root, ref.path), items[kind].capturedAt); ref.sha256 = await shaFile(join(fixture.root, ref.path)); } await resign(fixture, run, transcript); reject(fixture, "CAPTURE_ORDER_INVALID"); });
  test("rejects captured body timestamp mismatches", async () => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const transcript = JSON.parse(await readFile(join(fixture.root, run.path))); const ref = transcript.scenarios[0].request.body; const body = JSON.parse(await readFile(join(fixture.root, ref.path))); body.capturedAt = "2026-08-26T11:57:00.000Z"; await json(join(fixture.root, ref.path), body); await touch(join(fixture.root, ref.path), CAPTURE_AT); ref.sha256 = await shaFile(join(fixture.root, ref.path)); await resign(fixture, run, transcript); reject(fixture, "CAPTURE_TIMESTAMP_INVALID"); });
  test("requires receipt timestamp after every constituent observation", async () => { const fixture = await makeFixture(); fixture.receipt.recordedAt = "2026-08-26T11:59:00.000Z"; reject(fixture, "TIMESTAMP_INVALID"); });
  test.each([
    ["run", (fixture) => fixture.receipt.runs[0]],
    ["performance", (fixture) => fixture.receipt.performance],
    ["probe/latest constituent", (fixture) => fixture.receipt.probes],
  ])("rejects receipt-equals-latest-transcript for %s", async (_name, select) => { const fixture = await makeFixture(); const reference = select(fixture); const value = JSON.parse(await readFile(join(fixture.root, reference.path))); value.recordedAt = NOW; await json(join(fixture.root, reference.path), value); await touch(join(fixture.root, reference.path), NOW); const content = await readFile(join(fixture.root, reference.path)); reference.sha256 = sha(content); reference.signature = sign(null, content, fixture.privateKey).toString("base64"); reject(fixture, "TIMESTAMP_INVALID"); });
  test("rejects omitted independent literal scenario and probe requirements", async () => { const fixture = await makeFixture(); expect(REQUIRED_SCENARIOS).toHaveLength(30); expect(REQUIRED_PROBES).toHaveLength(7); const run = fixture.receipt.runs[0]; const value = JSON.parse(await readFile(join(fixture.root, run.path))); value.scenarios = value.scenarios.filter(({ id }) => id !== REQUIRED_SCENARIOS.at(-1)[0]); await resign(fixture, run, value); reject(fixture, "SCENARIO_MISSING"); });
  test("requires installed evidence for qualification bundles and binds enclosing expectations", async () => { const bundle = createSyntheticBundle(NOW); bundle.purpose = "qualification"; expect(validateProductBundle(bundle, { now: NOW, profile: "qualification", k17RunnerLabel: "lab" })).toEqual(expect.objectContaining({ ok: false, code: "INSTALLED_PRODUCT_RECEIPT_REQUIRED" })); });
  test("requires installed evidence for qualification even under fixture profile", () => { const bundle = createSyntheticBundle(NOW); bundle.purpose = "qualification"; expect(validateProductBundle(bundle, { now: NOW, profile: "fixture" })).toEqual({ ok: false, code: "INSTALLED_PRODUCT_RECEIPT_REQUIRED", path: "installedProductReceipt" }); });
  test.each([
    ["header-only RPM", "server", "candidate.rpm", Buffer.from([0xed, 0xab, 0xee, 0xdb])],
    ["header-only ZIP", "control-web", "candidate.zip", Buffer.from("PK\x03\x04")],
    ["header-only APK", "control-android", "candidate.apk", Buffer.from("PK\x03\x04")],
    ["header-only MSIX", "control-windows", "candidate.msix", Buffer.from("PK\x03\x04")],
    ["header-only MSI", "renderer", "candidate.msi", Buffer.from([0xd0, 0xcf, 0x11, 0xe0])],
  ])("rejects %s", (_name, role, path, bytes) => expect(validatePackageStructure(role, path, bytes)).toBe(false));
  test.each([["captured response body", "response"], ["captured event body", "event"]])("rejects direct %s mutation", async (_name, kind) => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const transcript = JSON.parse(await readFile(join(fixture.root, run.path))); const ref = transcript.scenarios[0][kind].body; await writeFixtureAt(join(fixture.root, ref.path), "mutated", CAPTURE_AT); reject(fixture, "DIGEST_MISMATCH"); });
  test("rejects duplicate run identities", async () => { const fixture = await makeFixture(); fixture.receipt.runs[1].runId = fixture.receipt.runs[0].runId; reject(fixture, "RUN_ID_INVALID"); });
  test("rejects header-only crafted package placeholders", async () => { const fixture = await makeFixture(); const server = fixture.receipt.artifacts.find(({ role }) => role === "server"); await writeFixtureAt(join(fixture.root, server.path), "!<arch>\n", PACKAGE_AT); reject(fixture, "CANDIDATE_ARTIFACT_MISMATCH"); });
  test.each([
    ["duplicate process id", (value) => { value.processes[1].id = value.processes[0].id; }, "PROCESS_IDENTITY_INVALID"],
    ["duplicate pid", (value) => { value.processes[1].pid = value.processes[0].pid; }, "PROCESS_IDENTITY_INVALID"],
    ["wrong startup order", (value) => { value.processes.find(({ role }) => role === "server").startedAt = "2026-08-26T11:58:00.000Z"; }, "STARTUP_ORDER_INVALID"],
    ["missing process inventory link", (value) => { value.inventories.allocated = value.inventories.allocated.filter(({ type }) => type !== "process"); }, "CLEANUP_INCOMPLETE"],
    ["future process end", (value) => { value.processes[0].endedAt = "2026-08-26T12:06:00.000Z"; }, "PROCESS_OBSERVATION_INVALID"],
    ["semantic response mismatch", (value) => { value.scenarios[0].response.code = "WRONG"; }, "SCENARIO_MISMATCH"],
    ["semantic event mismatch", (value) => { value.scenarios[0].event.stateRevision += 4; }, "AUTHORITY_NOT_PRESERVED"],
  ])("rejects %s", async (_name, mutate, code) => { const fixture = await makeFixture(); const run = fixture.receipt.runs[0]; const value = JSON.parse(await readFile(join(fixture.root, run.path))); mutate(value); await resign(fixture, run, value); reject(fixture, code); });
  test("accepts enclosing bundle bindings and rejects candidate A plus installed receipt B", async () => {
    const fixture = await makeFixture(); const bundle = createSyntheticBundle(NOW); const binding = fixture.receipt.bindings;
    bundle.source = { revision: fixture.receipt.source.revision, sha256: binding.sourceSha256 };
    bundle.contracts = { controlSha256: binding.controlContractSha256, rendererSha256: binding.rendererContractSha256 };
    bundle.peers = fixture.receipt.peers.map(({ component, sha256 }) => ({ component, sha256 }));
    const candidate = bundle.receipts.find(({ kind }) => kind === "candidate"); candidate.payload = { artifactSetSha256: binding.artifactSetSha256, artifacts: fixture.candidateArtifacts };
    for (const lane of bundle.receipts) { lane.binding = { ...binding }; if (lane.kind === "server_control_e2e") lane.payload.artifactSha256 = binding.artifactSetSha256; if (lane.kind === "k17") lane.payload.artifactSha256 = binding.artifactSetSha256; }
    bundle.installedProductReceipt = fixture.receipt;
    const options = { now: NOW, profile: "fixture", installedProductRoot: fixture.root, trustedCandidates: fixture.trustedCandidates, harnessTrust: fixture.options.harnessTrust };
    expect(validateProductBundle(bundle, options).ok).toBe(true);
    candidate.payload.artifacts[0].sha256 = "a".repeat(64); candidate.payload.artifactSetSha256 = shaJson(candidate.payload.artifacts); for (const lane of bundle.receipts) { lane.binding.artifactSetSha256 = candidate.payload.artifactSetSha256; if (lane.kind === "server_control_e2e") lane.payload.artifactSha256 = candidate.payload.artifactSetSha256; if (lane.kind === "k17") lane.payload.artifactSha256 = candidate.payload.artifactSetSha256; }
    expect(validateProductBundle(bundle, options)).toEqual(expect.objectContaining({ ok: false, code: "EXPECTED_BINDING_MISMATCH" }));
  });
  test("compiles and executes product and installed schemas with external ref resolution", async () => { const fixture = await makeFixture(); const compiled = compileReceiptSchemas(); expect(compiled.installed(fixture.receipt)).toEqual([]); const bad = structuredClone(fixture.receipt); delete bad.runs; expect(compiled.installed(bad).some(({ keyword }) => keyword === "required")).toBe(true); expect(compiled.productRefResolved).toBe(true); });
});


describe("receipt redaction value scanning", () => {
  test.each([
    ["Bearer credential", "Authorization: Bearer abc.def.ghi", "SECRET_PRESENT"], ["JWT", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.signature", "SECRET_PRESENT"],
    ["embedded path", "media path=/srv/music/song.flac", "ABSOLUTE_PATH_PRESENT"], ["UNC", "\\\\nas.internal\\media\\song.flac", "ABSOLUTE_PATH_PRESENT"],
    ["MAC", "device 02:42:ac:11:00:02", "LAN_IDENTITY_PRESENT"], ["endpoint id", "endpoint-id=raw-device-42", "LAN_IDENTITY_PRESENT"],
    ["private hostname", "server.lan", "LAN_IDENTITY_PRESENT"], ["internal hostname", "renderer.internal", "LAN_IDENTITY_PRESENT"],
    ["private suffix", "speaker.private", "LAN_IDENTITY_PRESENT"], ["localhost URL", "http://localhost:8080/media", "LAN_IDENTITY_PRESENT"],
    ["IPv4 loopback", "127.0.0.1", "LAN_IDENTITY_PRESENT"], ["IPv4 link local", "169.254.7.8", "LAN_IDENTITY_PRESENT"],
    ["IPv6 loopback", "http://[::1]/", "LAN_IDENTITY_PRESENT"], ["IPv6 link local", "fe80::1234", "LAN_IDENTITY_PRESENT"],
    ["raw device identity", "device-id=renderer-living-room", "LAN_IDENTITY_PRESENT"],
    ["unlabeled raw device", "raw-device-42", "LAN_IDENTITY_PRESENT"], ["prose-prefixed raw device", "device raw-device-42", "LAN_IDENTITY_PRESENT"],
  ])("rejects %s", (_name, value, code) => expect(findUnsafeEvidence({ value })).toEqual(expect.objectContaining({ code })));
});
