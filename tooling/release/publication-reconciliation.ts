import { lstatSync, rmSync } from "node:fs";
import { publicationCommands, registryCommands } from "./publication-commands";
import {
  providerDigest,
  providerJsonRecord,
  providerNotFound,
  PublicationExecutionError,
  type PreflightState,
} from "./publication-preflight";
import { AuthorizedRunner } from "./publication-runner";
import type {
  ObservedReleaseAsset,
  PreparedPublication,
  ProviderCommand,
  ProviderResult,
  ResourceOwnership,
} from "./publication-types";

export type TransactionState = PreflightState & {
  release: ResourceOwnership;
  temporaryOci: ResourceOwnership;
  finalOci: ResourceOwnership;
  providerObservedAssets: ObservedReleaseAsset[];
  cleanupFailures: string[];
};

export type ReleaseProbe = { readonly ownership: ResourceOwnership; readonly draft?: boolean };

export const initialPublicationState = (): TransactionState => ({
  registryLoggedIn: false,
  registryAuth: "absent",
  releasePreflightAbsent: false,
  temporaryOciPreflightAbsent: false,
  temporaryOciPreflightOccupied: false,
  finalOciPreflightAbsent: false,
  release: "absent",
  temporaryOci: "absent",
  finalOci: "absent",
  providerObservedAssets: [],
  cleanupFailures: [],
});

export const asCleanupCommand = (command: ProviderCommand, id = command.id): ProviderCommand => ({ ...command, id, phase: "cleanup" });
const releaseMarker = (prepared: PreparedPublication): string => `publication-run:${prepared.request.publisherRun.id}:${prepared.request.publisherRun.attempt}`;

export const probeRelease = async (prepared: PreparedPublication, runner: AuthorizedRunner, state: TransactionState, cleanup = false): Promise<ReleaseProbe> => {
  const base = publicationCommands(prepared).releaseProbe;
  let result: ProviderResult;
  try {
    result = await runner.run(cleanup ? asCleanupCommand(base, "release-cleanup-probe") : base);
  } catch (error) {
    if (!cleanup) throw error;
    return { ownership: "indeterminate" };
  }
  if (providerNotFound(result)) return { ownership: "absent" };
  if (result.exitCode !== 0) return { ownership: "indeterminate" };
  try {
    const value = providerJsonRecord(result, base.id);
    if (!state.releasePreflightAbsent || value["tag_name"] !== prepared.candidate.releaseTag || value["body"] !== releaseMarker(prepared) || typeof value["draft"] !== "boolean") return { ownership: "indeterminate" };
    return { ownership: "owned", draft: value["draft"] };
  } catch {
    return { ownership: "indeterminate" };
  }
};

export const probeOci = async (input: Readonly<{
  prepared: PreparedPublication;
  runner: AuthorizedRunner;
  command: ProviderCommand;
  absenceProven: boolean;
  cleanupId?: string;
}>): Promise<ResourceOwnership> => {
  let result: ProviderResult;
  try {
    result = await input.runner.run(input.cleanupId === undefined ? input.command : asCleanupCommand(input.command, input.cleanupId));
  } catch (error) {
    if (input.cleanupId === undefined) throw error;
    return "indeterminate";
  }
  if (providerNotFound(result)) return "absent";
  if (result.exitCode !== 0) return "indeterminate";
  try {
    return input.absenceProven && `sha256:${providerDigest(result.stdout.trim())}` === input.prepared.verified.serverOci.indexDigest ? "owned" : "indeterminate";
  } catch {
    return "indeterminate";
  }
};

export const observeReleaseAssets = async (prepared: PreparedPublication, runner: AuthorizedRunner, state: TransactionState): Promise<ObservedReleaseAsset[]> => {
  const command = publicationCommands(prepared).releaseAssetsProbe;
  const result = await runner.run(command);
  if (providerNotFound(result)) {
    state.release = "absent";
    throw new PublicationExecutionError("RELEASE_ASSET_RECONCILIATION_INDETERMINATE", command.id);
  }
  const value = providerJsonRecord(result, command.id);
  if (!state.releasePreflightAbsent || value["tag_name"] !== prepared.candidate.releaseTag || value["body"] !== releaseMarker(prepared) || typeof value["draft"] !== "boolean") {
    state.release = "indeterminate";
    throw new PublicationExecutionError("RELEASE_OWNERSHIP_INDETERMINATE", command.id);
  }
  state.release = "owned";
  const rawAssets = value["assets"];
  if (!Array.isArray(rawAssets)) throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID", command.id);
  const observed = rawAssets.map((raw): ObservedReleaseAsset => {
    if (typeof raw !== "object" || raw === null || Array.isArray(raw)) throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID", command.id);
    const asset = Object.fromEntries(Object.entries(raw));
    if (typeof asset["name"] !== "string" || typeof asset["size"] !== "number" || !Number.isSafeInteger(asset["size"]) || asset["size"] < 0) throw new PublicationExecutionError("PROVIDER_RESPONSE_INVALID", command.id);
    return { name: asset["name"], size: asset["size"], sha256: providerDigest(asset["digest"]) };
  });
  const ordered = [...observed].sort((left, right) => left.name.localeCompare(right.name));
  state.providerObservedAssets = ordered;
  const expected = prepared.artifacts.map(({ name, sha256, size }) => ({ name, sha256, size })).sort((left, right) => left.name.localeCompare(right.name));
  if (ordered.length !== expected.length || ordered.some((asset, index) => {
    const approved = expected[index];
    return approved === undefined || asset.name !== approved.name || asset.sha256 !== approved.sha256 || asset.size !== approved.size;
  })) throw new PublicationExecutionError("RELEASE_ASSET_DIGEST_MISMATCH", command.id);
  return ordered;
};

