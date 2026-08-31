import { describe, expect, test } from "bun:test";
import { authorizeK17ProviderInput, authenticateProviderTuple, type K17ProviderAuthorizationInput } from "./provider-observation-contract";

const revision = "a".repeat(40);
const observedAt = "2026-08-26T12:00:00Z";
const run = { id: 1001, run_attempt: 2, head_sha: revision, repository: { full_name: "furyheimdall/jastreamer" }, path: ".github/workflows/renderer-release.yml", event: "workflow_dispatch", conclusion: "success" };
const artifactWorkflowRun = { id: 1001, run_attempt: 2, repository: "furyheimdall/jastreamer", head_sha: revision };
const artifact = { id: 3001, name: "renderer-candidate-ci", digest: `sha256:${"b".repeat(64)}`, size_in_bytes: 123, created_at: "2026-08-26T11:00:00Z", expires_at: "2026-08-27T12:00:00Z", expired: false, workflow_run: artifactWorkflowRun };
const expectation = { repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/renderer-release.yml", eventName: "workflow_dispatch", runId: "1001", runAttempt: 2, headSha: revision, observedAt, artifacts: [{ name: "renderer-candidate-ci" }] } as const;
const k17Input: K17ProviderAuthorizationInput = {
  repository: "furyheimdall/jastreamer",
  workflowPath: ".github/workflows/server-release.yml",
  eventName: "workflow_dispatch",
  runId: "1001",
  runAttempt: 2,
  currentSha: revision,
  conclusion: "success",
  artifactId: "3001",
  artifactName: "k17-qualification",
  artifactDigest: "b".repeat(64),
  artifactSize: 123,
  artifactCreatedAt: "2026-08-26T11:00:00Z",
  artifactExpiresAt: "2026-08-27T12:00:00Z",
  qualificationRunId: "2001",
  qualificationCurrentSha: revision,
};

describe("shared provider observation tuple", () => {
  test("authenticates the complete run artifact and provider-time tuple", () => {
    const result = authenticateProviderTuple(expectation, run, [artifact]);
    expect(result.artifacts[0]).toEqual({ id: "3001", name: "renderer-candidate-ci", digest: "b".repeat(64), size: 123, createdAt: "2026-08-26T11:00:00Z", expiresAt: "2026-08-27T12:00:00Z" });
  });

  test("authorizes a distinct completed K17 provider tuple bound to the current SHA", () => {
    expect(authorizeK17ProviderInput(k17Input)).toEqual({
      repository: "furyheimdall/jastreamer",
      workflowPath: ".github/workflows/server-release.yml",
      eventName: "workflow_dispatch",
      runId: k17Input.runId,
      runAttempt: k17Input.runAttempt,
      headSha: k17Input.currentSha,
      artifacts: [{ name: k17Input.artifactName, id: k17Input.artifactId, digest: k17Input.artifactDigest, size: k17Input.artifactSize, createdAt: k17Input.artifactCreatedAt, expiresAt: k17Input.artifactExpiresAt }],
    });
  });

  test.each([
    ["current qualification run", { ...k17Input, runId: k17Input.qualificationRunId }],
    ["repository", { ...k17Input, repository: "attacker/repository" }],
    ["workflow", { ...k17Input, workflowPath: ".github/workflows/product-qualification-dispatch.yml" }],
    ["event", { ...k17Input, eventName: "push" }],
    ["attempt", { ...k17Input, runAttempt: 0 }],
    ["current SHA", { ...k17Input, currentSha: "c".repeat(40) }],
    ["conclusion", { ...k17Input, conclusion: "in_progress" }],
    ["artifact ID", { ...k17Input, artifactId: "0" }],
    ["artifact name", { ...k17Input, artifactName: "other" }],
    ["artifact digest", { ...k17Input, artifactDigest: "c".repeat(63) }],
    ["artifact size", { ...k17Input, artifactSize: 0 }],
    ["artifact created", { ...k17Input, artifactCreatedAt: "invalid" }],
    ["artifact expires", { ...k17Input, artifactExpiresAt: k17Input.artifactCreatedAt }],
  ])("rejects protected K17 input with wrong %s", (_name, invalidInput) => {
    expect(() => authorizeK17ProviderInput(invalidInput)).toThrow();
  });

  test.each([
    ["workflow path", { run: { ...run, path: ".github/workflows/evil.yml" } }],
    ["event", { run: { ...run, event: "push" } }],
    ["attempt", { run: { ...run, run_attempt: 1 } }],
    ["current SHA", { run: { ...run, head_sha: "c".repeat(40) } }],
    ["conclusion", { run: { ...run, conclusion: "failure" } }],
    ["artifact workflow run", { artifact: { ...artifact, workflow_run: { ...artifactWorkflowRun, id: 999 } } }],
    ["missing artifact attempt", { artifact: { ...artifact, workflow_run: { id: 1001, repository: expectation.repository, head_sha: revision } } }],
    ["wrong artifact attempt", { artifact: { ...artifact, workflow_run: { ...artifactWorkflowRun, run_attempt: 1 } } }],
    ["missing artifact repository", { artifact: { ...artifact, workflow_run: { id: 1001, run_attempt: 2, head_sha: revision } } }],
    ["wrong artifact repository", { artifact: { ...artifact, workflow_run: { ...artifactWorkflowRun, repository: "attacker/repository" } } }],
    ["created time", { artifact: { ...artifact, created_at: "2026-08-26T13:00:00Z" } }],
    ["expiry", { artifact: { ...artifact, expires_at: observedAt } }],
    ["expired flag", { artifact: { ...artifact, expired: true } }],
    ["size", { artifact: { ...artifact, size_in_bytes: 0 } }],
  ])("rejects mismatched %s", (_name, patch) => {
    expect(() => authenticateProviderTuple(expectation, "run" in patch ? patch.run : run, ["artifact" in patch ? patch.artifact : artifact])).toThrow();
  });
});
