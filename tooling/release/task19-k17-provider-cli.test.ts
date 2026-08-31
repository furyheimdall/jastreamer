import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test } from "bun:test";

const revision = "a".repeat(40);
const crcTable = Array.from({ length: 256 }, (_, index) => { let value = index; for (let bit = 0; bit < 8; bit += 1) value = (value & 1) === 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1; return value >>> 0; });
const crc32 = (bytes: Uint8Array): number => { let value = 0xffffffff; for (const byte of bytes) value = (crcTable[(value ^ byte) & 255] ?? 0) ^ (value >>> 8); return (value ^ 0xffffffff) >>> 0; };
const zip = (path: string, bytes: Uint8Array): Uint8Array => {
  const name = Buffer.from(path); const local = Buffer.alloc(30); local.writeUInt32LE(0x04034b50); local.writeUInt16LE(20, 4); local.writeUInt32LE(crc32(bytes), 14); local.writeUInt32LE(bytes.length, 18); local.writeUInt32LE(bytes.length, 22); local.writeUInt16LE(name.length, 26);
  const central = Buffer.alloc(46); central.writeUInt32LE(0x02014b50); central.writeUInt16LE(0x0314, 4); central.writeUInt16LE(20, 6); central.writeUInt32LE(crc32(bytes), 16); central.writeUInt32LE(bytes.length, 20); central.writeUInt32LE(bytes.length, 24); central.writeUInt16LE(name.length, 28); central.writeUInt32LE((0o100644 << 16) >>> 0, 38);
  const end = Buffer.alloc(22); end.writeUInt32LE(0x06054b50); end.writeUInt16LE(1, 8); end.writeUInt16LE(1, 10); end.writeUInt32LE(central.length + name.length, 12); end.writeUInt32LE(local.length + name.length + bytes.length, 16);
  return Buffer.concat([local, name, Buffer.from(bytes), central, name, end]);
};

