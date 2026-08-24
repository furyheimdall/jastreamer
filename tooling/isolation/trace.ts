import { isAbsolute, relative, resolve } from "node:path";
import type { ComponentName, TraceContext } from "./types.ts";

type TraceResult = {
  readonly accessedPaths: readonly string[];
  readonly missingPaths: readonly string[];
};

const decodeQuoted = (raw: string): string | undefined => {
  try {
    const value: unknown = JSON.parse(`"${raw}"`);
    return typeof value === "string" ? value : undefined;
  } catch (error) {
    if (error instanceof SyntaxError) return undefined;
    throw error;
  }
};

const repositoryPath = (absolutePath: string, context: TraceContext): string | undefined => {
  let normalized = absolutePath;
  for (const alias of context.aliases) {
    const candidate = relative(alias.traceRoot, normalized);
    if (candidate === "" || (candidate !== ".." && !candidate.startsWith("../") && !isAbsolute(candidate))) {
      normalized = resolve(alias.hostRoot, candidate);
      break;
    }
  }
  const namespacePath = relative(context.namespaceRoot, normalized);
  if (namespacePath === "" || (namespacePath !== ".." && !namespacePath.startsWith("../") && !isAbsolute(namespacePath))) {
    return namespacePath === "" ? "@namespaces" : `@namespaces/${namespacePath}`;
  }
  for (const root of [context.worktree, context.repositoryRoot]) {
    const candidate = relative(root, normalized);
    if (candidate === "") return ".";
    if (candidate === ".git" || candidate.startsWith(".git/")) return undefined;
    if (candidate !== ".." && !candidate.startsWith("../") && !isAbsolute(candidate)) return candidate;
  }
  return undefined;
};

const processId = (line: string): string => line.match(/^\s*(\d+)\s/)?.[1] ?? "root";
const SINGLE_PATH_SYSCALLS = new Set([
  "access", "chdir", "chmod", "chown", "creat", "execve", "execveat", "faccessat", "faccessat2", "fchmodat",
  "fchownat", "lchown", "lstat", "mkdir", "mkdirat", "mknod", "mknodat", "newfstatat", "open", "openat", "openat2",
  "readlink", "readlinkat", "rmdir", "stat", "statfs", "statx", "truncate", "unlink", "unlinkat", "utime", "utimes",
]);
const TWO_PATH_SYSCALLS = new Set(["link", "linkat", "rename", "renameat", "renameat2", "symlink", "symlinkat"]);

export function normalizeTrace(trace: string, context: TraceContext): TraceResult {
  const accessed = new Set<string>();
  const missing = new Set<string>();
  const directories = new Map<string, string>();
  const pending = new Map<string, string>();
  directories.set("root", context.initialDirectory);

  for (const rawLine of trace.split("\n")) {
    const pid = processId(rawLine);
    if (rawLine.includes("<unfinished ...>")) {
      pending.set(pid, rawLine.replace("<unfinished ...>", ""));
      continue;
    }
    const resumed = rawLine.match(/<\.\.\. [a-zA-Z0-9_]+ resumed>(.*)$/)?.[1];
    const line = resumed === undefined ? rawLine : `${pending.get(pid) ?? ""}${resumed}`;
    if (resumed !== undefined) pending.delete(pid);
    const currentDirectory = directories.get(pid) ?? context.initialDirectory;
    const child = line.match(/\b(?:clone|clone3|fork|vfork)\([^)]*\)\s+=\s+(\d+)/)?.[1];
    if (child !== undefined) directories.set(child, currentDirectory);

    const successfulChdir = line.match(/\bchdir\("((?:[^"\\]|\\.)*)"\)\s+=\s+0/);
    if (successfulChdir?.[1] !== undefined) {
      const decoded = decodeQuoted(successfulChdir[1]);
      if (decoded !== undefined) directories.set(pid, isAbsolute(decoded) ? resolve(decoded) : resolve(currentDirectory, decoded));
    }
    const successfulFchdir = line.match(/\bfchdir\(\d+<([^>]+)>\)\s+=\s+0/);
    if (successfulFchdir?.[1] !== undefined) directories.set(pid, resolve(successfulFchdir[1]));

    const syscall = line.match(/\b([a-zA-Z0-9_]+)\(/)?.[1];
    if (syscall === undefined || (!SINGLE_PATH_SYSCALLS.has(syscall) && !TWO_PATH_SYSCALLS.has(syscall))) continue;
    const quoted = [...line.matchAll(/"((?:[^"\\]|\\.)*)"/g)];
    const paths = TWO_PATH_SYSCALLS.has(syscall) ? quoted.slice(0, 2) : quoted.slice(0, 1);
    for (const match of paths) {
      const raw = match[1];
      if (raw === undefined) continue;
      const decoded = decodeQuoted(raw);
      if (decoded === undefined || decoded.length === 0) continue;
      const prefix = line.slice(0, match.index);
      const dirfd = [...prefix.matchAll(/(?:AT_FDCWD<([^>]+)>|\d+<([^>]+)>|AT_FDCWD)\s*,\s*$/g)].at(-1);
      const dirfdPath = dirfd?.[1] ?? dirfd?.[2];
      const base = dirfdPath === undefined ? currentDirectory : dirfdPath;
      const absolute = isAbsolute(decoded) ? resolve(decoded) : resolve(base, decoded);
      const path = repositoryPath(absolute, context);
      if (path === undefined) continue;
      accessed.add(path);
      if (line.includes("ENOENT")) missing.add(path);
    }
  }
  return { accessedPaths: [...accessed].sort(), missingPaths: [...missing].sort() };
}

const ANCESTOR_METADATA_PROBE = /^(?:apps(?:\/(?:server|control|renderer))?\/)?(?:Cargo\.toml|rust-toolchain(?:\.toml)?|clippy\.toml|\.clippy\.toml|\.cargo\/config(?:\.toml)?|go\.work|BUILD\.gn|blaze-out|dart\/config\/ide\/flutter\.json|\.git|\.bzr|\.fslckout|\.hg|\.svn|_FOSSIL_)$/;

export const outsideAllowlist = (accessed: readonly string[], allowed: readonly string[], missing: readonly string[], component: ComponentName): readonly string[] =>
  accessed.filter((path) => path !== "." && path !== "@namespaces" && path !== `@namespaces/${component}` && !path.startsWith(`@namespaces/${component}/`) && !(missing.includes(path) && ANCESTOR_METADATA_PROBE.test(path)) && !allowed.some((entry) =>
    path === entry || path.startsWith(`${entry}/`) || entry.startsWith(`${path}/`),
  )).sort();
