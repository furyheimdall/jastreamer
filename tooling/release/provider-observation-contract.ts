export type ProviderArtifactExpectation = Readonly<{ name: string; id?: string; digest?: string; size?: number; createdAt?: string; expiresAt?: string }>;
export type ProviderTupleExpectation = Readonly<{
  repository: "furyheimdall/jastreamer";
  workflowPath: string;
  eventName: string;
  runId: string;
  runAttempt: number;
  headSha: string;
  observedAt: string;
  artifacts: readonly ProviderArtifactExpectation[];
}>;
export type AuthenticatedProviderArtifact = Readonly<{ id: string; name: string; digest: string; size: number; createdAt: string; expiresAt: string }>;
export type AuthenticatedProviderTuple = Readonly<{ context: Omit<ProviderTupleExpectation, "artifacts">; artifacts: readonly AuthenticatedProviderArtifact[] }>;

export type K17ProviderAuthorizationInput = Readonly<{
  repository: string;
  workflowPath: string;
  eventName: string;
  runId: string;
  runAttempt: number;
  currentSha: string;
  conclusion: string;
  artifactId: string;
  artifactName: string;
  artifactDigest: string;
  artifactSize: number;
  artifactCreatedAt: string;
  artifactExpiresAt: string;
  qualificationRunId: string;
  qualificationCurrentSha: string;
}>;
export type AuthorizedK17ProviderInput = Omit<ProviderTupleExpectation, "observedAt">;

type JsonRecord = Readonly<Record<string, unknown>>;
class ProviderTupleError extends Error { readonly name = "ProviderTupleError"; readonly code: string; constructor(code: string) { super(code); this.code = code; } }
const fail = (code: string): never => { throw new ProviderTupleError(code); };
const record = (value: unknown): JsonRecord => typeof value === "object" && value !== null && !Array.isArray(value) ? Object.fromEntries(Object.entries(value)) : fail("PROVIDER_TUPLE_INVALID");
const positiveId = (value: unknown): string => (typeof value === "number" || typeof value === "string") && /^[1-9][0-9]*$/.test(String(value)) ? String(value) : fail("PROVIDER_TUPLE_INVALID");
const sha256 = (value: unknown): string => { const normalized = typeof value === "string" ? value.replace(/^sha256:/, "") : ""; return /^[0-9a-f]{64}$/.test(normalized) ? normalized : fail("PROVIDER_TUPLE_INVALID"); };
const instant = (value: unknown): string => typeof value === "string" && Number.isFinite(Date.parse(value)) ? value : fail("PROVIDER_TUPLE_INVALID");
const size = (value: unknown): number => typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : fail("PROVIDER_TUPLE_INVALID");

export const authorizeK17ProviderInput = (input: K17ProviderAuthorizationInput): AuthorizedK17ProviderInput => {
  const createdAt = instant(input.artifactCreatedAt); const expiresAt = instant(input.artifactExpiresAt);
  if (input.repository !== "furyheimdall/jastreamer" || input.workflowPath !== ".github/workflows/server-release.yml" || input.eventName !== "workflow_dispatch" || input.conclusion !== "success" || input.artifactName !== "k17-qualification") fail("K17_PROVIDER_AUTHORIZATION_DENIED");
  if (positiveId(input.runId) === positiveId(input.qualificationRunId) || !Number.isSafeInteger(input.runAttempt) || input.runAttempt < 1 || !/^[0-9a-f]{40}$/.test(input.currentSha) || input.currentSha !== input.qualificationCurrentSha || Date.parse(expiresAt) <= Date.parse(createdAt)) fail("K17_PROVIDER_AUTHORIZATION_DENIED");
  return Object.freeze({ repository: "furyheimdall/jastreamer", workflowPath: ".github/workflows/server-release.yml", eventName: "workflow_dispatch", runId: input.runId, runAttempt: input.runAttempt, headSha: input.currentSha, artifacts: Object.freeze([{ name: "k17-qualification", id: positiveId(input.artifactId), digest: sha256(input.artifactDigest), size: size(input.artifactSize), createdAt, expiresAt }]) });
};

export const authenticateProviderTuple = (expected: ProviderTupleExpectation, rawRun: unknown, rawArtifacts: readonly unknown[]): AuthenticatedProviderTuple => {
  if (!/^[1-9][0-9]*$/.test(expected.runId) || !Number.isSafeInteger(expected.runAttempt) || expected.runAttempt < 1 || !/^[0-9a-f]{40}$/.test(expected.headSha) || !Number.isFinite(Date.parse(expected.observedAt))) fail("PROVIDER_CONTEXT_INVALID");
  const run = record(rawRun); const repository = record(run["repository"]);
  if (positiveId(run["id"]) !== expected.runId || run["run_attempt"] !== expected.runAttempt || run["head_sha"] !== expected.headSha || repository["full_name"] !== expected.repository || run["path"] !== expected.workflowPath || run["event"] !== expected.eventName) fail("PROVIDER_RUN_IDENTITY_MISMATCH");
  if (run["conclusion"] !== "success") fail("PROVIDER_RUN_UNSUCCESSFUL");
  const observed = Date.parse(expected.observedAt); const artifacts: AuthenticatedProviderArtifact[] = [];
  for (const artifactExpectation of expected.artifacts) {
    const matches = rawArtifacts.map(record).filter((candidate) => candidate["name"] === artifactExpectation.name);
    if (matches.length !== 1) fail(matches.length === 0 ? "PROVIDER_ARTIFACT_MISSING" : "PROVIDER_ARTIFACT_DUPLICATE");
    const artifact = matches[0] ?? fail("PROVIDER_ARTIFACT_MISSING"); const workflowRun = record(artifact["workflow_run"]);
    const authenticated = { id: positiveId(artifact["id"]), name: artifactExpectation.name, digest: sha256(artifact["digest"]), size: size(artifact["size_in_bytes"]), createdAt: instant(artifact["created_at"]), expiresAt: instant(artifact["expires_at"]) };
    if (artifact["expired"] !== false || positiveId(workflowRun["id"]) !== expected.runId || workflowRun["run_attempt"] !== expected.runAttempt || workflowRun["repository"] !== expected.repository || workflowRun["head_sha"] !== expected.headSha) fail(artifact["expired"] === true ? "PROVIDER_ARTIFACT_EXPIRED" : "PROVIDER_ARTIFACT_IDENTITY_MISMATCH");
    if (Date.parse(authenticated.createdAt) > observed + 300_000 || Date.parse(authenticated.expiresAt) <= observed) fail("PROVIDER_ARTIFACT_STALE");
    if ((artifactExpectation.id !== undefined && authenticated.id !== artifactExpectation.id) || (artifactExpectation.digest !== undefined && authenticated.digest !== artifactExpectation.digest) || (artifactExpectation.size !== undefined && authenticated.size !== artifactExpectation.size) || (artifactExpectation.createdAt !== undefined && authenticated.createdAt !== artifactExpectation.createdAt) || (artifactExpectation.expiresAt !== undefined && authenticated.expiresAt !== artifactExpectation.expiresAt)) fail("PROVIDER_ARTIFACT_IDENTITY_MISMATCH");
    artifacts.push(authenticated);
  }
  return Object.freeze({ context: { repository: expected.repository, workflowPath: expected.workflowPath, eventName: expected.eventName, runId: expected.runId, runAttempt: expected.runAttempt, headSha: expected.headSha, observedAt: expected.observedAt }, artifacts: Object.freeze(artifacts) });
};
