import { publicationCommands, registryCommands } from "./publication-commands";
import { approveProviderTransfer } from "./publication-files";
import {
  assertProviderSuccess,
  PublicationExecutionError,
  runPublicationPreflight,
} from "./publication-preflight";
import {
  asCleanupCommand,
  cleanupPublication,
  cleanupRegistryAuth,
  initialPublicationState,
  observeReleaseAssets,
  probeOci,
  probeRelease,
  type TransactionState,
} from "./publication-reconciliation";
import { writeAuthenticatedReceipt, type UnsignedPublicationReceipt } from "./publication-receipt";
import { AuthorizedRunner } from "./publication-runner";
import type {
  PreparedPublication,
  ProviderCommand,
  ProviderResult,
  PublicationDriver,
  PublicationReceipt,
  ResourceOwnership,
} from "./publication-types";

const receiptBody = (prepared: PreparedPublication, runner: AuthorizedRunner, state: TransactionState, status: PublicationReceipt["status"], failure?: PublicationReceipt["failure"]): UnsignedPublicationReceipt => {
  const ownership = { release: state.release, finalOci: state.finalOci, temporaryOci: state.temporaryOci, registryAuth: state.registryAuth } as const;
  const resourceName = (key: string): string => key === "finalOci" ? "final-oci" : key === "temporaryOci" ? "temporary-oci" : key === "registryAuth" ? "registry-auth-root" : key;
  const indeterminateResources = Object.entries(ownership).filter(([, value]) => value === "indeterminate").map(([key]) => resourceName(key));
  const residualResources = Object.entries(ownership).filter(([key, value]) => value !== "absent" && (status === "failed" || key === "registryAuth")).map(([key]) => resourceName(key));
  return {
    schemaVersion: 1,
    kind: "publication_transaction_receipt",
    status,
    component: prepared.request.component,
    releaseTag: prepared.candidate.releaseTag,
    publisherRun: prepared.request.publisherRun,
    productGateSha256: prepared.request.gate.expectedReceiptSha256,
    manifestSha256: prepared.candidate.manifest.sha256,
    artifactSetSha256: prepared.manifest.artifactSetSha256,
    selectedAssets: prepared.artifacts.map(({ name, sha256, size }) => ({ name, sha256, size })),
    providerObservedAssets: state.providerObservedAssets,
    providerCommands: runner.trace,
    cleanup: {
      releaseOwnedByRun: state.release === "indeterminate" ? null : state.release === "owned",
      finalOciOwnedByRun: state.finalOci === "indeterminate" ? null : state.finalOci === "owned",
      temporaryOciOwnedByRun: state.temporaryOci === "indeterminate" ? null : state.temporaryOci === "owned",
      registryAuthOwnedByRun: state.registryAuth === "indeterminate" ? null : state.registryAuth === "owned",
      ownership,
      indeterminateResources,
      residualResources,
      priorReleaseTouched: false,
      priorOciTouched: false,
      failures: state.cleanupFailures,
    },
    ...(failure === undefined ? {} : { failure }),
  };
};

const reconcileWrite = async (input: Readonly<{
  result: ProviderResult;
  command: ProviderCommand;
  ownership: ResourceOwnership;
}>): Promise<void> => {
  assertProviderSuccess(input.result, input.command.id);
  if (input.ownership !== "owned") throw new PublicationExecutionError("PROVIDER_WRITE_RECONCILIATION_FAILED", input.command.id);
};

export class PublicationTransactionError extends Error {
  readonly name = "PublicationTransactionError";
  constructor(readonly receipt: PublicationReceipt, readonly causeValue: unknown) {
    super(receipt.failure?.code ?? "PUBLICATION_TRANSACTION_FAILED");
  }
}

export const executePublication = async (input: Readonly<{ prepared: PreparedPublication; driver: PublicationDriver; receiptKey: Buffer }>): Promise<PublicationReceipt> => {
  const approved = approveProviderTransfer(input.prepared);
  const prepared = approved.prepared;
  const runner = new AuthorizedRunner(prepared, input.driver);
  const state = initialPublicationState();
  if (prepared.request.dockerConfigRoot !== undefined) state.registryAuth = "indeterminate";
  try {
    try {
      await runPublicationPreflight({ prepared, runner, state });
      const release = publicationCommands(prepared);
      state.release = "indeterminate";
      const createResult = await runner.run(release.createRelease);
      state.release = (await probeRelease(prepared, runner, state)).ownership;
      await reconcileWrite({ result: createResult, command: release.createRelease, ownership: state.release });

      const uploadResult = await runner.run(release.uploadRelease);
      state.providerObservedAssets = await observeReleaseAssets(prepared, runner, state);
      assertProviderSuccess(uploadResult, release.uploadRelease.id);

      if (prepared.request.component === "server") {
        const registry = registryCommands(prepared);
        state.temporaryOci = "indeterminate";
        const temporaryResult = await runner.run(registry.copyTemporary);
        state.temporaryOci = await probeOci({ prepared, runner, command: registry.inspectTemporary, absenceProven: state.temporaryOciPreflightAbsent });
        await reconcileWrite({ result: temporaryResult, command: registry.copyTemporary, ownership: state.temporaryOci });

        state.finalOci = "indeterminate";
        const finalResult = await runner.run(registry.copyFinal);
        state.finalOci = await probeOci({ prepared, runner, command: registry.inspectFinal, absenceProven: state.finalOciPreflightAbsent });
        await reconcileWrite({ result: finalResult, command: registry.copyFinal, ownership: state.finalOci });

        state.temporaryOci = "indeterminate";
        const removeResult = await runner.run(asCleanupCommand(registry.deleteTemporary, "registry-remove-temporary-before-commit"));
        state.temporaryOci = await probeOci({ prepared, runner, command: registry.inspectTemporary, absenceProven: state.temporaryOciPreflightAbsent, cleanupId: "registry-remove-temporary-probe" });
        if (removeResult.exitCode !== 0 || state.temporaryOci !== "absent") throw new PublicationExecutionError("PROVIDER_WRITE_FAILED", "registry-remove-temporary-before-commit");
      }

      state.providerObservedAssets = await observeReleaseAssets(prepared, runner, state);
      state.release = "indeterminate";
      const publishResult = await runner.run(release.publishRelease);
      const published = await probeRelease(prepared, runner, state);
      state.release = published.ownership;
      assertProviderSuccess(publishResult, release.publishRelease.id);
      if (state.release !== "owned" || published.draft !== false) throw new PublicationExecutionError("RELEASE_PUBLISH_RECONCILIATION_FAILED", release.publishRelease.id);
      if (!await cleanupRegistryAuth(prepared, runner, state)) throw new PublicationExecutionError("REGISTRY_AUTH_CLEANUP_FAILED", "registry-logout");

      return writeAuthenticatedReceipt({ path: prepared.request.receiptPath, key: input.receiptKey, receipt: receiptBody(prepared, runner, state, "published") });
    } catch (error) {
      await cleanupPublication(prepared, runner, state);
      const execution = error instanceof PublicationExecutionError ? error : undefined;
      const contract = error instanceof Error && "code" in error && typeof error.code === "string" ? error.code : undefined;
      const failure = { code: execution?.code ?? contract ?? "PUBLICATION_TRANSACTION_FAILED", ...(execution?.commandId === undefined ? {} : { commandId: execution.commandId }) };
      const receipt = writeAuthenticatedReceipt({ path: prepared.request.receiptPath, key: input.receiptKey, receipt: receiptBody(prepared, runner, state, "failed", failure) });
      throw new PublicationTransactionError(receipt, error);
    }
  } finally {
    approved.dispose();
  }
};
