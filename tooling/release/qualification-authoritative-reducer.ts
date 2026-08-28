import { createHash } from "node:crypto";
import { parseQualificationArtifactBinding, type QualificationArtifactBinding } from "./qualification-artifact";
import { expectedProviderArtifactName, expectedPublicationFiles, type ProviderArtifactObservation } from "./qualification-provider-observer";

export type TerminalJobResult = "success" | "failure" | "cancelled" | "skipped";
export type PublicComponent = "server" | "control";
type JsonObject = Readonly<Record<string, unknown>>;

export type ChildMachineOutputs = Readonly<{
  callerRunId: string; callerRunAttempt: number; callerRef: string; callerSha: string;
  calledWorkflowPath: string; calledJob: PublicComponent; calledOutputIdentity: "jobs.stage.outputs";
  artifactId: string; artifactName: string; artifactDigest: string;
  stagingBindingArtifactId: string; stagingBindingArtifactName: string; stagingBindingArtifactDigest: string; stagingBindingSha256: string;
}>;
export type ReducerChild = Readonly<{ component: PublicComponent; result: TerminalJobResult; outputs?: ChildMachineOutputs }>;
export type ReducerContext = Readonly<{ repository: "furyheimdall/jastreamer"; runId: string; runAttempt: number; ref: string; sha: string; workflowPath: ".github/workflows/product-qualification-dispatch.yml" }>;
export type CombinedStaging = Readonly<Record<string, unknown> & { calledJobResult: "success" }>;
export type AuthoritativeReduction = Readonly<{
  schemaVersion: 1; kind: "authoritative_product_qualification"; status: "satisfied" | "denied"; caller: ReducerContext;
  children: Readonly<Record<PublicComponent, Readonly<{ result: TerminalJobResult; outputs?: ChildMachineOutputs }>>>;
  candidates?: Readonly<Record<PublicComponent, CombinedStaging>>; promotableInput: boolean; retryDispatches: 0;
}>;

const sha256 = (bytes: Uint8Array): string => createHash("sha256").update(bytes).digest("hex");
const fail = (code: string): never => { throw new Error(code); };
const exactRecord = (value: unknown, keys: readonly string[], code: string): JsonObject => {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error(code);
  const objectValue: object = value;
  const item = Object.fromEntries(Object.entries(objectValue));
  if (JSON.stringify(Object.keys(item).sort()) !== JSON.stringify([...keys].sort())) fail(code);
  return item;
};
const text = (value: unknown, pattern: RegExp, code: string): string => typeof value === "string" && pattern.test(value) ? value : fail(code);
const positive = (value: unknown, code: string): number => typeof value === "number" && Number.isSafeInteger(value) && value > 0 ? value : fail(code);
const digest = (value: unknown, code: string): string => text(value, /^[0-9a-f]{64}$/, code);
const id = (value: unknown, code: string): string => text(value, /^[1-9][0-9]*$/, code);
const decode = (bytes: Uint8Array | undefined, code: string): unknown => {
  const present = bytes ?? fail(code);
  try { return JSON.parse(Buffer.from(present).toString("utf8")); } catch { return fail(code); }
};

const stagingKeys = ["repository", "workflowPath", "eventName", "headSha", "runId", "runAttempt", "artifactId", "artifactName", "artifactDigest", "releaseTag", "callerRunId", "callerRunAttempt", "callerRef", "callerSha", "calledWorkflowPath", "calledJob", "artifactAttemptProvenance", "artifactManifestSha256", "artifactContentManifestSha256"] as const;
const outputKeys = ["callerRunId", "callerRunAttempt", "callerRef", "callerSha", "calledWorkflowPath", "calledJob", "calledOutputIdentity", "artifactId", "artifactName", "artifactDigest", "stagingBindingArtifactId", "stagingBindingArtifactName", "stagingBindingArtifactDigest", "stagingBindingSha256"] as const;

const parseOutputs = (value: ChildMachineOutputs, component: PublicComponent): ChildMachineOutputs => {
  const item = exactRecord(value, outputKeys, "REDUCER_OUTPUT_INVALID");
  if (item["calledJob"] !== component || item["calledWorkflowPath"] !== `.github/workflows/${component}-release.yml` || item["calledOutputIdentity"] !== "jobs.stage.outputs") fail("REDUCER_OUTPUT_INVALID");
  return { callerRunId: id(item["callerRunId"], "REDUCER_OUTPUT_INVALID"), callerRunAttempt: positive(item["callerRunAttempt"], "REDUCER_OUTPUT_INVALID"), callerRef: text(item["callerRef"], /^refs\/heads\/[A-Za-z0-9._/-]{1,200}$/, "REDUCER_OUTPUT_INVALID"), callerSha: text(item["callerSha"], /^[0-9a-f]{40}$/, "REDUCER_OUTPUT_INVALID"), calledWorkflowPath: item["calledWorkflowPath"] as string, calledJob: component, calledOutputIdentity: "jobs.stage.outputs", artifactId: id(item["artifactId"], "REDUCER_OUTPUT_INVALID"), artifactName: text(item["artifactName"], new RegExp(`^${component}-publication-stage-[1-9][0-9]*-[1-9][0-9]*$`), "REDUCER_OUTPUT_INVALID"), artifactDigest: digest(item["artifactDigest"], "REDUCER_OUTPUT_INVALID"), stagingBindingArtifactId: id(item["stagingBindingArtifactId"], "REDUCER_OUTPUT_INVALID"), stagingBindingArtifactName: text(item["stagingBindingArtifactName"], new RegExp(`^${component}-publication-staging-binding-[1-9][0-9]*-[1-9][0-9]*$`), "REDUCER_OUTPUT_INVALID"), stagingBindingArtifactDigest: digest(item["stagingBindingArtifactDigest"], "REDUCER_OUTPUT_INVALID"), stagingBindingSha256: digest(item["stagingBindingSha256"], "REDUCER_OUTPUT_INVALID") };
};