test("current qualification run is denied before provider reads and leaves zero residue", async () => {
  const root = mkdtempSync(join(tmpdir(), "task19-k17-current-run-"));
  const context = join(root, "context.json"); const outputRoot = join(root, "inputs", "k17"); const status = join(root, "status.json");
  writeFileSync(context, JSON.stringify({ repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/server-release.yml", eventName: "workflow_dispatch", runId: "1001", runAttempt: 2, currentSha: revision, conclusion: "success", artifactId: "3001", artifactName: "k17-qualification", artifactDigest: "b".repeat(64), artifactSize: 123, artifactCreatedAt: "2026-08-26T11:00:00Z", artifactExpiresAt: "2026-08-27T12:00:00Z", qualificationRunId: "1001", qualificationCurrentSha: revision }));
  try {
    const child = Bun.spawn(["bun", resolve(import.meta.dirname, "task19-k17-provider-cli.ts"), "--context", context, "--output-root", outputRoot, "--status", status], { env: { ...process.env }, stdout: "pipe", stderr: "pipe" });
    expect(await child.exited).toBe(77);
    expect(JSON.parse(readFileSync(status, "utf8"))).toMatchObject({ status: "pending", code: "K17_PROVIDER_AUTHORIZATION_DENIED", candidateBytesWritten: 0, externalWrites: 0 });
    expect(existsSync(outputRoot)).toBe(false);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

test("completed upstream K17 tuple is authenticated before one atomic materialization", async () => {
  const root = mkdtempSync(join(tmpdir(), "task19-k17-completed-"));
  const context = join(root, "context.json"); const outputRoot = join(root, "inputs", "k17"); const output = join(outputRoot, "k17-qualification.json"); const status = join(root, "status.json");
  const archive = zip("k17-qualification.json", Buffer.from("{\"qualification_status\":\"qualified\"}")); const digest = createHash("sha256").update(archive).digest("hex"); const headers = { date: "Tue, 26 Aug 2026 12:00:00 GMT" };
  writeFileSync(context, JSON.stringify({ repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/server-release.yml", eventName: "workflow_dispatch", runId: "1001", runAttempt: 2, currentSha: revision, conclusion: "success", artifactId: "3001", artifactName: "k17-qualification", artifactDigest: digest, artifactSize: archive.length, artifactCreatedAt: "2026-08-26T11:00:00Z", artifactExpiresAt: "2026-08-27T12:00:00Z", qualificationRunId: "2001", qualificationCurrentSha: revision }));
  const server = Bun.serve({ port: 0, fetch(request) { const path = new URL(request.url).pathname; if (path.endsWith("/actions/runs/1001")) return Response.json({ id: 1001, run_attempt: 2, head_sha: revision, repository: { full_name: "furyheimdall/jastreamer" }, path: ".github/workflows/server-release.yml", event: "workflow_dispatch", conclusion: "success" }, { headers }); if (path.endsWith("/artifacts")) return Response.json({ total_count: 1, artifacts: [{ id: 3001, name: "k17-qualification", digest: `sha256:${digest}`, size_in_bytes: archive.length, created_at: "2026-08-26T11:00:00Z", expires_at: "2026-08-27T12:00:00Z", expired: false, workflow_run: { id: 1001, run_attempt: 2, repository: "furyheimdall/jastreamer", head_sha: revision } }] }, { headers }); if (path.endsWith("/3001/zip")) return new Response(Buffer.from(archive), { headers }); return new Response("missing", { status: 404, headers }); } });
  try {
    const child = Bun.spawn(["bun", resolve(import.meta.dirname, "task19-k17-provider-cli.ts"), "--context", context, "--output-root", outputRoot, "--status", status], { env: { ...process.env, GITHUB_TOKEN: "test", GITHUB_API_URL: server.url.toString().replace(/\/$/, "") }, stdout: "pipe", stderr: "pipe" });
    expect(await child.exited).toBe(0);
    expect(JSON.parse(readFileSync(status, "utf8"))).toMatchObject({ status: "observed", authorization: { runId: "1001", qualificationRunId: "2001", currentSha: revision, conclusion: "success", artifactId: "3001", artifactDigest: digest, artifactSize: archive.length }, provider: { headSha: revision, artifact: { id: "3001", createdAt: "2026-08-26T11:00:00Z", expiresAt: "2026-08-27T12:00:00Z" } }, externalWrites: 0 });
    expect(readFileSync(output, "utf8")).toBe("{\"qualification_status\":\"qualified\"}");
  } finally { server.stop(true); rmSync(root, { recursive: true, force: true }); }
});

test("partial K17 provider failure leaves no transient output or final physical root", async () => {
  const root = mkdtempSync(join(tmpdir(), "task19-k17-partial-"));
  const context = join(root, "context.json"); const outputRoot = join(root, "inputs", "k17"); const output = join(outputRoot, "k17-qualification.json"); const status = join(root, "status.json"); const finalRoot = join(root, "physical");
  const archive = zip("k17-qualification.json", Buffer.from("{}")); const digest = createHash("sha256").update(archive).digest("hex"); const headers = { date: "Tue, 26 Aug 2026 12:00:00 GMT" };
  writeFileSync(context, JSON.stringify({ repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/server-release.yml", eventName: "workflow_dispatch", runId: "1001", runAttempt: 2, currentSha: revision, conclusion: "success", artifactId: "3001", artifactName: "k17-qualification", artifactDigest: digest, artifactSize: archive.length + 1, artifactCreatedAt: "2026-08-26T11:00:00Z", artifactExpiresAt: "2026-08-27T12:00:00Z", qualificationRunId: "2001", qualificationCurrentSha: revision }));
  const server = Bun.serve({ port: 0, fetch(request) { const path = new URL(request.url).pathname; if (path.endsWith("/actions/runs/1001")) return Response.json({ id: 1001, run_attempt: 2, head_sha: revision, repository: { full_name: "furyheimdall/jastreamer" }, path: ".github/workflows/server-release.yml", event: "workflow_dispatch", conclusion: "success" }, { headers }); if (path.endsWith("/artifacts")) return Response.json({ total_count: 1, artifacts: [{ id: 3001, name: "k17-qualification", digest: `sha256:${digest}`, size_in_bytes: archive.length + 1, created_at: "2026-08-26T11:00:00Z", expires_at: "2026-08-27T12:00:00Z", expired: false, workflow_run: { id: 1001, run_attempt: 2, repository: "furyheimdall/jastreamer", head_sha: revision } }] }, { headers }); if (path.endsWith("/3001/zip")) return new Response(Buffer.from(archive), { headers }); return new Response("missing", { status: 404, headers }); } });
  try {
    const child = Bun.spawn(["bun", resolve(import.meta.dirname, "task19-k17-provider-cli.ts"), "--context", context, "--output-root", outputRoot, "--status", status], { env: { ...process.env, GITHUB_TOKEN: "test", GITHUB_API_URL: server.url.toString().replace(/\/$/, "") }, stdout: "pipe", stderr: "pipe" });
    expect(await child.exited).toBe(77);
    expect(JSON.parse(readFileSync(status, "utf8"))).toMatchObject({ status: "pending", code: "TASK19_K17_PROVIDER_SIZE_MISMATCH", candidateBytesWritten: 0 });
    expect(existsSync(output)).toBe(false);
    expect(existsSync(finalRoot)).toBe(false);
  } finally { server.stop(true); rmSync(root, { recursive: true, force: true }); }
});
