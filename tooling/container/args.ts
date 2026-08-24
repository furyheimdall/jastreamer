import { createHash } from "node:crypto";
import { existsSync, readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import type { Options } from "./types";

export class UsageError extends Error { readonly exitCode = 64; }
export class GateError extends Error { readonly exitCode = 65; }

const optionNames = new Set(["--platform", "--compose", "--scenario", "--oci-layout", "--output", "--fixture"]);
export function parseArgs(args: readonly string[], root: string): Options {
  const values = new Map<string, string>();
  for (let index = 0; index < args.length; index += 2) {
    const name = args[index]; const value = args[index + 1];
    if (!name || !optionNames.has(name) || !value || value.startsWith("--")) throw new UsageError(`invalid argument near ${name ?? "end of input"}`);
    if (values.has(name)) throw new UsageError(`duplicate argument ${name}`);
    values.set(name, value);
  }
  if (values.get("--platform") !== "linux/amd64,linux/arm64") throw new UsageError("--platform must be exactly linux/amd64,linux/arm64");
  for (const name of ["--compose", "--scenario", "--oci-layout", "--output"]) if (!values.has(name)) throw new UsageError(`missing ${name}`);
  if (values.get("--scenario") !== "replacement-persistence") throw new UsageError("unsupported --scenario");
  const compose = resolve(root, values.get("--compose")!);
  if (!existsSync(compose)) throw new UsageError(`compose file does not exist: ${compose}`);
  const version = readFileSync(resolve(root, "apps/server/VERSION"), "utf8").trim();
  const revision = process.env.JASTREAMER_REVISION?.trim() || sourceIdentity(root);
  const epoch = process.env.SOURCE_DATE_EPOCH;
  const created = epoch ? new Date(Number(epoch) * 1000).toISOString() : new Date().toISOString();
  if (!version || !revision || !created) throw new GateError("BUILD_METADATA_MISSING");
  return { compose, layout: resolve(values.get("--oci-layout")!), output: resolve(values.get("--output")!),
    scenario: "replacement-persistence", fixture: values.get("--fixture"), version, revision, created };
}

function sourceIdentity(root: string): string {
  const hash = createHash("sha256"); const files: string[] = [];
  const walk = (directory: string): void => { for (const entry of readdirSync(directory)) { const path = join(directory, entry); const relativePath = relative(root, path); if (statSync(path).isDirectory()) walk(path); else if (!relativePath.endsWith("jastreamer-server") && !relativePath.endsWith("_test.go")) files.push(relativePath); } };
  for (const directory of ["apps/server", "packaging/container"]) walk(join(root, directory));
  files.push("LICENSE");
  for (const path of files.sort()) hash.update(path).update("\0").update(readFileSync(join(root, path))).update("\0");
  return `sha256:${hash.digest("hex")}`;
}

const forbidden = /(^|\/)(apps\/control|control(?:\/|[-_.])|flutter_assets|AssetManifest\.json)|\.(?:apk|aab|msix)$/i;
export function scanContext(root: string, fixture?: string): void {
  if (fixture) {
    const path = resolve(root, fixture);
    if (!existsSync(path) || readdirSync(path, { recursive: true }).some((entry) => forbidden.test(String(entry)))) throw new GateError("FORBIDDEN_CONTROL_ASSET");
  }
  const dockerfile = readFileSync(resolve(root, "apps/server/Dockerfile"), "utf8");
  for (const line of dockerfile.split("\n")) {
    if (/^\s*(COPY|ADD)\b/i.test(line) && forbidden.test(line)) throw new GateError("FORBIDDEN_CONTROL_ASSET");
  }
  const requestedIgnore = readFileSync(resolve(root, "apps/server/.dockerignore"), "utf8");
  const effectivePath = resolve(root, "apps/server/Dockerfile.dockerignore");
  if (!existsSync(effectivePath)) throw new GateError("EFFECTIVE_DOCKERIGNORE_MISSING");
  const ignored = readFileSync(effectivePath, "utf8").split("\n");
  if (!ignored.includes("apps/control") || !ignored.includes("apps/renderer") || readFileSync(effectivePath, "utf8") !== requestedIgnore) throw new GateError("FORBIDDEN_CONTROL_ASSET");
}
