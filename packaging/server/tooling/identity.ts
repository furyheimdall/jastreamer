import { createHash } from "node:crypto";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const excludedDirectories = new Set(["node_modules", ".cache", ".tools"]);
export function sourceIdentity(root: string): string {
  const paths: string[] = [];
  const add = (path: string): void => {
    const stat = statSync(path);
    if (!stat.isDirectory()) { paths.push(relative(root, path)); return; }
    for (const name of readdirSync(path)) if (!excludedDirectories.has(name)) add(join(path, name));
  };
  for (const input of [
    "apps/server", "packaging/server", "packaging/container", "deploy/docker/server",
    "tooling/container", "tooling/fixtures/music", "tooling/componentctl", ".github/workflows/server-release.yml", "LICENSE",
  ]) add(join(root, input));
  const hash = createHash("sha256");
  for (const path of paths.sort()) hash.update(path).update("\0").update(readFileSync(join(root, path))).update("\0");
  return `sha256:${hash.digest("hex")}`;
}
