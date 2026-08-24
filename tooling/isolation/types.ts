export const COMPONENTS = ["server", "control", "renderer"] as const;
export type ComponentName = (typeof COMPONENTS)[number];
export type InjectionId = "server-imports-control";

export type IsolationInput = {
  readonly components: readonly ComponentName[];
  readonly injection?: InjectionId;
};

export type ScopeManifest = {
  readonly schema: 1;
  readonly components: Readonly<Record<ComponentName, { readonly paths: readonly string[] }>>;
};

export type CommandReceipt = {
  readonly command: string;
  readonly exitCode: number;
};

export type SiblingProof = Readonly<Record<ComponentName, boolean>>;
export type WorktreeProof = {
  readonly detached: boolean;
  readonly sparse: boolean;
  readonly coneMode: boolean;
  readonly patterns: readonly string[];
  readonly siblingsPresent: SiblingProof;
};

export type CanaryEvidence = {
  readonly cachePath: string;
  readonly secretPath: string;
  readonly artifactPath: string;
  readonly token: string;
  readonly collisionFree: boolean;
};

export type PackageReceipt = {
  readonly artifacts: readonly string[];
  readonly platformDeferrals: readonly string[];
};

export type CleanupReceipt = {
  readonly directory: "removed" | "failed";
  readonly administration: "restored" | "changed";
  readonly removeExitCode: number;
};

export type ComponentResult = {
  readonly name: ComponentName;
  readonly status: "passed" | "failed" | "infrastructure_failed";
  readonly commands: readonly CommandReceipt[];
  readonly allowedPaths: readonly string[];
  readonly materializedPaths: readonly string[];
  readonly accessedPaths: readonly string[];
  readonly missingPaths: readonly string[];
  readonly violations: readonly string[];
  readonly worktree: WorktreeProof;
  readonly namespaces: {
    readonly cache: string;
    readonly secret: string;
    readonly artifact: string;
  };
  readonly canary: CanaryEvidence;
  readonly package: PackageReceipt;
  readonly derivedImageCleanup: "removed" | "not_applicable" | "failed";
  readonly cleanup: CleanupReceipt;
  readonly error?: string;
};

export type IsolationResult = {
  readonly ok: boolean;
  readonly infrastructureFailure: boolean;
  readonly components: readonly ComponentResult[];
  readonly runDirectoryCleanup: "removed" | "failed";
};

export type TraceAlias = { readonly traceRoot: string; readonly hostRoot: string };
export type TraceContext = {
  readonly repositoryRoot: string;
  readonly worktree: string;
  readonly initialDirectory: string;
  readonly namespaceRoot: string;
  readonly aliases: readonly TraceAlias[];
};
