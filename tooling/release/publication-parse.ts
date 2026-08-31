import { readFileSync } from "node:fs";
import type {
  CandidateStaging,
  ManifestArtifact,
  PublicationCandidate,
  PublicationManifest,
  PublicationRequest,
  PublicComponent,
  VerifiedPublication,
} from "./publication-types";

const sha256Pattern = /^[0-9a-f]{64}$/;
const revisionPattern = /^[0-9a-f]{40}$/;
const positiveIntegerPattern = /^[1-9][0-9]*$/;

type JsonRecord = Readonly<Record<string, unknown>>;

export class PublicationContractError extends Error {
  readonly name = "PublicationContractError";
  constructor(readonly code: string) {
    super(code);
  }
}

const record = (value: unknown, keys: readonly string[]): JsonRecord => {
  if (typeof value !== "object" || value === null || Array.isArray(value)) throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  const parsed = Object.fromEntries(Object.entries(value));
  if (JSON.stringify(Object.keys(parsed).sort()) !== JSON.stringify([...keys].sort())) throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  return parsed;
};

const string = (value: unknown): string => {
  if (typeof value !== "string" || value.length === 0) throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  return value;
};

const sha256 = (value: unknown): string => {
  const parsed = string(value);
  if (!sha256Pattern.test(parsed)) throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  return parsed;
};

const positiveId = (value: unknown): string => {
  const parsed = string(value);
  if (!positiveIntegerPattern.test(parsed)) throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  return parsed;
};

const positiveInteger = (value: unknown): number => {
  if (!Number.isSafeInteger(value) || typeof value !== "number" || value < 1) throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  return value;
};

const component = (value: unknown): PublicComponent => {
  if (value !== "server" && value !== "control") throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  return value;
};

const parseEvent = (value: unknown): PublicationRequest["event"] => {
  const item = record(value, ["name", "ref", "refType", "refName", "sha"]);
  return { name: string(item["name"]), ref: string(item["ref"]), refType: string(item["refType"]), refName: string(item["refName"]), sha: string(item["sha"]) };
};

const parsePublisherRun = (value: unknown): PublicationRequest["publisherRun"] => {
  const item = record(value, ["id", "attempt", "actor"]);
  return { id: positiveId(item["id"]), attempt: positiveInteger(item["attempt"]), actor: string(item["actor"]) };
};

const parseGate = (value: unknown): PublicationRequest["gate"] => {
  const item = record(value, ["root", "receiptPath", "verifiedPath", "expectedReceiptSha256"]);
  return { root: string(item["root"]), receiptPath: string(item["receiptPath"]), verifiedPath: string(item["verifiedPath"]), expectedReceiptSha256: sha256(item["expectedReceiptSha256"]) };
};

export const parsePublicationRequest = (path: string): PublicationRequest => {
  const value: unknown = JSON.parse(readFileSync(path, "utf8"));
  const item = record(value, ["schemaVersion", "mode", "component", "repository", "environment", "event", "publisherRun", "gate", "stageRoot", "receiptPath", ...(typeof Object.fromEntries(Object.entries(value ?? {}))["dockerConfigRoot"] === "string" ? ["dockerConfigRoot"] : [])]);
  if (item["schemaVersion"] !== 1 || (item["mode"] !== "production" && item["mode"] !== "observe") || item["repository"] !== "furyheimdall/jastreamer" || item["environment"] !== "product-promotion") throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  const dockerConfigRoot = item["dockerConfigRoot"];
  return {
    schemaVersion: 1, mode: item["mode"], component: component(item["component"]), repository: "furyheimdall/jastreamer", environment: "product-promotion",
    event: parseEvent(item["event"]), publisherRun: parsePublisherRun(item["publisherRun"]), gate: parseGate(item["gate"]),
    stageRoot: string(item["stageRoot"]), receiptPath: string(item["receiptPath"]),
    ...(dockerConfigRoot === undefined ? {} : { dockerConfigRoot: string(dockerConfigRoot) }),
  };
};

const parseStaging = (value: unknown): CandidateStaging => {
  const item = record(value, ["repository", "workflowPath", "eventName", "headSha", "callerRunId", "callerRunAttempt", "callerRef", "callerSha", "calledWorkflowPath", "calledJob", "calledJobResult", "runId", "runAttempt", "artifactId", "artifactName", "artifactDigest", "artifactAttemptProvenance", "artifactManifestSha256", "artifactContentManifestSha256"]); const calledJob = component(item["calledJob"]);
  if (item["repository"] !== "furyheimdall/jastreamer" || item["eventName"] !== "workflow_dispatch" || typeof item["headSha"] !== "string" || !revisionPattern.test(item["headSha"]) || typeof item["callerSha"] !== "string" || !revisionPattern.test(item["callerSha"]) || typeof item["callerRef"] !== "string" || !/^refs\/heads\/[A-Za-z0-9._/-]{1,200}$/.test(item["callerRef"]) || item["calledWorkflowPath"] !== `.github/workflows/${calledJob}-qualification-staging.yml` || item["calledJobResult"] !== "success") throw new PublicationContractError("VERIFIED_PRODUCT_GATE_INVALID");
  return { repository: "furyheimdall/jastreamer", workflowPath: string(item["workflowPath"]), eventName: "workflow_dispatch", headSha: item["headSha"], callerRunId: positiveId(item["callerRunId"]), callerRunAttempt: positiveInteger(item["callerRunAttempt"]), callerRef: item["callerRef"], callerSha: item["callerSha"], calledWorkflowPath: item["calledWorkflowPath"], calledJob, calledJobResult: "success", runId: positiveId(item["runId"]), runAttempt: positiveInteger(item["runAttempt"]), artifactId: positiveId(item["artifactId"]), artifactName: string(item["artifactName"]), artifactDigest: sha256(item["artifactDigest"]), artifactAttemptProvenance: item["artifactAttemptProvenance"] === "caller-run+upload-output+embedded-manifest" ? item["artifactAttemptProvenance"] : (() => { throw new PublicationContractError("VERIFIED_PRODUCT_GATE_INVALID"); })(), artifactManifestSha256: sha256(item["artifactManifestSha256"]), artifactContentManifestSha256: sha256(item["artifactContentManifestSha256"]) };
};

