import { createHash } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, test } from "bun:test";
import { expectedTask19ArtifactName } from "./qualification-provider-observer";

const revision = "a".repeat(40); const date = "Tue, 26 Aug 2026 12:00:00 GMT"; const sha256 = (bytes: Uint8Array): string => createHash("sha256").update(bytes).digest("hex");
const execute = async (mode: "run-state" | "size") => {
  const root = mkdtempSync(join(tmpdir(), "task19-provider-cli-")); const context = join(root, "context.json"); const output = join(root, "candidate.json"); const archive = Uint8Array.from([1]);
  writeFileSync(context, JSON.stringify({ repository: "furyheimdall/jastreamer", runId: "1001", runAttempt: 2, headSha: revision }));
  const server = Bun.serve({ port: 0, fetch(request) { const path = new URL(request.url).pathname; const headers = { date, "content-type": "application/json" }; if (path.endsWith("/actions/runs/1001")) return Response.json({ id: 1001, run_attempt: 2, head_sha: revision, repository: { full_name: "furyheimdall/jastreamer" }, path: ".github/workflows/product-qualification-dispatch.yml", event: "workflow_dispatch", conclusion: mode === "run-state" ? "failure" : "success" }, { headers }); if (path.endsWith("/artifacts")) return Response.json({ total_count: 1, artifacts: [{ id: 3001, name: expectedTask19ArtifactName("1001", 2), digest: `sha256:${sha256(archive)}`, size_in_bytes: 2, created_at: "2026-08-26T11:00:00Z", expires_at: "2026-08-27T12:00:00Z", expired: false, workflow_run: { id: 1001, run_attempt: 2, repository: "furyheimdall/jastreamer", head_sha: revision } }] }, { headers }); if (path.endsWith("/actions/artifacts/3001/zip")) return new Response(archive, { headers: { date } }); return new Response("not found", { status: 404, headers: { date } }); } });
  try { const child = Bun.spawn(["bun", resolve(import.meta.dirname, "task19-provider-observer-cli.ts"), "--context", context, "--output-root", join(root, "bytes"), "--output", output], { env: { ...process.env, GITHUB_TOKEN: "test", GITHUB_API_URL: server.url.toString().replace(/\/$/, "") }, stdout: "pipe", stderr: "pipe" }); const stdout = new Response(child.stdout).text(); const exitCode = await child.exited; const receipt = JSON.parse(await stdout); return { exitCode, receipt }; } finally { server.stop(true); rmSync(root, { recursive: true, force: true }); }
};

describe("Task19 provider observer CLI denial boundary", () => {
  test("converts unsuccessful provider run observation into a stable exit-77 receipt", async () => expect(await execute("run-state")).toEqual({ exitCode: 77, receipt: { schemaVersion: 1, kind: "task19_provider_observation_receipt", status: "denied", code: "PROVIDER_RUN_UNSUCCESSFUL", productCommandsExecuted: 0, externalWrites: 0 } }));
  test("converts authenticated artifact size mismatch into a stable exit-77 receipt", async () => expect(await execute("size")).toEqual({ exitCode: 77, receipt: { schemaVersion: 1, kind: "task19_provider_observation_receipt", status: "denied", code: "PROVIDER_ARCHIVE_SIZE_MISMATCH", productCommandsExecuted: 0, externalWrites: 0 } }));
});
