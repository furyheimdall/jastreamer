import { createHash } from "node:crypto";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const excludedDirectories = new Set(["node_modules", ".cache", ".tools"]);
const generatedServerOutputs = new Set([
  "apps/server/jastreamer-server",
  "apps/server/jastreamer-server.exe",
]);
export const serverSourceInputs = [
  "apps/server", "packaging/server", "packaging/container", "deploy/docker/server",
  "tooling/container", "tooling/fixtures/music", "tooling/componentctl", ".github/workflows/server-release.yml",
  ".github/workflows/server-qualification-staging.yml",
  ".github/workflows/server-qualification-platforms.yml",
  ".github/workflows/server-qualification-windows.yml",
  ".github/workflows/server-qualification-stage.yml",
  "LICENSE",
] as const;
export function sourceInputFiles(root: string, inputs: readonly string[] = serverSourceInputs): readonly string[] {
  const paths: string[] = [];
  const add = (path: string): void => {
    const sourcePath = relative(root, path).replaceAll("\\", "/");
    if (generatedServerOutputs.has(sourcePath) || sourcePath.startsWith("apps/server/bin/") || sourcePath.startsWith("apps/server/dist/")) return;
    const stat = statSync(path);
    if (!stat.isDirectory()) { paths.push(sourcePath); return; }
    for (const name of readdirSync(path)) if (!excludedDirectories.has(name)) add(join(path, name));
  };
  for (const input of inputs) add(join(root, input));
  return paths.sort();
}
export function sourceIdentity(root: string, inputs: readonly string[] = serverSourceInputs): string {
  const hash = createHash("sha256");
  for (const path of sourceInputFiles(root, inputs)) hash.update(path).update("\0").update(readFileSync(join(root, path))).update("\0");
  return `sha256:${hash.digest("hex")}`;
}
