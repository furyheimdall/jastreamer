import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { execFileSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, utimes, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";
import { materializeScenarioContract, TASK19_SCENARIO_IDS, task19Scenario } from "./scenario-contract.mjs";

const NOW = "2026-08-26T12:00:00.000Z";
const BUILD_AT = "2026-08-26T11:50:00.000Z";
const OBSERVED_AT = "2026-08-26T11:59:30.000Z";
const CAPTURE_AT = "2026-08-26T11:58:00.000Z";
const REQUIRED_SCENARIOS = TASK19_SCENARIO_IDS.map((id) => { const contract = task19Scenario(id); return [id, contract.expected.status, contract.expected.code]; });
const REQUIRED_PROBES = ["malformed-input", "stale-state", "dirty-worktree", "hung-command", "flaky-test", "misleading-output", "repeated-interruption"];
const PLATFORMS = ["web", "windows", "android"];
const ORDERS = ["server_first", "control_first"];
const sha = (value) => createHash("sha256").update(value).digest("hex");
const shaFile = async (path) => sha(await readFile(path));
const shaJson = (value) => sha(JSON.stringify(value));
const json = (path, value) => writeFile(path, `${JSON.stringify(value, null, 2)}\n`);
const git = (root, ...args) => execFileSync("git", ["-C", root, ...args], { encoding: "utf8" }).trim();
const touch = (path, date) => utimes(path, new Date(date), new Date(date));

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
export const createInstalledProductFixture = async (requestedRoot) => {
  const root = requestedRoot ?? await mkdtemp(join(tmpdir(), "task19-secure-receipt-"));
  if (requestedRoot !== undefined) await mkdir(root, { recursive: true });
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
  await createZip(root, packages.get("control-web"), [["index.html", "<html>Jake Streamer</html>"], ["main.dart.js", "console.log('flutter control')"], ["flutter_bootstrap.js", "console.log('bootstrap')"], ["flutter.js", "console.log('loader')"], ["manifest.json", "{\"name\":\"Jake Streamer\"}"], ["assets/AssetManifest.bin", "manifest"]]);
  await createZip(root, packages.get("control-windows"), [["AppxManifest.xml", "<Package><Identity Name=\"JakeStreamer\"/></Package>"], ["AppxBlockMap.xml", "<BlockMap/ >"], ["AppxSignature.p7x", Buffer.concat([Buffer.from("PKCX"), Buffer.alloc(252, 7)])], ["control.exe", Buffer.alloc(512, 8)]]);
  await createZip(root, packages.get("control-android"), [["AndroidManifest.xml", Buffer.alloc(256, 1)], ["classes.dex", Buffer.alloc(512, 2)], ["META-INF/MANIFEST.MF", "Manifest-Version: 1.0\nName: classes.dex\nSHA-256-Digest: fixture"], ["META-INF/CERT.SF", "Signature-Version: 1.0\nSHA-256-Digest-Manifest: fixture"], ["META-INF/CERT.RSA", Buffer.concat([Buffer.from([0x30, 0x82, 0x00, 0xfc]), Buffer.alloc(252, 3)])]]);
  const msi = Buffer.alloc(1024); Buffer.from([0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1]).copy(msi); msi.writeUInt16LE(9, 30); msi.writeUInt32LE(1, 44); Buffer.from("Property ProductName JakeStreamer ProductVersion 1.2.3").copy(msi, 512); await writeFile(join(root, packages.get("renderer")), msi);
  const artifacts = [];
  for (const [role, path] of packages) { await touch(join(root, path), "2026-08-26T11:49:00Z"); artifacts.push({ role, candidateComponent: role.startsWith("control-") ? "control" : role, path, sha256: await shaFile(join(root, path)) }); }
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
    server: { installCommand: ["wsl.exe", "--exec", "sudo", "dpkg", "-i"], packageArgumentIndex: 5, launchCommand: ["wsl.exe", "--exec", "systemctl", "start", "jastreamer-server.service"] },
    "control-web": { installCommand: ["unzip"], packageArgumentIndex: 1, launchCommand: ["chromium", "app-mode"] },
    "control-windows": { installCommand: ["Add-AppxPackage"], packageArgumentIndex: 1, launchCommand: ["jastreamer-control"] },
    "control-android": { installCommand: ["adb", "install"], packageArgumentIndex: 2, launchCommand: ["adb", "shell", "am", "start"] },
    renderer: { installCommand: ["msiexec", "/i"], packageArgumentIndex: 2, launchCommand: ["jastreamer-renderer"] },
  };
  const runs = []; let nextPid = 2000;
  await mkdir(join(root, "bound/capture"));
  for (const platform of PLATFORMS) for (const startupOrder of ORDERS) {
    const runId = `task19-${platform}-${startupOrder.replace("_", "-")}`; const controlRole = `control-${platform}`;
    const processRoles = ["server", controlRole, "renderer"];
    const scenarios = [];
    for (const [index, [id, status, code]] of REQUIRED_SCENARIOS.entries()) {
      const prefix = `bound/capture/${runId}-${id}`;
      const material = { zoneId: "zone-live", rendererId: "renderer-live", controllerId: "controller-live", catalogRoot: "catalog-live", trackId: "track-live", unavailableTrackId: "track-unavailable", entryId: "entry-live" }; const contract = materializeScenarioContract(task19Scenario(id), material); const observedMethod = contract.expected.surface === "control" ? "CONTROL" : contract.surface.request.method; const observedTarget = contract.expected.surface === "control" ? "control-certificate-binding" : contract.surface.request.route; const observedBody = contract.expected.surface === "control" ? null : contract.surface.request.body; const beforeRevision = index * 2 + 1; const unchangedScenario = contract.expected.failure || contract.expected.eventRequired === false; const afterRevision = unchangedScenario ? beforeRevision : beforeRevision + 1;
      const before = await captured(`${prefix}-before.json`, { scenarioId: id, phase: "before", revision: beforeRevision, authority: "server", state: { named: contract.state.namedDelta, revision: beforeRevision } }, "2026-08-26T11:57:50.000Z");
      const requestBody = await captured(`${prefix}-request.json`, { scenarioId: id, method: observedMethod, target: observedTarget, headers: Object.fromEntries(contract.surface.request.requiredHeaders.map((name) => [name, `${name}-observed`])), body: observedBody }, "2026-08-26T11:57:55.000Z");
      const responseBody = await captured(`${prefix}-response.json`, { scenarioId: id, status, code, raw: { status, body: { semantic: contract.expected.semantic }, semantic: contract.expected.semantic }, revision: afterRevision }, "2026-08-26T11:58:00.000Z");
      const rawEvent = unchangedScenario ? { absent: true } : { type: contract.event.kind, resource: contract.event.resource, revision: afterRevision };
      const eventBody = await captured(`${prefix}-event.json`, { scenarioId: id, initialEvent: { type: "snapshot", stateRevision: beforeRevision }, rawEvent }, "2026-08-26T11:58:01.000Z");
      const after = await captured(`${prefix}-after.json`, { scenarioId: id, phase: "after", revision: afterRevision, authority: "server", state: { named: contract.state.namedDelta, revision: afterRevision } }, "2026-08-26T11:58:02.000Z");
      scenarios.push({ id, material, request: { processId: `${runId}-control`, sequence: unchangedScenario ? 0 : index + 1, method: observedMethod, target: observedTarget, body: requestBody }, response: { status, code, body: responseBody }, event: { sequence: unchangedScenario ? 0 : index + 1, type: unchangedScenario ? "none" : contract.event.kind, stateRevision: unchangedScenario ? 0 : afterRevision, body: eventBody }, before: { revision: beforeRevision, snapshot: before }, after: { revision: afterRevision, snapshot: after } });
    }
    const startSequence = startupOrder === "server_first" ? ["server", "renderer", controlRole] : [controlRole, "server", "renderer"];
    const processes = processRoles.map((role, index) => { const artifact = artifacts.find((item) => item.role === role); const orderIndex = startSequence.indexOf(role); const tlsBinding = role === "server" ? { kind: "capture-origin", host: "loopback_dns", port: 9555, certificateSha256: "c".repeat(64), spkiSha256: "d".repeat(64), browserTrust: "exact-spki" } : role === "control-web" ? { kind: "web-origin", host: "loopback_ipv4", port: 9443, certificateSha256: "c".repeat(64), spkiSha256: "d".repeat(64), browserTrust: "exact-spki" } : undefined; return { id: index === 1 ? `${runId}-control` : `${runId}-${role}`, role, packagePath: artifact.path, packageSha256: artifact.sha256, installCommand: [...launch[role].installCommand, artifact.path], packageArgumentIndex: launch[role].packageArgumentIndex, launchCommand: launch[role].launchCommand, pid: nextPid++, startedAt: `2026-08-26T11:55:0${orderIndex}.000Z`, endedAt: "2026-08-26T11:59:00.000Z", exitCode: 0, ...(tlsBinding === undefined ? {} : { tlsBinding }) }; });
    const allocated = [...processes.map(({ id, pid }) => ({ type: "process", id, pid, observedBy: "windows-cim-process" })), ...["container", "browser", "emulator", "temporary_directory", "port"].map((type, index) => ({ type, id: `${runId}-${type.replaceAll("_", "-")}-${index}`, observedBy: `${type}-enumerator` }))];
    const transcript = { schemaVersion: 2, kind: "installed_product_run_transcript", recordedAt: OBSERVED_AT, runId, platform, startupOrder, artifactSetSha256, tlsPlan: { capture: processes.find((process) => process.role === "server").tlsBinding, ...(platform === "web" ? { web: processes.find((process) => process.role === "control-web").tlsBinding } : {}) }, processes,
      inventories: { before: [], allocated, after: [] }, scenarios };
    runs.push({ runId, platform, startupOrder, ...(await signedReference(`bound/${runId}.transcript.json`, transcript)) });
  }
  const performanceValue = { schemaVersion: 2, kind: "installed_product_performance_transcript", recordedAt: OBSERVED_AT, artifactSetSha256, runIds: runs.map(({ runId }) => runId), tracks: 100000, zones: Array.from({ length: 8 }, (_, i) => ({ id: `zone-${i + 1}`, queueEntries: 10000 })), browseObservations: [["first", 0, "track-000000"], ["middle", 50000, "track-050000"], ["last", 99999, "track-099999"]].map(([page, offset, trackId]) => ({ page, offset, trackId })), mutationLatenciesMs: Array.from({ length: 160 }, (_, i) => 300 + i % 100) };
  const performance = await signedReference("bound/performance.transcript.json", performanceValue);
  const probeRecords = [];
  for (const [index, id] of REQUIRED_PROBES.entries()) { const stdout = await captured(`bound/probe-${id}-stdout.json`, { line: id === "misleading-output" ? "qualification succeeded" : "rejected" }, "2026-08-26T11:58:00.000Z"); const stderr = await captured(`bound/probe-${id}-stderr.json`, { code: id === "misleading-output" ? "MISLEADING_SUCCESS_REJECTED" : "INPUT_REJECTED" }, "2026-08-26T11:58:01.000Z"); probeRecords.push({ id, runId: runs[index % runs.length].runId, command: ["receipt-validator", id], startedAt: "2026-08-26T11:57:30.000Z", endedAt: "2026-08-26T11:59:00.000Z", exitCode: 1, stdout, stderr }); }
  const probesValue = { schemaVersion: 2, kind: "installed_product_probe_transcript", recordedAt: OBSERVED_AT, artifactSetSha256, records: probeRecords };
  const probes = await signedReference("bound/probes.transcript.json", probesValue);
  const sourceFiles = [".gitignore", "source.txt"]; const sourceSha256 = shaJson(await Promise.all(sourceFiles.sort().map(async (path) => ({ path, sha256: await shaFile(join(root, path)) }))));
  const receipt = { schemaVersion: 3, kind: "installed_server_control_e2e", recordedAt: NOW, runId: "task19-installed-product", source: { revision, sha256: sourceSha256, dirty: false, files: sourceFiles }, bindings: { sourceSha256, artifactSetSha256, controlContractSha256: contracts[0].sha256, rendererContractSha256: contracts[1].sha256, peerSetSha256: shaJson(peers.map(({ component, sha256 }) => ({ component, sha256 }))) }, artifacts, contracts, peers, runs, performance, probes };
  const options = { now: NOW, root, trustedCandidates, harnessTrust: { keyId, publicKey }, expectedBindings: { ...receipt.bindings } };
  return { root, receipt, options, privateKey, publicKey, keyId, performanceValue, probesValue, trustedCandidates, candidateArtifacts };
};
