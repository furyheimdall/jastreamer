import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { resolve, sep } from "node:path";

const repositoryRoot = resolve(import.meta.dirname, "..", "..");
const sha256 = (value) => createHash("sha256").update(value).digest("hex");
const sourceTree = (root, directory) => readdirSync(resolve(root, directory), { withFileTypes: true })
  .flatMap((entry) => entry.isDirectory() ? sourceTree(root, `${directory}/${entry.name}`) : [`${directory}/${entry.name}`]);
const changedFilesPath = ".omo/evidence/functional-jastreamer-products/task-22/changed-files.txt";
const postTask23SynthesisEvidence = new Set([
  "final/final-delivery-integration.json",
  "final/final-source-freeze-restage.json",
  "final/f1-plan-compliance.json",
  "final/f2-quality-security.json",
  "final/f3-real-product-qa.json",
  "final/f4-scope-release.json",
]);

export const deliveryStatus = (root = repositoryRoot) => {
  const entries = execFileSync("git", ["-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all"]).toString().split("\0");
  const records = [];
  for (let index = 0; index < entries.length; index += 1) {
    const entry = entries[index];
    if (entry === "") continue;
    const status = entry.slice(0, 2); const path = entry.slice(3); let previousPath;
    if (/[RC]/.test(status)) { previousPath = entries[index + 1]; index += 1; }
    const present = !status.includes("D");
    const state = status === "??" ? "untracked" : status.includes("D") ? "deleted" : /[RC]/.test(status) ? "renamed" : "modified";
    records.push({ path, status, state, present, sha256: present ? sha256(readFileSync(resolve(root, path))) : null, ...(previousPath === undefined ? {} : { previousPath }), classification: "delivery_source" });
  }
  return records.sort((left, right) => left.path < right.path ? -1 : left.path > right.path ? 1 : 0);
};

export const todo22ChangedFiles = readFileSync(resolve(repositoryRoot, changedFilesPath), "utf8").trim().split("\n").filter(Boolean);
export const currentDeliveryScope = deliveryStatus();
export const documentedNonSourceCategories = Object.freeze({});
export const generatedArtifactCategories = Object.freeze([
  { classification: "candidate_binary", scope: "dist" },
  { classification: "screenshot", scope: ".omo/evidence/functional-jastreamer-products/**/*.png" },
  { classification: "generated_evidence", scope: ".omo/evidence/functional-jastreamer-products excluding task-23 and screenshots" },
]);
export const productFiles = [...new Set([
  "README.md", "docs/claims.json", "docs/control-android.md", "docs/control-web.md", "docs/control-windows.md", "docs/releasing.md", "docs/renderer-windows.md", "docs/server-pairing.md", "docs/synology.md",
  "packaging/control/manifest.json", "packaging/renderer/config.json", "packaging/server/manifest.json",
  "tooling/docs/claims-contract-v1.json", "tooling/docs/cleanup-owned-server-qa.sh", "tooling/docs/compose-command-qa.sh", "tooling/docs/k17-false-gate-qa.sh", "tooling/docs/server-command-qa.sh",
  "tooling/docs/task23-evidence-contract.mjs", "tooling/docs/task23-evidence-contract.test.mjs", "tooling/docs/task23-evidence.mjs", "tooling/docs/task23-evidence.test.mjs", "tooling/docs/task23-inventory.mjs", "tooling/docs/task23-source-policy.mjs",
  "tooling/docs/verifier-negative-qa.sh", "tooling/docs/verify.mjs", "tooling/docs/verify.test.mjs", "tooling/docs/web-command-qa.sh",
  "tooling/docs/windows-server-commands.json", "tooling/docs/windows-server-commands.test.mjs", "tooling/qa/product-receipt.schema.json",
  changedFilesPath, ...todo22ChangedFiles, ...sourceTree(repositoryRoot, ".github/workflows"), ...sourceTree(repositoryRoot, "tooling/release"),
  ...currentDeliveryScope.filter(({ present }) => present).map(({ path }) => path),
])].sort();

const covered = new Set([...productFiles, ...Object.values(documentedNonSourceCategories).flat()]);
const uncovered = todo22ChangedFiles.filter((path) => !covered.has(path));
if (uncovered.length !== 0) throw new Error(`TODO22_SOURCE_COVERAGE_INVALID:${uncovered.join(",")}`);
if (currentDeliveryScope.some(({ classification }) => classification !== "delivery_source")) throw new Error("DELIVERY_SCOPE_CLASSIFICATION_INVALID");

const treeRecords = async (root, directory, include) => {
  const absolute = resolve(root, directory); if (!existsSync(absolute)) return [];
  const paths = sourceTree(root, directory).filter(include).sort();
  return Promise.all(paths.map(async (path) => ({ path, sha256: sha256(await readFile(resolve(root, path))) })));
};

export const generatedArtifactInventory = async (root = repositoryRoot) => {
  const evidencePrefix = ".omo/evidence/functional-jastreamer-products/";
  const candidates = await treeRecords(root, "dist", () => true);
  const screenshots = await treeRecords(root, ".omo/evidence/functional-jastreamer-products", (path) => path.endsWith(".png"));
  const evidence = await treeRecords(root, ".omo/evidence/functional-jastreamer-products", (path) => {
    const local = path.slice(evidencePrefix.length);
    const task23Private = local.startsWith("task-23.staging-") || local.startsWith("task-23.backup-");
    return !path.endsWith(".png") &&
      !local.startsWith(`task-23${sep}`) &&
      local !== "task-23" &&
      !task23Private &&
      !postTask23SynthesisEvidence.has(local);
  });
  return [
    { ...generatedArtifactCategories[0], count: candidates.length, digest: sha256(JSON.stringify(candidates)) },
    { ...generatedArtifactCategories[1], count: screenshots.length, digest: sha256(JSON.stringify(screenshots)) },
    { ...generatedArtifactCategories[2], count: evidence.length, digest: sha256(JSON.stringify(evidence)) },
  ];
};

export const sourceFileRecords = async (root = repositoryRoot) => Promise.all(productFiles.map(async (path) => ({
  path,
  sha256: sha256(await readFile(resolve(root, path))),
})));
export const productDigest = (files) => sha256(JSON.stringify(files));
