import type { ManifestArtifact, VerifiedPublication } from "./publication-types";

type VerificationSuccess = VerifiedPublication & {
  readonly ok: true;
  readonly rebuild: false;
  readonly externalMutations: 0;
  readonly candidateManifests: Readonly<Record<"server" | "control", string>>;
  readonly rendererPublicAssets: readonly [];
};

type VerificationFailure = {
  readonly ok: false;
  readonly code: string;
  readonly path: string;
  readonly externalMutations: number;
};

export function verifyProductGate(receiptPath: string, options: Readonly<Record<string, unknown>>): VerificationSuccess | VerificationFailure;
export type { ManifestArtifact };
