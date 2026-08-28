import { createHash } from "node:crypto";
import { readFileSync, readdirSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const repositoryRoot = resolve(import.meta.dirname, "..", "..");
const sourceTree = (root, directory) => readdirSync(resolve(root, directory), { withFileTypes: true })
  .flatMap((entry) => entry.isDirectory() ? sourceTree(root, `${directory}/${entry.name}`) : [`${directory}/${entry.name}`]);
const changedFilesPath = ".omo/evidence/functional-jastreamer-products/task-22/changed-files.txt";

export const todo22ChangedFiles = readFileSync(resolve(repositoryRoot, changedFilesPath), "utf8").trim().split("\n").filter(Boolean);
export const documentedNonSourceCategories = Object.freeze({});
export const productFiles = [...new Set([
  "README.md", "docs/claims.json", "docs/control-android.md", "docs/control-web.md", "docs/control-windows.md", "docs/releasing.md", "docs/renderer-windows.md", "docs/server-pairing.md", "docs/synology.md",
  "packaging/control/manifest.json", "packaging/renderer/config.json", "packaging/server/manifest.json",
  "tooling/docs/claims-contract-v1.json", "tooling/docs/cleanup-owned-server-qa.sh", "tooling/docs/compose-command-qa.sh", "tooling/docs/k17-false-gate-qa.sh", "tooling/docs/server-command-qa.sh",
  "tooling/docs/task23-evidence-contract.mjs", "tooling/docs/task23-evidence-contract.test.mjs", "tooling/docs/task23-evidence.mjs", "tooling/docs/task23-evidence.test.mjs", "tooling/docs/task23-inventory.mjs", "tooling/docs/task23-source-policy.mjs",
  "tooling/docs/verifier-negative-qa.sh", "tooling/docs/verify.mjs", "tooling/docs/verify.test.mjs", "tooling/docs/web-command-qa.sh",
  "tooling/docs/windows-server-commands.json", "tooling/docs/windows-server-commands.test.mjs", "tooling/qa/product-receipt.schema.json",
  changedFilesPath, ...todo22ChangedFiles, ...sourceTree(repositoryRoot, ".github/workflows"), ...sourceTree(repositoryRoot, "tooling/release"),
])].sort();

const covered = new Set([...productFiles, ...Object.values(documentedNonSourceCategories).flat()]);
const uncovered = todo22ChangedFiles.filter((path) => !covered.has(path));
if (uncovered.length !== 0) throw new Error(`TODO22_SOURCE_COVERAGE_INVALID:${uncovered.join(",")}`);

export const sourceFileRecords = async (root = repositoryRoot) => Promise.all(productFiles.map(async (path) => ({
  path,
  sha256: createHash("sha256").update(await readFile(resolve(root, path))).digest("hex"),
})));
export const productDigest = (files) => createHash("sha256").update(JSON.stringify(files)).digest("hex");
