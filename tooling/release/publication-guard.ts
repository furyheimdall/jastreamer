import { createHash } from "node:crypto";
import { lstatSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

const components = ["server", "control", "renderer"] as const;
type Component = (typeof components)[number];
type Event = "push" | "workflow_dispatch";
type Artifact = { readonly name: string; readonly sha256: string };
type StagedManifest = {
  readonly component: Component;
  readonly source_revision: string;
  readonly artifacts: readonly Artifact[];
};
type GuardCode = "PRODUCT_GATE_REQUIRED" | "PRODUCT_GATE_MISMATCH" | "PRODUCT_GATE_INVALID" | "NON_PROMOTABLE_EVENT";
type GuardReceipt = {
  readonly candidate: {
    readonly status: "staged" | "blocked" | "authorized";
    readonly component: Component;
    readonly manifest_sha256: string;
  };
  readonly product_gate_sha256?: string;
  readonly code?: GuardCode;
  readonly external_writes: readonly [];
};
export type GuardOptions = {
  readonly component: Component;
  readonly event: Event;
  readonly manifestPath: string;
  readonly outputPath: string;
  readonly stageRoot?: string;
  readonly productGateReceiptPath?: string;
  readonly verifiedProductGatePath?: string;
  readonly expectedProductGateSha256?: string;
};
export type GuardResult =
  | { readonly ok: true; readonly receipt: GuardReceipt }
  | { readonly ok: false; readonly code: GuardCode; readonly receipt: GuardReceipt };

const sha256Pattern = /^[0-9a-f]{64}$/;

export class CandidateManifestError extends Error {
  readonly name = "CandidateManifestError";
  readonly code = "CANDIDATE_MANIFEST_INVALID";
  constructor() {
    super("CANDIDATE_MANIFEST_INVALID");
  }
}

function parseManifest(path: string): StagedManifest {
  const value: unknown = JSON.parse(readFileSync(path, "utf8"));
  if (typeof value !== "object" || value === null) throw new CandidateManifestError();
  const record = Object.fromEntries(Object.entries(value));
  const component = components.find((candidate) => candidate === record["component"]);
  const sourceRevision = record["source_revision"];
  const artifacts = record["artifacts"];
  if (component === undefined || typeof sourceRevision !== "string" || sourceRevision.length === 0 || !Array.isArray(artifacts)) {
    throw new CandidateManifestError();
  }
  const parsedArtifacts: Artifact[] = [];
  for (const artifact of artifacts) {
    if (typeof artifact !== "object" || artifact === null) throw new CandidateManifestError();
    const entry = Object.fromEntries(Object.entries(artifact));
    if (typeof entry["name"] !== "string" || typeof entry["sha256"] !== "string" || !sha256Pattern.test(entry["sha256"])) {
      throw new CandidateManifestError();
    }
    parsedArtifacts.push({ name: entry["name"], sha256: entry["sha256"] });
  }
  if (parsedArtifacts.length === 0) throw new CandidateManifestError();
  return { component, source_revision: sourceRevision, artifacts: parsedArtifacts };
}

function digest(path: string): string {
  return createHash("sha256").update(readFileSync(path)).digest("hex");
}

type VerifiedGate = {
  readonly schemaVersion: 1;
  readonly kind: "product_gate_verification";
  readonly status: "authorized";
  readonly productGateSha256: string;
  readonly candidateManifests: Readonly<Record<"server" | "control", string>>;
  readonly rebuild: false;
  readonly externalMutations: 0;
  readonly rendererPublicAssets: readonly [];
  readonly selection: readonly { readonly kind: string; readonly path: string; readonly sha256: string }[];
};

function parseVerifiedGate(path: string): VerifiedGate | undefined {
  const value: unknown = JSON.parse(readFileSync(path, "utf8"));
  if (typeof value !== "object" || value === null) return undefined;
  const record = Object.fromEntries(Object.entries(value));
  const manifests = record["candidateManifests"];
  if (typeof manifests !== "object" || manifests === null) return undefined;
  const candidateManifests = Object.fromEntries(Object.entries(manifests));
  if (record["schemaVersion"] !== 1 || record["kind"] !== "product_gate_verification" || record["status"] !== "authorized"
    || typeof record["productGateSha256"] !== "string" || !sha256Pattern.test(record["productGateSha256"])
    || typeof candidateManifests["server"] !== "string" || typeof candidateManifests["control"] !== "string"
    || record["rebuild"] !== false || record["externalMutations"] !== 0 || !Array.isArray(record["rendererPublicAssets"])
    || record["rendererPublicAssets"].length !== 0 || !Array.isArray(record["selection"])) return undefined;
  const selection: { readonly kind: string; readonly path: string; readonly sha256: string }[] = [];
  for (const item of record["selection"]) {
    if (typeof item !== "object" || item === null) return undefined;
    const entry = Object.fromEntries(Object.entries(item));
    if (typeof entry["kind"] !== "string" || typeof entry["path"] !== "string" || typeof entry["sha256"] !== "string" || !sha256Pattern.test(entry["sha256"])) return undefined;
    selection.push({ kind: entry["kind"], path: entry["path"], sha256: entry["sha256"] });
  }
  return {
    schemaVersion: 1, kind: "product_gate_verification", status: "authorized",
    productGateSha256: record["productGateSha256"],
    candidateManifests: { server: candidateManifests["server"], control: candidateManifests["control"] },
    rebuild: false, externalMutations: 0, rendererPublicAssets: [], selection,
  };
}

function exactStageBytes(root: string, manifest: StagedManifest, verified: VerifiedGate): boolean {
  const names = manifest.artifacts.map((item) => item.name).sort();
  if (names.some((name) => basename(name) !== name) || JSON.stringify(readdirSync(root).sort()) !== JSON.stringify(names)) return false;
  const selected = new Map(verified.selection.filter((item) => item.kind.startsWith(`${manifest.component}-`)).map((item) => [basename(item.path), item.sha256]));
  if (selected.size !== manifest.artifacts.length) return false;
  for (const artifact of manifest.artifacts) {
    const path = join(root, artifact.name); const before = lstatSync(path); if (!before.isFile() || before.isSymbolicLink()) return false;
    const bytes = readFileSync(path); const after = lstatSync(path);
    if (before.dev !== after.dev || before.ino !== after.ino || before.size !== after.size || before.mtimeMs !== after.mtimeMs) return false;
    const actual = createHash("sha256").update(bytes).digest("hex"); if (actual !== artifact.sha256 || selected.get(artifact.name) !== actual) return false;
  }
  return true;
}

export function guardPublication(options: GuardOptions): GuardResult {
  const manifest = parseManifest(options.manifestPath);
  if (manifest.component !== options.component) throw new CandidateManifestError();
  const manifestSha256 = digest(options.manifestPath);
  switch (options.event) {
    case "workflow_dispatch": {
      const blocked: GuardReceipt = {
        candidate: { status: "blocked", component: options.component, manifest_sha256: manifestSha256 },
        code: "NON_PROMOTABLE_EVENT", external_writes: [],
      };
      writeFileSync(options.outputPath, `${JSON.stringify(blocked, null, 2)}\n`);
      return { ok: false, code: "NON_PROMOTABLE_EVENT", receipt: blocked };
    }
    case "push":
      break;
    default:
      options.event satisfies never;
  }
  const suppliedReceipt = options.productGateReceiptPath;
  const verifiedPath = options.verifiedProductGatePath;
  const expectedDigest = options.expectedProductGateSha256;
  let code: GuardCode | undefined;
  if (suppliedReceipt === undefined || expectedDigest === undefined) code = "PRODUCT_GATE_REQUIRED";
  else if (digest(suppliedReceipt) !== expectedDigest) code = "PRODUCT_GATE_MISMATCH";
  else if (verifiedPath === undefined) code = "PRODUCT_GATE_REQUIRED";
  else {
    const verified = parseVerifiedGate(verifiedPath);
    const manifestBinding = options.component === "renderer" ? undefined : verified?.candidateManifests[options.component];
    if (verified === undefined || verified.productGateSha256 !== expectedDigest || manifestBinding !== manifestSha256 || options.stageRoot === undefined) code = "PRODUCT_GATE_INVALID";
    else {
      try { if (!exactStageBytes(options.stageRoot, manifest, verified)) code = "PRODUCT_GATE_INVALID"; }
      catch { code = "PRODUCT_GATE_INVALID"; }
    }
    if (code === undefined) {
      const authorized: GuardReceipt = {
        candidate: { status: "authorized", component: options.component, manifest_sha256: manifestSha256 },
        product_gate_sha256: expectedDigest,
        external_writes: [],
      };
      writeFileSync(options.outputPath, `${JSON.stringify(authorized, null, 2)}\n`);
      return { ok: true, receipt: authorized };
    }
  }
  const blocked: GuardReceipt = {
    candidate: { status: "blocked", component: options.component, manifest_sha256: manifestSha256 },
    code,
    external_writes: [],
  };
  writeFileSync(options.outputPath, `${JSON.stringify(blocked, null, 2)}\n`);
  return { ok: false, code, receipt: blocked };
}
