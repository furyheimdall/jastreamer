import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { describe, expect, test } from "bun:test";

const revision = "a".repeat(40);
const execute = async (workflowRun: Readonly<Record<string, unknown>>) => {
  const root = mkdtempSync(join(tmpdir(), "provider-observation-hostile-"));
  const context = join(root, "context.json");
  const output = join(root, "observation.json");
  writeFileSync(context, JSON.stringify({ repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/renderer-release.yml", eventName: "workflow_dispatch", runId: "1001", runAttempt: 2, headSha: revision, artifacts: [{ name: "renderer-candidate-ci" }] }));
  const headers = { date: "Tue, 26 Aug 2026 12:00:00 GMT" };
  const server = Bun.serve({ port: 0, fetch(request) {
    const path = new URL(request.url).pathname;
    if (path.endsWith("/actions/runs/1001")) return Response.json({ id: 1001, run_attempt: 2, head_sha: revision, repository: { full_name: "furyheimdall/jastreamer" }, path: ".github/workflows/renderer-release.yml", event: "workflow_dispatch", conclusion: "success" }, { headers });
    if (path.endsWith("/artifacts")) return Response.json({ total_count: 1, artifacts: [{ id: 3001, name: "renderer-candidate-ci", digest: `sha256:${"b".repeat(64)}`, size_in_bytes: 123, created_at: "2026-08-26T11:00:00Z", expires_at: "2026-08-27T12:00:00Z", expired: false, workflow_run: workflowRun }] }, { headers });
    return new Response("missing", { status: 404, headers });
  } });
  try {
    const child = Bun.spawn(["bun", resolve(import.meta.dirname, "provider-observation-cli.ts"), "--context", context, "--output", output], { env: { ...process.env, GITHUB_TOKEN: "test", GITHUB_API_URL: server.url.toString().replace(/\/$/, "") }, stdout: "pipe", stderr: "pipe" });
    const receipt = JSON.parse(await new Response(child.stdout).text());
    return { exitCode: await child.exited, receipt, outputExists: Bun.file(output).size > 0 };
  } finally { server.stop(true); rmSync(root, { recursive: true, force: true }); }
};

describe("provider observer hostile artifact identity denial", () => {
  test.each([
    ["missing attempt", { id: 1001, repository: "furyheimdall/jastreamer", head_sha: revision }],
    ["wrong attempt", { id: 1001, run_attempt: 1, repository: "furyheimdall/jastreamer", head_sha: revision }],
    ["missing repository", { id: 1001, run_attempt: 2, head_sha: revision }],
    ["wrong repository", { id: 1001, run_attempt: 2, repository: "attacker/repository", head_sha: revision }],
  ])("returns typed exit 77 for %s", async (_name, workflowRun) => {
    const result = await execute(workflowRun);
    expect(result).toEqual({ exitCode: 77, receipt: { status: "denied", code: "PROVIDER_ARTIFACT_IDENTITY_MISMATCH", externalWrites: 0 }, outputExists: false });
  });
});
