export const PUBLIC_COMPONENTS = ["server", "control"] as const;
export type PublicComponent = (typeof PUBLIC_COMPONENTS)[number];

export type PublicationRequest = {
  readonly schemaVersion: 1;
  readonly mode: "production" | "observe";
  readonly component: PublicComponent;
  readonly repository: "furyheimdall/jastreamer";
  readonly environment: "product-promotion";
  readonly event: {
    readonly name: string;
    readonly ref: string;
    readonly refType: string;
    readonly refName: string;
    readonly sha: string;
  };
  readonly publisherRun: {
    readonly id: string;
    readonly attempt: number;
    readonly actor: string;
  };
  readonly gate: {
    readonly root: string;
    readonly receiptPath: string;
    readonly verifiedPath: string;
    readonly expectedReceiptSha256: string;
  };
  readonly stageRoot: string;
  readonly receiptPath: string;
  readonly dockerConfigRoot?: string;
};

export type CandidateStaging = {
  readonly repository: "furyheimdall/jastreamer";
  readonly workflowPath: string;
  readonly eventName: "workflow_dispatch";
  readonly headSha: string;
  readonly callerRunId: string;
  readonly callerRunAttempt: number;
  readonly callerRef: string;
  readonly callerSha: string;
  readonly calledWorkflowPath: string;
  readonly calledJob: PublicComponent;
  readonly calledJobResult: "success";
  readonly runId: string;
  readonly runAttempt: number;
  readonly artifactId: string;
  readonly artifactName: string;
  readonly artifactDigest: string;
  readonly artifactAttemptProvenance: "caller-run+upload-output+embedded-manifest";
  readonly artifactManifestSha256: string;
  readonly artifactContentManifestSha256: string;
};

export type PublicationCandidate = {
  readonly releaseTag: string;
  readonly manifest: { readonly path: string; readonly sha256: string };
  readonly staging: CandidateStaging;
};

export type VerifiedPublication = {
  readonly productGateSha256: string;
  readonly authoritativeReducer: { readonly sha256: string; readonly result: "success" };
  readonly trust: {
    readonly profile: "production" | "fixture";
    readonly trustPolicyVersion: string;
    readonly rotationEpoch: number;
    readonly gateKeyId: string;
  };
  readonly publication: {
    readonly repository: "furyheimdall/jastreamer";
    readonly environment: "product-promotion";
    readonly receiptKeyId: string;
    readonly artifactSetSha256: string;
    readonly candidates: Readonly<Record<PublicComponent, PublicationCandidate>>;
  };
  readonly selection: readonly ManifestArtifact[];
  readonly serverOci: {
    readonly artifactSha256: string;
    readonly indexDigest: string;
    readonly platforms: readonly ["linux/amd64", "linux/arm64"];
    readonly attestations: readonly [string, string];
  };
};

export type ManifestArtifact = {
  readonly kind: string;
  readonly path: string;
  readonly sha256: string;
};

export type PublicationManifest = {
  readonly component: PublicComponent;
  readonly tag: string;
  readonly sourceRevision: string;
  readonly artifactSetSha256: string;
  readonly artifacts: readonly ManifestArtifact[];
};

export type SelectedArtifact = ManifestArtifact & {
  readonly name: string;
  readonly absolutePath: string;
  readonly size: number;
};

export type PreparedPublication = {
  readonly request: PublicationRequest;
  readonly verified: VerifiedPublication;
  readonly candidate: PublicationCandidate;
  readonly manifest: PublicationManifest;
  readonly artifacts: readonly SelectedArtifact[];
  readonly version: string;
  readonly immutableFiles: readonly { readonly path: string; readonly sha256: string }[];
  readonly authorizationFiles: readonly { readonly path: string; readonly sha256: string }[];
};

export type ProviderPhase = "read" | "write" | "cleanup";
export type ProviderCommand = {
  readonly id: string;
  readonly phase: ProviderPhase;
  readonly argv: readonly string[];
  readonly stdin: "github-token" | "none";
  readonly mutates?: true;
};

export type AuthorizedProviderCommand = ProviderCommand & {
  readonly authorization: {
    readonly kind: "publication-closure-sha256" | "cleanup-ownership-sha256";
    readonly sha256: string;
  };
};

export type ProviderResult = {
  readonly exitCode: number;
  readonly stdout: string;
  readonly stderr: string;
};

export interface PublicationDriver {
  run(command: AuthorizedProviderCommand): Promise<ProviderResult>;
}

export type PublicationTrace =
  | { readonly sequence: number; readonly kind: "write-intent"; readonly commandId: string; readonly disposition: "possibly-committed" }
  | { readonly sequence: number; readonly kind: "rehash"; readonly commandId: string; readonly closureSha256: string }
  | { readonly sequence: number; readonly kind: "provider"; readonly commandId: string; readonly phase: ProviderPhase; readonly argv: readonly string[]; readonly exitCode: number };

export type ResourceOwnership = "absent" | "owned" | "indeterminate";
export type ObservedReleaseAsset = { readonly name: string; readonly sha256: string; readonly size: number };

export type PublicationReceipt = {
  readonly schemaVersion: 1;
  readonly kind: "publication_transaction_receipt";
  readonly status: "published" | "failed";
  readonly component: PublicComponent;
  readonly releaseTag: string;
  readonly publisherRun: { readonly id: string; readonly attempt: number };
  readonly productGateSha256: string;
  readonly manifestSha256: string;
  readonly artifactSetSha256: string;
  readonly selectedAssets: readonly ObservedReleaseAsset[];
  readonly providerObservedAssets: readonly ObservedReleaseAsset[];
  readonly providerCommands: readonly PublicationTrace[];
  readonly failure?: { readonly code: string; readonly commandId?: string };
  readonly cleanup: {
    readonly releaseOwnedByRun: boolean | null;
    readonly finalOciOwnedByRun: boolean | null;
    readonly temporaryOciOwnedByRun: boolean | null;
    readonly registryAuthOwnedByRun: boolean | null;
    readonly ownership: {
      readonly release: ResourceOwnership;
      readonly finalOci: ResourceOwnership;
      readonly temporaryOci: ResourceOwnership;
      readonly registryAuth: ResourceOwnership;
    };
    readonly indeterminateResources: readonly string[];
    readonly residualResources: readonly string[];
    readonly priorReleaseTouched: false;
    readonly priorOciTouched: false;
    readonly failures: readonly string[];
  };
  readonly authentication: {
    readonly algorithm: "HMAC-SHA256";
    readonly keyId: string;
    readonly payloadSha256: string;
    readonly signature: string;
  };
};
