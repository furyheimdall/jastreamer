import { publicationCommands, registryCommands } from "./publication-commands";
import { AuthorizedRunner } from "./publication-runner";
import type { PreparedPublication, ProviderResult, ResourceOwnership } from "./publication-types";

export class PublicationExecutionError extends Error {
  readonly name = "PublicationExecutionError";
  constructor(readonly code: string, readonly commandId?: string) {
    super(code);
  }
}

type JsonRecord = Readonly<Record<string, unknown>>;

export const providerJsonRecord = (result: ProviderResult, commandId: string): JsonRecord => {
  if (result.exitCode !== 0) throw new PublicationExecutionError("PROVIDER_READ_FAILED", commandId);
  try {
    const value: unknown = JSON.parse(result.stdout);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID", commandId);
    return Object.fromEntries(Object.entries(value));
  } catch (error) {
    if (error instanceof PublicationExecutionError) throw error;
    throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID", commandId);
  }
};

const id = (value: unknown): string => {
  if ((typeof value !== "number" && typeof value !== "string") || !/^[1-9][0-9]*$/.test(String(value))) throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID");
  return String(value);
};

export const providerDigest = (value: unknown): string => {
  if (typeof value !== "string") throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID");
  const normalized = value.startsWith("sha256:") ? value.slice("sha256:".length) : value;
  if (!/^[0-9a-f]{64}$/.test(normalized)) throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID");
  return normalized;
};

export const providerNotFound = (result: ProviderResult): boolean => result.exitCode !== 0 && /HTTP 404|name unknown|manifest unknown|not found/i.test(`${result.stdout}\n${result.stderr}`);

const success = (result: ProviderResult, commandId: string): void => {
  if (result.exitCode !== 0) throw new PublicationExecutionError("PROVIDER_WRITE_FAILED", commandId);
};

const validateCandidateRun = async (prepared: PreparedPublication, runner: AuthorizedRunner): Promise<void> => {
  const commands = publicationCommands(prepared);
  const value = providerJsonRecord(await runner.run(commands.run), commands.run.id);
  const staging = prepared.candidate.staging;
  if (id(value["id"]) !== staging.callerRunId || value["event"] !== staging.eventName || value["head_sha"] !== staging.callerSha || value["head_branch"] !== staging.callerRef.replace(/^refs\/heads\//, "") || value["run_attempt"] !== staging.callerRunAttempt || value["conclusion"] !== "success" || value["path"] !== staging.workflowPath) throw new PublicationExecutionError("CANDIDATE_RUN_DRIFT", commands.run.id);

  const artifact = providerJsonRecord(await runner.run(commands.artifact), commands.artifact.id);
  const workflowRunValue = artifact["workflow_run"];
  if (typeof workflowRunValue !== "object" || workflowRunValue === null || Array.isArray(workflowRunValue)) throw new PublicationExecutionError("CANDIDATE_ARTIFACT_DRIFT", commands.artifact.id);
  const workflowRun = Object.fromEntries(Object.entries(workflowRunValue));
  if (id(artifact["id"]) !== staging.artifactId || artifact["name"] !== staging.artifactName || artifact["expired"] !== false
    || providerDigest(artifact["digest"]) !== staging.artifactDigest || staging.artifactName !== `${prepared.request.component}-publication-stage-${staging.callerRunId}-${staging.callerRunAttempt}` || staging.artifactAttemptProvenance !== "caller-run+upload-output+embedded-manifest" || id(workflowRun["id"]) !== staging.callerRunId || workflowRun["head_sha"] !== staging.callerSha) throw new PublicationExecutionError("CANDIDATE_ARTIFACT_DRIFT", commands.artifact.id);
};

const validateReleaseAbsence = async (prepared: PreparedPublication, runner: AuthorizedRunner): Promise<void> => {
  const release = publicationCommands(prepared).release;
  const result = await runner.run(release);
  if (result.exitCode === 0) throw new PublicationExecutionError("RELEASE_ALREADY_EXISTS", release.id);
  if (!providerNotFound(result)) throw new PublicationExecutionError("PROVIDER_READ_FAILED", release.id);
};

const registryTags = (result: ProviderResult): readonly string[] => {
  if (result.exitCode !== 0) {
    if (/name unknown|manifest unknown|not found/i.test(`${result.stdout}\n${result.stderr}`)) return [];
    throw new PublicationExecutionError("PROVIDER_READ_FAILED", "registry-preflight");
  }
  const value = providerJsonRecord(result, "registry-preflight");
  const tags = value["Tags"] ?? value["tags"];
  if (!Array.isArray(tags) || tags.some((tag) => typeof tag !== "string" || !/^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$/.test(tag))) throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID", "registry-preflight");
  return tags;
};

export type PreflightState = {
  /** Transaction-local absence proofs and registry lifecycle; mutation is the purpose of this state. */
  registryLoggedIn: boolean;
  registryAuth: ResourceOwnership;
  releasePreflightAbsent: boolean;
  temporaryOciPreflightAbsent: boolean;
  temporaryOciPreflightOccupied: boolean;
  finalOciPreflightAbsent: boolean;
};

export const runPublicationPreflight = async (input: Readonly<{ readonly prepared: PreparedPublication; readonly runner: AuthorizedRunner; readonly state: PreflightState }>): Promise<void> => {
  const { prepared, runner, state } = input;
  await validateCandidateRun(prepared, runner);
  await validateReleaseAbsence(prepared, runner);
  state.releasePreflightAbsent = true;
  if (prepared.request.component === "control") return;
  const commands = registryCommands(prepared);
  state.registryAuth = "indeterminate";
  success(await runner.run(commands.login), commands.login.id);
  state.registryLoggedIn = true;
  state.registryAuth = "owned";
  const tags = registryTags(await runner.run(commands.listTags));
  if (tags.includes(prepared.version)) throw new PublicationExecutionError("OCI_TAG_ALREADY_EXISTS", commands.listTags.id);
  const temporary = await runner.run(commands.inspectTemporary);
  if (temporary.exitCode === 0) {
    state.temporaryOciPreflightOccupied = true;
    throw new PublicationExecutionError("OCI_TEMPORARY_ALREADY_EXISTS", commands.inspectTemporary.id);
  }
  if (!providerNotFound(temporary)) throw new PublicationExecutionError("PROVIDER_READ_FAILED", commands.inspectTemporary.id);
  state.temporaryOciPreflightAbsent = true;
  for (const tag of tags) {
    const inspect = commands.inspectTag(tag);
    const result = await runner.run(inspect);
    if (result.exitCode !== 0) throw new PublicationExecutionError("PROVIDER_READ_FAILED", inspect.id);
    if (`sha256:${providerDigest(result.stdout.trim())}` === prepared.verified.serverOci.indexDigest) throw new PublicationExecutionError("OCI_DIGEST_ALREADY_PUBLISHED", inspect.id);
  }
  state.finalOciPreflightAbsent = true;
};

export const assertProviderSuccess = success;
