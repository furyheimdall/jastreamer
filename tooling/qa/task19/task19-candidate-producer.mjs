#!/usr/bin/env node
import { createHash } from "node:crypto";
import { lstatSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const fail = (code, path) => ({ ok: false, code, path });
const parseArgs = (values) => { const parsed = {}; for (let index = 0; index < values.length; index += 2) { if (!values[index]?.startsWith("--") || values[index + 1] === undefined) return { issue: "TASK19_PRODUCER_USAGE" }; parsed[values[index].slice(2)] = values[index + 1]; } return parsed; };
const safeRead = (root, path, field) => {
  if (typeof path !== "string" || path === "" || isAbsolute(path) || path.split(/[\\/]/).includes("..")) return { issue: fail("TASK19_PRODUCER_PATH_INVALID", field) };
  const absolute = resolve(root, path); const inside = relative(root, absolute); if (inside === "" || inside === ".." || inside.startsWith(`..${sep}`) || isAbsolute(inside)) return { issue: fail("TASK19_PRODUCER_PATH_INVALID", field) };
  let current = resolve(root); for (const part of inside.split(sep)) { current = resolve(current, part); try { if (lstatSync(current).isSymbolicLink()) return { issue: fail("TASK19_PRODUCER_REPARSE_POINT", field) }; } catch { return { issue: fail("TASK19_PRODUCER_INPUT_MISSING", field) }; } }
  return { bytes: readFileSync(absolute), absolute };
};
const jsonRead = (root, path, field) => { const read = safeRead(root, path, field); if (read.issue) return read; try { return { ...read, value: JSON.parse(read.bytes) }; } catch { return { issue: fail("TASK19_PRODUCER_JSON_INVALID", field) }; } };
const reference = (kind, path, bytes) => ({ kind, path, sha256: sha256(bytes), size: bytes.length });
const observedArtifacts = (root, path, component, revision, selected) => {
  const manifest = jsonRead(root, path, `${component}Manifest`); if (manifest.issue) return manifest;
  if (manifest.value.component !== component || manifest.value.source_revision !== revision || !Array.isArray(manifest.value.artifacts)) return { issue: fail("TASK19_OBSERVATION_MANIFEST_INVALID", `${component}Manifest`) };
  for (const item of selected) { const match = manifest.value.artifacts.filter((artifact) => artifact?.name === basename(item.path)); if (match.length !== 1 || match[0].sha256 !== sha256(item.bytes)) return { issue: fail("TASK19_OBSERVED_ARTIFACT_DRIFT", item.kind) }; }
  return { value: manifest.value };
};

export const produceTask19Closure = (input) => {
  const reducer = jsonRead(input.root, input.reducer, "reducer"); if (reducer.issue) return reducer.issue;
  if (reducer.value.schemaVersion !== 1 || reducer.value.kind !== "authoritative_product_qualification" || reducer.value.status !== "satisfied" || reducer.value.promotableInput !== true) return fail("TASK19_AUTHORITATIVE_REDUCER_PENDING", "reducer");
  const revision = reducer.value.caller?.sha; if (typeof revision !== "string" || !/^[0-9a-f]{40}$/.test(revision)) return fail("TASK19_SOURCE_REVISION_INVALID", "reducer.caller.sha");
  const inputs = [
    ["server-linux-deb", input.server], ["control-web", input.web], ["control-windows", input.windows], ["control-android", input.android], ["renderer-windows-msi", input.renderer],
    ["k17-receipt", input.k17], ["wasapi-receipt", input.wasapi], ["authoritative-staged-manifest", input.reducer],
  ];
  const reads = [];
  for (const [kind, path] of inputs) { const read = safeRead(input.root, path, kind); if (read.issue) return read.issue; reads.push({ kind, path, ...read }); }
  const byKind = Object.fromEntries(reads.map((item) => [item.kind, item]));
  const serverObservation = observedArtifacts(input.root, input.serverManifest, "server", revision, [byKind["server-linux-deb"]]); if (serverObservation.issue) return serverObservation.issue;
  const controlObservation = observedArtifacts(input.root, input.controlManifest, "control", revision, [byKind["control-web"], byKind["control-windows"], byKind["control-android"]]); if (controlObservation.issue) return controlObservation.issue;
  const rendererObservation = observedArtifacts(input.root, input.rendererManifest, "renderer", revision, [byKind["renderer-windows-msi"]]); if (rendererObservation.issue) return rendererObservation.issue;
  for (const [kind, path] of [["k17", input.k17], ["wasapi", input.wasapi]]) { const receipt = jsonRead(input.root, path, kind); if (receipt.issue) return receipt.issue; if (receipt.value.qualification_status !== "qualified") return fail("TASK19_PHYSICAL_RECEIPT_PENDING", kind); if (receipt.value.source_revision !== revision) return fail("TASK19_PHYSICAL_RECEIPT_BINDING_DRIFT", kind); if (kind === "k17" && receipt.value.candidate_sha256 !== input.candidateSha256) return fail("TASK19_PHYSICAL_RECEIPT_BINDING_DRIFT", kind); if (kind === "wasapi" && receipt.value.binding?.candidate_sha256 !== input.candidateSha256) return fail("TASK19_PHYSICAL_RECEIPT_BINDING_DRIFT", kind); }
  const trust = JSON.parse(readFileSync(resolve(new URL("task19-production-trust-v1.json", import.meta.url).pathname))); const driverBytes = readFileSync(resolve(new URL(trust.driver.path.startsWith("tooling/") ? `../../../${trust.driver.path}` : trust.driver.path, import.meta.url).pathname));
  if (sha256(driverBytes) !== trust.driver.sha256) return fail("TASK19_REPOSITORY_DRIVER_DIGEST_MISMATCH", "producer.driver");
  const destination = resolve(input.outputRoot); mkdirSync(destination, { recursive: false, mode: 0o700 }); const refs = {};
  for (const item of reads) { const path = `files/${item.kind}-${basename(item.path)}`; const target = join(destination, path); mkdirSync(dirname(target), { recursive: true, mode: 0o700 }); writeFileSync(target, item.bytes, { mode: 0o400, flag: "wx" }); const immediate = readFileSync(target); if (sha256(immediate) !== sha256(item.bytes)) return fail("TASK19_PRODUCER_SNAPSHOT_DRIFT", item.kind); refs[item.kind] = reference(item.kind, path, item.bytes); }
  const closure = { schemaVersion: 2, kind: "task19_exact_candidate_closure", source: { revision }, producer: { driverSha256: trust.driver.sha256 }, files: { server: refs["server-linux-deb"], controlWeb: refs["control-web"], controlWindows: refs["control-windows"], controlAndroid: refs["control-android"], renderer: refs["renderer-windows-msi"] }, receipts: { k17: refs["k17-receipt"], wasapi: refs["wasapi-receipt"] }, stagedManifest: refs["authoritative-staged-manifest"] };
  writeFileSync(join(destination, "task19-candidate-closure.json"), `${JSON.stringify(closure, null, 2)}\n`, { mode: 0o400 }); return { ok: true, closure };
};

if (process.argv[1] && resolve(process.argv[1]) === new URL(import.meta.url).pathname) {
  const args = parseArgs(process.argv.slice(2)); const required = ["root", "reducer", "server", "server-manifest", "control-manifest", "web", "windows", "android", "renderer", "renderer-manifest", "k17", "wasapi", "candidate-sha256", "output-root", "status"];
  if (args.issue || required.some((key) => !args[key])) { process.stderr.write("TASK19_PRODUCER_USAGE\n"); process.exitCode = 64; }
  else { const result = produceTask19Closure({ root: resolve(args.root), reducer: args.reducer, server: args.server, serverManifest: args["server-manifest"], controlManifest: args["control-manifest"], web: args.web, windows: args.windows, android: args.android, renderer: args.renderer, rendererManifest: args["renderer-manifest"], k17: args.k17, wasapi: args.wasapi, candidateSha256: args["candidate-sha256"], outputRoot: args["output-root"] }); writeFileSync(resolve(args.status), `${JSON.stringify(result.ok ? { schemaVersion: 1, kind: "task19_candidate_producer_status", status: "satisfied", promotable: false, closureSha256: sha256(Buffer.from(JSON.stringify(result.closure))) } : { schemaVersion: 1, kind: "task19_candidate_producer_status", status: "denied", promotable: false, code: result.code, path: result.path }, null, 2)}\n`); if (!result.ok) process.exitCode = 77; }
}