const verifySuccess = (context: ReducerContext, child: ReducerChild, observations: readonly ProviderArtifactObservation[]): Readonly<{ outputs: ChildMachineOutputs; staging: CombinedStaging }> => {
  const { component } = child; const childOutputs = child.outputs ?? fail("REDUCER_OUTPUT_INVALID"); const outputs = parseOutputs(childOutputs, component);
  if (outputs.callerRunId !== context.runId || outputs.callerRunAttempt !== context.runAttempt || outputs.callerRef !== context.ref || outputs.callerSha !== context.sha) fail("REDUCER_CALLER_MISMATCH");
  const componentObservations = observations.filter((observation) => observation.component === component); const stageMatches = componentObservations.filter((observation) => observation.kind === "publication-stage"); const bindingMatches = componentObservations.filter((observation) => observation.kind === "staging-binding");
  if (componentObservations.length !== 2 || stageMatches.length !== 1 || bindingMatches.length !== 1) fail("REDUCER_PROVIDER_SET_INVALID");
  const stage = stageMatches[0] ?? fail("REDUCER_PROVIDER_SET_INVALID"); const bindingArtifact = bindingMatches[0] ?? fail("REDUCER_PROVIDER_SET_INVALID");
  for (const observation of [stage, bindingArtifact]) if (observation.repository !== context.repository || observation.runId !== context.runId || observation.runAttempt !== context.runAttempt || observation.headSha !== context.sha || observation.expired !== false || observation.name !== expectedProviderArtifactName(component, observation.kind, context.runId, context.runAttempt) || observation.archiveSha256 !== observation.digest || observation.size < 1) fail("REDUCER_PROVIDER_CONTEXT_MISMATCH");
  if (stage.id !== outputs.artifactId || stage.name !== outputs.artifactName || stage.digest !== outputs.artifactDigest || bindingArtifact.id !== outputs.stagingBindingArtifactId || bindingArtifact.name !== outputs.stagingBindingArtifactName || bindingArtifact.digest !== outputs.stagingBindingArtifactDigest) fail("REDUCER_ARTIFACT_IDENTITY_MISMATCH");
  const stagingBytes = bindingArtifact.entries[`${component}-publication-staging.json`] ?? fail("REDUCER_STAGING_DIGEST_MISMATCH"); if (sha256(stagingBytes) !== outputs.stagingBindingSha256) fail("REDUCER_STAGING_DIGEST_MISMATCH");
  const raw = exactRecord(decode(stagingBytes, "REDUCER_STAGING_INVALID"), stagingKeys, "REDUCER_STAGING_INVALID");
  if ("calledJobResult" in raw || raw["repository"] !== context.repository || raw["workflowPath"] !== context.workflowPath || raw["eventName"] !== "workflow_dispatch" || raw["headSha"] !== context.sha || raw["runId"] !== context.runId || raw["runAttempt"] !== context.runAttempt || raw["callerRunId"] !== context.runId || raw["callerRunAttempt"] !== context.runAttempt || raw["callerRef"] !== context.ref || raw["callerSha"] !== context.sha || raw["calledWorkflowPath"] !== outputs.calledWorkflowPath || raw["calledJob"] !== component || raw["artifactId"] !== outputs.artifactId || raw["artifactName"] !== outputs.artifactName || raw["artifactDigest"] !== outputs.artifactDigest || raw["artifactAttemptProvenance"] !== "caller-run+upload-output+embedded-manifest") fail("REDUCER_STAGING_MISMATCH");
  const releaseTag = text(raw["releaseTag"], new RegExp(`^${component}-v(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)\\.(?:0|[1-9][0-9]*)$`), "REDUCER_STAGING_INVALID");
  const bindingBytes = stage.entries["qualification/candidate-binding.json"] ?? fail("REDUCER_EMBEDDED_DIGEST_MISMATCH"); const contentBytes = stage.entries["qualification/content-manifest.json"] ?? fail("REDUCER_EMBEDDED_DIGEST_MISMATCH");
  if (sha256(bindingBytes) !== raw["artifactManifestSha256"] || sha256(contentBytes) !== raw["artifactContentManifestSha256"]) fail("REDUCER_EMBEDDED_DIGEST_MISMATCH");
  const contentManifest = decode(contentBytes, "REDUCER_CONTENT_MANIFEST_INVALID"); const manifest = typeof contentManifest === "object" && contentManifest !== null && !Array.isArray(contentManifest) ? Object.fromEntries(Object.entries(contentManifest)) : fail("REDUCER_CONTENT_MANIFEST_INVALID"); if (manifest["component"] !== component || manifest["tag"] !== releaseTag || !Array.isArray(manifest["artifacts"])) fail("REDUCER_CONTENT_MANIFEST_INVALID"); const expectedFiles = expectedPublicationFiles(component, releaseTag); const expectedEntries = [...expectedFiles.map((name) => `publication-stage/${name}`), "qualification/candidate-binding.json", "qualification/content-manifest.json"].sort(); if (JSON.stringify(Object.keys(stage.entries).sort()) !== JSON.stringify(expectedEntries)) fail("REDUCER_PROVIDER_INVENTORY_MISMATCH"); for (const name of expectedFiles) { const manifestArtifact = manifest["artifacts"].find((item: unknown) => typeof item === "object" && item !== null && !Array.isArray(item) && Object.fromEntries(Object.entries(item))["name"] === name); const parsed = typeof manifestArtifact === "object" && manifestArtifact !== null ? Object.fromEntries(Object.entries(manifestArtifact)) : fail("REDUCER_CONTENT_MANIFEST_INVALID"); const bytes = stage.entries[`publication-stage/${name}`] ?? fail("REDUCER_PROVIDER_INVENTORY_MISMATCH"); if (parsed["sha256"] !== sha256(bytes)) fail("REDUCER_PROVIDER_FILE_DIGEST_MISMATCH"); }
  const embedded: QualificationArtifactBinding = parseQualificationArtifactBinding(decode(bindingBytes, "REDUCER_EMBEDDED_BINDING_INVALID"));
  if (embedded.component !== component || embedded.callerRunId !== context.runId || embedded.callerRunAttempt !== context.runAttempt || embedded.callerRef !== context.ref || embedded.callerSha !== context.sha || embedded.calledWorkflowPath !== outputs.calledWorkflowPath || embedded.calledJob !== component || embedded.contentManifestSha256 !== sha256(contentBytes)) fail("REDUCER_EMBEDDED_BINDING_MISMATCH");
  return { outputs, staging: { ...raw, calledJobResult: "success" } };
};

