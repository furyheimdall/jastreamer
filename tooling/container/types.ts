export type Platform = "linux/amd64" | "linux/arm64";
export type OwnedDockerResources = Readonly<{
  names: Set<string>;
  projects: Set<string>;
  volumes: Set<string>;
}>;
export type ImportedImage = Readonly<{ platform: Platform; tag: string }>;
export type ComposeReplacementInput = Readonly<{ compose: string; image: string; workspace: string }>;
export type Options = Readonly<{
  compose: string; layout: string; output: string; scenario: "replacement-persistence";
  fixture?: string; version: string; revision: string; created: string;
}>;
export type Descriptor = Readonly<{
  mediaType: string; digest: string; size: number;
  platform?: { os: string; architecture: string };
  annotations?: Record<string, string>;
}>;
export type Manifest = Readonly<{ schemaVersion: number; mediaType: string; config: Descriptor; layers: readonly Descriptor[] }>;
export type ImageRecord = Readonly<{ platform: Platform; descriptor: Descriptor; manifest: Manifest; config: Record<string, unknown> }>;
export type AttestationRecord = Readonly<{ subject: string; predicateType: string; statement: Record<string, unknown> }>;
export type LayoutInspection = Readonly<{
  digest: string; images: readonly ImageRecord[]; attestations: readonly AttestationRecord[];
  sbom: Record<string, unknown>; provenance: Record<string, unknown>;
}>;
export type RuntimeFact = Readonly<{
  platform: Platform; classification: "native" | "qemu-emulated"; hostArch: string; containerArch: string;
  uid: string; gid: string; health: string; portal: boolean; productVersion: string; sourceRevision: string;
  contractRevision: string; catalogRevision: number;
}>;
export type FilesystemFact = Readonly<{
  platform: Platform;
  requiredPaths: readonly string[];
  scannedEntries: number;
  forbiddenControlAssets: readonly string[];
  licenseSha256: string;
  noticesSha256: string;
  noticeEntries: number;
}>;
export type CleanupFact = Readonly<{
  taskContainersRemoved: boolean;
  composeContainersRemoved: boolean;
  composeNetworksRemoved: boolean;
  composeVolumesRemoved: boolean;
  taskVolumesRemoved: boolean;
}>;
