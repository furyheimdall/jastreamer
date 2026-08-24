import { existsSync, mkdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { COMPONENTS, type CleanupReceipt, type ComponentName, type WorktreeProof } from "./types.ts";

export class WorktreeError extends Error {
  override readonly name = "WorktreeError";
  constructor(readonly operation: string, readonly exitCode: number, readonly stderr: string) {
    super(`${operation} failed (${exitCode}): ${stderr.trim()}`);
  }
}

const git = (repository: string, args: readonly string[], stdin?: string) =>
  Bun.spawnSync(["git", ...args], { cwd: repository, stdin: stdin === undefined ? undefined : Buffer.from(stdin), stdout: "pipe", stderr: "pipe" });

const requireGit = (repository: string, operation: string, args: readonly string[], stdin?: string): ReturnType<typeof git> => {
  const result = git(repository, args, stdin);
  if (result.exitCode !== 0) throw new WorktreeError(operation, result.exitCode, result.stderr.toString());
  return result;
};

export const worktreeList = (repository: string): string =>
  requireGit(repository, "list worktrees", ["worktree", "list", "--porcelain"]).stdout.toString();

export function createWorktree(repository: string, path: string, patterns: readonly string[]): WorktreeProof {
  mkdirSync(dirname(path), { recursive: true });
  requireGit(repository, "create detached worktree", ["worktree", "add", "--detach", "--no-checkout", path, "HEAD"]);
  requireGit(repository, "initialize non-cone sparse checkout", ["-C", path, "sparse-checkout", "init", "--no-cone"]);
  requireGit(repository, "set exact sparse paths", ["-C", path, "sparse-checkout", "set", "--no-cone", "--stdin"], `${patterns.join("\n")}\n`);
  requireGit(repository, "checkout sparse HEAD", ["-C", path, "checkout", "--detach", "HEAD"]);
  const symbolic = git(repository, ["-C", path, "symbolic-ref", "-q", "HEAD"]);
  const sparse = requireGit(repository, "read sparse mode", ["-C", path, "config", "--bool", "core.sparseCheckout"]).stdout.toString().trim() === "true";
  const cone = git(repository, ["-C", path, "config", "--bool", "core.sparseCheckoutCone"]).stdout.toString().trim() === "true";
  const siblings: Record<ComponentName, boolean> = { server: false, control: false, renderer: false };
  for (const component of COMPONENTS) siblings[component] = existsSync(join(path, "apps", component));
  return { detached: symbolic.exitCode !== 0, sparse, coneMode: cone, patterns: [...patterns], siblingsPresent: siblings };
}

export function removeWorktree(repository: string, path: string, before: string): CleanupReceipt {
  const removal = git(repository, ["worktree", "remove", "--force", path]);
  if (removal.exitCode !== 0) {
    rmSync(path, { recursive: true, force: true });
    git(repository, ["worktree", "prune"]);
  }
  const after = worktreeList(repository);
  return {
    directory: existsSync(path) ? "failed" : "removed",
    administration: after === before ? "restored" : "changed",
    removeExitCode: removal.exitCode,
  };
}