export const reduceAuthoritativeQualification = (rawContext: ReducerContext, children: readonly ReducerChild[], observations: readonly ProviderArtifactObservation[]): AuthoritativeReduction => {
  const contextItem = exactRecord(rawContext, ["repository", "runId", "runAttempt", "ref", "sha", "workflowPath"], "REDUCER_CONTEXT_INVALID");
  if (contextItem["repository"] !== "furyheimdall/jastreamer" || contextItem["workflowPath"] !== ".github/workflows/product-qualification-dispatch.yml") fail("REDUCER_CONTEXT_INVALID");
  const context: ReducerContext = { repository: "furyheimdall/jastreamer", runId: id(contextItem["runId"], "REDUCER_CONTEXT_INVALID"), runAttempt: positive(contextItem["runAttempt"], "REDUCER_CONTEXT_INVALID"), ref: text(contextItem["ref"], /^refs\/heads\/[A-Za-z0-9._/-]{1,200}$/, "REDUCER_CONTEXT_INVALID"), sha: text(contextItem["sha"], /^[0-9a-f]{40}$/, "REDUCER_CONTEXT_INVALID"), workflowPath: ".github/workflows/product-qualification-dispatch.yml" };
  if (children.length !== 2 || children.filter(({ component }) => component === "server").length !== 1 || children.filter(({ component }) => component === "control").length !== 1) fail("REDUCER_CHILD_SET_INVALID");
  const server = children.find(({ component }) => component === "server") ?? fail("REDUCER_CHILD_SET_INVALID"); const control = children.find(({ component }) => component === "control") ?? fail("REDUCER_CHILD_SET_INVALID");
  const denialBase = { schemaVersion: 1 as const, kind: "authoritative_product_qualification" as const, caller: { ...context }, children: { server: { result: server.result }, control: { result: control.result } }, promotableInput: false as const, retryDispatches: 0 as const };
  if (server.result !== "success" || control.result !== "success") return { ...denialBase, status: "denied" };
  if (observations.length !== 4) fail("REDUCER_PROVIDER_SET_INVALID");
  const verifiedServer = verifySuccess(context, server, observations); const verifiedControl = verifySuccess(context, control, observations);
  return { ...denialBase, status: "satisfied", promotableInput: true, children: { server: { result: "success", outputs: verifiedServer.outputs }, control: { result: "success", outputs: verifiedControl.outputs } }, candidates: { server: verifiedServer.staging, control: verifiedControl.staging } };
};