const cleanupOwnedResource = async (input: Readonly<{
  state: TransactionState;
  key: "release" | "temporaryOci" | "finalOci";
  command: ProviderCommand;
  probe: () => Promise<ResourceOwnership>;
  runner: AuthorizedRunner;
}>): Promise<void> => {
  const before = await input.probe();
  input.state[input.key] = before;
  if (before === "absent") return;
  if (before === "indeterminate") {
    input.state.cleanupFailures.push(`${input.command.id}-ownership-indeterminate`);
    return;
  }
  input.state[input.key] = "indeterminate";
  let dispatchFailed = false;
  try {
    dispatchFailed = (await input.runner.run(input.command)).exitCode !== 0;
  } catch {
    dispatchFailed = true;
  }
  const after = await input.probe();
  input.state[input.key] = after;
  if (after === "absent") return;
  input.state.cleanupFailures.push(after === "indeterminate" ? `${input.command.id}-ownership-indeterminate` : dispatchFailed ? input.command.id : `${input.command.id}-delete-not-observed`);
};

const pathIsAbsent = (path: string): boolean => {
  try {
    lstatSync(path);
    return false;
  } catch (error) {
    return (error as NodeJS.ErrnoException).code === "ENOENT";
  }
};

const addCleanupFailure = (state: TransactionState, failure: string): void => {
  if (!state.cleanupFailures.includes(failure)) state.cleanupFailures.push(failure);
};

export const cleanupRegistryAuth = async (prepared: PreparedPublication, runner: AuthorizedRunner, state: TransactionState): Promise<boolean> => {
  const root = prepared.request.dockerConfigRoot;
  if (root === undefined) {
    state.registryAuth = "absent";
    return true;
  }
  let logoutFailed = false;
  if (state.registryLoggedIn) {
    try {
      logoutFailed = (await runner.run(asCleanupCommand(registryCommands(prepared).logout))).exitCode !== 0;
    } catch {
      logoutFailed = true;
    }
    state.registryLoggedIn = false;
  }
  try {
    rmSync(root, { recursive: true, force: true });
  } catch {
    addCleanupFailure(state, "registry-auth-root");
  }
  if (pathIsAbsent(root)) {
    state.registryAuth = "absent";
    state.cleanupFailures = state.cleanupFailures.filter((failure) => failure !== "registry-auth-root" && failure !== "registry-logout");
    return true;
  }
  state.registryAuth = "indeterminate";
  addCleanupFailure(state, "registry-auth-root");
  if (logoutFailed) addCleanupFailure(state, "registry-logout");
  return false;
};

export const cleanupPublication = async (prepared: PreparedPublication, runner: AuthorizedRunner, state: TransactionState): Promise<void> => {
  if (prepared.request.component === "server") {
    const commands = registryCommands(prepared);
    if (state.temporaryOciPreflightOccupied) state.temporaryOci = "indeterminate";
    if (state.finalOci !== "absent") await cleanupOwnedResource({ state, key: "finalOci", command: commands.deleteFinal, runner, probe: () => probeOci({ prepared, runner, command: commands.inspectFinal, absenceProven: state.finalOciPreflightAbsent, cleanupId: "registry-cleanup-final-probe" }) });
    if (state.temporaryOci !== "absent" && !state.temporaryOciPreflightOccupied) await cleanupOwnedResource({ state, key: "temporaryOci", command: commands.deleteTemporary, runner, probe: () => probeOci({ prepared, runner, command: commands.inspectTemporary, absenceProven: state.temporaryOciPreflightAbsent, cleanupId: "registry-cleanup-temporary-probe" }) });
  }
  if (state.release !== "absent") await cleanupOwnedResource({ state, key: "release", command: publicationCommands(prepared).deleteRelease, runner, probe: async () => (await probeRelease(prepared, runner, state, true)).ownership });
  await cleanupRegistryAuth(prepared, runner, state);
};