const parseCandidate = (value: unknown): PublicationCandidate => {
  const item = record(value, ["releaseTag", "manifest", "staging"]);
  const manifest = record(item["manifest"], ["path", "sha256"]);
  return { releaseTag: string(item["releaseTag"]), manifest: { path: string(manifest["path"]), sha256: sha256(manifest["sha256"]) }, staging: parseStaging(item["staging"]) };
};

const parseArtifact = (value: unknown): ManifestArtifact => {
  const item = record(value, ["kind", "path", "sha256"]);
  return { kind: string(item["kind"]), path: string(item["path"]), sha256: sha256(item["sha256"]) };
};

const parseArtifacts = (value: unknown): readonly ManifestArtifact[] => {
  if (!Array.isArray(value) || value.length === 0) throw new PublicationContractError("PUBLICATION_INPUT_INVALID");
  return value.map(parseArtifact);
};

export const parsePublicationManifest = (bytes: Buffer): PublicationManifest => {
  const value: unknown = JSON.parse(bytes.toString("utf8"));
  const item = record(value, ["schemaVersion", "component", "tag", "sourceRevision", "sourceDirtySha256", "artifactSetSha256", "peerSetSha256", "controlContractSha256", "rendererContractSha256", "artifacts"]);
  if (item["schemaVersion"] !== 1 || typeof item["sourceRevision"] !== "string" || !revisionPattern.test(item["sourceRevision"])) throw new PublicationContractError("PUBLICATION_MANIFEST_INVALID");
  return { component: component(item["component"]), tag: string(item["tag"]), sourceRevision: item["sourceRevision"], artifactSetSha256: sha256(item["artifactSetSha256"]), artifacts: parseArtifacts(item["artifacts"]) };
};

export const parseVerifiedPublication = (bytes: Buffer): VerifiedPublication => {
  const value: unknown = JSON.parse(bytes.toString("utf8"));
  const item = record(value, ["schemaVersion", "kind", "status", "ok", "productGateSha256", "rebuild", "externalMutations", "authoritativeReducer", "trust", "publication", "serverOci", "candidateManifests", "selection", "rendererPublicAssets"]);
  if (item["schemaVersion"] !== 1 || item["kind"] !== "product_gate_verification" || item["status"] !== "authorized" || item["ok"] !== true || item["rebuild"] !== false || item["externalMutations"] !== 0 || !Array.isArray(item["rendererPublicAssets"]) || item["rendererPublicAssets"].length !== 0) throw new PublicationContractError("VERIFIED_PRODUCT_GATE_INVALID");
  const authoritativeReducer = record(item["authoritativeReducer"], ["sha256", "result"]); if (authoritativeReducer["result"] !== "success") throw new PublicationContractError("VERIFIED_PRODUCT_GATE_INVALID");
  const trust = record(item["trust"], ["profile", "trustPolicyVersion", "rotationEpoch", "gateKeyId"]);
  if (trust["profile"] !== "production" && trust["profile"] !== "fixture") throw new PublicationContractError("VERIFIED_PRODUCT_GATE_INVALID");
  const publication = record(item["publication"], ["repository", "environment", "receiptKeyId", "artifactSetSha256", "candidates"]);
  if (publication["repository"] !== "furyheimdall/jastreamer" || publication["environment"] !== "product-promotion") throw new PublicationContractError("VERIFIED_PRODUCT_GATE_INVALID");
  const candidates = record(publication["candidates"], ["server", "control"]);
  const oci = record(item["serverOci"], ["artifactSha256", "indexDigest", "platforms", "attestations"]);
  if (!Array.isArray(oci["platforms"]) || JSON.stringify(oci["platforms"]) !== JSON.stringify(["linux/amd64", "linux/arm64"]) || !Array.isArray(oci["attestations"]) || oci["attestations"].length !== 2) throw new PublicationContractError("OCI_PUBLICATION_INVALID");
  const attestations = oci["attestations"].map(sha256);
  const firstAttestation = attestations[0]; const secondAttestation = attestations[1];
  if (firstAttestation === undefined || secondAttestation === undefined || typeof oci["indexDigest"] !== "string" || !/^sha256:[0-9a-f]{64}$/.test(oci["indexDigest"])) throw new PublicationContractError("OCI_PUBLICATION_INVALID");
  return {
    productGateSha256: sha256(item["productGateSha256"]),
    authoritativeReducer: { sha256: sha256(authoritativeReducer["sha256"]), result: "success" },
    trust: { profile: trust["profile"], trustPolicyVersion: string(trust["trustPolicyVersion"]), rotationEpoch: positiveInteger(trust["rotationEpoch"]), gateKeyId: sha256(trust["gateKeyId"]) },
    publication: { repository: "furyheimdall/jastreamer", environment: "product-promotion", receiptKeyId: sha256(publication["receiptKeyId"]), artifactSetSha256: sha256(publication["artifactSetSha256"]), candidates: { server: parseCandidate(candidates["server"]), control: parseCandidate(candidates["control"]) } },
    selection: parseArtifacts(item["selection"]),
    serverOci: { artifactSha256: sha256(oci["artifactSha256"]), indexDigest: oci["indexDigest"], platforms: ["linux/amd64", "linux/arm64"], attestations: [firstAttestation, secondAttestation] },
  };
};
