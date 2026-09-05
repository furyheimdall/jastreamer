import { existsSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { collectPackageReceipt } from "./artifacts.ts";
import { executeCommands } from "./commands.ts";
import { installInjection } from "./injection.ts";
import { createNamespaces, verifyCanary } from "./namespaces.ts";
import { parseScopeManifest } from "./parse.ts";
import { normalizeTraceFile, outsideAllowlist } from "./trace.ts";
import type { CleanupReceipt, ComponentName, ComponentResult, IsolationInput, IsolationResult, WorktreeProof } from "./types.ts";
import { createWorktree, removeWorktree, worktreeList } from "./worktree.ts";

class RuntimeInfrastructureError extends Error {
  override readonly name = "RuntimeInfrastructureError";
  constructor(readonly detail: string) { super(detail); }
}

type RunContext = {
  readonly repository: string;
  readonly runRoot: string;
  readonly input: IsolationInput;
  readonly namespaces: ReturnType<typeof createNamespaces>;
  readonly manifest: ReturnType<typeof parseScopeManifest>;
};

const emptyProof = (patterns: readonly string[]): WorktreeProof => ({
  detached: false, sparse: false, coneMode: false, patterns,
  siblingsPresent: { server: false, control: false, renderer: false },
});
const failedCleanup: CleanupReceipt = { directory: "failed", administration: "changed", removeExitCode: -1 };

const materializedFiles = (repository: string, worktree: string): readonly string[] => {
  const result = Bun.spawnSync(["git", "-C", worktree, "ls-files", "-t"], { cwd: repository, stdout: "pipe", stderr: "pipe" });
  if (result.exitCode !== 0) throw new RuntimeInfrastructureError(`list materialized files failed: ${result.stderr.toString().trim()}`);
  return result.stdout.toString().split("\n").filter((line) => line.startsWith("H ")).map((line) => line.slice(2)).sort();
};

function runComponent(component: ComponentName, context: RunContext): ComponentResult {
  const allowedPaths = context.manifest.components[component].paths;
  const worktree = join(context.runRoot, "worktrees", component);
  const tracePath = join(context.runRoot, "traces", `${component}.strace`);
  const namespace = context.namespaces[component];
  const before = worktreeList(context.repository);
  let proof = emptyProof(allowedPaths);
  let cleanup = failedCleanup;
  let commands: ComponentResult["commands"] = [];
  let derivedImageCleanup: ComponentResult["derivedImageCleanup"] = "not_applicable";
  let packageReceipt = collectPackageReceipt(component, namespace.artifact);
  let canary = namespace.evidence;
  let materializedPaths: readonly string[] = [];
  let accessedPaths: readonly string[] = [];
  let missingPaths: readonly string[] = [];
  let violations: readonly string[] = [];
  let error: string | undefined;
  let infrastructureFailure = false;
  try {
    proof = createWorktree(context.repository, worktree, allowedPaths);
    const siblingPresent = Object.entries(proof.siblingsPresent).some(([name, present]) => name !== component && present);
    if (!proof.detached || !proof.sparse || proof.coneMode || siblingPresent) throw new RuntimeInfrastructureError("worktree isolation proof failed");
    materializedPaths = materializedFiles(context.repository, worktree);
    if (context.input.injection !== undefined && component === "server") installInjection(worktree, context.input.injection);
    const execution = executeCommands({ component, worktree, namespaceRoot: join(context.runRoot, "namespaces"), artifactRoot: namespace.artifact, tracePath });
    commands = execution.commands;
    derivedImageCleanup = execution.derivedImageCleanup;
    if (commands.some((receipt) => receipt.exitCode === 127)) throw new RuntimeInfrastructureError("required executable is unavailable");
    const normalized = normalizeTraceFile(tracePath, {
      repositoryRoot: context.repository, worktree, initialDirectory: join(worktree, "apps", component), namespaceRoot: join(context.runRoot, "namespaces"),
      aliases: component === "control" ? [
        { traceRoot: "/workspace", hostRoot: worktree },
        { traceRoot: "/pub-cache", hostRoot: join(namespace.cache, "pub") },
        { traceRoot: "/artifacts", hostRoot: namespace.artifact },
      ] : [],
    });
    accessedPaths = normalized.accessedPaths;
    missingPaths = normalized.missingPaths;
    if (component === "control" && accessedPaths.length === 0) throw new RuntimeInfrastructureError("Control inner trace contained no normalized accesses");
    violations = outsideAllowlist(accessedPaths, allowedPaths, missingPaths, component);
    canary = verifyCanary(component, context.namespaces);
    packageReceipt = collectPackageReceipt(component, namespace.artifact);
    if (!canary.collisionFree) violations = [...violations, "@namespaces/canary-corruption"];
    if (packageReceipt.artifacts.length === 0) violations = [...violations, "@package/missing-artifact"];
    if (derivedImageCleanup === "failed") throw new RuntimeInfrastructureError("derived Control trace image cleanup failed");
  } catch (caught) {
    infrastructureFailure = true;
    error = caught instanceof Error ? caught.message : "unknown infrastructure failure";
  } finally {
    cleanup = removeWorktree(context.repository, worktree, before);
    if (cleanup.directory === "failed" || cleanup.administration === "changed") {
      infrastructureFailure = true;
      error = error ?? "worktree cleanup failed";
    }
  }
  const commandFailure = commands.some((receipt) => receipt.exitCode !== 0);
  const status = infrastructureFailure ? "infrastructure_failed" : commandFailure || violations.length > 0 ? "failed" : "passed";
  const base = {
    name: component, status, commands, allowedPaths, materializedPaths, accessedPaths, missingPaths, violations, worktree: proof,
    namespaces: { cache: namespace.cache, secret: namespace.secret, artifact: namespace.artifact },
    canary, package: packageReceipt, derivedImageCleanup, cleanup,
  } satisfies Omit<ComponentResult, "error">;
  return error === undefined ? base : { ...base, error };
}

export function verifyIsolation(input: IsolationInput, repository = process.cwd()): IsolationResult {
  const manifest = parseScopeManifest(join(repository, "tooling/scope-manifest.yaml"));
  const runRoot = mkdtempSync(join(tmpdir(), "jastreamer-isolation-"));
  let components: readonly ComponentResult[] = [];
  let runDirectoryCleanup: IsolationResult["runDirectoryCleanup"] = "failed";
  try {
    const namespaces = createNamespaces(runRoot);
    const context: RunContext = { repository, runRoot, input, namespaces, manifest };
    components = input.components.map((component) => runComponent(component, context));
  } finally {
    Bun.spawnSync(["sh", "-c", 'chmod -R u+w "$1" && rm -rf "$1"', "isolation-cleanup", runRoot], { stdout: "ignore", stderr: "pipe" });
    runDirectoryCleanup = existsSync(runRoot) ? "failed" : "removed";
  }
  const infrastructureFailure = runDirectoryCleanup === "failed" || components.some((component) => component.status === "infrastructure_failed");
  return { ok: components.every((component) => component.status === "passed"), infrastructureFailure, components, runDirectoryCleanup };
}
